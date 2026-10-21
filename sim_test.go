//go:build sim

package main

// TestSim replays the requests testdata/extract.py extracts from local logs through simulated accounts, routed by
// CPA's built-in routing or by this plugin through its ABI, and prints what each policy spent. `make sim` runs it;
// docs/benchmarks.md describes the model, the SIM_* settings and the results.

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
	simWeek        = 7 * 86400.0
	simFiveH       = 5 * 3600.0
	ttlMain        = 3600.0 // Claude Code caches main chats for an hour, and CPA's own breakpoints too
	ttlSub         = 300.0  // and Claude Code subagents and workflow agents for five minutes
	cpaAffinityTTL = 3600.0 // routing.session-affinity-ttl
)

// meter is the assumed subscription meter per token, relative to uncached input: cache read, five-minute write,
// one-hour write, output (API price ratios). codexMeter is Codex's cache read and its write premium (none reported).
// openingHit is SIM_OPENING_HIT.
var (
	meter      = [4]float64{0.05, 1.25, 2, 5}
	codexMeter = [2]float64{0.1, 1}
	codexTTL   = math.Inf(1) // SIM_CODEX_TTL: Codex keeps a logged-in user's cache indefinitely
	openingHit = 1.0
	modelMult  = map[string]float64{"opus": 1, "fable": 1, "sonnet": 0.6, "haiku": 0.2}
	plans      = map[string][2]float64{"20x": {110, 500}, "5x": {27.5, 125}, "pro": {5.5, 25}} // units per 5h, week
)

func units(provider, model string, input, read, w5, w1h, out int64) float64 {
	if provider == "codex" {
		return (float64(input) + codexMeter[1]*float64(w5+w1h) + codexMeter[0]*float64(read) + 5*float64(out)) / 1e6
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
	provider, model           string
	sid, parent               string // CPA's canonical session ids
	session, agent            string // the client's own ids, sent as headers
	class, client, version    string
	first                     bool // the chat's first request
	input, read, w5, w1h, out int64
	ttl                       float64
	opening, share            string // shared prefixes (extract.py columns 15-18)
	openingTokens, shareTok   int64
	bg                        bool // SIM_BG: counts in bg, outside m
	toolSearch                bool // Claude Code's tool search is on: recorded after 2026-10-12T22:16Z, or SIM_OPENINGS=deferred
}

func (r *simRequest) prompt() int64 { return r.read + r.w5 + r.w1h } // the cacheable part

// sessionless is a request without a session id, which CPA keys by an id it derives from the prompt.
func (r *simRequest) sessionless() bool { return strings.HasPrefix(r.sid, "derived:") }

func (r *simRequest) bindKey() string { return r.sid + "::" + r.model } // CPA's session affinity key

// kind groups first requests for the cold-start measures: Claude Code's main, sub and wf, the harness's sessions,
// codex and codex-sub for chats sent by a Codex client (Claude models and the harness's included), and the background
// sources.
func (r *simRequest) kind() string {
	k, _, _ := strings.Cut(r.class, ":")
	return k
}

func loadWork(t *testing.T, path string, deferred bool) []simRequest {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var reqs []simRequest
	lastDone := map[string]float64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		c := strings.Split(sc.Text(), "\t")
		ts, err := time.Parse(time.RFC3339Nano, c[0])
		if err != nil {
			t.Fatal(err)
		}
		n := func(i int) int64 { v, _ := strconv.ParseInt(c[i], 10, 64); return v }
		r := simRequest{done: float64(ts.UnixNano()) / 1e9, provider: c[1], session: c[2], agent: c[3], model: c[5],
			input: n(6), read: n(7), w5: n(8), w1h: n(9), out: n(10), class: c[12], client: c[13], version: c[14],
			opening: c[15], openingTokens: n(16), share: c[17], shareTok: n(18), ttl: ttlMain}
		prefix := "claude"
		if strings.HasPrefix(r.class, "codex") {
			prefix = "codex"
		}
		r.sid = prefix + ":" + r.session
		if r.agent != "" {
			r.parent = r.sid
			if c[4] != "" {
				r.parent = prefix + ":" + c[4]
			}
			r.sid += ":agent:" + r.agent
		}
		if k := r.kind(); k == "sub" || k == "wf" {
			r.ttl = ttlSub
		}
		if r.provider == "codex" {
			r.ttl = codexTTL
		}
		r.first = c[21] == "1"
		r.toolSearch = deferred || c[19] == "" && !strings.Contains(r.class, "harness")
		s, err := time.Parse(time.RFC3339Nano, c[11])
		if err != nil {
			t.Fatal(err)
		}
		r.start = float64(s.UnixNano()) / 1e9
		if d, l := n(19), n(20); deferred && d > 0 {
			r.deferOpening(d, l)
		}
		if prev, ok := lastDone[r.sid]; ok { // a chat's requests run one at a time
			r.start = max(r.start, prev+0.001)
		}
		lastDone[r.sid] = r.done
		reqs = append(reqs, r)
	}
	// Only a fork's first request shares a chat (column 17); CPA sends that chat as parent for all of the fork's.
	forks := map[string]string{}
	for _, r := range reqs {
		if r.agent == "" && strings.HasPrefix(r.share, "claude:") {
			forks[r.sid] = r.share
		}
	}
	for i := range reqs {
		reqs[i].parent = cmp.Or(forks[reqs[i].sid], reqs[i].parent)
	}
	slices.SortStableFunc(reqs, func(a, b simRequest) int { return cmp.Compare(a.start, b.start) })
	return reqs
}

