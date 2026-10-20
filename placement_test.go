package main

import (
	"cmp"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const placementNow = 1790060400.0

type placementQuota struct {
	used5, usedW, days float64
	expired5           bool
	weeklyOnly         bool
	noWeekly           bool    // Claude's response carries no weekly headers
	tokens             float64 // input tokens the usage record reports
}

type placementStep struct {
	at          float64
	sid, parent string
	model       string
	alias       string // the client's model name: pick's requested_model and the usage record's Alias
	upstream    string // the usage record's Model when it differs from model
	candidates  string // account IDs in order
	quota       *placementQuota
	auth        string
	failed      bool   // the usage record reports a failed request
	toolSearch  bool   // the pick carries Claude Code's tool-search beta header
	betaSplit   bool   // with toolSearch, the beta names arrive as two Anthropic-Beta values, not one
	want        string // independently specified pick, never computed by this router
}

// Each case seeds the accounts' observed usage, then checks picks and further usage reports in order.
func TestPlacement(t *testing.T) {
	tests := []struct {
		name, provider string
		weights        []float64
		initial        []placementQuota
		steps          []placementStep
	}{
		{"elapsed-week deficit", "claude", []float64{5, 20},
			[]placementQuota{{used5: .1, usedW: .8, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{{sid: "fresh", candidates: "ab", want: "a"}}},
		{"fractional deficit, not absolute capacity", "claude", []float64{5, 20},
			[]placementQuota{{used5: .1, usedW: .2, days: 3.5}, {used5: .1, usedW: .35, days: 3.5}},
			[]placementStep{{sid: "fresh", candidates: "ab", want: "a"}}},
		{"observed admission after a burst", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .2, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{
				{at: 1, sid: "burn-a", auth: "a", quota: &placementQuota{used5: .2, usedW: .3, days: 1}},
				{at: 2, sid: "fresh", candidates: "ab", want: "a"},
				{at: 3, sid: "burn-a", auth: "a", quota: &placementQuota{used5: .96, usedW: .3, days: 1}},
				{at: 4, sid: "fresh2", candidates: "ab", want: "b"},
			}},
		{"projected weekly guard retained", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .8, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{
				{at: 1, sid: "burn-a", auth: "a", quota: &placementQuota{used5: .1, usedW: .95, days: 1}},
				{at: 2, sid: "fresh", candidates: "ab", want: "b"},
			}},
		{"weekly expiry waiver retained", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .98, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{{sid: "fresh", candidates: "ab", want: "a"}}},
		{"expired five-hour window", "claude", []float64{5, 20},
			[]placementQuota{{used5: .96, usedW: .8, days: 1, expired5: true}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{{sid: "fresh", candidates: "ab", want: "a"}}},
		{"five-hour margin is inclusive", "claude", []float64{20, 20},
			[]placementQuota{{used5: .95, usedW: .8, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{
				{sid: "fresh", candidates: "ab", want: "a"},
				{at: 1, sid: "burn-a", auth: "a", quota: &placementQuota{used5: .95001, usedW: .8, days: 1}},
				{at: 2, sid: "fresh2", candidates: "ab", want: "b"},
			}},
		{"all guarded accounts retain projected fallback", "claude", []float64{5, 20},
			[]placementQuota{{used5: .99, usedW: .8, days: 1}, {used5: .995, usedW: .7, days: 6}},
			[]placementStep{{at: 1, sid: "fresh", candidates: "ab", want: "b"}}},
		{"unobserved windows break ties by projected capacity", "claude", []float64{5, 20}, nil,
			[]placementStep{{sid: "fresh", candidates: "ab", want: "b"}}},
		{"equal deficit and headroom preserve candidate order", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .2, days: 3.5}, {used5: .1, usedW: .2, days: 3.5}},
			[]placementStep{{sid: "fresh", candidates: "ba", want: "b"}}},
		{"a fork follows its parent chat's binding", "claude", []float64{20, 20},
			[]placementQuota{{used5: .5, usedW: .5, days: 3}, {used5: .1, usedW: .5, days: 3}},
			[]placementStep{
				{sid: "root", candidates: "a", want: "a"},
				{at: 1, sid: "fork", parent: "root", candidates: "ab", want: "a"},
			}},
		{"a family's subagents share a binding apart from the chat's, sliding with their usage; with tool search each is placed alone, however the beta header arrives", "claude",
			[]float64{5, 20}, []placementQuota{{used5: .1, usedW: .8, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{
				{sid: "root", candidates: "a", want: "a"},
				{at: 1, sid: "root:agent:child", parent: "root", candidates: "ab", want: "b"},
				{at: 2, sid: "root:agent:child", parent: "root", auth: "b", quota: &placementQuota{used5: .96, usedW: .7, days: 6}},
				{at: 3, sid: "root:agent:sibling", parent: "root", candidates: "ab", want: "b"},
				{at: 4, sid: "root:agent:nested", parent: "root:agent:child", candidates: "ab", want: "b"},
				{at: 5, sid: "root", candidates: "ab", want: "a"},
				{at: 6, sid: "root:agent:searcher", parent: "root", toolSearch: true, betaSplit: true, candidates: "ab", want: "a"},
				// the family's binding was last set at 4 s; the child's usage at 50 min slides it, so the sibling still follows it
				{at: 50 * 60, sid: "root:agent:child", parent: "root", auth: "b", quota: &placementQuota{used5: .96, usedW: .7, days: 6}},
				{at: 100 * 60, sid: "root:agent:sibling", parent: "root", candidates: "ab", want: "b"},
			}},
		{"a tool-search subagent's own binding slides with its usage", "claude", []float64{5, 20},
			[]placementQuota{{used5: .1, usedW: .8, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{
				{sid: "r:agent:s", parent: "r", toolSearch: true, candidates: "a", want: "a"},
				{at: 50 * 60, sid: "r:agent:s", parent: "r", auth: "a", quota: &placementQuota{used5: .1, usedW: .8, days: 1}},
				{at: 100 * 60, sid: "r:agent:s", parent: "r", toolSearch: true, candidates: "ab", want: "a"},
			}},
		{"retained usage history without a binding", "claude", []float64{5, 20},
			[]placementQuota{{used5: .1, usedW: .8, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{
				{sid: "history", auth: "b", quota: &placementQuota{used5: .1, usedW: .7, days: 6}},
				{at: 1, sid: "history", candidates: "ab", want: "b"},
			}},
		{"Codex reopens an expired weekly window first", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .5, days: -1, weeklyOnly: true}, {usedW: .1, days: 6, weeklyOnly: true}},
			[]placementStep{{sid: "fresh", candidates: "ab", want: "a"}}},
		{"allowance about to expire outweighs five-hour room under weekly pressure", "codex", []float64{20, 20},
			[]placementQuota{{used5: .6, usedW: .75, days: .5}, {used5: .1, usedW: .75, days: 6}},
			[]placementStep{{sid: "fresh", candidates: "ab", want: "a"}}},
		{"a new chat goes where its model's prompt prefix is cached", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .7, days: 6}, {used5: .1, usedW: .8, days: 1}},
			[]placementStep{
				{sid: "fresh", model: "m", candidates: "ab", want: "b"},
				{at: 1, sid: "other", model: "m", auth: "a", quota: &placementQuota{used5: .1, usedW: .7, days: 6}},
				{at: 2, sid: "fresh2", model: "m", candidates: "ab", want: "a"},
				{at: 3, sid: "fresh", model: "n", auth: "a", failed: true, quota: &placementQuota{used5: .1, usedW: .7, days: 6}},
				{at: 4, sid: "fresh3", model: "n", candidates: "ab", want: "b"},
				{at: 5, sid: "fresh", model: "m", candidates: "ab", want: "b"},
				// a subagent's prefix is its own, cached for 5 minutes (4 with the margin); a main chat's for 55
				{at: 6, sid: "x:agent:s", parent: "x", model: "m", candidates: "ab", want: "b"},
				{at: 7, sid: "y:agent:s", parent: "y", model: "m", auth: "a", quota: &placementQuota{used5: .1, usedW: .7, days: 6}},
				{at: 7 + 230, sid: "z:agent:s", parent: "z", model: "m", candidates: "ab", want: "a"},
				{at: 7 + 270, sid: "w:agent:s", parent: "w", model: "m", candidates: "ab", want: "b"},
				{at: 1 + 54*60, sid: "fresh4", model: "m", candidates: "ab", want: "a"},
				{at: 1 + 56*60, sid: "fresh5", model: "m", candidates: "ab", want: "b"},
			}},
		{"a cached prefix does not override the margins", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .7, days: 6}, {used5: .1, usedW: .8, days: 1}},
			[]placementStep{
				{at: 1, sid: "other", model: "m", auth: "a", quota: &placementQuota{used5: .97, usedW: .7, days: 6}},
				{at: 2, sid: "fresh", model: "m", candidates: "ab", want: "b"},
			}},
		// a is cold for m and placed first; b is warm but its projected five-hour use fails, so c, warm and fitting, takes the chat
		{"a warm account that fits takes a new chat when the first warm choice fails the margins", "claude", []float64{20, 20, 20},
			[]placementQuota{{used5: .1, days: 6}, {used5: .85, usedW: .05, days: 6}, {used5: .1, usedW: .12, days: 6}},
			[]placementStep{
				{at: 1, sid: "other", model: "m", auth: "b", quota: &placementQuota{used5: .9, usedW: .05, days: 6}},
				{at: 2, sid: "other2", model: "m", auth: "c", quota: &placementQuota{used5: .1, usedW: .12, days: 6}},
				{at: 3, sid: "fresh", model: "m", candidates: "abc", want: "c"},
			}},
		// a is cold for m and furthest behind its weekly target, so it wins the cold choice; b has the most projected
		// headroom and c the larger weekly deficit, so the warm choice must follow the deficit, not the headroom
		{"a warm chat takes the account furthest behind its weekly target, not the one with most headroom", "claude", []float64{20, 20, 20},
			[]placementQuota{{used5: .1, usedW: .1, days: 3.5}, {used5: .05, usedW: .05, days: 6}, {used5: .3, usedW: .2, days: 3.5}},
			[]placementStep{
				{at: 1, sid: "other", model: "m", auth: "b", quota: &placementQuota{used5: .05, usedW: .05, days: 6}},
				{at: 2, sid: "other2", model: "m", auth: "c", quota: &placementQuota{used5: .3, usedW: .2, days: 3.5}},
				{at: 3, sid: "fresh", model: "m", candidates: "abc", want: "c"},
			}},
		// With a day to the weekly reset, a's urgency is 1.25 per unit of its unused allowance: a at .259999 leads b at .5
		// by 1.25 * .240001 (just over 0.3, keeps a); a at .260001 by 1.25 * .239999 (just under, moves to b). The
		// boundary is probed on either side of 0.3 rather than at it: an exact-equality case would depend on float
		// operation order.
		{"a cold chat keeps its account at a lead just over 0.3", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .259999, days: 1}, {used5: .1, usedW: .5, days: 1}},
			[]placementStep{
				{sid: "other", model: "m", auth: "b", quota: &placementQuota{used5: .1, usedW: .5, days: 1}},
				{sid: "fresh", model: "m", candidates: "ab", want: "a"},
			}},
		{"a cold chat moves to the warm account at a lead just under 0.3", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .260001, days: 1}, {used5: .1, usedW: .5, days: 1}},
			[]placementStep{
				{sid: "other", model: "m", auth: "b", quota: &placementQuota{used5: .1, usedW: .5, days: 1}},
				{sid: "fresh", model: "m", candidates: "ab", want: "b"},
			}},
		{"a cold chat moves to the warm account when the warm one's allowance is expiring faster", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: 0, days: 3.5}, {used5: .1, usedW: .7, days: .4}},
			[]placementStep{
				{sid: "other", model: "m", auth: "b", quota: &placementQuota{used5: .1, usedW: .7, days: .4}},
				{sid: "fresh", model: "m", candidates: "ab", want: "b"},
			}},
		{"a cold chat moves to a warm account projected to overrun its week", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .3, days: 6}, {used5: .1, usedW: .2, days: 5}},
			[]placementStep{
				{at: 60, sid: "burn-b", model: "x", auth: "b", quota: &placementQuota{used5: .1, usedW: .6, days: 5}},
				{at: 3*3600 - 600, sid: "other", model: "m", auth: "b", quota: &placementQuota{used5: .1, usedW: .6, days: 5}},
				{at: 3 * 3600, sid: "fresh", model: "m", candidates: "ab", want: "b"},
			}},
		{"a cold chat's urgency reads the weekly window elapsed so far, not the last stored one", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .9, days: .1}, {used5: .1, usedW: .7, days: 3}},
			[]placementStep{
				{at: 3*3600 - 600, sid: "other", model: "m", auth: "b", quota: &placementQuota{used5: .1, usedW: .7, days: 3}},
				{at: 3 * 3600, sid: "fresh", model: "m", candidates: "ab", want: "b"},
			}},
		// CPA picks with the routed model and reports usage under the upstream one; both key on the client's name.
		{"warm cache keys on the client's model", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .7, days: 6}, {used5: .1, usedW: .8, days: 1}},
			[]placementStep{
				{at: 1, sid: "other", model: "claude-opus-5-5", alias: "smart", upstream: "claude-opus-5-5-20261001", auth: "a",
					quota: &placementQuota{used5: .1, usedW: .7, days: 6}},
				{at: 2, sid: "fresh", model: "claude-opus-5-5", alias: "smart", candidates: "ab", want: "a"},
			}},
		// Codex must skip the Claude-only warm step and rank weekly-only accounts by expiry urgency, not spare room. Equal
		// weights and weekly use; a's reset is 2.2 days out and b's 2, so urgencies are .284 for a and .3125 for b and b wins
		// on urgency. The lead, .028, is under keepCold's 0.3, so a Codex pick that ran the warm step would move to a. It is
		// also under 0.05, so the case still catches that warm step if keepCold is lowered to 0.05.
		{"Codex ignores cached prefixes", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .5, days: 2.2, weeklyOnly: true}, {usedW: .5, days: 2, weeklyOnly: true}},
			[]placementStep{
				{at: 1, sid: "other", model: "m", auth: "a", quota: &placementQuota{usedW: .5, days: 2.2, weeklyOnly: true}},
				{at: 2, sid: "fresh", model: "m", candidates: "ab", want: "b"},
			}},
		{"Codex leaves an account spending credits", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .5, days: 3, weeklyOnly: true}, {usedW: .5, days: 3, weeklyOnly: true}},
			[]placementStep{
				{sid: "chat", candidates: "ab", want: "a"},
				{at: 1, sid: "chat", auth: "a", quota: &placementQuota{usedW: 1, days: 3, weeklyOnly: true}},
				{at: 2, sid: "chat", candidates: "ab", want: "b"},
				{at: 3, sid: "fresh", candidates: "ab", want: "b"},
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newRouter()
			pick := func(s placementStep) string {
				req := pickRequest(tt.provider, s.sid)
				req.Model = s.model
				if s.parent != "" {
					req.Options.Metadata["parent_session_id"] = s.parent
				}
				req.Options.Metadata["requested_model"] = cmp.Or(s.alias, s.model)
				if s.toolSearch {
					beta := []string{"claude-code-20250219,advanced-tool-use-2025-11-20"}
					if s.betaSplit {
						beta = []string{"claude-code-20250219", "advanced-tool-use-2025-11-20"}
					}
					req.Options.Headers = http.Header{"Anthropic-Beta": beta}
				}
				for _, id := range s.candidates {
					req.Candidates = append(req.Candidates, pluginapi.SchedulerAuthCandidate{ID: string(id), Provider: tt.provider,
						Attributes: map[string]string{"weight": strconv.FormatFloat(tt.weights[int(id-'a')], 'f', -1, 64)}})
				}
				got := call[pluginapi.SchedulerPickResponse](t, p, pluginabi.MethodSchedulerPick, req, placementNow+s.at)
				if !got.Handled {
					t.Fatal("pick unexpectedly fell back to the built-in selector")
				}
				return got.AuthID
			}
			for i, q := range tt.initial {
				id, sid := string(rune('a'+i)), fmt.Sprintf("seed%d", i)
				pick(placementStep{sid: sid, candidates: id})
				placementObserve(t, p, tt.provider, placementStep{sid: sid, auth: id, quota: &q})
			}
			for i, s := range tt.steps {
				if s.quota != nil {
					placementObserve(t, p, tt.provider, s)
				} else if got := pick(s); got != s.want {
					t.Errorf("step %d: picked %s, want %s", i, got, s.want)
				}
			}
		})
	}
}

