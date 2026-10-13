//go:build sim

package main

// Replays recorded Claude Code and Codex requests (testdata/extract.py) through simulated accounts, routed by CPA's
// built-in round-robin with session affinity or by this plugin through its ABI, and prints what each cost:
//
//	python3 -I testdata/extract.py testdata/work.tsv
//	SIM_POLICY=plugin SIM_SEEDS=15 go test -tags sim -run TestSim -count=1 .
//
// Accounts behave as CPA sees them. Claude has five-hour and weekly windows, reported as utilization headers and
// answered with a 429 at the limit; plans come from SIM_PLANS. Codex has weekly windows opened by the first request
// after expiry, reported as used-percent headers; the windows the recording shows, early resets included, are
// replayed, and an account with credits keeps serving past its limit. CPA keeps a 429'd account out of the
// candidates until the reset it announces. A request starts before its recorded completion and its cache writes
// become readable when it completes. It reads its own chat's cache if the chat last ran on the account within the
// cache TTL, otherwise the prompt prefix shared by chats of the same model and kind if one of them did.

import (
	"bufio"
	"cmp"
	"container/heap"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const (
	simWeek  = 7 * 86400.0
	simFiveH = 5 * 3600.0
	ttlMain  = 3600.0 // Claude Code caches main chats for an hour
	ttlSub   = 300.0  // and subagents for five minutes
)

// meter is the assumed subscription meter per token, relative to uncached input: cache read, five-minute write,
// one-hour write, output (API price ratios). Codex reads cost 0.1 and its writes no premium.
var (
	meter     = [4]float64{0.05, 1.25, 2, 5}
	modelMult = map[string]float64{"opus": 1, "fable": 1, "sonnet": 0.6, "haiku": 0.2}
	plans     = map[string][2]float64{"20x": {110, 500}, "5x": {27.5, 125}, "pro": {5.5, 25}} // units per 5h, week
)

func units(provider, model string, input, read, w5, w1h, out int64) float64 {
	if provider == "codex" {
		return (float64(input+w5+w1h) + 0.1*float64(read) + 5*float64(out)) / 1e6
	}
	m := 1.0
	for k, v := range modelMult {
		if strings.Contains(model, k) {
			m = v
		}
	}
	return m * (float64(input) + meter[0]*float64(read) + meter[1]*float64(w5) + meter[2]*float64(w1h) +
		meter[3]*float64(out)) / 1e6
}

type simRequest struct {
	start, done               float64 // unix seconds; done is the recorded completion
	provider, sid, parent     string
	model, key                string // key: the shared prompt prefix (model and kind of agent)
	first                     bool
	input, read, w5, w1h, out int64
	ttl                       float64
	shared                    int64 // tokens of the shared prefix, measured from first requests that read it
}

func loadWork(t *testing.T, path string) []simRequest {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var reqs []simRequest
	seen, lastDone := map[string]bool{}, map[string]float64{}
	warmFirst := map[string][]int64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		c := strings.Split(sc.Text(), "\t")
		ts, err := time.Parse(time.RFC3339Nano, c[0])
		if err != nil {
			t.Fatal(err)
		}
		n := func(i int) int64 { v, _ := strconv.ParseInt(c[i], 10, 64); return v }
		r := simRequest{done: float64(ts.UnixNano()) / 1e9, provider: c[1], sid: c[1] + ":" + c[2], model: c[5],
			input: n(6), read: n(7), w5: n(8), w1h: n(9), out: n(10), ttl: ttlMain}
		kind := "main"
		if c[3] != "" {
			r.parent, r.sid, kind = r.sid, r.sid+":agent:"+c[3], "subagent"
			if r.provider == "claude" {
				r.ttl = ttlSub
			}
		}
		r.key = r.model + "/" + kind
		// latency: prefill of the uncached prompt plus the streamed output; a chat's requests run one at a time
		r.start = r.done - (2 + float64(r.input+r.w5+r.w1h)/40000 + float64(min(r.out, 4000))/60)
		if prev, ok := lastDone[r.sid]; ok {
			r.start = max(r.start, prev+0.001)
		}
		lastDone[r.sid] = r.done
		r.first = !seen[r.sid]
		seen[r.sid] = true
		if r.first && r.read > 0 {
			warmFirst[r.key] = append(warmFirst[r.key], r.read)
		}
		reqs = append(reqs, r)
	}
	for i := range reqs {
		r := &reqs[i]
		if v := warmFirst[r.key]; len(v) > 0 {
			slices.Sort(v)
			r.shared = min(v[len(v)/2], r.read+r.w5+r.w1h)
		}
	}
	slices.SortStableFunc(reqs, func(a, b simRequest) int { return cmp.Compare(a.start, b.start) })
	return reqs
}

