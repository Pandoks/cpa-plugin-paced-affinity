"""winner.py - reference implementation of the recommended CLIProxyAPI routing strategy:
"PACED AFFINITY" v4 (the simulator's tuned HYBRID). The previous version is winner_v3.py.

ONE INTENTIONAL DEVIATION from the study's v4 (md5 c4a52e7d63366655752fc35adf44939d): the first response observed
from an account only records its headers -- no burn delta and no calibration sample for either window. v4 counted the
whole current 5h utilization as that one request's usage, which is only right when the plugin starts on a fresh window;
after a proxy restart mid-window it spiked the burn estimate and underestimated the account's 5h capacity.

Dependency-free, single file, written to be ported line-by-line to a Go scheduler plugin.
tests/test_winner.py checks it makes exactly the same decisions as the simulated HYBRID.

WHAT IT DOES
  1. Affinity: a chat stays on its account for as long as its binding lives. The binding is sliding,
     1h idle TTL, which matches Claude Code's 1h main-thread cache TTL. Only a 429 or a known-exhausted
     account moves it before then. A warm chat is therefore never moved voluntarily, and once a chat has
     been idle for an hour its cache is cold anyway, so re-placing it costs nothing.
  2. Paced placement: a new chat, an expired binding or a failover goes to the account with the most
     PROJECTED HEADROOM at its next 5h reset, i.e. (1 - u5 - burn*time_to_reset) in absolute units, so
     plan sizes are comparable. The minimum is taken with a short weekly look-ahead. Accounts projected to
     cross (1 - margin) before their reset are skipped while any other account is feasible. A closed 5h
     window counts as full headroom, which opens idle accounts' windows early: an earlier start means an
     earlier reset and more 5h capacity during the day.
  3. Weekly "use it before it resets": when the observed weekly pressure is high, defined as mean projected
     weekly utilization at reset >= 0.4 with a full effect at 0.7, the strategy also prefers accounts whose
     weekly allowance would otherwise expire unused before their weekly reset. When weekly pressure is low
     this term is off, because steering by weekly reset then only concentrates load and costs 5h capacity.
     v4 change, the EXPIRING-ALLOWANCE OVERRIDE: the 3% weekly safety margin is waived for an account whose
     weekly allowance is projected to expire unused at its reset (1 - uW - long_run_burn * ttrW > 0). v3
     stranded the last <=3% of the soonest-resetting account when the pool was saturated; live round-robin
     drains it until the 429. This was found end-to-end and reproduced in the simulator's "saturated" family.
     v4 also REOPENS EXPIRED FIRST-USE WEEKLY WINDOWS FIRST (Codex Pro): an account whose rolling 7-day window has
     expired holds idle capacity, and its next window only starts on the next request, so a new chat goes there
     first (if it is feasible).
  4. Subagents (non-fork) do not read the parent's cache. Each gets its own binding (its agent id) and is
     placed by the same rule. Forks share the parent's prefix and follow the family binding.

INPUTS - every one is observable by a CLIProxyAPI scheduler plugin + usage observer:
  static config : account id, provider, plan-tier weight, rough capacity hint (a prior only; the real scale
                  is learned online), 5h anchoring rule, weekly anchoring rule (Claude: fixed weekly time;
                  Codex: 7d from first use)  -> account attributes
  pick()        : provider, session id, family id, agent kind (main / subagent / fork),
                  candidate accounts (those the proxy does not have in cooldown)
  on_response() : account, ids, usage tokens (cache_read, cache_creation, output), rate-limit headers
                  Claude: anthropic-ratelimit-unified-5h-utilization / -5h-reset, -7d-utilization / -7d-reset
                  Codex : x-codex-*-used-percent/100 and *-reset-at, classified by *-window-minutes
  on_reject()   : account + the headers carried by the 429
No ground truth is used: utilization and reset times are "as of the last response from that account".

ID MAPPING (verified against a real CLIProxyAPI v8.0.12):
  * Claude Code subagents carry the PARENT's session id and are told apart by X-Claude-Code-Agent-Id.
    Call pick/on_response with sid = agent id (or the session id for the main thread) and
    fam = session id. Forks: kind=FORK, fam = session id.
  * Codex: sid = fam = session-id / prompt_cache_key thread id.
  * Codex Pro reports its WEEKLY window as "primary". Classify windows by *-window-minutes
    (300 -> 5h, 10080 -> weekly), never by the primary/secondary name.
PROXY FACTS the port must respect:
  * The proxy cools an account down only on a 429. A 200 with utilization >= 1 never cools it down,
    so concurrent admits overshoot slightly; this router therefore also skips accounts it knows are
    exhausted.
  * Usage records reach the observer asynchronously, so this router binds the session at pick time.
  * When every account is cooling, the plugin is not consulted and the client gets a 429.

UNITS: times are MINUTES (float); in Go use unix_seconds/60.0. Utilization is a fraction (0..1+).
The token weights below are only the plugin's ASSUMPTION (API price ratios), used to convert tokens to
utilization; the true meter scale is learned from header deltas.
"""
from __future__ import annotations

