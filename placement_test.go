package main

import (
	"fmt"
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
}

type placementStep struct {
	at          float64
	sid, parent string
	model       string
	candidates  string // account IDs in order
	quota       *placementQuota
	auth        string
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
		{"warm, child, fork, failover and retained root", "claude", []float64{5, 20, 20},
			[]placementQuota{{used5: .1, usedW: .8, days: 1}, {used5: .1, usedW: .7, days: 6}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{
				{sid: "root", candidates: "b", want: "b"},
				{sid: "root", auth: "b", quota: &placementQuota{used5: .1, usedW: .7, days: 6}},
				{at: 1, sid: "root", candidates: "abc", want: "b"},
				{at: 2, sid: "fork", parent: "root", candidates: "abc", want: "b"},
				{at: 3, sid: "root:agent:child", parent: "root", candidates: "ac", want: "c"},
				{at: 4, sid: "root:agent:child", parent: "root", candidates: "ac", want: "c"},
				{at: 5, sid: "root", auth: "b", quota: &placementQuota{used5: 1, usedW: .7, days: 6}},
				{at: 6, sid: "root", candidates: "abc", want: "c"},
				{at: 3700, sid: "root", candidates: "abc", want: "c"},
			}},
		{"subagents follow the parent's account", "claude", []float64{5, 20},
			[]placementQuota{{used5: .1, usedW: .8, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{
				{sid: "root", candidates: "a", want: "a"},
				{at: 1, sid: "root:agent:child", parent: "root", candidates: "ab", want: "a"},
				{at: 2, sid: "root:agent:nested", parent: "root:agent:child", candidates: "ab", want: "a"},
			}},
		{"Codex subagents follow the parent's account", "codex", []float64{5, 20},
			[]placementQuota{{used5: .1, usedW: .8, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{
				{sid: "root", candidates: "a", want: "a"},
				{at: 1, sid: "root:agent:child", parent: "root", candidates: "ab", want: "a"},
			}},
		{"retained usage history without a binding", "claude", []float64{5, 20},
			[]placementQuota{{used5: .1, usedW: .8, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{
				{sid: "history", auth: "b", quota: &placementQuota{used5: .1, usedW: .7, days: 6}},
				{at: 1, sid: "history", candidates: "ab", want: "b"},
			}},
		{"Codex weekly-only unchanged", "codex", []float64{5, 20},
			[]placementQuota{{usedW: .8, days: 1, weeklyOnly: true}, {usedW: .7, days: 6, weeklyOnly: true}},
			[]placementStep{{sid: "fresh", candidates: "ab", want: "b"}}},
		{"Codex dual-window unchanged", "codex", []float64{5, 20},
			[]placementQuota{{used5: .1, usedW: .8, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{{sid: "fresh", candidates: "ab", want: "b"}}},
		{"a new chat goes where its model's prompt prefix is cached", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .7, days: 6}, {used5: .1, usedW: .8, days: 1}},
			[]placementStep{
				{sid: "fresh", model: "m", candidates: "ab", want: "b"},
				{at: 1, sid: "other", model: "m", auth: "a", quota: &placementQuota{used5: .1, usedW: .7, days: 6}},
				{at: 2, sid: "fresh2", model: "m", candidates: "ab", want: "a"},
				{at: 3, sid: "fresh3", model: "n", candidates: "ab", want: "b"},
				{at: 3600, sid: "fresh4", model: "m", candidates: "ab", want: "b"},
			}},
		{"a cached prefix does not override the margins", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .7, days: 6}, {used5: .1, usedW: .8, days: 1}},
			[]placementStep{
				{at: 1, sid: "other", model: "m", auth: "a", quota: &placementQuota{used5: .97, usedW: .7, days: 6}},
				{at: 2, sid: "fresh", model: "m", candidates: "ab", want: "b"},
			}},
		{"Codex ignores cached prefixes", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .5, days: 6, weeklyOnly: true}, {usedW: .5, days: 1, weeklyOnly: true}},
			[]placementStep{
				{at: 1, sid: "other", model: "m", auth: "a", quota: &placementQuota{usedW: .5, days: 6, weeklyOnly: true}},
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
			p := newPlugin()
			pick := func(s placementStep) string {
				req := pickRequest(tt.provider, s.sid)
				req.Model = s.model
				if s.parent != "" {
					req.Options.Metadata["parent_session_id"] = s.parent
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

func placementObserve(t *testing.T, p *plugin, provider string, s placementStep) {
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
		h.Set("Anthropic-Ratelimit-Unified-7d-Utilization", f(q.usedW))
		h.Set("Anthropic-Ratelimit-Unified-7d-Reset", f(placementNow+86400*q.days))
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
	call[struct{}](t, p, pluginabi.MethodUsageHandle, pluginapi.UsageRecord{Provider: provider, Model: s.model,
		AuthID: s.auth, SessionID: s.sid, ParentSessionID: s.parent, ResponseHeaders: h}, placementNow+s.at)
}
