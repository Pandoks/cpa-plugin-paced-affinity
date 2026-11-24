package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// A real retrieveUserQuotaSummary reply from a Pro account, as the official CLI received it.
const quotaFixture = `{"groups":[{"buckets":[
 {"bucketId":"gemini-weekly","displayName":"Weekly Limit Remaining","window":"weekly","resetTime":"2026-11-30T20:03:09Z","description":"You have used some of your weekly limit, it will fully refresh in 6 days.","remainingFraction":0.80813533},
 {"bucketId":"gemini-5h","displayName":"Five Hour Limit Remaining","window":"5h","resetTime":"2026-11-24T23:16:00Z","description":"You have used some of your 5-hour limit, it will fully refresh in 3 hours, 34 minutes.","remainingFraction":0.98145247}],
 "displayName":"Gemini Models","description":"Models within this group: Gemini 4 Neon, Gemini 4 Carbon, Gemini 4 Argon, Gemini 4 Hydrogen, Orchestrator (Neon), Orchestrator (Carbon), Orchestrator (Argon), Gemini 3.8 Flash"},
 {"buckets":[
 {"bucketId":"3p-weekly","displayName":"Weekly Limit Remaining","window":"weekly","remainingFraction":1},
 {"bucketId":"3p-5h","displayName":"Five Hour Limit Remaining","window":"5h","remainingFraction":1}],
 "displayName":"Claude and GPT models","description":"Models within this group: Claude Opus 5.5, Claude Sonnet 5.5, GPT-OSS 120B"}],
 "description":"Within each group, models share a weekly limit and a 5-hour limit. Quota is consumed proportionally to the cost of the tokens."}`

// fakeQuotaHost stands in for CPA's host callbacks: it lists auths a and b, serves their files, and answers quota polls
// with the body or failure set for each, geminiInUse by default. Tests change it only while no poll is in flight.
type fakeQuotaHost struct {
	mu     sync.Mutex
	files  map[string]string        // auth id -> auth file JSON
	reply  map[string]string        // auth id -> quota body; "status 401" or "host error" fails the poll
	hold   map[string]chan struct{} // auth id -> closed to let its polls answer
	polls  map[string]int
	gets   map[string]int // auth files served
	polled []pluginapi.HTTPRequest
}

func newFakeQuotaHost(t *testing.T) (*router, *fakeQuotaHost) {
	p := newRouter()
	f := &fakeQuotaHost{files: map[string]string{}, reply: map[string]string{}, polls: map[string]int{}, gets: map[string]int{}}
	for _, id := range []string{"a", "b"} {
		f.files[id] = fmt.Sprintf(`{"type":"antigravity","access_token":"tok-%s","project_id":"proj-%s"}`, id, id)
	}
	old := hostCall
	hostCall = f.call
	t.Cleanup(func() { p.fetching.Wait(); hostCall = old })
	return p, f
}

func (f *fakeQuotaHost) call(method string, req []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out any = struct{}{}
	switch method {
	case pluginabi.MethodHostAuthList:
		var files []pluginapi.HostAuthFileEntry
		for id := range f.files {
			files = append(files, pluginapi.HostAuthFileEntry{ID: id, AuthIndex: "idx-" + id})
		}
		out = map[string]any{"files": files}
	case pluginabi.MethodHostAuthGet:
		var get pluginapi.HostAuthGetRequest
		json.Unmarshal(req, &get)
		id, listed := strings.CutPrefix(get.AuthIndex, "idx-")
		if !listed {
			return nil, fmt.Errorf("auth %q not found", get.AuthIndex)
		}
		f.gets[id]++
		out = pluginapi.HostAuthGetResponse{AuthIndex: get.AuthIndex, JSON: json.RawMessage(f.files[id])}
	case pluginabi.MethodHostHTTPOperationOpen:
		out = map[string]string{"operation_id": "op"}
	case pluginabi.MethodHostHTTPDo:
		var do pluginapi.HTTPRequest
		json.Unmarshal(req, &do)
		f.polled = append(f.polled, do)
		id := strings.TrimPrefix(do.Headers.Get("Authorization"), "Bearer tok-")
		f.polls[id]++
		if gate := f.hold[id]; gate != nil {
			f.mu.Unlock()
			<-gate
			f.mu.Lock()
		}
		switch reply := cmp.Or(f.reply[id], geminiInUse); {
		case reply == "host error":
			return nil, errors.New("execute host http request: connection refused")
		case reply == "status 401":
			out = pluginapi.HTTPResponse{StatusCode: http.StatusUnauthorized, Body: []byte(geminiInUse)} // a body that would parse
		default:
			out = pluginapi.HTTPResponse{StatusCode: http.StatusOK, Body: []byte(reply)}
		}
	}
	return json.Marshal(out)
}