import math

MAIN, SUBAGENT, FORK = 0, 1, 2
WINDOW_5H = 300.0            # minutes
WEEK = 10080.0               # minutes
INF = float("inf")

# Assumed per-token weights relative to base input: (cache_read, write_main, write_subagent, output)
WEIGHTS = {"claude": (0.05, 2.0, 1.25, 5.0),      # Opus 5.5: read .05x, 1h write 2x, 5m write 1.25x, out 5x
           "codex": (0.10, 1.0, 1.0, 5.0)}        # Codex credits: cached .1x, no write premium, out 5x


class Config:
    """Tuned values: results/tuned.json ("hy"), from train seeds 100-109; evaluated on held-out seeds 0-29."""

    def __init__(self, **kw):
        self.affinity_ttl = 60.0        # sliding binding TTL in minutes (== Claude Code main-thread cache TTL)
        self.subagent_rule = "free"     # "free": own binding, placed like a session | "inherit": follow parent
        self.margin_5h = 0.05           # feasible if projected 5h utilization at reset <= 1 - margin
        self.margin_weekly = 0.03       # ... and projected weekly utilization over the look-ahead <= 1 - margin
        self.tau = 30.0                 # burn-rate EWMA time constant (minutes)
        self.tau_weekly_long = 1440.0   # long-run weekly burn EWMA (minutes)
        self.tau_session = 30.0         # per-session burn EWMA (minutes)
        self.weekly_lookahead = 300.0   # weekly wall look-ahead (minutes)
        self.weekly_expire_override = True  # v4: waive the weekly margin if allowance would expire unused
        self.reopen_expired_weekly = True   # v4: prefer an account whose first-use weekly window has expired
        self.weekly_weight = 2.0        # weight of the weekly-expiry urgency term
        self.weekly_gate = True         # scale weekly_weight by observed weekly pressure
        self.gate_lo = 0.4
        self.gate_hi = 0.7
        self.burn_mult = 1.0            # forecast multiplier for a new session's burn
        for k, v in kw.items():
            if not hasattr(self, k):
                raise KeyError(k)
            setattr(self, k, v)


class Account:
    def __init__(self, idx, provider, plan, has_5h, cap5_hint, capW_hint, hour_anchor=False,
                 weekly_first_use=False):
        self.idx = idx
        self.provider = provider
        self.plan = plan
        self.has_5h = has_5h
        self.hour_anchor = hour_anchor            # 5h window start floored to the hour
        self.weekly_first_use = weekly_first_use  # Codex: weekly window opens on first use after expiry
        self.k5_hint = (1.0 / cap5_hint) if has_5h else 0.0   # utilization per weighted unit (prior)
        self.kW_hint = 1.0 / capW_hint
        # last observed headers
        self.u5 = 0.0
        self.r5 = -1.0
        self.uW = 0.0
        self.rW = -1.0
        self.t_obs = -1.0
        # burn-rate accumulators (utilization, exponentially decayed)
        self.acc5 = 0.0
        self.acc_w_short = 0.0
        self.acc_w_long = 0.0
        self.acc_t = 0.0
        # online calibration: sum of utilization deltas / sum of assumed-weight units
        self.sum_du5 = 0.0
        self.sum_w5 = 0.0
        self.sum_duW = 0.0
        self.sum_wW = 0.0
        self.active = {}          # main session id -> last activity time (for the mean burn per session)


class Session:
    def __init__(self, acct, t, ctx, cost, kind):
        self.acct = acct
        self.last_t = t
        self.ctx = ctx
        self.burn = cost          # decayed weighted units (rate = burn / tau_session)
        self.burn_t = t
        self.kind = kind


class Binding:
    def __init__(self, acct, t):
        self.acct = acct
        self.last_t = t