type cacheEntry struct{ from, until float64 }

func (c cacheEntry) warm(t float64) bool { return c.from <= t && t < c.until }

type recordedWindow struct {
	start, used float64
	credits     bool
}

type simAccount struct {
	id, provider       string
	cap5, capW, weight float64
	used5, usedW       float64 // units in the current windows
	start5, resetW     float64 // five-hour window start (-1: closed); weekly reset (-1: closed until first use)
	credits            bool
	cooldown           float64
	private, shared    map[string]cacheEntry
	creditUnits        float64
	windows            []recordedWindow // recorded Codex windows still to open
}

type simMetrics struct {
	Provider    string  `json:"provider"`
	Requests    int     `json:"requests"`
	Unserved    int     `json:"unserved"`  // no account could take the request
	Units       float64 `json:"units"`     // quota units of served requests
	Recorded    float64 `json:"recorded"`  // what the same requests cost as recorded
	ColdMain    int     `json:"cold_main"` // first requests of a chat that read nothing from cache
	FirstMain   int     `json:"first_main"`
	ColdSub     int     `json:"cold_sub"`
	FirstSub    int     `json:"first_sub"`
	WarmMoves   int     `json:"warm_moves"`   // a chat left an account where its cache was still warm
	Rejections  int     `json:"rejections"`   // upstream 429s
	CreditUnits float64 `json:"credit_units"` // Codex usage paid with credits
}

type completion struct {
	r           *simRequest
	a           *simAccount
	read, write int64
	u           float64
}

type completions []completion

func (c completions) Len() int           { return len(c) }
func (c completions) Less(i, j int) bool { return c[i].r.done < c[j].r.done }
func (c completions) Swap(i, j int)      { c[i], c[j] = c[j], c[i] }
func (c *completions) Push(x any)        { *c = append(*c, x.(completion)) }
func (c *completions) Pop() any {
	old := *c
	x := old[len(old)-1]
	*c = old[:len(old)-1]
	return x
}

type simBinding struct {
	acct  *simAccount
	lastT float64
}

type sim struct {
	accounts map[string][]*simAccount
	rng      *rand.Rand
	p        *plugin                // nil: CPA's built-in routing
	bind     map[string]*simBinding // CPA's session affinity
	rr       map[string]int
	last     map[string]*simAccount // chat -> account of its previous request
	pending  completions
	m        map[string]*simMetrics
}

