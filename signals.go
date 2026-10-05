package main

import (
	"cmp"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const (
	kindMain = iota
	kindSubagent
	kindFork
)

// sessionKey maps CPA's canonical session ids to an agent kind and the binding it uses. Claude Code subagents
// share the parent's session and get ids like "claude:<session>:agent:<agent-id>"; they don't read the parent's
// cache, so they get their own binding. Any other session with a parent is a fork and follows the family.
func sessionKey(sid, parent string) (int, string) {
	switch {
	case strings.Contains(sid, ":agent:"):
		return kindSubagent, sid
	case parent != "":
		return kindFork, parent
	}
	return kindMain, sid
}

// pickProvider is the provider shared by every candidate (mixed-mode picks leave Provider empty).
func pickProvider(req *pluginapi.SchedulerPickRequest) string {
	if len(req.Candidates) == 0 {
		return ""
	}
	provider := cmp.Or(req.Provider, req.Candidates[0].Provider)
	for _, c := range req.Candidates {
		if c.Provider != provider {
			return ""
		}
	}
	return provider
}

func credentialWeight(attrs map[string]string) float64 {
	if w, err := strconv.ParseFloat(attrs["weight"], 64); err == nil && w > 0 {
		return w
	}
	return 1
}

func tokenCost(a *account, kind int, d pluginapi.UsageDetail) float64 {
	w := a.tokenWeights
	read := float64(d.CacheReadTokens)
	write := float64(d.InputTokens + d.CacheCreationTokens) // Claude's input excludes cached tokens
	if a.provider == "codex" {
		write = max(float64(d.InputTokens)-read, 0) // Codex's input includes them
	}
	writeWeight := w[2]
	if kind == kindMain {
		writeWeight = w[1]
	}
	return (read*w[0] + write*writeWeight + float64(d.OutputTokens)*w[3]) * 1e-6
}

type window struct {
	util, reset float64 // reset in minutes since the epoch
	ok          bool
}

func quotaWindows(provider string, h http.Header, now float64) (w5, wW window) {
	if provider == "claude" {
		return claudeWindow(h, "5h"), claudeWindow(h, "7d")
	}
	// Codex names its windows primary/secondary; Pro reports the weekly one as primary, so go by length.
	for _, slot := range []string{"Primary", "Secondary"} {
		minutes, err := strconv.ParseFloat(h.Get("X-Codex-"+slot+"-Window-Minutes"), 64)
		w := codexWindow(h, slot, now)
		switch {
		case err != nil || !w.ok:
		case minutes < 1440:
			w5 = w
		default:
			wW = w
		}
	}
	return w5, wW
}

func claudeWindow(h http.Header, name string) window {
	u, err := strconv.ParseFloat(h.Get("Anthropic-Ratelimit-Unified-"+name+"-Utilization"), 64)
	reset, ok := epochSeconds(h.Get("Anthropic-Ratelimit-Unified-" + name + "-Reset"))
	return window{u, reset / 60, err == nil && ok}
}

func codexWindow(h http.Header, slot string, now float64) window {
	pct, err := strconv.ParseFloat(h.Get("X-Codex-"+slot+"-Used-Percent"), 64)
	reset, ok := epochSeconds(h.Get("X-Codex-" + slot + "-Reset-At"))
	if after, errAfter := strconv.ParseFloat(h.Get("X-Codex-"+slot+"-Reset-After-Seconds"), 64); !ok && errAfter == nil {
		reset, ok = now+after, true
	}
	return window{pct / 100, reset / 60, err == nil && ok}
}

func epochSeconds(v string) (float64, bool) {
	if s, err := strconv.ParseFloat(v, 64); err == nil {
		return s, true
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return float64(t.UnixNano()) / 1e9, true
	}
	return 0, false
}