class Router:
    def __init__(self, accounts, config=None):
        self.cfg = config or Config()
        self.acc = accounts
        self.pool = {}
        for a in accounts:
            self.pool.setdefault(a.provider, []).append(a.idx)
        self.sess = {}
        self.bind = {}
        c = self.cfg
        self.itau = 1.0 / c.tau                 # inverses, so arithmetic matches the simulator bit-for-bit
        self.itauWl = 1.0 / c.tau_weekly_long
        self.itaus = 1.0 / c.tau_session

    # ------------------------------------------------------------------ helpers
    def _key(self, sid, fam, kind):
        if kind == MAIN or (kind == SUBAGENT and self.cfg.subagent_rule != "inherit"):
            return sid
        return fam               # forks (and inherited subagents) follow the family binding

    def _k5(self, a):
        A = self.acc[a]
        if A.sum_du5 >= 0.05 and A.sum_w5 > 0.0:
            return A.sum_du5 / A.sum_w5
        return A.k5_hint

    def _kW(self, a):
        A = self.acc[a]
        if A.sum_duW >= 0.01 and A.sum_wW > 0.0:
            return A.sum_duW / A.sum_wW
        return A.kW_hint

    def _weekly(self, a, t):
        """(utilization now, minutes to weekly reset)."""
        A = self.acc[a]
        if A.rW < 0.0:
            return 0.0, WEEK * 0.5
        if A.rW > t:
            return A.uW, A.rW - t
        if A.weekly_first_use:
            return 0.0, WEEK
        k = math.floor((t - A.rW) / WEEK) + 1
        return 0.0, A.rW + k * WEEK - t

    def _exhausted(self, a, t):
        A = self.acc[a]
        if A.has_5h and A.r5 > t and A.u5 >= 1.0:
            return True
        return A.rW > t and A.uW >= 1.0

    def _rate_abs(self, a, t):
        A = self.acc[a]
        dt = t - A.acc_t
        if A.has_5h:
            return A.acc5 * math.exp(-dt * self.itau) * self.itau / self._k5(a)
        return A.acc_w_short * math.exp(-dt * self.itau) * self.itau / self._kW(a)

    def _n_active(self, a, t):
        d = self.acc[a].active
        stale = [k for k, v in d.items() if t - v > 60.0]
        for k in stale:
            del d[k]
        return len(d)

    def _new_session_burn(self, prov, t):
        """Mean burn per active main session (weighted units/min) - forecast for a new session."""
        tot = 0.0
        n = 0
        for a in self.pool[prov]:
            tot += self._rate_abs(a, t)
            n += self._n_active(a, t)
        return tot / n if n > 0 else 0.0

    def _session_burn(self, sid, t):
        s = self.sess.get(sid)
        if s is None:
            return 0.0
        return s.burn * math.exp(-(t - s.burn_t) * self.itaus) * self.itaus

    def _project(self, a, t, b):
        """Projection if an extra burn b (weighted units/min) is added to account a.
        Returns (proj5, projW, head5_abs, headW_abs, expiringW_abs, ttrW).
        expiringW_abs > 0 means weekly allowance is projected to expire unused at the reset."""
        c = self.cfg
        A = self.acc[a]
        k5 = self._k5(a)
        kW = self._kW(a)
        dt = t - A.acc_t
        f = math.exp(-dt * self.itau)
        if A.has_5h:
            if A.r5 > t:
                u5 = A.u5
                ttr = A.r5 - t
            else:                                         # closed window: would open now
                u5 = 0.0
                ttr = (WINDOW_5H - (t % 60.0)) if A.hour_anchor else WINDOW_5H
            r5 = A.acc5 * f * self.itau
            proj5 = u5 + (r5 + b * k5) * ttr
            h5 = (1.0 - proj5) / k5
        else:
            proj5, h5 = 0.0, INF
        uW, ttrW = self._weekly(a, t)
        rWs = A.acc_w_short * f * self.itau
        rWl = A.acc_w_long * math.exp(-dt * self.itauWl) * self.itauWl
        hz = ttrW if ttrW < c.weekly_lookahead else c.weekly_lookahead
        projW = uW + (rWs + b * kW) * hz
        hW = (1.0 - projW) / kW
        eW = (1.0 - uW - rWl * ttrW) / kW
        if eW < 0.0:
            eW = 0.0
        return proj5, projW, h5, hW, eW, ttrW

    def _weekly_pressure(self, cs, t):
        """Mean projected weekly utilization at reset (long-run burn), mapped to [0, 1]."""
        c = self.cfg
        tot = 0.0
        for a in cs:
            A = self.acc[a]
            uW, ttrW = self._weekly(a, t)
            rWl = A.acc_w_long * math.exp(-(t - A.acc_t) * self.itauWl) * self.itauWl
            tot += uW + rWl * ttrW
        P = tot / len(cs)
        g = (P - c.gate_lo) / (c.gate_hi - c.gate_lo)
        return 0.0 if g < 0.0 else (1.0 if g > 1.0 else g)

    # ------------------------------------------------------------------ placement
    def _place(self, cands, t, b):
        """Account for a new / unbound / failed-over session with forecast burn b."""
        c = self.cfg
        cs = [a for a in cands if not self._exhausted(a, t)]
        if not cs:
            cs = list(cands)
        bb = b * c.burn_mult
        rows = []
        fallback, fallback_h = -1, -INF
        for a in cs:
            proj5, projW, h5, hW, eW, ttrW = self._project(a, t, bb)
            h = h5 if h5 < hW else hW
            if h > fallback_h:
                fallback_h, fallback = h, a
            ok_w = projW <= 1.0 - c.margin_weekly or (c.weekly_expire_override and eW > 0.0)
            if proj5 <= 1.0 - c.margin_5h and ok_w:
                rows.append((a, h, eW / (ttrW if ttrW > 60.0 else 60.0)))
        if not rows:
            return fallback                     # nothing feasible: most headroom
        max_h = max(r[1] for r in rows)
        max_u = max(r[2] for r in rows)
        lam = c.weekly_weight
        if lam > 0.0 and c.weekly_gate:
            lam *= self._weekly_pressure(cs, t)
        best, best_pri = -1, -INF
        for a, h, u in rows:
            A = self.acc[a]
            if c.reopen_expired_weekly and A.weekly_first_use and 0.0 <= A.rW <= t:
                pri = 10.0 + 1e-9 * h                # expired first-use window: reopen it (starts its clock)
                if pri > best_pri:
                    best_pri, best = pri, a
                continue
            uw = u / max_u if max_u > 0 else 0.0
            if self.acc[a].has_5h:
                pri = (h / max_h if max_h > 0 else 0.0) + lam * uw
            else:
                pri = uw                         # weekly-only pool: use the expiring weekly allowance first
            pri += 1e-9 * h
            if pri > best_pri:
                best_pri, best = pri, a
        return best

    def _transfer(self, sid, a_from, a_to, t):
        """On failover, move this session's share of the burn estimate to the new account."""
        s = self.sess.get(sid)
        if s is None:
            return
        c = self.cfg
        units = s.burn * math.exp(-(t - s.burn_t) * self.itaus)
        for a, sign in ((a_from, -1.0), (a_to, 1.0)):
            A = self.acc[a]
            dt = t - A.acc_t
            if dt > 0.0:
                f = math.exp(-dt * self.itau)
                A.acc5 *= f
                A.acc_w_short *= f
                A.acc_w_long *= math.exp(-dt * self.itauWl)
                A.acc_t = t
            if A.has_5h:
                A.acc5 = max(0.0, A.acc5 + sign * units * self._k5(a) * c.tau / c.tau_session)
            A.acc_w_short = max(0.0, A.acc_w_short + sign * units * self._kW(a) * c.tau / c.tau_session)
        self.acc[a_from].active.pop(sid, None)
        self.acc[a_to].active[sid] = t

    # ------------------------------------------------------------------ plugin hooks
    def pick(self, prov, sid, fam, kind, flags, cands, t):
        """Scheduler hook. cands = this provider's accounts not in proxy cooldown. Returns one of them.
        `flags` (compaction headers) is accepted for interface compatibility; the tuned strategy ignores it."""
        key = self._key(sid, fam, kind)
        b = self.bind.get(key)
        old = -1
        if b is not None and t - b.last_t <= self.cfg.affinity_ttl:
            a = b.acct
            if a in cands and not self._exhausted(a, t):
                return a                          # affinity: never move a live binding voluntarily
            old = a                               # bound account limited -> forced failover
        bs = self._session_burn(sid, t) if sid in self.sess else self._new_session_burn(prov, t)
        n = self._place(cands, t, bs)
        if old >= 0 and n != old and kind == MAIN:
            self._transfer(sid, old, n, t)
        self.bind[key] = Binding(n, t)
        return n

    def on_response(self, a, t, sid, fam, kind, flags, cache_read, cache_write, output, u5, r5, uW, rW):
        """Usage-observer hook: called for every successful response."""
        c = self.cfg
        A = self.acc[a]
        w = WEIGHTS[A.provider]
        cost = (cache_read * w[0] + cache_write * (w[1] if kind == MAIN else w[2]) + output * w[3]) * 1e-6
        if A.t_obs < 0.0:
            d5 = 0.0
            dW = 0.0
        else:
            d5 = (u5 - A.u5) if r5 == A.r5 else u5    # new 5h window -> delta from 0
            dW = (uW - A.uW) if rW == A.rW else uW    # new weekly window -> delta from 0
            if d5 < 0.0:
                d5 = 0.0
            if dW < 0.0:
                dW = 0.0
            A.sum_duW += dW
            A.sum_wW += cost
            A.sum_du5 += d5
            A.sum_w5 += cost
        dt = t - A.acc_t
        if dt > 0.0:
            f = math.exp(-dt * self.itau)
            A.acc5 *= f
            A.acc_w_short *= f
            A.acc_w_long *= math.exp(-dt * self.itauWl)
            A.acc_t = t
        A.acc5 += d5
        A.acc_w_short += dW
        A.acc_w_long += dW
        A.u5, A.r5, A.uW, A.rW, A.t_obs = u5, r5, uW, rW, t
        ctx = cache_read + cache_write + output
        s = self.sess.get(sid)
        if s is None:
            self.sess[sid] = Session(a, t, ctx, cost, kind)
        else:
            if kind == MAIN and s.acct != a:
                self.acc[s.acct].active.pop(sid, None)
            dts = t - s.burn_t
            f = math.exp(-dts * self.itaus) if dts > 0.0 else 1.0
            s.burn = s.burn * f + cost
            s.burn_t = t
            s.acct = a
            s.last_t = t
            s.ctx = ctx
        if kind == MAIN:
            A.active[sid] = t
        b = self.bind.get(self._key(sid, fam, kind))
        if b is not None:
            b.acct = a
            b.last_t = t

    def on_reject(self, a, t, u5, r5, uW, rW):
        """429 from account a: record its headers (the proxy also puts it in cooldown)."""
        A = self.acc[a]
        A.u5 = max(u5, 1.0) if (A.has_5h and u5 >= 0.999) else u5
        A.r5 = r5
        A.uW = uW
        A.rW = rW
        if A.t_obs < 0.0:
            A.t_obs = t


