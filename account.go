package main

import "math"

// Times are minutes since the Unix epoch, utilization is a fraction of the window, usage is millions of
// price-weighted tokens ("units"). Typed constants keep the arithmetic bit-identical to the reference.
const (
	window5h        float64 = 300
	week            float64 = 10080
	tau             float64 = 30   // burn-rate EWMA
	tauWeeklyLong   float64 = 1440 // long-run weekly burn EWMA
	tauSession      float64 = 30   // per-session burn EWMA
	weeklyLookahead float64 = 300
	itau                    = 1 / tau
	itauWl                  = 1 / tauWeeklyLong
	itaus                   = 1 / tauSession
)

type profile struct {
	cap5, capW     float64    // prior capacity per unit of credential weight (units per 5h / per week)
	tokenWeights   [4]float64 // assumed price vs base input: cache read, cache write (main / other), output
	has5h          bool
	hourAnchor     bool // a 5h window starts on the hour
	weeklyFirstUse bool // the weekly window opens on the first request after it expired
}

// Claude: weight 1 = Pro (Max 5x = 5, Max 20x = 20), ~$30 of Opus per 5h, weekly ~6 windows.
// Codex: Pro = 24, Business ProLite = 10, ~$60 of GPT per week per unit; weekly-only until a 5h window is seen.
// Priors only: each account's real scale is learned from its rate-limit headers.
var profiles = map[string]profile{
	"claude": {cap5: 7.5, capW: 45, tokenWeights: [4]float64{0.05, 2, 1.25, 5}, has5h: true, hourAnchor: true},
	"codex":  {cap5: 1, capW: 6, tokenWeights: [4]float64{0.1, 1, 1, 5}, weeklyFirstUse: true},
}

type account struct {
	profile
	id, provider   string
	k5Hint, kWHint float64 // utilization per unit, from the weight
	u5, r5, uW, rW float64 // last observed utilization and reset (-1 = unknown)
	tObs           float64

	acc5, accWShort, accWLong, accT float64 // exponentially decayed utilization deltas (burn rates)
	sumDu5, sumW5, sumDuW, sumWW    float64 // online calibration: utilization deltas vs assumed units

	active map[string]float64 // main session -> last activity
}

func newAccount(id, provider string) *account {
	return &account{profile: profiles[provider], id: id, provider: provider, r5: -1, rW: -1, tObs: -1,
		active: map[string]float64{}}
}

func (a *account) setWeight(w float64) {
	a.k5Hint, a.kWHint = 1/(w*a.cap5), 1/(w*a.capW)
}

func (a *account) k5() float64 {
	if a.sumDu5 >= 0.05 && a.sumW5 > 0 {
		return a.sumDu5 / a.sumW5
	}
	return a.k5Hint
}

func (a *account) kW() float64 {
	if a.sumDuW >= 0.01 && a.sumWW > 0 {
		return a.sumDuW / a.sumWW
	}
	return a.kWHint
}

// weekly returns the weekly utilization now and the minutes until the weekly reset.
func (a *account) weekly(t float64) (float64, float64) {
	switch {
	case a.rW < 0:
		return 0, week * 0.5
	case a.rW > t:
		return a.uW, a.rW - t
	case a.weeklyFirstUse:
		return 0, week
	}
	return 0, a.rW + (math.Floor((t-a.rW)/week)+1)*week - t
}

func (a *account) exhausted(t float64) bool {
	return a.has5h && a.r5 > t && a.u5 >= 1 || a.rW > t && a.uW >= 1
}

func (a *account) decay(t float64) {
	if dt := t - a.accT; dt > 0 {
		f := math.Exp(-dt * itau)
		a.acc5 *= f
		a.accWShort *= f
		a.accWLong *= math.Exp(-dt * itauWl)
		a.accT = t
	}
}

// rateAbs is the current burn in units per minute.
func (a *account) rateAbs(t float64) float64 {
	f := math.Exp(-(t - a.accT) * itau)
	if a.has5h {
		return a.acc5 * f * itau / a.k5()
	}
	return a.accWShort * f * itau / a.kW()
}

func (a *account) longBurn(t float64) float64 {
	return a.accWLong * math.Exp(-(t-a.accT)*itauWl) * itauWl
}

func (a *account) nActive(t float64) int {
	for sid, last := range a.active {
		if t-last > 60 {
			delete(a.active, sid)
		}
	}
	return len(a.active)
}

type projection struct {
	proj5, projW float64 // projected utilization at the 5h reset / over the weekly look-ahead
	h5, hW       float64 // headroom left at those points, in units
	eW, ttrW     float64 // weekly units that would expire unused at the reset; minutes to the weekly reset
}

// project adds an extra burn b (units per minute) to the account's current burn.
func (a *account) project(t, b float64) (p projection) {
	k5, kW := a.k5(), a.kW()
	f := math.Exp(-(t - a.accT) * itau)
	if a.has5h {
		u5, ttr := a.u5, a.r5-t
		if a.r5 <= t { // closed window: the next request opens it
			u5, ttr = 0, window5h
			if a.hourAnchor {
				ttr = window5h - math.Mod(t, 60)
			}
		}
		p.proj5 = u5 + (a.acc5*f*itau+b*k5)*ttr
		p.h5 = (1 - p.proj5) / k5
	} else {
		p.h5 = math.Inf(1)
	}
	var uW float64
	uW, p.ttrW = a.weekly(t)
	p.projW = uW + (a.accWShort*f*itau+b*kW)*min(p.ttrW, weeklyLookahead)
	p.hW = (1 - p.projW) / kW
	p.eW = max((1-uW-a.longBurn(t)*p.ttrW)/kW, 0)
	return p
}

func (a *account) reject(t, u5, r5, uW, rW float64) {
	if a.has5h && u5 >= 0.999 {
		u5 = max(u5, 1)
	}
	a.u5, a.r5, a.uW, a.rW = u5, r5, uW, rW
	if a.tObs < 0 {
		a.tObs = t
	}
}