// quotaReply builds a quota summary from bucket id -> remaining fraction (negative: exhausted, so omitted) and the
// hours to its reset (0: none yet).
func quotaReply(buckets map[string][2]float64) string {
	var bs []string
	for id, b := range buckets {
		fields := []string{fmt.Sprintf(`"bucketId":%q`, id)}
		if b[0] >= 0 {
			fields = append(fields, fmt.Sprintf(`"remainingFraction":%v`, b[0]))
		}
		if b[1] != 0 {
			fields = append(fields, fmt.Sprintf(`"resetTime":%q`, time.Unix(int64(placementNow+3600*b[1]), 0).UTC().Format(time.RFC3339)))
		}
		bs = append(bs, "{"+strings.Join(fields, ",")+"}")
	}
	return `{"groups":[{"buckets":[` + strings.Join(bs, ",") + `]}]}`
}

var (
	// Gemini exhausted for the week; Claude and GPT models unused.
	geminiExhausted = quotaReply(map[string][2]float64{"gemini-5h": {.5, 2}, "gemini-weekly": {-1, 72}, "3p-5h": {1, 0}, "3p-weekly": {1, 0}})
	geminiInUse     = quotaReply(map[string][2]float64{"gemini-5h": {.9, 2}, "gemini-weekly": {.8, 72}, "3p-5h": {1, 0}, "3p-weekly": {1, 0}})
)

// agyRequest is a pick as CPA sends it for Antigravity: no route provider, the client's model as requested_model. a
// has weight 20 and b weight 1, so a has the room unless its allowance is out.
func agyRequest(sid, model string) pluginapi.SchedulerPickRequest {
	req := pickRequest("antigravity", sid, "a", "b")
	req.Provider = ""
	req.Options.Metadata["requested_model"] = model
	req.Candidates[0].Attributes = map[string]string{"weight": "20"}
	return req
}

// agyTry sends a pick and waits for any poll it starts.
func agyTry(t *testing.T, p *router, sid, model string, at float64) pluginapi.SchedulerPickResponse {
	t.Helper()
	got := call[pluginapi.SchedulerPickResponse](t, p, pluginabi.MethodSchedulerPick, agyRequest(sid, model), placementNow+at)
	p.fetching.Wait()
	return got
}

// agyPick sends a pick the plugin must handle, waits for any poll it starts, and returns the account picked.
func agyPick(t *testing.T, p *router, sid, model string, at float64) string {
	t.Helper()
	got := agyTry(t, p, sid, model, at)
	if !got.Handled {
		t.Fatalf("%s %s: not handled", sid, model)
	}
	return got.AuthID
}

// agyUse sends auth's usage record for chat sid (on gemini-4-neon unless rec names a model) and waits for any poll it
// starts.
func agyUse(t *testing.T, p *router, auth, sid string, at float64, rec pluginapi.UsageRecord) {
	t.Helper()
	rec.Provider, rec.AuthID, rec.SessionID, rec.Alias = "antigravity", auth, sid, cmp.Or(rec.Alias, "gemini-4-neon")
	call[struct{}](t, p, pluginabi.MethodUsageHandle, rec, placementNow+at)
	p.fetching.Wait()
}