def accounts_from_plugin_config(plug):
    """Build Account objects from the simulator's plugin-visible account config."""
    return [Account(i, p["prov"], p["plan"], p["has5"], p["cap5_hint"], p["capW_hint"],
                    hour_anchor=p.get("anchor5") == "hour", weekly_first_use=p.get("wk_anchor") == "first")
            for i, p in enumerate(plug)]


if __name__ == "__main__":
    # Tiny self-contained demo (no simulator): two Claude accounts (Max20 + Max5), one chat, one subagent.
    accts = [Account(0, "claude", 20.0, True, cap5_hint=200.0, capW_hint=1200.0),
             Account(1, "claude", 5.0, True, cap5_hint=50.0, capW_hint=300.0)]
    r = Router(accts)
    t = 9 * 60.0                                         # 09:00, minutes
    a = r.pick("claude", "sess-1", "sess-1", MAIN, 0, [0, 1], t)
    r.on_response(a, t, "sess-1", "sess-1", MAIN, 0, 0, 30000, 400, 0.01, t + 300, 0.10, t + 3 * 1440)
    s = r.pick("claude", "agent-7", "sess-1", SUBAGENT, 0, [0, 1], t + 1)
    again = r.pick("claude", "sess-1", "sess-1", MAIN, 0, [0, 1], t + 2)
    print(f"chat -> account {a}; subagent -> account {s}; chat again -> account {again} (sticky)")
    r.on_reject(a, t + 3, 1.02, t + 300, 0.2, t + 3 * 1440)   # 429 on the chat's account
    other = [x for x in (0, 1) if x != a]
    print("after 429 the chat fails over to", r.pick("claude", "sess-1", "sess-1", MAIN, 0, other, t + 3))
