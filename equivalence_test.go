package main

import (
	"compress/gzip"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// A projected-policy trace (testdata/gen_traces.py) is what the plugin saw while the reference router (testdata/reference/winner.py)
// made every pick; replaying it through the ABI dispatch must reproduce each pick exactly.
type trace struct {
	Accounts [][3]any `json:"accounts"` // id, provider, weight, in the reference's account order
	Events   []struct {
		Op     string      `json:"op"`
		T      float64     `json:"t"` // unix seconds
		Prov   string      `json:"prov"`
		Sid    string      `json:"sid"`
		Parent string      `json:"parent"`
		Cands  []string    `json:"cands"`
		Want   *string     `json:"want"` // nil: not handled
		Auth   string      `json:"auth"`
		Status int         `json:"status"`
		Tok    [4]int64    `json:"tok"` // input, cache read, cache creation, output
		Hdr    http.Header `json:"hdr"`
	} `json:"events"`
}

func TestDecisionEquivalence(t *testing.T) {
	files, _ := filepath.Glob("testdata/traces/*.json.gz")
	if len(files) == 0 {
		t.Fatal("no traces: run python3 testdata/gen_traces.py")
	}
	total := 0
	for _, file := range files {
		picks, mismatches := replay(t, file)
		total += picks
		t.Logf("%s: %d picks, %d mismatches", filepath.Base(file), picks, mismatches)
	}
	t.Logf("total: %d picks", total)
}

func replay(t *testing.T, file string) (picks, mismatches int) {
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var tr trace
	if err := json.NewDecoder(zr).Decode(&tr); err != nil {
		t.Fatal(err)
	}
	var p *plugin
	provider, weight := map[string]string{}, map[string]string{}
	start := func() { // a fresh plugin knowing the reference's static account list
		p = newPlugin()
		// These immutable Python traces specify the original projected policy.
		call[map[string]any](t, p, pluginabi.MethodPluginRegister,
			map[string][]byte{"config_yaml": []byte("claude-placement: projected\n")}, 0)
		for _, a := range tr.Accounts {
			id, prov, w := a[0].(string), a[1].(string), a[2].(float64)
			p.r.account(id, prov, w)
			provider[id], weight[id] = prov, strconv.FormatFloat(w, 'f', -1, 64)
		}
	}
	start()
	for i, ev := range tr.Events {
		switch ev.Op {
		case "restart":
			start()
		case "pick":
			req := pluginapi.SchedulerPickRequest{Provider: ev.Prov, Options: pluginapi.SchedulerOptions{
				Metadata: map[string]any{"canonical_session_id": ev.Sid}}}
			if ev.Parent != "" {
				req.Options.Metadata["parent_session_id"] = ev.Parent
			}
			for _, id := range ev.Cands {
				req.Candidates = append(req.Candidates, pluginapi.SchedulerAuthCandidate{ID: id, Provider: provider[id],
					Attributes: map[string]string{"weight": weight[id]}})
			}
			got := call[pluginapi.SchedulerPickResponse](t, p, pluginabi.MethodSchedulerPick, req, ev.T)
			want := ""
			if ev.Want != nil {
				want = *ev.Want
			}
			picks++
			if got.AuthID != want || got.Handled != (want != "") {
				if mismatches++; mismatches <= 5 {
					t.Errorf("%s event %d: picked %q (handled %v), reference %q", filepath.Base(file), i, got.AuthID, got.Handled, want)
				}
			}
		case "usage":
			call[struct{}](t, p, pluginabi.MethodUsageHandle, pluginapi.UsageRecord{Provider: provider[ev.Auth],
				AuthID: ev.Auth, SessionID: ev.Sid, ParentSessionID: ev.Parent, Failed: ev.Status != http.StatusOK,
				Failure: pluginapi.UsageFailure{StatusCode: ev.Status}, ResponseHeaders: ev.Hdr,
				Detail: pluginapi.UsageDetail{InputTokens: ev.Tok[0], CacheReadTokens: ev.Tok[1], CachedTokens: ev.Tok[1],
					CacheCreationTokens: ev.Tok[2], OutputTokens: ev.Tok[3]}}, ev.T)
		}
	}
	return picks, mismatches
}

// call sends one request through the ABI dispatch, as JSON like the host does.
func call[T any](t *testing.T, p *plugin, method string, req any, now float64) T {
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