// deferOpening replays a request as if its MCP tools were deferred (tool search on): the opening shrinks to d
// tokens and is the same for every chat of the client, version, model and class (a workflow agent's still differs
// by its output schema), and the chat's own prefix gains the deferred tool list, l tokens, which its first request
// writes. The prompt shrinks from the cache read if the request read the opening, else from its writes; where the
// list outweighs the deferred tools (cut < 0) the prompt grows instead.
func (r *simRequest) deferOpening(d, l int64) {
	cut := r.openingTokens - d - l
	if r.shareTok > 0 {
		r.shareTok = max(r.shareTok-cut, d)
	}
	switch {
	case r.read < r.openingTokens && cut < 0: // it wrote the opening, now a larger prefix
		r.addWrite(-cut)
	case r.read < r.openingTokens:
		w1 := min(cut, r.w1h)
		w5 := min(cut-w1, r.w5)
		r.w1h, r.w5, r.read = r.w1h-w1, r.w5-w5, max(r.read-(cut-w1-w5), 0)
	case r.first:
		r.read -= r.openingTokens - d
		r.addWrite(l)
	default:
		r.read -= cut
	}
	if r.class != "wf" {
		r.opening = "deferred:" + r.client + "/" + r.version + "/" + r.class
	}
	r.openingTokens = d
}

// addWrite adds n tokens to the cache writes of r's TTL.
func (r *simRequest) addWrite(n int64) {
	if r.ttl == ttlMain {
		r.w1h += n
	} else {
		r.w5 += n
	}
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
	from               float64               // when the account joined CPA
	cache              map[string]cacheEntry // model|prefix
	windows            []recordedWindow      // recorded Codex windows still to open
}

// touch makes the prefix key readable on a from a second after r starts until r's TTL after it completes.
func (a *simAccount) touch(r *simRequest, key string) {
	k := r.model + "|" + key
	e, from := a.cache[k], min(r.start+1, r.done)
	if e.until <= r.start {
		e.from = from
	}
	e.from, e.until = min(e.from, from), max(e.until, r.done+r.ttl)
	a.cache[k] = e
}

func (a *simAccount) warm(r *simRequest, key string) bool {
	return a.cache[r.model+"|"+key].warm(r.start)
}