func newSim(t *testing.T, seed uint64, t0 float64, claude []string, claudeScale float64, codexCap map[string]float64,
	windows map[string][]recordedWindow, weights, plugin bool) *sim {
	s := &sim{accounts: map[string][]*simAccount{}, rng: rand.New(rand.NewPCG(seed, 7)), bind: map[string]*simBinding{},
		rr: map[string]int{}, last: map[string]*simAccount{},
		m: map[string]*simMetrics{"claude": {Provider: "claude"}, "codex": {Provider: "codex"}}}
	for i, plan := range claude {
		c, ok := plans[plan]
		if !ok {
			t.Fatalf("unknown Claude plan %q", plan)
		}
		a := &simAccount{id: fmt.Sprintf("claude-%d-%s", i, plan), provider: "claude", cap5: c[0] * claudeScale,
			capW: c[1] * claudeScale, start5: -1, private: map[string]cacheEntry{}, shared: map[string]cacheEntry{}}
		if weights {
			a.weight = map[string]float64{"20x": 20, "5x": 5, "pro": 1}[plan]
		}
		// unknown weekly phase and starting usage, drawn per seed: at most the elapsed share of the week
		a.resetW = t0 + s.rng.Float64()*simWeek
		a.usedW = a.capW * s.rng.Float64() * (1 - (a.resetW-t0)/simWeek)
		s.accounts["claude"] = append(s.accounts["claude"], a)
	}
	for _, plan := range slices.Sorted(maps.Keys(windows)) {
		ws := windows[plan]
		a := &simAccount{id: "codex-" + plan, provider: "codex", capW: codexCap[plan], start5: -1, resetW: -1,
			private: map[string]cacheEntry{}, shared: map[string]cacheEntry{}}
		if a.capW == 0 {
			a.capW = 100
		}
		if weights {
			a.weight = map[bool]float64{true: 24, false: 10}[plan == "pro"]
		}
		for len(ws) > 0 && ws[0].start <= t0 { // the window in force when the recording starts
			a.usedW, a.resetW, a.credits = ws[0].used*a.capW, ws[0].start+simWeek, ws[0].credits
			ws = ws[1:]
		}
		a.windows = ws
		s.accounts["codex"] = append(s.accounts["codex"], a)
	}
	if plugin {
		s.p = newPlugin()
		call[map[string]any](t, s.p, pluginabi.MethodPluginRegister, map[string][]byte{}, t0)
	}
	return s
}

// advance applies window resets up to time t.
func (s *sim) advance(a *simAccount, t float64) {
	if a.provider == "claude" {
		for t >= a.resetW {
			a.usedW, a.resetW = 0, a.resetW+simWeek
		}
		if a.start5 >= 0 && t >= a.start5+simFiveH {
			a.start5, a.used5 = -1, 0
		}
		return
	}
	for len(a.windows) > 0 && t >= a.windows[0].start {
		w := a.windows[0]
		a.usedW, a.resetW, a.credits, a.cooldown = w.used*a.capW, w.start+simWeek, w.credits, 0
		a.windows = a.windows[1:]
	}
	if a.resetW > 0 && t >= a.resetW {
		a.usedW, a.resetW = 0, -1
	}
}

func (s *sim) headers(a *simAccount, t float64) http.Header {
	h := http.Header{}
	f := func(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }
	if a.provider == "claude" {
		u5, r5 := 0.0, t+simFiveH
		if a.start5 >= 0 {
			u5, r5 = a.used5/a.cap5, a.start5+simFiveH
		}
		h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", f(math.Round(u5*100)/100))
		h.Set("Anthropic-Ratelimit-Unified-5h-Reset", f(math.Floor(r5)))
		h.Set("Anthropic-Ratelimit-Unified-7d-Utilization", f(math.Round(a.usedW/a.capW*100)/100))
		h.Set("Anthropic-Ratelimit-Unified-7d-Reset", f(math.Floor(a.resetW)))
		return h
	}
	reset := a.resetW
	if reset < 0 {
		reset = t + simWeek
	}
	h.Set("X-Codex-Primary-Used-Percent", f(math.Min(100, math.Floor(a.usedW/a.capW*100))))
	h.Set("X-Codex-Primary-Window-Minutes", "10080")
	h.Set("X-Codex-Primary-Reset-At", f(math.Floor(reset)))
	return h
}

// limitReset is when a rejecting account's binding limit lifts, or 0 if the account admits requests.
func (s *sim) limitReset(a *simAccount) float64 {
	var r float64
	if a.provider == "claude" && a.start5 >= 0 && a.used5 >= a.cap5 {
		r = a.start5 + simFiveH
	}
	if a.usedW >= a.capW && !a.credits {
		r = max(r, a.resetW)
	}
	return r
}

// pickBuiltin is CPA's round-robin with session affinity: a chat keeps its account for an idle hour, and a chat
// with a parent (subagent or fork) starts on the parent's account.
func (s *sim) pickBuiltin(r *simRequest, cands []*simAccount) *simAccount {
	for _, id := range []string{r.sid, r.parent} {
		if b := s.bind[id]; id != "" && b != nil && r.start-b.lastT <= 3600 && slices.Contains(cands, b.acct) {
			return b.acct
		}
	}
	key := r.provider + "/" + r.model
	a := cands[s.rr[key]%len(cands)]
	s.rr[key]++
	return a
}