func tokens(n int64) pluginapi.UsageRecord {
	return pluginapi.UsageRecord{Detail: pluginapi.UsageDetail{InputTokens: n}}
}

func TestAntigravityAllowances(t *testing.T) {
	p, f := newFakeQuotaHost(t)
	f.files["b"] = `{"access_token":"tok-b","project_id":"proj-b","base_url":"https://sandbox.example/"}`
	f.reply["a"] = geminiExhausted
	agyPick(t, p, "agy:warmup", "claude-opus-5-5-thinking", 0) // the polls record the Gemini allowance too

	if got := agyPick(t, p, "agy:s", "gemini-4-neon", 1); got != "b" {
		t.Errorf("gemini on %s, want b: a's Gemini allowance is out", got)
	}
	if got := agyPick(t, p, "agy:s", "claude-opus-5-5-thinking", 2); got != "a" {
		t.Errorf("claude on %s, want a: its Claude and GPT allowance has the most room", got)
	}
	for _, r := range f.polled {
		id := strings.TrimPrefix(r.Headers.Get("Authorization"), "Bearer tok-")
		base := map[string]string{"a": "https://daily-cloudcode-pa.googleapis.com", "b": "https://sandbox.example"}[id]
		if r.Method != "POST" || r.URL != base+"/v1internal:retrieveUserQuotaSummary" || string(r.Body) != `{"project":"proj-`+id+`"}` ||
			r.Headers.Get("Content-Type") != "application/json" || r.Headers.Get("User-Agent") != quotaUserAgent {
			t.Errorf("poll of %s: %s %s %v %s", id, r.Method, r.URL, r.Headers, r.Body)
		}
	}

	// Six minutes on, a's Gemini allowance is back and the larger, and its Claude and GPT 5h window nearly used; a new
	// chat, named with a CPA prefix, goes to a, but the chat keeps both of its bindings, which its requests to the other
	// allowance didn't overwrite.
	f.reply["a"] = quotaReply(map[string][2]float64{"gemini-5h": {.9, 2}, "gemini-weekly": {.8, 72}, "3p-5h": {.03, 2}, "3p-weekly": {.5, 72}})
	agyPick(t, p, "agy:x", "gemini-4-neon", 360)
	if got := agyPick(t, p, "agy:new", "team/gemini-4-neon", 361); got != "a" {
		t.Errorf("new gemini chat on %s, want a", got)
	}
	if got := agyPick(t, p, "agy:s", "gemini-4-neon", 362); got != "b" {
		t.Errorf("the chat's gemini request moved to %s, want b", got)
	}
	if got := agyPick(t, p, "agy:s", "claude-opus-5-5-thinking", 363); got != "a" {
		t.Errorf("the chat's claude request moved to %s, want a", got)
	}
	// A response keeps the chat's binding past the hour since it was placed.
	agyUse(t, p, "b", "agy:s", 3000, tokens(1000))
	if got := agyPick(t, p, "agy:s", "gemini-4-neon", 4000); got != "b" {
		t.Errorf("an active chat moved to %s after an hour, want b", got)
	}
}

// The captured reply reads as a Gemini allowance little used and a Claude and GPT one unused, so a, with the room,
// takes new chats to both; with its Gemini weekly fraction gone, as Google omits a used-up bucket's, Gemini chats go to b.
func TestAntigravityQuotaFixture(t *testing.T) {
	spent := strings.Replace(quotaFixture, `,"remainingFraction":0.80813533`, "", 1)
	for body, gemini := range map[string]string{quotaFixture: "a", spent: "b"} {
		p, f := newFakeQuotaHost(t)
		f.reply["a"] = body
		agyPick(t, p, "agy:warmup", "gemini-4-neon", 0)
		for model, want := range map[string]string{"gemini-4-neon": gemini, "claude-opus-5-5-thinking": "a"} {
			if got := agyPick(t, p, "agy:"+model, model, 1); got != want {
				t.Errorf("%s on %s, want %s", model, got, want)
			}
		}
	}
}

