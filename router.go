package main

import (
	"math"
	"slices"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Projected placement preserves winner.py v4 (testdata/reference), decision for decision.
// The default only changes the first placement of a Claude main chat.
const (
	affinityTTL    float64 = 60 // sliding: a warm chat is never moved voluntarily
	margin5h       float64 = 0.05
	marginWeekly   float64 = 0.03
	weeklyWeight   float64 = 2
	weeklyLanding  float64 = 1.1         // elapsed-week target, capped at the full allowance
	gateLo, gateHi float64 = 0.4, 0.7    // weekly pressure that switches the weekly-expiry term on
	sessionTTL     float64 = 7 * 24 * 60 // forget idle sessions (winner.py keeps them forever)
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
func (r *router) pick(provider, sid, parent string, cands []pluginapi.SchedulerAuthCandidate, t float64, placement claudePlacement) (*account, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweep(t)
	cs := make([]*account, len(cands))
	for i, c := range cands {
		cs[i] = r.account(c.ID, provider, credentialWeight(c.Attributes))
	}
	kind, key := sessionKey(sid, parent)
	var old *account
	if b := r.bindings[key]; b != nil && t-b.lastT <= affinityTTL {
		if slices.Contains(cs, b.acct) && !b.acct.exhausted(t) {
			return b.acct, "affinity"
		}
		old = b.acct // cooling down after a 429, or known to be exhausted
	}
	var burn float64
	if s := r.sessions[sid]; s != nil {
		burn = s.rate(t)
	} else {
		burn = r.newSessionBurn(provider, t)
	}
	var a *account
	if placement == observedWeeklyPlacement && provider == "claude" && kind == kindMain &&
		r.bindings[key] == nil && r.sessions[sid] == nil {
		a = r.placeObservedWeekly(cs, t, burn)
	} else {
		a = r.place(cs, t, burn)
	}
	reason := "placed"
	if old != nil {
		reason = "failover"
		if a != old && kind == kindMain {
			r.transfer(sid, old, a, t)
		}
	}
	r.bindings[key] = &binding{a, t}
	return a, reason
}

// placeObservedWeekly spends the account furthest behind its elapsed-week target.
// It guards observed 5h usage, retains projected weekly safety and breaks ties by
// the original projected absolute headroom. If no account passes, place supplies
// the original fallback rather than leaving the request unhandled.
func (r *router) placeObservedWeekly(cands []*account, t, burn float64) *account {
	fallback := r.place(cands, t, burn)
	hasLive := slices.ContainsFunc(cands, func(a *account) bool { return !a.exhausted(t) })
	best := fallback
	bestScore, bestH := math.Inf(-1), math.Inf(-1)
	for _, a := range cands {
		if hasLive && a.exhausted(t) {
			continue
		}
		p := a.project(t, burn)
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
		h := p.hW
		if p.h5 < p.hW {
			h = p.h5
		}
		if h > fallbackH {
			fallback, fallbackH = a, h
		}
		// v4: the weekly margin is waived when the allowance would expire unused anyway
		if p.proj5 <= 1-margin5h && (p.projW <= 1-marginWeekly || p.eW > 0) {
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
			pri = 10 // v4: an expired first-use weekly window holds idle capacity; reopening it starts its clock
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

// transfer moves a failed-over session's share of the burn estimate to its new account.
func (r *router) transfer(sid string, from, to *account, t float64) {
	s := r.sessions[sid]
	if s == nil {
		return
	}
	units := s.burn * math.Exp(-(t-s.burnT)*itaus)
	for _, m := range []struct {
		a    *account
		sign float64
	}{{from, -1}, {to, 1}} {
		m.a.decay(t)
		if m.a.has5h {
			m.a.acc5 = max(0, m.a.acc5+m.sign*units*m.a.k5()*tau/tauSession)
		}
		m.a.accWShort = max(0, m.a.accWShort+m.sign*units*m.a.kW()*tau/tauSession)
	}
	delete(from.active, sid)
	to.active[sid] = t
}

// observe feeds one usage record (one per upstream attempt, 429s included) into the account and session state.
func (r *router) observe(rec *pluginapi.UsageRecord, now float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.accounts[rec.AuthID]
	if a == nil {
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
	t := now / 60
	switch {
	case !rec.Failed:
		r.response(a, t, rec.SessionID, rec.ParentSessionID, rec.Detail, u5, r5, uW, rW)
	case rec.Failure.StatusCode == 429 && (w5.ok || wW.ok):
		a.reject(t, u5, r5, uW, rW)
	}
}

func (r *router) response(a *account, t float64, sid, parent string, d pluginapi.UsageDetail, u5, r5, uW, rW float64) {
	kind, key := sessionKey(sid, parent)
	cost := tokenCost(a, kind, d)
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
