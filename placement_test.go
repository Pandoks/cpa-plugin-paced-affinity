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
	credits            string  // Codex's X-Codex-Credits-Has-Credits, if reported
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
		// Codex must skip the Claude-only warm step and place on weekly-only accounts by the share of the week used. Equal
		// weights; b has used .48 and a .5, so b wins. a's reset is 2.2 days out and b's 2, so expiry urgencies are .284 for
		// a and .325 for b. The lead, .041, is under keepCold's 0.3, so a Codex pick that ran the warm step would move to a.
		// It is also under 0.05, so the case still catches that warm step if keepCold is lowered to 0.05.
		{"Codex ignores cached prefixes", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .5, days: 2.2, weeklyOnly: true}, {usedW: .48, days: 2, weeklyOnly: true}},
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
		// a has no credits and is within the 2% reserve, so it is held; b pays credits past its limit, which beats a 429.
		{"Codex holds an account without credits short of its limit", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .985, days: 3, weeklyOnly: true, credits: "False"}, {usedW: 1, days: 3, weeklyOnly: true, credits: "True"}},
			[]placementStep{{at: 1, sid: "fresh", candidates: "ab", want: "b"}}},
		// Three hours after a was last heard from, one new chat probes it; the next new chat doesn't, nor does a chat with
		// history.
		{"a new chat probes a held Codex account every three hours", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .985, days: 3, weeklyOnly: true, credits: "False"}, {usedW: .5, days: 3, weeklyOnly: true, credits: "True"}},
			[]placementStep{
				{at: 1, sid: "fresh", candidates: "ab", want: "b"},
				{at: 2, sid: "fresh", auth: "b", quota: &placementQuota{usedW: .5, days: 3, weeklyOnly: true, credits: "True"}},
				{at: 3 * 3600, sid: "fresh", candidates: "ab", want: "b"},
				{at: 3*3600 + 1, sid: "fresh2", candidates: "ab", want: "a"},
				{at: 3*3600 + 2, sid: "fresh3", candidates: "ab", want: "b"},
			}},
		// a has used half its week and b .4, so b wins, although a's allowance expires sooner and is larger.
		{"Codex places weekly-only work on the smallest share of the week used, not by expiry or absolute room", "codex", []float64{24, 10},
			[]placementQuota{{usedW: .5, days: 1, weeklyOnly: true}, {usedW: .4, days: 6, weeklyOnly: true}},
			[]placementStep{{sid: "fresh", candidates: "ab", want: "b"}}},
		{"Codex breaks a tie in the share used by the most allowance left", "codex", []float64{10, 24},
			[]placementQuota{{usedW: .3, days: 3, weeklyOnly: true}, {usedW: .3, days: 3, weeklyOnly: true}},
			[]placementStep{{sid: "fresh", candidates: "ab", want: "b"}}},
		// a's weekly allowance expires in half a day, so it wins on expiry urgency though it has used more of its week.
		{"Codex with five-hour windows keeps placing by expiry urgency", "codex", []float64{20, 20},
			[]placementQuota{{used5: .6, usedW: .75, days: .5}, {used5: .1, usedW: .7, days: 6}},
			[]placementStep{{sid: "fresh", candidates: "ab", want: "a"}}},
		{"a Codex pool with any five-hour window keeps placing by expiry urgency", "codex", []float64{20, 20},
			[]placementQuota{{used5: .6, usedW: .75, days: .5}, {usedW: .7, days: 6, weeklyOnly: true}},
			[]placementStep{{sid: "fresh", candidates: "ab", want: "a"}}},
		// a sits just under its reserve; at .985 the case above holds it.
		{"Codex holds an account without credits only from its reserve on", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .975, days: 3, weeklyOnly: true, credits: "False"}, {usedW: 1, days: 3, weeklyOnly: true, credits: "True"}},
			[]placementStep{{at: 1, sid: "fresh", candidates: "ab", want: "a"}}},
		{"Codex stops holding an account without credits once its weekly window expires", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .99, days: -1, weeklyOnly: true, credits: "False"}, {usedW: .1, days: 6, weeklyOnly: true, credits: "False"}},
			[]placementStep{{at: 1, sid: "fresh", candidates: "ab", want: "a"}}},
		// Once every account is held, a live chat stays where it is instead of moving to whichever has used less.
		{"a Codex chat stays on a held account when every account is held", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .99, days: 3, weeklyOnly: true, credits: "False"}, {usedW: .985, days: 3, weeklyOnly: true, credits: "False"}},
			[]placementStep{
				{sid: "chat", candidates: "ab", want: "b"},
				{at: 1, sid: "chat", auth: "b", quota: &placementQuota{usedW: .995, days: 3, weeklyOnly: true, credits: "False"}},
				{at: 2, sid: "chat", candidates: "ab", want: "b"},
			}},
		{"a Codex chat leaves a held account for one with room", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .4, days: 3, weeklyOnly: true, credits: "False"}, {usedW: .5, days: 3, weeklyOnly: true, credits: "True"}},
			[]placementStep{
				{sid: "chat", candidates: "ab", want: "a"},
				{at: 1, sid: "chat", auth: "a", quota: &placementQuota{usedW: .985, days: 3, weeklyOnly: true, credits: "False"}},
				{at: 2, sid: "chat", candidates: "ab", want: "b"},
			}},
		{"a Claude chat stays on an account in its last 2% of the week", "claude", []float64{20, 20},
			[]placementQuota{{used5: .1, usedW: .5, days: 3}, {used5: .1, usedW: .5, days: 3}},
			[]placementStep{
				{sid: "chat", candidates: "a", want: "a"},
				{at: 1, sid: "chat", auth: "a", quota: &placementQuota{used5: .1, usedW: .985, days: 3}},
				{at: 2, sid: "chat", candidates: "ab", want: "a"},
			}},
		// b would answer with a 429 and a has room; c pays credits, so only c is probed, and only by a main chat that is
		// new, three hours after c was last heard from.
		{"a new chat probes only an exhausted Codex account that answers without a 429", "codex", []float64{20, 20, 20},
			[]placementQuota{{usedW: .5, days: 3, weeklyOnly: true, credits: "True"}, {usedW: 1, days: 3, weeklyOnly: true, credits: "False"},
				{usedW: 1, days: 3, weeklyOnly: true, credits: "True"}},
			[]placementStep{
				{at: 3*3600 - 60, sid: "early", candidates: "abc", want: "a"},
				{at: 3 * 3600, sid: "x:agent:s", parent: "x", candidates: "abc", want: "a"},
				{at: 3*3600 + 1, sid: "fresh", candidates: "abc", want: "c"},
			}},
		// a and b are both held and due for a probe: one new chat probes a, the next b, the next goes to c.
		{"new chats probe two held Codex accounts one at a time", "codex", []float64{20, 20, 20},
			[]placementQuota{{usedW: .985, days: 3, weeklyOnly: true, credits: "False"}, {usedW: .985, days: 3, weeklyOnly: true, credits: "False"},
				{usedW: .5, days: 3, weeklyOnly: true, credits: "True"}},
			[]placementStep{
				{at: 3*3600 + 1, sid: "fresh", candidates: "abc", want: "a"},
				{at: 3*3600 + 2, sid: "fresh2", candidates: "abc", want: "b"},
				{at: 3*3600 + 3, sid: "fresh3", candidates: "abc", want: "c"},
			}},
		// a has used half its reserve; the other half is left for the request a plugin restart sends it.
		{"a held Codex account is no longer probed once half its reserve is used", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .99, days: 3, weeklyOnly: true, credits: "False"}, {usedW: .5, days: 3, weeklyOnly: true, credits: "True"}},
			[]placementStep{{at: 3*3600 + 1, sid: "fresh", candidates: "ab", want: "b"}}},
		// b is exhausted and hasn't said whether it pays credits, so it may answer with a 429.
		{"a new chat does not probe an exhausted Codex account whose credits are unknown", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .5, days: 3, weeklyOnly: true, credits: "True"}, {usedW: 1, days: 3, weeklyOnly: true}},
			[]placementStep{{at: 3*3600 + 1, sid: "fresh", candidates: "ab", want: "a"}}},
		// Codex reports whole percents, so 98% is exactly the reserve.
		{"Codex holds an account without credits at exactly its reserve", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .98, days: 3, weeklyOnly: true, credits: "False"}, {usedW: 1, days: 3, weeklyOnly: true, credits: "True"}},
			[]placementStep{{at: 1, sid: "fresh", candidates: "ab", want: "b"}}},
		// The websocket path sends the header in lowercase; a response without it leaves what was last reported. Both are
		// exhausted, and only b reported credits, so only b answers without a 429.
		{"Codex reads the credits header in lowercase and keeps it through a response without it", "codex", []float64{20, 20},
			[]placementQuota{{usedW: 1, days: 3, weeklyOnly: true}, {usedW: 1, days: 3, weeklyOnly: true, credits: "true"}},
			[]placementStep{
				{at: 1, sid: "other", auth: "b", quota: &placementQuota{usedW: 1, days: 3, weeklyOnly: true}},
				{at: 2, sid: "fresh", candidates: "ab", want: "b"},
			}},
		// a hasn't said whether it pays credits, so it is held like one without; b pays past its limit.
		{"Codex holds an account whose credits are unknown", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .985, days: 3, weeklyOnly: true}, {usedW: 1, days: 3, weeklyOnly: true, credits: "True"}},
			[]placementStep{{at: 1, sid: "fresh", candidates: "ab", want: "b"}}},
		// Both are exhausted; only b is known to pay credits rather than answer with a 429.
		{"an exhausted Codex account known to pay credits beats one whose credits are unknown", "codex", []float64{20, 20},
			[]placementQuota{{usedW: 1, days: 3, weeklyOnly: true}, {usedW: 1, days: 3, weeklyOnly: true, credits: "True"}},
			[]placementStep{{at: 1, sid: "fresh", candidates: "ab", want: "b"}}},
		// As after a restart, b hasn't answered yet and reads as unused: it takes one chat, and neither a second chat nor
		// that chat's subagent joins it before it answers; once it has, the pool places by the share used again.
		{"a Codex account not yet heard from takes one request until it answers", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .5, days: 3, weeklyOnly: true, credits: "False"}},
			[]placementStep{
				{at: 1, sid: "x", candidates: "ab", want: "b"},
				{at: 2, sid: "x:agent:s", parent: "x", candidates: "ab", want: "a"},
				{at: 3, sid: "y", candidates: "ab", want: "a"},
				{at: 4, sid: "x", auth: "b", quota: &placementQuota{usedW: .3, days: 3, weeklyOnly: true, credits: "False"}},
				{at: 5, sid: "z", candidates: "ab", want: "b"},
			}},
		// a has used its week without credits, so it would answer with a 429; b, not yet heard from, takes the second chat too.
		{"a Codex account not yet heard from is not skipped for one that would answer with a 429", "codex", []float64{20, 20},
			[]placementQuota{{usedW: 1, days: 3, weeklyOnly: true, credits: "False"}},
			[]placementStep{{at: 1, sid: "x", candidates: "ab", want: "b"}, {at: 2, sid: "y", candidates: "ab", want: "b"}}},
		// a has used its week but pays credits, so the second chat goes to a while b hasn't answered.
		{"a Codex account not yet heard from is skipped for one that pays credits past its limit", "codex", []float64{20, 20},
			[]placementQuota{{usedW: 1, days: 3, weeklyOnly: true, credits: "True"}},
			[]placementStep{{at: 1, sid: "x", candidates: "ab", want: "b"}, {at: 2, sid: "y", candidates: "ab", want: "a"}}},
		// b's first request never answered; three hours after it was picked, it takes another request.
		{"a Codex account not yet heard from takes another request three hours after the last", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .5, days: 3, weeklyOnly: true, credits: "False"}},
			[]placementStep{
				{at: 1, sid: "x", candidates: "ab", want: "b"},
				{at: 2, sid: "y", candidates: "ab", want: "a"},
				{at: 3*3600 + 1, sid: "z", candidates: "ab", want: "b"},
			}},
		// b has not answered, so x's chat bound to it is skipped too; its next request goes to a, which has answered.
		{"a chat bound to a Codex account not yet heard from is moved to another", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .5, days: 3, weeklyOnly: true, credits: "False"}},
			[]placementStep{
				{at: 1, sid: "x", candidates: "ab", want: "b"},
				{at: 2, sid: "x", candidates: "ab", want: "a"},
			}},
		// a has used its week and hasn't said whether it pays credits, so it doesn't answer; b, not yet heard from, takes the chat.
		{"a new Codex chat does not take an exhausted account of unknown credits over one not yet heard from", "codex", []float64{20, 20},
			[]placementQuota{{usedW: 1, days: 3, weeklyOnly: true}},
			[]placementStep{
				{at: 1, sid: "x", candidates: "ab", want: "b"},
				{at: 2, sid: "y", candidates: "ab", want: "b"},
			}},
		// a is held but answers without a 429, so a second chat goes to it while b, not yet heard from, waits.
		{"a Codex account not yet heard from is skipped for a held one", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .985, days: 3, weeklyOnly: true, credits: "False"}},
			[]placementStep{{at: 1, sid: "x", candidates: "ab", want: "b"}, {at: 2, sid: "y", candidates: "ab", want: "a"}}},
		// c was picked and hasn't answered; a has used its week without credits and would answer with a 429, so with c
		// waiting the new chat goes to b, which pays credits, though a is listed first.
		{"a Codex account not yet heard from is skipped only for those that answer", "codex", []float64{20, 20, 20},
			[]placementQuota{{usedW: 1, days: 3, weeklyOnly: true, credits: "False"}, {usedW: 1, days: 3, weeklyOnly: true, credits: "True"}},
			[]placementStep{
				{at: 1, sid: "x", candidates: "c", want: "c"},
				{at: 2, sid: "fresh", candidates: "abc", want: "b"},
			}},
		// a (Plus) is cooling down and left out of the candidates, but the pool still has a five-hour window: a new chat
		// goes by expiry urgency to b, whose larger allowance expires in a day, not to c, which has used less of its week.
		{"a Codex pool keeps its five-hour rules while its five-hour account cools down: placing", "codex", []float64{20, 24, 10},
			[]placementQuota{{used5: .1, usedW: .5, days: 3}, {usedW: .5, days: 1, weeklyOnly: true}, {usedW: .4, days: 6, weeklyOnly: true}},
			[]placementStep{{at: 1, sid: "fresh", candidates: "bc", want: "b"}}},
		// As above, so b's chat stays on it in its last 2% without credits.
		{"a Codex pool keeps its five-hour rules while its five-hour account cools down: holding", "codex", []float64{20, 20, 20},
			[]placementQuota{{used5: .1, usedW: .5, days: 3}, {usedW: .5, days: 3, weeklyOnly: true}, {usedW: .1, days: 3, weeklyOnly: true}},
			[]placementStep{
				{sid: "chat", candidates: "b", want: "b"},
				{at: 1, sid: "chat", auth: "b", quota: &placementQuota{usedW: .985, days: 3, weeklyOnly: true, credits: "False"}},
				{at: 2, sid: "chat", candidates: "bc", want: "b"},
			}},
		// As after a restart, a hasn't answered yet; it is the only account, so it takes every request.
		{"a one-account Codex pool keeps placing before its account answers", "codex", []float64{20},
			nil,
			[]placementStep{{at: 1, sid: "x", candidates: "a", want: "a"}, {at: 2, sid: "y", candidates: "a", want: "a"}}},
		// c was picked and hasn't answered, so the pool isn't yet known to be weekly-only: a new chat goes by expiry
		// urgency to a, whose larger allowance expires in a day, not to b, which has used less of its week.
		{"a Codex pool places by the share used only once every account has answered", "codex", []float64{24, 10, 20},
			[]placementQuota{{usedW: .5, days: 1, weeklyOnly: true}, {usedW: .4, days: 6, weeklyOnly: true}},
			[]placementStep{
				{at: 1, sid: "x", candidates: "c", want: "c"},
				{at: 2, sid: "fresh", candidates: "abc", want: "a"},
			}},
		// a is held and due for a probe, but c was picked two seconds in and hasn't answered, so no new chat probes a.
		{"a Codex pool probes only once every account has answered", "codex", []float64{20, 20, 20},
			[]placementQuota{{usedW: .985, days: 3, weeklyOnly: true, credits: "False"}, {usedW: .5, days: 3, weeklyOnly: true, credits: "True"}},
			[]placementStep{
				{at: 2, sid: "x", candidates: "c", want: "c"},
				{at: 3*3600 + 1, sid: "fresh", candidates: "abc", want: "b"},
			}},
		// a is held, but it answered an hour in, under three hours before the new chat.
		{"a new chat does not probe a held Codex account heard from within three hours", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .985, days: 3, weeklyOnly: true, credits: "False"}, {usedW: .5, days: 3, weeklyOnly: true, credits: "True"}},
			[]placementStep{
				{at: 3600, sid: "other", auth: "a", quota: &placementQuota{usedW: .985, days: 3, weeklyOnly: true, credits: "False"}},
				{at: 3*3600 + 1, sid: "fresh", candidates: "ab", want: "b"},
			}},
		// a's chat is held once a answers at its reserve; b pays credits past its limit, so the chat moves to b.
		{"a live Codex chat on a held account fails over to one that pays credits", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .5, days: 3, weeklyOnly: true, credits: "False"}, {usedW: 1, days: 3, weeklyOnly: true, credits: "True"}},
			[]placementStep{
				{sid: "chat", candidates: "ab", want: "a"},
				{at: 1, sid: "chat", auth: "a", quota: &placementQuota{usedW: .985, days: 3, weeklyOnly: true, credits: "False"}},
				{at: 2, sid: "chat", candidates: "ab", want: "b"},
			}},
		// Until b (perhaps a Plus account) answers, a's chat stays on it in its reserve, as in a pool with five-hour windows.
		{"a Codex pool holds no account until every account has answered", "codex", []float64{20, 20},
			[]placementQuota{{usedW: .5, days: 3, weeklyOnly: true, credits: "False"}},
			[]placementStep{
				{sid: "chat", candidates: "a", want: "a"},
				{at: 1, sid: "chat", auth: "a", quota: &placementQuota{usedW: .985, days: 3, weeklyOnly: true, credits: "False"}},
				{at: 2, sid: "chat", candidates: "ab", want: "a"},
			}},
		// In a pool with any five-hour window nothing is held or probed: a (Plus, its five-hour window full) would answer
		// with a 429, b (weekly-only) is placed on like any account, and a new chat goes to c, which has the most room.
		{"a Codex pool with five-hour windows never probes", "codex", []float64{20, 20, 20},
			[]placementQuota{{used5: 1, usedW: .985, days: 3, credits: "False"}, {usedW: .985, days: 3, weeklyOnly: true, credits: "False"},
				{used5: .1, usedW: .5, days: 3, credits: "True"}},
			[]placementStep{{at: 3*3600 + 1, sid: "fresh", candidates: "abc", want: "c"}}},
		// Plus a and weekly-only b, each in its last 2% without credits, keep their live chats.
		{"a Codex pool with five-hour windows holds no account", "codex", []float64{20, 20, 20},
			[]placementQuota{{used5: .1, usedW: .5, days: 3}, {usedW: .5, days: 3, weeklyOnly: true}, {used5: .1, usedW: .1, days: 3}},
			[]placementStep{
				{sid: "chat-a", candidates: "a", want: "a"},
				{sid: "chat-b", candidates: "b", want: "b"},
				{at: 1, sid: "chat-a", auth: "a", quota: &placementQuota{used5: .1, usedW: .985, days: 3, credits: "False"}},
				{at: 1, sid: "chat-b", auth: "b", quota: &placementQuota{usedW: .985, days: 3, weeklyOnly: true, credits: "False"}},
				{at: 2, sid: "chat-a", candidates: "abc", want: "a"},
				{at: 2, sid: "chat-b", candidates: "abc", want: "b"},
			}},
		// c's five-hour window is full, so place drops it even though it pays credits; a (Plus) leads b (weekly-only)
		// on headroom plus urgency.
		{"a Codex pool with five-hour windows places on accounts in their last 2% before exhausted ones", "codex", []float64{20, 20, 20},
			[]placementQuota{{used5: .1, usedW: .985, days: 3, credits: "False"}, {usedW: .985, days: 3, weeklyOnly: true, credits: "False"},
				{used5: 1, usedW: .5, days: 3, credits: "True"}},
			[]placementStep{{at: 1, sid: "fresh", candidates: "abc", want: "a"}}},
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
		if q.credits != "" {
			h.Set("X-Codex-Credits-Has-Credits", q.credits)
		}
	}
	call[struct{}](t, p, pluginabi.MethodUsageHandle, pluginapi.UsageRecord{Provider: provider, Model: cmp.Or(s.upstream, s.model),
		Alias: cmp.Or(s.alias, s.model), AuthID: s.auth, Failed: s.failed, SessionID: s.sid, ParentSessionID: s.parent, ResponseHeaders: h,
		Detail: pluginapi.UsageDetail{InputTokens: int64(q.tokens)}}, placementNow+s.at)
}