// A poll that fails after one succeeded leaves routing working on what that one showed, until none has succeeded for
// 15 minutes.
func TestAntigravityFailedPoll(t *testing.T) {
	for _, failure := range []string{"status 401", "host error", "not json", `{"groups":[]}`,
		`{"groups":[{"buckets":[{"bucketId":"gemini-weekly","remainingFraction":1},{"bucketId":"gemini-5h","resetTime":"soon"}]}]}`} {
		for _, tt := range []struct{ name, before, want string }{{"exhausted", geminiExhausted, "b"}, {"in use", geminiInUse, "a"}} {
			t.Run(tt.name+"/"+failure, func(t *testing.T) {
				p, f := newFakeQuotaHost(t)
				f.reply["a"] = tt.before
				agyPick(t, p, "agy:warmup", "gemini-4-neon", 0)
				f.reply["a"] = failure
				agyPick(t, p, "agy:warmup", "gemini-4-neon", 310) // 5 min 10 s on
				if got := agyPick(t, p, "agy:later", "gemini-4-neon", 311); got != tt.want {
					t.Errorf("after a failed poll, gemini on %s, want %s", got, tt.want)
				}
				agyPick(t, p, "agy:later", "gemini-4-neon", 1000)
				if got := agyTry(t, p, "agy:later", "gemini-4-neon", 1001); got.Handled {
					t.Errorf("handled on %s after 15 min of failed polls", got.AuthID)
				}
				if f.polls["a"] != 3 {
					t.Errorf("a polled %d times, want 3", f.polls["a"])
				}
			})
		}
	}
}

// An auth whose quota can't be read would look unused and take every new chat, so CPA routes the picks it is a
// candidate for once its polls have failed since the start or for 15 minutes, until one succeeds. Those picks still
// poll it.
func TestAntigravityUnreadQuota(t *testing.T) {
	p, f := newFakeQuotaHost(t)
	f.reply["a"] = "host error"
	for _, step := range []struct {
		at      float64
		reply   string
		handled bool
	}{
		{0, "", true}, // a's first poll is still in flight
		{60, "", false},
		{310, geminiInUse, false}, // polls a, which succeeds after the pick
		{311, "", true},
		{620, "", true}, // polls a, which succeeds again
		{930, "host error", true},
		{1240, "", true},  // a's last poll failed 5 min after the latest success
		{1550, "", true},  // and this one 10.3 min after it, though 15.5 after the first
		{1551, "", false}, // and this one 15.5 min after it
	} {
		if step.reply != "" {
			f.reply["a"] = step.reply
		}
		if got := agyTry(t, p, "agy:s", "gemini-4-neon", step.at); got.Handled != step.handled {
			t.Errorf("pick at %v s: handled %v, want %v", step.at, got.Handled, step.handled)
		}
	}
	if n := f.polls["a"]; n != 6 {
		t.Errorf("a polled %d times, want 6", n)
	}
}

// A chat CPA routed while a candidate was blind stays where CPA kept it once that candidate's quota reads again.
func TestAntigravityBlindKeepsChat(t *testing.T) {
	p, f := newFakeQuotaHost(t)
	f.reply["a"] = "host error"
	agyTry(t, p, "agy:warmup", "gemini-4-neon", 0)
	for _, at := range []float64{60, 120} {
		if got := agyTry(t, p, "agy:c", "gemini-4-neon", at); got.Handled {
			t.Fatalf("pick at %v s handled, on %s", at, got.AuthID)
		}
		agyUse(t, p, "b", "agy:c", at+1, tokens(1000))
	}
	agyUse(t, p, "b", "agy:c", 122, tokens(1000))
	delete(f.reply, "a")
	agyTry(t, p, "agy:warmup", "gemini-4-neon", 310) // polls a, which succeeds after the pick
	if got := agyPick(t, p, "agy:c", "gemini-4-neon", 311); got != "b" {
		t.Errorf("the chat moved to %s, want b", got)
	}
}