func (s *sim) pickPlugin(t *testing.T, r *simRequest, cands []*simAccount) *simAccount {
	req := pluginapi.SchedulerPickRequest{Provider: r.provider, Model: r.model,
		Options: pluginapi.SchedulerOptions{Metadata: map[string]any{"canonical_session_id": r.sid}}}
	if r.parent != "" {
		req.Options.Metadata["parent_session_id"] = r.parent
	}
	for _, a := range cands {
		c := pluginapi.SchedulerAuthCandidate{ID: a.id, Provider: a.provider}
		if a.weight > 0 {
			c.Attributes = map[string]string{"weight": strconv.FormatFloat(a.weight, 'f', -1, 64)}
		}
		req.Candidates = append(req.Candidates, c)
	}
	resp := call[pluginapi.SchedulerPickResponse](t, s.p, pluginabi.MethodSchedulerPick, req, r.start)
	for _, a := range cands {
		if resp.Handled && a.id == resp.AuthID {
			return a
		}
	}
	return s.pickBuiltin(r, cands)
}

func (s *sim) observe(t *testing.T, r *simRequest, a *simAccount, at float64, status int, read, write int64) {
	if s.p == nil {
		return
	}
	rec := pluginapi.UsageRecord{Provider: r.provider, Model: r.model, AuthID: a.id, SessionID: r.sid,
		ParentSessionID: r.parent, ResponseHeaders: s.headers(a, at), Failed: status != http.StatusOK,
		Failure: pluginapi.UsageFailure{StatusCode: status}}
	if status == http.StatusOK {
		rec.Detail = pluginapi.UsageDetail{InputTokens: r.input, CacheReadTokens: read, CacheCreationTokens: write,
			OutputTokens: r.out}
		if r.provider == "codex" { // Codex reports input including cached tokens
			rec.Detail = pluginapi.UsageDetail{InputTokens: r.input + read + write, CacheReadTokens: read, OutputTokens: r.out}
		}
	}
	call[struct{}](t, s.p, pluginabi.MethodUsageHandle, rec, at)
}

// complete applies a finished request: quota usage, readable cache entries, and the usage record CPA sends.
func (s *sim) complete(t *testing.T, c completion) {
	r, a := c.r, c.a
	s.advance(a, r.done)
	if r.provider == "claude" && a.start5 < 0 {
		a.start5 = math.Floor(r.start/600) * 600
	}
	if r.provider == "codex" && a.resetW < 0 {
		a.resetW = r.start + simWeek
	}
	if a.credits && a.usedW >= a.capW {
		a.creditUnits += c.u
	} else {
		a.used5 += c.u
		a.usedW += c.u
	}
	for _, e := range []struct {
		m map[string]cacheEntry
		k string
	}{{a.private, r.sid}, {a.shared, r.key}} {
		old, from := e.m[e.k], r.done
		if old.warm(r.done) {
			from = old.from
		}
		e.m[e.k] = cacheEntry{from, max(old.until, r.done+r.ttl)}
	}
	s.observe(t, r, a, r.done, http.StatusOK, c.read, c.write)
}

