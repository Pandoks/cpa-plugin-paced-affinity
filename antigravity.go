package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const (
	quotaBaseURL = "https://daily-cloudcode-pa.googleapis.com" // CPA's default for consumer credentials
	quotaPath    = "/v1internal:retrieveUserQuotaSummary"
	quotaTimeout = 30 * time.Second
	// The header official CLI 1.5.16 sent on this call.
	quotaUserAgent = "antigravity/cli/1.5.16 (aidev_client; os_type=linux; arch=amd64; cl=1028571191; auth_method=consumer)"
)

// allowance returns the key suffix of the allowance a model draws on: "" outside Antigravity.
func allowance(provider, model string) string {
	switch {
	case provider != "antigravity":
		return ""
	case strings.HasPrefix(model[strings.LastIndex(model, "/")+1:], "gemini"): // past any CPA prefix
		return "|gemini"
	}
	return "|3p"
}

// quotaBuckets maps the buckets the plugin reads to their allowance and window: 0 five-hour, 1 weekly.
var quotaBuckets = map[string]struct {
	al string
	i  int
}{"gemini-5h": {"|gemini", 0}, "gemini-weekly": {"|gemini", 1}, "3p-5h": {"|3p", 0}, "3p-weekly": {"|3p", 1}}

// quotaGroups reads a quota summary polled at t into each allowance's five-hour and weekly windows. A bucket without a
// reset is taken as a window not yet opened, closed at t; a missing fraction means none left (JSON omits a zero).
func quotaGroups(body []byte, t float64) (map[string][2]window, error) {
	var summary struct {
		Groups []struct {
			Buckets []struct {
				BucketID          string   `json:"bucketId"`
				RemainingFraction *float64 `json:"remainingFraction"`
				ResetTime         string   `json:"resetTime"`
			} `json:"buckets"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(body, &summary); err != nil {
		return nil, err
	}
	out := map[string][2]window{}
	for _, g := range summary.Groups {
		for _, b := range g.Buckets {
			q, known := quotaBuckets[b.BucketID]
			if !known {
				continue
			}
			w := window{util: 1, reset: t, ok: true}
			if b.RemainingFraction != nil {
				w.util = 1 - *b.RemainingFraction
			}
			if b.ResetTime != "" {
				reset, ok := epochSeconds(b.ResetTime)
				if !ok {
					return nil, fmt.Errorf("bad reset time %q", b.ResetTime)
				}
				w.reset = reset / 60
			}
			ws := out[q.al]
			ws[q.i] = w
			out[q.al] = ws
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no known quota buckets")
	}
	return out, nil
}

type poll struct {
	sent, okT, weight   float64 // when the last poll was sent, and the last that succeeded; the credential's weight
	busy, served, blind bool    // a poll is in flight; the account has served since the last was sent; quota unknown
}

func (r *router) pollState(id string) *poll {
	p := r.polls[id]
	if p == nil {
		p = &poll{}
		r.polls[id] = p
	}
	return p
}

// poll starts a quota poll of auth id unless one is in flight or the last was sent less than after minutes ago. The
// caller holds r.mu; the poll runs without it.
func (r *router) poll(id string, t, after float64) {
	p := r.pollState(id)
	if p.busy || t-p.sent < after {
		return
	}
	p.busy, p.served, p.sent = true, false, t
	r.fetching.Go(func() { r.refresh(id, t) })
}

// refresh polls auth id's quota and, if the poll succeeds, records it as observed at t. A failed poll changes no
// account; it leaves the auth blind if none succeeded in the pollBlind minutes before it.
func (r *router) refresh(id string, t float64) {
	var err error
	defer func() {
		if v := recover(); v != nil {
			hostLog("warn", fmt.Sprintf("paced-affinity: recovered from a panic polling %s: %v", id, v))
			err = fmt.Errorf("panic: %v", v)
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		p := r.polls[id]
		if p.busy, p.blind = false, err != nil && t-p.okT > pollBlind; err == nil {
			p.okT = t
		}
	}()
	groups, err := r.fetchQuota(id, t)
	if err != nil {
		hostLog("debug", fmt.Sprintf("paced-affinity: quota poll of %s failed: %v", id, err))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for al, ws := range groups {
		a := r.account(id, "antigravity", al, r.polls[id].weight)
		a.record(ws[0], ws[1], a.pending, t)
		a.pending = 0
	}
}

// fetchQuota polls auth id's quota summary through the host, from the endpoint the official CLI's /usage reads, as
// Antigravity's responses carry none. The access token is read from the auth file CPA keeps fresh and used for this
// request only; the plugin never refreshes it.
func (r *router) fetchQuota(id string, t float64) (map[string][2]window, error) {
	var list struct {
		Files []pluginapi.HostAuthFileEntry `json:"files"`
	}
	if err := host(pluginabi.MethodHostAuthList, struct{}{}, &list); err != nil {
		return nil, err
	}
	i := slices.IndexFunc(list.Files, func(f pluginapi.HostAuthFileEntry) bool { return f.ID == id })
	if i < 0 {
		return nil, fmt.Errorf("auth not listed")
	}
	var file pluginapi.HostAuthGetResponse
	if err := host(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: list.Files[i].AuthIndex}, &file); err != nil {
		return nil, err
	}
	var cred struct {
		AccessToken string `json:"access_token"`
		ProjectID   string `json:"project_id"`
		BaseURL     string `json:"base_url"`
		ProxyURL    string `json:"proxy_url"`
	}
	if err := json.Unmarshal(file.JSON, &cred); err != nil || cred.AccessToken == "" {
		return nil, fmt.Errorf("auth file has no access token")
	}
	// host.http.do uses CPA's global proxy, so a poll would send this token from an address its requests don't use.
	if strings.TrimSpace(cred.ProxyURL) != "" {
		return nil, fmt.Errorf("auth has its own proxy")
	}
	if cred.ProjectID == "" { // without it Google is reported to return only the Claude and GPT allowance
		return nil, fmt.Errorf("auth file has no project id")
	}
	body, _ := json.Marshal(struct {
		Project string `json:"project"`
	}{cred.ProjectID})
	// The host gives plugin requests no deadline; a poll that hangs would stop this account's polls for good.
	var op struct {
		OperationID string `json:"operation_id"`
	}
	if err := host(pluginabi.MethodHostHTTPOperationOpen, struct{}{}, &op); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	defer close(done)
	r.fetching.Go(func() {
		select {
		case <-done:
		case <-time.After(quotaTimeout):
			host(pluginabi.MethodHostHTTPCancel, op, nil)
		}
	})
	var resp pluginapi.HTTPResponse
	err := host(pluginabi.MethodHostHTTPDo, struct {
		OperationID string      `json:"operation_id"`
		Method      string      `json:"method"`
		URL         string      `json:"url"`
		Headers     http.Header `json:"headers"`
		Body        []byte      `json:"body"`
	}{op.OperationID, http.MethodPost, strings.TrimSuffix(cmp.Or(cred.BaseURL, quotaBaseURL), "/") + quotaPath, http.Header{
		"User-Agent":    {quotaUserAgent},
		"Authorization": {"Bearer " + cred.AccessToken},
		"Content-Type":  {"application/json"},
	}, body}, &resp) // Go's transport adds Accept-Encoding: gzip, as the CLI sends, and undoes it
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return quotaGroups(resp.Body, t)
}