// An account is polled again a minute after it serves, 10 seconds after a 429 (CPA benches only the model that got it,
// so its other models stay candidates until a poll shows the allowance out), and when a pick finds its last poll 5
// minutes old. Other failures poll nothing.
func TestAntigravityPollTriggers(t *testing.T) {
	p, f := newFakeQuotaHost(t)
	if got := agyPick(t, p, "agy:s", "gemini-4-neon", 0); got != "a" {
		t.Fatalf("gemini on %s, want a", got)
	}
	polls := func(when string, a, b int) {
		t.Helper()
		if f.polls["a"] != a || f.polls["b"] != b {
			t.Errorf("%s: a polled %d times and b %d, want %d and %d", when, f.polls["a"], f.polls["b"], a, b)
		}
	}
	agyUse(t, p, "a", "agy:s", 50, tokens(1000))
	polls("a response within a minute of the poll", 1, 1)
	agyUse(t, p, "a", "agy:s", 65, pluginapi.UsageRecord{Failed: true, Failure: pluginapi.UsageFailure{StatusCode: http.StatusInternalServerError}})
	polls("a 500 65 s after the last poll", 1, 1)
	agyPick(t, p, "agy:s", "gemini-4-neon", 90)
	polls("a pick 90 s in, after a served", 2, 1)
	f.reply["a"] = geminiExhausted
	failed := pluginapi.UsageRecord{Failed: true, Failure: pluginapi.UsageFailure{StatusCode: http.StatusTooManyRequests,
		Body: `{"error":{"code":429,"details":[{"reason":"QUOTA_EXHAUSTED","metadata":{"model":"gemini-4-neon"}}]}}`}}
	agyUse(t, p, "a", "agy:s", 101, failed)
	polls("a 429 11 s after the last poll", 3, 1)
	agyUse(t, p, "a", "agy:s", 110, failed) // 9 s after the last poll
	polls("two 429s", 3, 1)
	if got := agyPick(t, p, "agy:s", "gemini-4-flash", 111); got != "b" {
		t.Errorf("sibling gemini model on %s, want b: a's Gemini allowance is out", got)
	}
	agyPick(t, p, "agy:s", "gemini-4-flash", 299)
	polls("a pick 299 s after b's last poll", 3, 1)
	agyPick(t, p, "agy:s", "gemini-4-flash", 301)
	polls("a pick 301 s after b's last poll", 3, 2)
}