// cacheRead is what r reads on a, and the chance it reads nothing: what it read in the recording if its chat is warm
// there, otherwise the largest warm prefix it shares with other chats. A Claude Code main chat gets a warm opening
// with probability openingHit; the read is the expected value.
func (a *simAccount) cacheRead(r *simRequest) (read int64, cold float64) {
	if a.warm(r, r.sid) {
		return r.read, none(r.read)
	}
	var opening, shared int64
	if a.warm(r, r.opening) {
		opening = r.openingTokens
	}
	if a.warm(r, r.share) {
		shared = r.shareTok
	}
	p := 1.0
	if r.class == "main" {
		p = openingHit
	}
	hit, miss := min(max(opening, shared), r.prompt()), min(shared, r.prompt())
	return int64(math.Round(p*float64(hit) + (1-p)*float64(miss))), p*none(hit) + (1-p)*none(miss)
}

func none(read int64) float64 {
	if read == 0 {
		return 1
	}
	return 0
}

type simMetrics struct {
	Provider string  `json:"provider"`
	Requests int     `json:"requests"`
	Unserved int     `json:"unserved"` // no account could take the request
	Units    float64 `json:"units"`    // quota units of served requests
	Recorded float64 `json:"recorded"` // what the same requests cost as recorded
	// Cold-start rates are over served first requests: an unserved one is counted only in UnservedFirst, so policies
	// that serve different requests have different denominators.
	First         map[string]int     `json:"first"`          // served first requests of chats, by kind
	Cold          map[string]float64 `json:"cold"`           // of which read nothing from cache (expected)
	RecCold       map[string]int     `json:"rec_cold"`       // and as recorded
	UnservedFirst map[string]int     `json:"unserved_first"` // first requests no account could take, by kind
	WarmMoves     int                `json:"warm_moves"`     // a chat left an account where its cache was still warm
	Rejections    int                `json:"rejections"`     // upstream 429s
	CreditUnits   float64            `json:"credit_units"`   // Codex usage paid with credits
	ExpiredUnits  float64            `json:"expired_units"`  // Claude weekly allowance left unused at its resets
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
	accounts  map[string][]*simAccount // per provider, sorted by ID as CPA lists candidates
	rng       *rand.Rand
	policy    string                 // builtin (round-robin), fillfirst or plugin
	p         *router                // nil: CPA's built-in routing
	bind      map[string]*simBinding // CPA's session affinity: session::model
	rr        map[string]string      // round-robin: model -> last account picked
	last      map[string]*simAccount // chat -> account of its previous request
	pending   completions
	m         map[string]*simMetrics // per provider
	warmup    *simMetrics            // what requests before scoreFrom would have counted, discarded
	bgm       map[string]*simMetrics // background requests (SIM_BG), printed as bg, outside m
	scoreFrom float64
}

func newMetrics(prov string) *simMetrics {
	return &simMetrics{Provider: prov, First: map[string]int{}, Cold: map[string]float64{}, RecCold: map[string]int{},
		UnservedFirst: map[string]int{}}
}

// metrics is where r counts: nowhere if it starts before SIM_SCORE_FROM, else in bg if it is background.
func (s *sim) metrics(r *simRequest) *simMetrics {
	if r.start < s.scoreFrom {
		return s.warmup
	}
	if r.bg {
		return s.bgm[r.provider]
	}
	return s.m[r.provider]
}