func (s *sim) serve(t *testing.T, r *simRequest) {
	for len(s.pending) > 0 && s.pending[0].r.done <= r.start {
		s.complete(t, heap.Pop(&s.pending).(completion))
	}
	m := s.m[r.provider]
	m.Requests++
	tried := map[*simAccount]bool{}
	for {
		var cands []*simAccount
		for _, a := range s.accounts[r.provider] {
			s.advance(a, r.start)
			if !tried[a] && r.start >= a.cooldown {
				cands = append(cands, a)
			}
		}
		if len(cands) == 0 {
			m.Unserved++
			return
		}
		a := s.pickBuiltin(r, cands)
		if s.p != nil {
			a = s.pickPlugin(t, r, cands)
		}
		if lift := s.limitReset(a); lift > 0 {
			m.Rejections++
			a.cooldown = lift + 1 + 29*s.rng.Float64() // the announced reset plus CPA's 1-30s fuzz
			tried[a] = true
			delete(s.bind, r.sid)
			s.observe(t, r, a, r.start, http.StatusTooManyRequests, 0, 0)
			continue
		}
		prompt := r.read + r.w5 + r.w1h // the cacheable part of the prompt
		var read int64
		switch {
		case a.private[r.sid].warm(r.start):
			read = r.read
		case a.shared[r.key].warm(r.start) && r.first && r.read > 0:
			read = r.read // a first request reads the shared prefix it read in the recording
		case a.shared[r.key].warm(r.start):
			read = r.shared
		}
		read = min(read, prompt)
		write := prompt - read
		w5, w1h := write, int64(0)
		if r.ttl == ttlMain {
			w5, w1h = 0, write
		}
		u := units(r.provider, r.model, r.input, read, w5, w1h, r.out)
		m.Units += u
		m.Recorded += units(r.provider, r.model, r.input, r.read, r.w5, r.w1h, r.out)
		switch {
		case r.first && r.parent == "":
			m.FirstMain++
			if read == 0 {
				m.ColdMain++
			}
		case r.first:
			m.FirstSub++
			if read == 0 {
				m.ColdSub++
			}
		}
		if prev := s.last[r.sid]; prev != nil && prev != a && prev.private[r.sid].warm(r.start) {
			m.WarmMoves++
		}
		s.last[r.sid] = a
		s.bind[r.sid] = &simBinding{a, r.start}
		heap.Push(&s.pending, completion{r, a, read, write, u})
		return
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func TestSim(t *testing.T) {
	path := envOr("SIM_WORK", "testdata/work.tsv")
	work := loadWork(t, path)
	for env, drop := range map[string]func(r, at float64) bool{
		"SIM_FROM": func(r, at float64) bool { return r < at }, "SIM_TO": func(r, at float64) bool { return r >= at }} {
		if v := os.Getenv(env); v != "" { // RFC 3339 bounds on request start
			at, err := time.Parse(time.RFC3339, v)
			if err != nil {
				t.Fatal(err)
			}
			work = slices.DeleteFunc(work, func(r simRequest) bool { return drop(r.start, float64(at.Unix())) })
		}
	}
	windows := map[string][]recordedWindow{}
	if f, err := os.Open(path + ".windows"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			c := strings.Split(sc.Text(), "\t")
			start, _ := strconv.ParseFloat(c[1], 64)
			used, _ := strconv.ParseFloat(c[2], 64)
			windows[c[0]] = append(windows[c[0]], recordedWindow{start, used / 100, c[3] == "1"})
		}
		f.Close()
	}
	claudeScale, _ := strconv.ParseFloat(envOr("SIM_CLAUDE_SCALE", "1"), 64)
	codexCap := map[string]float64{}
	for _, kv := range strings.Split(envOr("SIM_CODEX_CAPS", "pro=140,self_serve_business_prolite=75"), ",") {
		k, v, _ := strings.Cut(kv, "=")
		codexCap[k], _ = strconv.ParseFloat(v, 64)
	}
	if v := os.Getenv("SIM_METER"); v != "" {
		for i, f := range strings.Split(v, ",") {
			meter[i], _ = strconv.ParseFloat(f, 64)
		}
	}
	seeds, _ := strconv.Atoi(envOr("SIM_SEEDS", "1"))
	policy := envOr("SIM_POLICY", "plugin")
	enc := json.NewEncoder(os.Stdout)
	for seed := 1; seed <= seeds; seed++ {
		reqs := slices.Clone(work)
		s := newSim(t, uint64(seed), reqs[0].start, strings.Split(envOr("SIM_PLANS", "20x,20x,5x,5x"), ","),
			claudeScale, codexCap, windows, os.Getenv("SIM_WEIGHTS") == "plan", policy == "plugin")
		for i := range reqs {
			s.serve(t, &reqs[i])
		}
		for len(s.pending) > 0 {
			s.complete(t, heap.Pop(&s.pending).(completion))
		}
		for _, prov := range []string{"claude", "codex"} {
			m := s.m[prov]
			for _, a := range s.accounts[prov] {
				m.CreditUnits += a.creditUnits
			}
			_ = enc.Encode(map[string]any{"seed": seed, "policy": policy, "label": os.Getenv("SIM_LABEL"), "m": m})
		}
	}
}
