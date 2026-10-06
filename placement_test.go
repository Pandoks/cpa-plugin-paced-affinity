package main

import (
	"encoding/json"
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
	at              float64
	sid, parent     string
	candidates      string // account IDs in order
	quota           *placementQuota
	auth            string
	success         bool
	observed, prior string // independently specified picks, never computed by this router
}

// Literal first-root/guard/lifecycle cases come from the independently tested Rust
// public pick/observe fixtures. Additional rows cover the public admission edges.
func TestClaudePlacement(t *testing.T) {
	tests := []struct {
		name, provider string
		weights        []float64
		initial        []placementQuota
		steps          []placementStep
	}{
		{"elapsed-week deficit", "claude", []float64{5, 20},
			[]placementQuota{{used5: .1, usedW: .8, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{{sid: "fresh", candidates: "ab", observed: "a", prior: "b"}}},
		{"fractional deficit, not absolute capacity", "claude", []float64{5, 20},
			[]placementQuota{{used5: .1, usedW: .2, days: 3.5}, {used5: .1, usedW: .35, days: 3.5}},
			[]placementStep{{sid: "fresh", candidates: "ab", observed: "a", prior: "b"}}},
		{"observed admission after a burst", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .2, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{
				{at: 1, sid: "burn-a", auth: "a", quota: &placementQuota{used5: .2, usedW: .3, days: 1}, success: true},
				{at: 2, sid: "fresh", candidates: "ab", observed: "a", prior: "b"},
				{at: 3, sid: "burn-a", auth: "a", quota: &placementQuota{used5: .96, usedW: .3, days: 1}},
				{at: 4, sid: "fresh2", candidates: "ab", observed: "b", prior: "b"},
			}},
		{"projected weekly guard retained", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .8, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{
				{at: 1, sid: "burn-a", auth: "a", quota: &placementQuota{used5: .1, usedW: .95, days: 1}, success: true},
				{at: 2, sid: "fresh", candidates: "ab", observed: "b", prior: "b"},
			}},
		{"weekly expiry waiver retained", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .98, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{{sid: "fresh", candidates: "ab", observed: "a", prior: "b"}}},
		{"expired five-hour window", "claude", []float64{5, 20},
			[]placementQuota{{used5: .96, usedW: .8, days: 1, expired5: true}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{{sid: "fresh", candidates: "ab", observed: "a", prior: "b"}}},
		{"five-hour margin is inclusive", "claude", []float64{20, 20},
			[]placementQuota{{used5: .95, usedW: .8, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{
				{sid: "fresh", candidates: "ab", observed: "a", prior: "a"},
				{at: 1, sid: "burn-a", auth: "a", quota: &placementQuota{used5: .95001, usedW: .8, days: 1}},
				{at: 2, sid: "fresh2", candidates: "ab", observed: "b", prior: "b"},
			}},
		{"all guarded accounts retain projected fallback", "claude", []float64{5, 20},
			[]placementQuota{{used5: .99, usedW: .8, days: 1}, {used5: .995, usedW: .7, days: 6}},
			[]placementStep{{at: 1, sid: "fresh", candidates: "ab", observed: "b", prior: "b"}}},
		{"unobserved windows break ties by projected capacity", "claude", []float64{5, 20}, nil,
			[]placementStep{{sid: "fresh", candidates: "ab", observed: "b", prior: "b"}}},
		{"equal deficit and headroom preserve candidate order", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .2, days: 3.5}, {used5: .1, usedW: .2, days: 3.5}},
			[]placementStep{{sid: "fresh", candidates: "ba", observed: "b", prior: "b"}}},
		{"warm, child, fork, failover and retained root", "claude", []float64{5, 20, 20},
			[]placementQuota{{used5: .1, usedW: .8, days: 1}, {used5: .1, usedW: .7, days: 6}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{
				{sid: "root", candidates: "b", observed: "b", prior: "b"},
				{sid: "root", auth: "b", quota: &placementQuota{used5: .1, usedW: .7, days: 6}, success: true},
				{at: 1, sid: "root", candidates: "abc", observed: "b", prior: "b"},
				{at: 2, sid: "fork", parent: "root", candidates: "abc", observed: "b", prior: "b"},
				{at: 3, sid: "root:agent:child", parent: "root", candidates: "ac", observed: "c", prior: "c"},
				{at: 4, sid: "root:agent:child", parent: "root", candidates: "ac", observed: "c", prior: "c"},
				{at: 5, sid: "root", auth: "b", quota: &placementQuota{used5: 1, usedW: .7, days: 6}},
				{at: 6, sid: "root", candidates: "abc", observed: "c", prior: "c"},
				{at: 3700, sid: "root", candidates: "abc", observed: "c", prior: "c"},
			}},
		{"retained usage history without a binding", "claude", []float64{5, 20},
			[]placementQuota{{used5: .1, usedW: .8, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{
				{sid: "history", auth: "b", quota: &placementQuota{used5: .1, usedW: .7, days: 6}, success: true},
				{at: 1, sid: "history", candidates: "ab", observed: "b", prior: "b"},
			}},
		{"Codex weekly-only unchanged", "codex", []float64{5, 20},
			[]placementQuota{{usedW: .8, days: 1, weeklyOnly: true}, {usedW: .7, days: 6, weeklyOnly: true}},
			[]placementStep{{sid: "fresh", candidates: "ab", observed: "b", prior: "b"}}},
		{"Codex dual-window unchanged", "codex", []float64{5, 20},
			[]placementQuota{{used5: .1, usedW: .8, days: 1}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{{sid: "fresh", candidates: "ab", observed: "b", prior: "b"}}},
	}
	for _, tt := range tests {
		for _, mode := range []string{"", "projected"} {
			t.Run(tt.name+"/"+mode, func(t *testing.T) {
				p := newPlugin()
				if mode != "" {
					call[map[string]any](t, p, pluginabi.MethodPluginRegister,
						map[string][]byte{"config_yaml": []byte("claude-placement: " + mode + "\n")}, placementNow)
				}
				pick := func(s placementStep) string {
					req := pickRequest(tt.provider, s.sid)
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
						continue
					}
					want := s.observed
					if mode == "projected" {
						want = s.prior
					}
					if got := pick(s); got != want {
						t.Errorf("step %d: picked %s, want %s", i, got, want)
					}
				}
			})
		}
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
	call[struct{}](t, p, pluginabi.MethodUsageHandle, pluginapi.UsageRecord{
		Provider: provider, AuthID: s.auth, SessionID: s.sid, ParentSessionID: s.parent,
		Failed: !s.success, Failure: pluginapi.UsageFailure{StatusCode: http.StatusTooManyRequests},
		ResponseHeaders: h,
	}, placementNow+s.at)
}

func TestClaudePlacementReconfigure(t *testing.T) {
	p := newPlugin()
	status := func() string {
		res := call[pluginapi.ManagementResponse](t, p, pluginabi.MethodManagementHandle, nil, placementNow)
		var body struct {
			Placement string `json:"claude_placement"`
		}
		if err := json.Unmarshal(res.Body, &body); err != nil {
			t.Fatal(err)
		}
		return body.Placement
	}
	configure := func(method, value string, valid bool) {
		raw, _ := json.Marshal(map[string][]byte{"config_yaml": []byte("claude-placement: " + value + "\n")})
		var env pluginabi.Envelope
		if err := json.Unmarshal(p.dispatch(method, raw, placementNow), &env); err != nil {
			t.Fatal(err)
		}
		if env.OK != valid {
			t.Fatalf("%s %q: OK=%v, want %v, error=%v", method, value, env.OK, valid, env.Error)
		}
	}
	if got := status(); got != "observed-weekly" {
		t.Fatalf("default placement=%q", got)
	}
	call[pluginapi.SchedulerPickResponse](t, p, pluginabi.MethodSchedulerPick, pickRequest("claude", "warm", "a"), placementNow)
	configure(pluginabi.MethodPluginRegister, "projected", true)
	if got := status(); got != "projected" {
		t.Fatalf("rollback placement=%q", got)
	}
	configure(pluginabi.MethodPluginReconfigure, "observed-weekly", true)
	configure(pluginabi.MethodPluginReconfigure, "unknown", false)
	if got := status(); got != "observed-weekly" {
		t.Fatalf("rejected config replaced active placement: %q", got)
	}
	if got := call[pluginapi.SchedulerPickResponse](t, p, pluginabi.MethodSchedulerPick,
		pickRequest("claude", "warm", "b", "a"), placementNow+1); !got.Handled || got.AuthID != "a" {
		t.Fatalf("reconfigure discarded warm binding: %+v", got)
	}
}