// TestExpiryUrgency reads an account's expiryUrgency after each table's observations, at the seconds the table gives.
func TestExpiryUrgency(t *testing.T) {
	oneWeek := []placementQuota{{used5: .1, usedW: .5, days: 2}}
	tests := []struct {
		name   string
		quotas []placementQuota // observed in order, all at placementNow
		after  float64          // seconds after placementNow at which the urgency is read
		want   float64
	}{
		{"a five-hour window seen without the weekly one", []placementQuota{{used5: .5, noWeekly: true, days: 2}}, 0, 0},
		// half the week unused, two days to the reset: 0.5 * 300 / (2880 / 6), with cap5/capW = 7.5/45 before calibration
		{"before calibration, a five-hour window is cap5/capW of the week", oneWeek, 0, 0.3125},
		// Each case opens new windows, so its totals are exactly the header values. math.Nextafter puts the window under
		// test just below its threshold, where subtracting decimal readings could land on the wrong side of it. The
		// other window's value gives a ratio other than the prior 1/6 if the threshold were loosened.
		// the weekly use is the float just under .01, so the ratio stays prior: 0.96 * 300 / (4320 / 6), where the weekly
		// burn term is .01 * 4320 / 1440 = .03 and 1 - .01 - .03 = .96
		{"weekly use under .01 keeps the prior ratio even with five-hour use past its threshold", []placementQuota{
			{used5: .1, usedW: .5, days: 2}, {used5: .2, expired5: true, usedW: math.Nextafter(.01, 0), days: 3, tokens: 1e6}}, 0, 0.96 * 300 / (4320 / 6)},
		// the five-hour use is the float just under .05, so the ratio stays prior: 0.72 * 300 / (4320 / 6), where the weekly
		// burn term is .07 * 4320 / 1440 = .21 and 1 - .07 - .21 = .72
		{"five-hour use under .05 keeps the prior ratio even with weekly use past its threshold", []placementQuota{
			{used5: .1, usedW: .5, days: 2}, {used5: math.Nextafter(.05, 0), expired5: true, usedW: .07, days: 3, tokens: 1e6}}, 0, 0.72 * 300 / (4320 / 6)},
		// the second report rises .051 five-hour and .011 weekly, both past their thresholds, so the ratio is .011 / .051.
		// The weekly burn term is .011 * 2880 / 1440 = .022, so 1 - .511 - .022 = .467: 0.467 * 300 / (2880 * .011 / .051)
		{"after calibration, a five-hour window is the observed weekly share", []placementQuota{
			{used5: .1, usedW: .5, days: 2, tokens: 1e6}, {used5: .151, usedW: .511, days: 2, tokens: 1e6}}, 0, 0.467 * 300 * .051 / (2880 * .011)},
		// the second report sits exactly on both thresholds; both windows are new, so the totals are the header values
		// (.05 and .01). The ratio is .01 / .05 = .2 and the weekly burn term is .01 * 4320 / 1440 = .03, so
		// 1 - .01 - .03 = .96: 0.96 * 300 / (4320 * .2). The tokens give the cost totals a value, as in the other cases.
		{"exactly at both thresholds, the ratio is calibrated", []placementQuota{
			{used5: .1, usedW: .5, days: 2}, {used5: .05, usedW: .01, days: 3, expired5: true, tokens: 1e6}}, 0, 0.96 * 300 / (4320 * .2)},
		{"grows as the reset nears: one day out", oneWeek, 86400, 0.625},
		{"grows as the reset nears: half a day out", oneWeek, 1.5 * 86400, 1.25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newRouter()
			a := p.account("a", "claude", 20)
			for i := range tt.quotas {
				placementObserve(t, p, "claude", placementStep{sid: "seed", auth: "a", quota: &tt.quotas[i]})
			}
			if got := a.expiryUrgency((placementNow + tt.after) / 60); math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("expiryUrgency = %v, want %v", got, tt.want)
			}
		})
	}
}

