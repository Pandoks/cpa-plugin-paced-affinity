package main

import (
	"math"
	"slices"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const (
	affinityTTL    float64 = 60 // sliding: a warm chat is never moved voluntarily
	margin5h       float64 = 0.05
	marginWeekly   float64 = 0.03
	weeklyWeight   float64 = 2
	weeklyLanding  float64 = 1.1         // elapsed-week target, capped at the full allowance
	gateLo, gateHi float64 = 0.4, 0.7    // weekly pressure that switches the weekly-expiry term on
	keepCold       float64 = 0.3         // expiry urgency by which a cold pick must lead a warm one to keep the work
	sessionTTL     float64 = 7 * 24 * 60 // forget idle sessions
)

type session struct {
	acct        *account
	burn, burnT float64 // decayed units (rate = burn / tauSession)
}

func (s *session) rate(t float64) float64 {
	return s.burn * math.Exp(-(t-s.burnT)*itaus) * itaus
}

type binding struct {
	acct  *account
	lastT float64
}

type router struct {
	mu       sync.Mutex
	accounts map[string]*account
	pools    map[string][]*account // per provider, in registration order
	sessions map[string]*session
	bindings map[string]*binding
	swept    float64
}

func newRouter() *router {
	return &router{accounts: map[string]*account{}, pools: map[string][]*account{},
		sessions: map[string]*session{}, bindings: map[string]*binding{}}
}

func (r *router) account(id, provider string, weight float64) *account {
	a := r.accounts[id]
	if a == nil {
		a = newAccount(id, provider)
		r.accounts[id] = a
		r.pools[provider] = append(r.pools[provider], a)
	}
	a.setWeight(weight)
	return a
}

// pick returns the account for this request and why: "affinity" (live binding), "placed" or "failover".
func (r *router) pick(provider, model, sid, parent string, own bool, cands []pluginapi.SchedulerAuthCandidate, t float64) (*account, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweep(t)
	cs := make([]*account, len(cands))
	for i, c := range cands {
		cs[i] = r.account(c.ID, provider, credentialWeight(c.Attributes))
	}
	kind, key := sessionKey(sid, parent, own)
	reason := "placed"
	if b := r.bindings[key]; b != nil && t-b.lastT <= affinityTTL {
		if slices.Contains(cs, b.acct) && !b.acct.exhausted(t) {
			return b.acct, "affinity"
		}
		reason = "failover" // cooling down after a 429, or reported exhausted
	}
	choose, burn := r.place, 0.0
	if s := r.sessions[sid]; s != nil {
		burn = s.rate(t)
	} else {
		burn = r.newSessionBurn(provider, t)
		if provider == "claude" && kind == kindMain {
			choose = r.placeObservedWeekly // a new main chat
		}
	}
	a := choose(cs, t, burn)
	// A Claude chat starts by sending its model's tools and system prompt, which other chats sent too; on an
	// account that served them within the cache TTL it reads them instead of writing them again, unless the pick's
	// expiry urgency leads the warm choice's by keepCold.
	if ck := cacheKey(model, kind); provider == "claude" && t-a.seen[ck] > cacheTTL(kind) {
		skip := func(w *account) bool { return t-w.seen[ck] > cacheTTL(kind) || !w.project(t, burn).fits() }
		if warm := slices.DeleteFunc(slices.Clone(cs), skip); len(warm) > 0 {
			if w := choose(warm, t, burn); a.expiryUrgency(t)-w.expiryUrgency(t) < keepCold {
				a = w
			}
		}
	}
	r.bindings[key] = &binding{a, t}
	return a, reason
}

// placeObservedWeekly spends the account furthest behind its elapsed-week target, among those whose observed 5h usage
// and projected weekly usage pass the margins; ties go to the most projected headroom. If none passes, place decides.
func (r *router) placeObservedWeekly(cands []*account, t, burn float64) *account {
	best := r.place(cands, t, burn) // if no account passes
	bestScore, bestH := math.Inf(-1), math.Inf(-1)
	for _, a := range cands {
		p := a.project(t, burn) // an exhausted account fails the guards below
		observed5 := 0.0
		if a.r5 > t {
			observed5 = a.u5
		}
		if observed5 > 1-margin5h || !(p.projW <= 1-marginWeekly || p.eW > 0) {
			continue
		}
		uW, ttrW := a.weekly(t)
		target := min(weeklyLanding*min(max(1-ttrW/week, 0), 1), 1)
		score, h := target-uW, min(p.h5, p.hW)
		if score > bestScore || score == bestScore && h > bestH {
			best, bestScore, bestH = a, score, h
		}
	}
	return best
}

// place picks the feasible account with the most projected headroom (absolute units, so plan sizes compare),
// plus a gated bonus for weekly allowance that would otherwise expire unused.
func (r *router) place(cands []*account, t, burn float64) *account {
	cs := slices.DeleteFunc(slices.Clone(cands), func(a *account) bool { return a.exhausted(t) })
	if len(cs) == 0 {
		cs = cands
	}
	type row struct {
		a          *account
		h, urgency float64
	}
	var rows []row
	var fallback *account
	fallbackH, maxH, maxU := math.Inf(-1), math.Inf(-1), math.Inf(-1)
	for _, a := range cs {
		p := a.project(t, burn)
		h := min(p.h5, p.hW)
		if h > fallbackH {
			fallback, fallbackH = a, h
		}
		if p.fits() {
			w := row{a, h, p.eW / max(p.ttrW, 60)}
			rows = append(rows, w)
			maxH, maxU = max(maxH, w.h), max(maxU, w.urgency)
		}
	}
	if len(rows) == 0 {
		return fallback
	}
	lam := weeklyWeight * weeklyPressure(cs, t)
	var best *account
	bestPri := math.Inf(-1)
	for _, w := range rows {
		var pri float64
		switch {
		case w.a.weeklyFirstUse && 0 <= w.a.rW && w.a.rW <= t:
			pri = 10 // an expired first-use weekly window holds idle capacity; reopening it starts its clock
		case w.a.has5h:
			pri = share(w.h, maxH) + lam*share(w.urgency, maxU)
		default:
			pri = share(w.urgency, maxU) // weekly-only: spend the allowance that would expire unused first
		}
		if pri += 1e-9 * w.h; pri > bestPri {
			best, bestPri = w.a, pri
		}
	}
	return best
}

// share is x relative to the best value among the feasible accounts.
func share(x, best float64) float64 {
	if best > 0 {
		return x / best
	}
	return 0
}

// weeklyPressure maps the mean projected weekly utilization at reset to [0, 1].
func weeklyPressure(cs []*account, t float64) float64 {
	tot := 0.0
	for _, a := range cs {
		uW, ttrW := a.weekly(t)
		tot += uW + a.longBurn(t)*ttrW
	}
	return min(max((tot/float64(len(cs))-gateLo)/(gateHi-gateLo), 0), 1)
}

// newSessionBurn forecasts a new session's burn as the mean burn per active main session.
func (r *router) newSessionBurn(provider string, t float64) float64 {
	tot, n := 0.0, 0
	for _, a := range r.pools[provider] {
		tot += a.rateAbs(t)
		n += a.nActive(t)
	}
	if n == 0 {
		return 0
	}
	return tot / float64(n)
}

// observe feeds one successful response's usage record into the account and session state. A 429 needs no
// handling here: CPA keeps the account out of the candidates until the reset its headers announce.
func (r *router) observe(rec *pluginapi.UsageRecord, now float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.accounts[rec.AuthID]
	if a == nil || rec.Failed {
		return
	}
	w5, wW := quotaWindows(a.provider, rec.ResponseHeaders, now)
	if w5.ok {
		a.has5h = true // e.g. Codex Plus: 5h window plus weekly
	}
	u5, r5, uW, rW := a.u5, a.r5, a.uW, a.rW // a missing header keeps the last observed value
	if w5.ok {
		u5, r5 = w5.util, w5.reset
	}
	if wW.ok {
		uW, rW = wW.util, wW.reset
	}
	t, sid := now/60, rec.SessionID
	// Records carry no request headers; only a subagent's own binding is keyed by its id.
	kind, key := sessionKey(sid, rec.ParentSessionID, r.bindings[sid] != nil)
	a.seen[cacheKey(rec.Alias, kind)] = t // Alias is the model as the client named it, the key pick uses
	cost := tokenCost(a, kind, rec.Detail)
	var d5, dW float64 // the first observation (e.g. after a restart mid-window) only records the headers
	if a.tObs >= 0 {
		d5, dW = u5, uW // a new window counts from zero
		if r5 == a.r5 {
			d5 = u5 - a.u5
		}
		if rW == a.rW {
			dW = uW - a.uW
		}
		d5, dW = max(d5, 0), max(dW, 0)
		a.sumDu5 += d5
		a.sumW5 += cost
		a.sumDuW += dW
		a.sumWW += cost
	}
	a.decay(t)
	a.acc5 += d5
	a.accWShort += dW
	a.accWLong += dW
	a.u5, a.r5, a.uW, a.rW, a.tObs = u5, r5, uW, rW, t

	if s := r.sessions[sid]; s == nil {
		r.sessions[sid] = &session{a, cost, t}
	} else {
		if kind == kindMain && s.acct != a {
			delete(s.acct.active, sid)
		}
		f := 1.0
		if dt := t - s.burnT; dt > 0 {
			f = math.Exp(-dt * itaus)
		}
		s.acct, s.burn, s.burnT = a, s.burn*f+cost, t
	}
	if kind == kindMain {
		a.active[sid] = t
	}
	if b := r.bindings[key]; b != nil {
		b.acct, b.lastT = a, t
	}
}

// sweep bounds memory: expired bindings behave exactly like missing ones; idle sessions are forgotten.
func (r *router) sweep(t float64) {
	if t-r.swept < 60 {
		return
	}
	r.swept = t
	for k, b := range r.bindings {
		if t-b.lastT > affinityTTL {
			delete(r.bindings, k)
		}
	}
	for k, s := range r.sessions {
		if t-s.burnT > sessionTTL {
			delete(r.sessions, k)
		}
	}
}