// A poll runs apart from the pick that starts it, and an account has one poll at a time.
func TestAntigravityPollInBackground(t *testing.T) {
	p, f := newFakeQuotaHost(t)
	gate := make(chan struct{})
	f.hold = map[string]chan struct{}{"a": gate}
	release := sync.OnceFunc(func() { close(gate) })
	t.Cleanup(release)
	raw, _ := json.Marshal(agyRequest("agy:s", "gemini-4-neon"))
	picked := make(chan []byte, 1)
	go func() { picked <- p.dispatch(pluginabi.MethodSchedulerPick, raw, placementNow) }()
	select {
	case out := <-picked:
		var env pluginabi.Envelope
		var got pluginapi.SchedulerPickResponse
		if json.Unmarshal(out, &env) != nil || json.Unmarshal(env.Result, &got) != nil || !got.Handled {
			t.Errorf("pick: %s", out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the pick waited for a's poll")
	}
	call[struct{}](t, p, pluginabi.MethodUsageHandle, pluginapi.UsageRecord{Provider: "antigravity", AuthID: "a", SessionID: "agy:s",
		Alias: "gemini-4-neon", Failed: true, Failure: pluginapi.UsageFailure{StatusCode: http.StatusTooManyRequests}}, placementNow+20)
	call[pluginapi.SchedulerPickResponse](t, p, pluginabi.MethodSchedulerPick, agyRequest("agy:s2", "gemini-4-neon"), placementNow+400) // a's quota is stale
	release()
	p.fetching.Wait()
	if n := f.polls["a"]; n != 1 {
		t.Errorf("a polled %d times while its poll was in flight, want 1", n)
	}
}

// Each poll sets an allowance's movement since the last against the cost served in between, kept across a failed poll,
// so a new chat goes where a token costs the least allowance.
func TestAntigravityCalibration(t *testing.T) {
	used := func(u float64) string {
		return quotaReply(map[string][2]float64{"gemini-5h": {1 - u, 0.5}, "gemini-weekly": {1, 72}, "3p-5h": {1, 0}, "3p-weekly": {1, 0}})
	}
	tests := []struct {
		name             string
		aReply           string // to the poll after a serves
		aTokens, bTokens int64  // input tokens each serves while its 5h window moves 0.1
	}{
		{"a's tokens cost the allowance more", used(0.1), 1_000_000, 1_200_000},
		{"a's poll fails, the next counts its tokens", "status 401", 500_000, 666_667},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, f := newFakeQuotaHost(t)
			f.reply["a"], f.reply["b"] = used(0), used(0)
			agyPick(t, p, "agy:w", "gemini-4-neon", 0)
			f.reply["a"], f.reply["b"] = tt.aReply, used(0.1)
			agyUse(t, p, "a", "agy:ua", 70, tokens(tt.aTokens))
			agyUse(t, p, "b", "agy:ub", 70, tokens(tt.bTokens))
			f.reply["a"] = used(0.1)
			agyUse(t, p, "a", "agy:ua", 140, tokens(0)) // nothing served or moved since a's last poll
			if f.polls["a"] != 3 || f.polls["b"] != 2 {
				t.Fatalf("a polled %d times and b %d, want 3 and 2", f.polls["a"], f.polls["b"])
			}
			if got := agyPick(t, p, "agy:new", "gemini-4-neon", 150); got != "b" {
				t.Errorf("new chat on %s, want b: a spends more of its allowance per token", got)
			}
		})
	}
}

// An auth with its own proxy is never polled, as the host would send its token through CPA's proxy, nor one without a
// project, for which only the Claude and GPT allowance is reported; CPA routes the picks it is a candidate for once
// that shows. Its auth file is read only when a poll is due.
func TestAntigravityUnpollable(t *testing.T) {
	for _, file := range []string{
		`{"access_token":"tok-b","project_id":"proj-b","proxy_url":"socks5://10.0.0.2:1080"}`,
		`{"access_token":"tok-b"}`,
	} {
		p, f := newFakeQuotaHost(t)
		f.files["b"] = file
		agyPick(t, p, "agy:s", "gemini-4-neon", 0)
		for _, at := range []float64{60, 120} {
			if got := agyTry(t, p, "agy:new", "gemini-4-neon", at); got.Handled {
				t.Errorf("%s: pick at %v s handled, on %s", file, at, got.AuthID)
			}
		}
		if f.polls["a"] != 1 || f.polls["b"] != 0 || f.gets["b"] != 1 {
			t.Errorf("%s: polls %v, auth files read %v; want a polled once, b's file read once and b never polled", file, f.polls, f.gets)
		}
	}
}

// The library outlives CPA unloading it, so polls resume once CPA loads it again.
func TestAntigravityPollsAfterReload(t *testing.T) {
	p, f := newFakeQuotaHost(t)
	old := global
	global = p
	t.Cleanup(func() { global = old })
	call[pluginapi.SchedulerPickResponse](t, global, pluginabi.MethodSchedulerPick, agyRequest("agy:s", "gemini-4-neon"), placementNow)
	cliproxyPluginShutdown()
	agyPick(t, global, "agy:s", "gemini-4-neon", 400)
	if n := f.polls["a"]; n != 2 {
		t.Errorf("a polled %d times across a reload, want 2", n)
	}
}