func placementObserve(t *testing.T, p *router, provider string, s placementStep) {
	t.Helper()
	q := s.quota
	reset5 := placementNow + 18000
	if q.expired5 {
		reset5 = placementNow - 1
	}
	f := func(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }
	h := http.Header{}
	if provider == "claude" {
		h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", f(q.used5))
		h.Set("Anthropic-Ratelimit-Unified-5h-Reset", f(reset5))
		if !q.noWeekly {
			h.Set("Anthropic-Ratelimit-Unified-7d-Utilization", f(q.usedW))
			h.Set("Anthropic-Ratelimit-Unified-7d-Reset", f(placementNow+86400*q.days))
		}
	} else {
		if !q.weeklyOnly {
			h.Set("X-Codex-Primary-Used-Percent", f(100*q.used5))
			h.Set("X-Codex-Primary-Window-Minutes", "300")
			h.Set("X-Codex-Primary-Reset-At", f(reset5))
		}
		h.Set("X-Codex-Secondary-Used-Percent", f(100*q.usedW))
		h.Set("X-Codex-Secondary-Window-Minutes", "10080")
		h.Set("X-Codex-Secondary-Reset-At", f(placementNow+86400*q.days))
	}
	call[struct{}](t, p, pluginabi.MethodUsageHandle, pluginapi.UsageRecord{Provider: provider, Model: cmp.Or(s.upstream, s.model),
		Alias: cmp.Or(s.alias, s.model), AuthID: s.auth, Failed: s.failed, SessionID: s.sid, ParentSessionID: s.parent, ResponseHeaders: h,
		Detail: pluginapi.UsageDetail{InputTokens: int64(q.tokens)}}, placementNow+s.at)
}