func newSim(t *testing.T, seed uint64, t0, scoreFrom float64, claude []string, claudeScale float64,
	codexCap map[string]float64, windows map[string][]recordedWindow, weights bool, policy string) *sim {
	s := &sim{accounts: map[string][]*simAccount{}, rng: rand.New(rand.NewPCG(seed, 7)), policy: policy,
		bind: map[string]*simBinding{}, rr: map[string]string{}, last: map[string]*simAccount{}, m: map[string]*simMetrics{},
		warmup: newMetrics(""), bgm: map[string]*simMetrics{}, scoreFrom: scoreFrom}
	for _, prov := range []string{"claude", "codex"} {
		s.m[prov], s.bgm[prov] = newMetrics(prov), newMetrics(prov)
	}
	for i, plan := range claude {
		plan, joined, _ := strings.Cut(plan, "@")
		c, ok := plans[plan]
		if !ok {
			t.Fatalf("unknown Claude plan %q", plan)
		}
		a := &simAccount{id: fmt.Sprintf("claude-%d-%s", i, plan), provider: "claude", cap5: c[0] * claudeScale,
			capW: c[1] * claudeScale, start5: -1, cache: map[string]cacheEntry{}}
		if weights {
			a.weight = map[string]float64{"20x": 20, "5x": 5, "pro": 1}[plan]
		}
		// unknown weekly phase and starting usage, drawn per seed: at most the elapsed share of the week
		a.resetW = t0 + s.rng.Float64()*simWeek
		a.usedW = a.capW * s.rng.Float64() * (1 - (a.resetW-t0)/simWeek)
		a.from = unixTime(t, joined)
		s.accounts["claude"] = append(s.accounts["claude"], a)
	}
	for _, plan := range slices.Sorted(maps.Keys(windows)) {
		ws := windows[plan]
		a := &simAccount{id: "codex-" + plan, provider: "codex", capW: codexCap[plan], start5: -1, resetW: -1,
			cache: map[string]cacheEntry{}}
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
	return s
}

// advance applies window resets up to time t.
func (s *sim) advance(a *simAccount, t float64) {
	if a.provider == "claude" {
		for t >= a.resetW {
			if a.resetW >= max(s.scoreFrom, a.from) {
				s.m["claude"].ExpiredUnits += max(a.capW-a.usedW, 0)
			}
			a.usedW, a.resetW = 0, a.resetW+simWeek
		}
		if a.start5 >= 0 && t >= a.start5+simFiveH {
			a.start5, a.used5 = -1, 0
		}
		return
	}
	for len(a.windows) > 0 && t >= a.windows[0].start { // an early reset leaves CPA's cooldown in place
		w := a.windows[0]
		a.usedW, a.resetW, a.credits = w.used*a.capW, w.start+simWeek, w.credits
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
	h.Set("X-Codex-Credits-Has-Credits", map[bool]string{true: "True", false: "False"}[a.credits]) // as upstream sends it
	return h
}

// inbound is the request header the plugin reads: Claude Code's Anthropic-Beta, which names tool search when on.
func inbound(r *simRequest) http.Header {
	if !r.toolSearch || r.sessionless() || strings.HasPrefix(r.class, "codex") {
		return nil
	}
	return http.Header{"Anthropic-Beta": {"claude-code-20250219,advanced-tool-use-2025-11-20"}}
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

func (s *sim) bound(key string, t float64) *simBinding {
	if b := s.bind[key]; b != nil && t-b.lastT <= cpaAffinityTTL {
		return b
	}
	return nil
}

// pickBuiltin is CPA's session affinity (session::model, sliding TTL) over its round-robin or fill-first. A subagent
// without a live binding of its own takes its parent's for the same model (session-affinity-subagents).
func (s *sim) pickBuiltin(r *simRequest, cands []*simAccount) *simAccount {
	key := r.bindKey()
	if b := s.bound(key, r.start); b != nil {
		b.lastT = r.start
		if slices.Contains(cands, b.acct) {
			return b.acct
		}
	} else if b := s.bound(r.parent+"::"+r.model, r.start); r.parent != "" && b != nil && slices.Contains(cands, b.acct) {
		s.bind[key] = &simBinding{b.acct, r.start}
		return b.acct
	}
	a := cands[0] // fill-first: the first available ID
	if s.policy != "fillfirst" {
		// round-robin (sdk/cliproxy/auth/selector.go:1195 and :1242, keyed mixed:<model>) moves to the ID after the
		// model's last pick, wrapping around (candidates are sorted by ID)
		a = cands[max(slices.IndexFunc(cands, func(c *simAccount) bool { return c.id > s.rr[r.model] }), 0)]
		s.rr[r.model] = a.id
	}
	s.bind[key] = &simBinding{a, r.start}
	return a
}

// pickPlugin asks the plugin as CPA's mixed-provider path does: no provider on the request, the canonical session
// ids (or the derived one) in the metadata and the client's headers. A sessionless request's retry carries the
// derived id as canonical: CPA's affinity picker, which took its first attempt, writes it into the metadata the
// attempts share (selector.go:1140).
func (s *sim) pickPlugin(t *testing.T, r *simRequest, cands []*simAccount, retry bool) *simAccount {
	key := "canonical_session_id"
	if r.sessionless() && !retry {
		key = "derived_session_id"
	}
	req := pluginapi.SchedulerPickRequest{Model: r.model, Options: pluginapi.SchedulerOptions{Headers: inbound(r),
		Metadata: map[string]any{key: r.sid, "requested_model": r.model}}}
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
	// The record carries the canonical session id the attempt ran under, a sessionless request's derived one included
	// (conductor_execution.go syncMetadataSessionToContext).
	rec := pluginapi.UsageRecord{Provider: r.provider, Model: r.model, Alias: r.model, AuthID: a.id, SessionID: r.sid,
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

// complete applies a finished request: quota usage, the affinity refresh on success, and the usage record CPA sends.
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
		s.metrics(r).CreditUnits += c.u
	} else {
		a.used5 += c.u
		a.usedW += c.u
	}
	if b := s.bind[r.bindKey()]; b != nil && b.acct == a {
		b.lastT = max(b.lastT, r.done)
	}
	s.observe(t, r, a, r.done, http.StatusOK, c.read, c.write)
}

func (s *sim) serve(t *testing.T, r *simRequest) {
	for len(s.pending) > 0 && s.pending[0].r.done <= r.start {
		s.complete(t, heap.Pop(&s.pending).(completion))
	}
	m := s.metrics(r)
	m.Requests++
	tried := map[*simAccount]bool{}
	for {
		var cands []*simAccount
		for _, a := range s.accounts[r.provider] {
			s.advance(a, r.start)
			if !tried[a] && r.start >= a.cooldown && r.start >= a.from {
				cands = append(cands, a)
			}
		}
		if len(cands) == 0 {
			m.Unserved++
			if r.first {
				m.UnservedFirst[r.kind()]++
			}
			return
		}
		var a *simAccount
		if s.p != nil {
			a = s.pickPlugin(t, r, cands, len(tried) > 0)
		} else {
			a = s.pickBuiltin(r, cands)
		}
		if lift := s.limitReset(a); lift > 0 {
			m.Rejections++
			a.cooldown = lift
			if a.provider == "claude" { // CPA's 1-30 s fuzz on Claude's announced reset (claude_ratelimit.go); Codex has none
				a.cooldown += 1 + 29*s.rng.Float64()
			}
			tried[a] = true
			delete(s.bind, r.bindKey())
			s.observe(t, r, a, r.start, http.StatusTooManyRequests, 0, 0)
			continue
		}
		read, cold := a.cacheRead(r)
		for _, k := range []string{r.sid, r.opening, r.share} {
			if k != "" {
				a.touch(r, k)
			}
		}
		write := r.prompt() - read
		w5, w1h := write, int64(0)
		if r.ttl == ttlMain {
			w5, w1h = 0, write
		}
		u := units(r.provider, r.model, r.input, read, w5, w1h, r.out)
		m.Units += u
		if !r.bg { // a background request's recording is its template chat's
			m.Recorded += units(r.provider, r.model, r.input, r.read, r.w5, r.w1h, r.out)
		}
		if r.first {
			k := r.kind()
			m.First[k]++
			m.Cold[k] += cold
			if r.read == 0 && !r.bg {
				m.RecCold[k]++
			}
		}
		if prev := s.last[r.sid]; prev != nil && prev != a && prev.warm(r, r.sid) {
			m.WarmMoves++
		}
		s.last[r.sid] = a
		heap.Push(&s.pending, completion{r, a, read, write, u})
		return
	}
}

// bgChats are the recorded Claude Code main chats, the templates of background chats.
func bgChats(work []simRequest) [][]simRequest {
	var chats [][]simRequest
	idx := map[string]int{}
	for _, r := range work {
		if r.provider != "claude" || r.class != "main" {
			continue
		}
		i, ok := idx[r.sid]
		if !ok && !r.first {
			continue
		}
		if !ok {
			i, idx[r.sid] = len(chats), len(chats)
			chats = append(chats, nil)
		}
		chats[i] = append(chats[i], r)
	}
	return chats
}

// background is the traffic CPA served that the recording misses, per hour, source and model (extract.py's
// work.tsv.bg): so many requests in so many chats, with lengths as even as the counts allow (an hour with requests but
// no new chats gets one). A chat starts at a random time in the hour and replays the first requests of a recorded main
// chat at least as long, their token counts times scale; requests are kept if they start from lo to hi. A source is
// one client with its own opening. Probes and unknown requests come from a Claude Code user agent: an unknown chat is
// one request, a probe's is its main request and a short side request at launch, whose model and size CPA doesn't log
// (replayed as a few tokens on the probe's model). Sessionless requests carry CPA's derived id and no Claude Code
// headers, and cache for five minutes (the API's default).
func background(t *testing.T, rng *rand.Rand, chats [][]simRequest, lo, hi float64, path string,
	scale float64) []simRequest {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	longest := len(slices.MaxFunc(chats, func(a, b []simRequest) int { return cmp.Compare(len(a), len(b)) }))
	var bg []simRequest
	sc := bufio.NewScanner(f)
	for id := 0; sc.Scan(); {
		c := strings.Split(sc.Text(), "\t") // hour source model requests chats
		hour, err := time.Parse(time.RFC3339, c[0])
		if err != nil {
			t.Fatal(err)
		}
		source, model := c[1], c[2]
		n, _ := strconv.Atoi(c[3])
		k, _ := strconv.Atoi(c[4])
		k = max(k, 1)
		for i := range k {
			want := n / k
			if i < n%k {
				want++
			}
			side := source == "probe" && want > 1 // a probe's second request is its side request
			if source == "probe" {
				want = 1
			}
			if want > longest {
				t.Fatalf("%s: a %s chat of %d requests, longer than any recorded main chat", c[0], source, want)
			}
			tpl := chats[rng.IntN(len(chats))]
			for len(tpl) < want {
				tpl = chats[rng.IntN(len(chats))]
			}
			off := float64(hour.Unix()) + 3600*rng.Float64() - tpl[0].start
			id++
			for _, r := range tpl[:want] {
				r.start, r.done, r.bg, r.model, r.class = r.start+off, r.done+off, true, model, source
				r.session, r.agent, r.parent = fmt.Sprintf("bg-%d", id), "", ""
				r.sid, r.client = "claude:"+r.session, "sdk-cli"
				if source == "sessionless" {
					r.sid, r.ttl, r.w5, r.w1h = "derived:"+r.session, ttlSub, r.w5+r.w1h, 0
				}
				r.opening, r.share, r.shareTok = "bg:"+source, "", 0
				for _, v := range []*int64{&r.input, &r.read, &r.w5, &r.w1h, &r.out, &r.openingTokens} {
					*v = int64(float64(*v) * scale)
				}
				if lo <= r.start && r.start <= hi {
					bg = append(bg, r)
					if side { // from launch, about 2s, sharing nothing
						r.first, r.done, r.opening, r.openingTokens = false, r.start+2, "", 0
						r.input, r.read, r.w5, r.w1h, r.out = 20, 0, 0, 0, 1
						bg = append(bg, r)
					}
				}
			}
		}
	}
	return bg
}

// unixTime is an RFC 3339 time in unix seconds, or 0 for "".
func unixTime(t *testing.T, v string) float64 {
	if v == "" {
		return 0
	}
	at, err := time.Parse(time.RFC3339, v)
	if err != nil {
		t.Fatal(err)
	}
	return float64(at.Unix())
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func TestSim(t *testing.T) {
	path := "testdata/work.tsv"
	openings := envOr("SIM_OPENINGS", "recorded")
	if !slices.Contains([]string{"recorded", "deferred"}, openings) {
		t.Fatalf("unknown SIM_OPENINGS %q", openings)
	}
	var err error
	if openingHit, err = strconv.ParseFloat(envOr("SIM_OPENING_HIT", "1"), 64); err != nil || openingHit < 0 || openingHit > 1 {
		t.Fatalf("SIM_OPENING_HIT %q: want a fraction from 0 to 1", os.Getenv("SIM_OPENING_HIT"))
	}
	if codexTTL, err = strconv.ParseFloat(envOr("SIM_CODEX_TTL", "inf"), 64); err != nil || codexTTL <= 0 {
		t.Fatalf("SIM_CODEX_TTL %q: want seconds, or inf", os.Getenv("SIM_CODEX_TTL"))
	}
	work := loadWork(t, path, openings == "deferred")
	bgPath := os.Getenv("SIM_BG")
	bgScale, _ := strconv.ParseFloat(envOr("SIM_BG_SCALE", "0.1"), 64)
	var templates [][]simRequest
	if bgPath != "" {
		templates = bgChats(work) // before SIM_TO drops requests from work
	}
	at := func(env string) float64 { return unixTime(t, os.Getenv(env)) }
	scoreFrom, pluginFrom := at("SIM_SCORE_FROM"), at("SIM_PLUGIN_FROM")
	if to := at("SIM_TO"); to > 0 {
		work = slices.DeleteFunc(work, func(r simRequest) bool { return r.start >= to })
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
	if v := os.Getenv("SIM_CODEX_METER"); v != "" {
		for i, f := range strings.Split(v, ",") {
			codexMeter[i], _ = strconv.ParseFloat(f, 64)
		}
	}
	seeds, _ := strconv.Atoi(envOr("SIM_SEEDS", "1"))
	seedFrom, _ := strconv.Atoi(envOr("SIM_SEED_FROM", "1"))
	policy := envOr("SIM_POLICY", "plugin")
	if !slices.Contains([]string{"plugin", "builtin", "fillfirst"}, policy) {
		t.Fatalf("unknown SIM_POLICY %q", policy)
	}
	enc := json.NewEncoder(os.Stdout)
	for seed := seedFrom; seed < seedFrom+seeds; seed++ {
		reqs := slices.Clone(work)
		if bgPath != "" {
			reqs = append(reqs, background(t, rand.New(rand.NewPCG(uint64(seed), 11)), templates, work[0].start,
				work[len(work)-1].start, bgPath, bgScale)...)
			slices.SortStableFunc(reqs, func(a, b simRequest) int { return cmp.Compare(a.start, b.start) })
		}
		// CPA's Claude accounts in ID order: the third joined when its auth file was written, the fourth is a Max 20x;
		// the other plans are assumed
		s := newSim(t, uint64(seed), reqs[0].start, scoreFrom, strings.Split(envOr("SIM_PLANS",
			"20x,5x,5x@2026-10-05T17:58:54Z,20x"), ","),
			claudeScale, codexCap, windows, os.Getenv("SIM_WEIGHTS") == "plan", policy)
		step := math.Inf(1)
		if h, _ := strconv.ParseFloat(os.Getenv("SIM_RESTART_H"), 64); h > 0 { // hours between plugin restarts
			step = h * 3600
		}
		next := math.Inf(1) // when a fresh plugin starts, forgetting what the last one learned
		if policy == "plugin" {
			next = cmp.Or(pluginFrom, reqs[0].start)
		}
		for i := range reqs {
			if reqs[i].start >= next {
				s.p = newRouter()
				call[map[string]any](t, s.p, pluginabi.MethodPluginRegister, map[string][]byte{}, reqs[i].start)
				for next <= reqs[i].start { // one restart per crossing, even after a gap in traffic
					next += step
				}
			}
			s.serve(t, &reqs[i])
		}
		for len(s.pending) > 0 {
			s.complete(t, heap.Pop(&s.pending).(completion))
		}
		for _, prov := range []string{"claude", "codex"} {
			line := map[string]any{"seed": seed, "policy": policy, "label": os.Getenv("SIM_LABEL"), "m": s.m[prov]}
			if bgPath != "" {
				line["bg"] = s.bgm[prov]
			}
			_ = enc.Encode(line)
		}
	}
}
