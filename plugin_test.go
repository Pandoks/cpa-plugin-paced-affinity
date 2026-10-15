package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestQuotaWindows(t *testing.T) {
	const now = 1790000000.0
	tests := []struct {
		name     string
		provider string
		h        http.Header
		w5, wW   window
	}{
		{"claude", "claude", http.Header{
			"Anthropic-Ratelimit-Unified-5h-Utilization": {"0.25"}, "Anthropic-Ratelimit-Unified-5h-Reset": {"1790006400"},
			"Anthropic-Ratelimit-Unified-7d-Utilization": {"0.5"}, "Anthropic-Ratelimit-Unified-7d-Reset": {"2026-09-26T06:00:00Z"}},
			window{0.25, 1790006400.0 / 60, true}, window{0.5, 1790402400.0 / 60, true}},
		{"claude without 7d", "claude", http.Header{
			"Anthropic-Ratelimit-Unified-5h-Utilization": {"1.02"}, "Anthropic-Ratelimit-Unified-5h-Reset": {"1790006400"}},
			window{1.02, 1790006400.0 / 60, true}, window{}},
		{"codex pro reports the weekly window as primary", "codex", http.Header{
			"X-Codex-Primary-Used-Percent": {"37"}, "X-Codex-Primary-Window-Minutes": {"10080"},
			"X-Codex-Primary-Reset-At": {"1790300000"}},
			window{}, window{0.37, 1790300000.0 / 60, true}},
		{"codex plus", "codex", http.Header{
			"X-Codex-Primary-Used-Percent": {"12.5"}, "X-Codex-Primary-Window-Minutes": {"300"},
			"X-Codex-Primary-Reset-After-Seconds": {"600"},
			"X-Codex-Secondary-Used-Percent":      {"40"}, "X-Codex-Secondary-Window-Minutes": {"10080"},
			"X-Codex-Secondary-Reset-At": {"1790300000"}},
			window{0.125, (now + 600) / 60, true}, window{0.4, 1790300000.0 / 60, true}},
		{"codex window without a length", "codex", http.Header{
			"X-Codex-Primary-Used-Percent": {"37"}, "X-Codex-Primary-Reset-At": {"1790300000"}},
			window{}, window{}},
	}
	for _, tt := range tests {
		w5, wW := quotaWindows(tt.provider, tt.h, now)
		if w5.ok != tt.w5.ok || wW.ok != tt.wW.ok || tt.w5.ok && w5 != tt.w5 || tt.wW.ok && wW != tt.wW {
			t.Errorf("%s: got %+v %+v, want %+v %+v", tt.name, w5, wW, tt.w5, tt.wW)
		}
	}
}

func pickRequest(provider, sid string, candidates ...string) pluginapi.SchedulerPickRequest {
	req := pluginapi.SchedulerPickRequest{Provider: provider,
		Options: pluginapi.SchedulerOptions{Metadata: map[string]any{"canonical_session_id": sid}}}
	for _, id := range candidates {
		req.Candidates = append(req.Candidates, pluginapi.SchedulerAuthCandidate{ID: id, Provider: provider})
	}
	return req
}

// Anything the plugin does not route itself must come back "not handled" so the built-in selector routes it.
func TestPickFallsBackToBuiltin(t *testing.T) {
	const now = 1790000000.0
	mixed := pickRequest("claude", "claude:s1", "a")
	mixed.Provider = "" // mixed-mode pick
	mixed.Candidates = append(mixed.Candidates, pluginapi.SchedulerAuthCandidate{ID: "b", Provider: "codex"})
	tests := []struct {
		name string
		p    *router
		req  any
	}{
		{"unknown provider", newRouter(), pickRequest("gemini", "gemini:s1", "a")},
		{"mixed providers", newRouter(), mixed},
		{"no session", newRouter(), pickRequest("claude", "", "a")},
		{"malformed request", newRouter(), "not a pick request"},
		{"panic", &router{}, pickRequest("claude", "claude:s1", "a")}, // nil maps
	}
	for _, tt := range tests {
		if got := call[pluginapi.SchedulerPickResponse](t, tt.p, pluginabi.MethodSchedulerPick, tt.req, now); got.Handled {
			t.Errorf("%s: handled, picked %q", tt.name, got.AuthID)
		}
	}
	if got := call[pluginapi.SchedulerPickResponse](t, newRouter(), pluginabi.MethodSchedulerPick,
		pickRequest("claude", "claude:s1", "a", "b"), now); !got.Handled || got.AuthID == "" {
		t.Errorf("a routed pick should be handled, got %+v", got)
	}
}

// The host calls in from many goroutines; `make test` runs this under the race detector.
func TestConcurrentCalls(t *testing.T) {
	p := newRouter()
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 100 {
				now, sid := 1790000000+float64(i), fmt.Sprintf("claude:s%d-%d", g, i%5)
				pick, _ := json.Marshal(pickRequest("claude", sid, "a", "b"))
				usage, _ := json.Marshal(pluginapi.UsageRecord{AuthID: "a", SessionID: sid, ResponseHeaders: http.Header{
					"Anthropic-Ratelimit-Unified-5h-Utilization": {"0.5"}, "Anthropic-Ratelimit-Unified-5h-Reset": {"1790006400"}}})
				for method, req := range map[string][]byte{pluginabi.MethodSchedulerPick: pick,
					pluginabi.MethodUsageHandle: usage, pluginabi.MethodPluginReconfigure: nil} {
					var env pluginabi.Envelope
					if err := json.Unmarshal(p.dispatch(method, req, now), &env); err != nil || !env.OK {
						t.Errorf("%s: %v %v", method, err, env.Error)
					}
				}
			}
		})
	}
	wg.Wait()
}

// call sends one request through the ABI dispatch, as JSON like the host does.
func call[T any](t *testing.T, p *router, method string, req any, now float64) T {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var env pluginabi.Envelope
	var out T
	if err := json.Unmarshal(p.dispatch(method, raw, now), &env); err != nil || !env.OK {
		t.Fatalf("%s: %s %v", method, env.Error, err)
	}
	if err := json.Unmarshal(env.Result, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
