#!/usr/bin/env python3
"""Writes the original projected-policy replay traces for equivalence_test.go.
The Go replay explicitly selects claude-placement: projected; these traces are
not the contract for the observed-weekly default. Run: python3 testdata/gen_traces.py

An event-driven Claude Code / Codex workload (sessions, subagents, forks, resumed sessions, failover after 429s,
proxy cooldowns, async usage records) runs against simulated subscription meters, and the reference router
(reference/winner.py: the study's v4 plus the first-observation fix in its docstring) makes every pick. A trace records what the plugin sees -- scheduler picks
(candidates, session metadata) and usage records (tokens, rate-limit headers) -- together with the reference's pick.

The adapter between the two mirrors the plugin (signals.go, router.go observe, account.go profiles):
  kind     ":agent:" in the session id -> subagent; otherwise a parent session -> fork; otherwise main
  prior    credential weight x per-provider capacity
  headers  parsed like the plugin; a missing window keeps its last value; a 429 without any window is ignored;
           a Codex window shorter than a day is the 5h window and marks the account as having one
  no session id -> not handled (the built-in selector picks)
"""
import gzip
import hashlib
import heapq
import json
import os
import random
import sys

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "reference"))
import winner as W  # noqa: E402

REFERENCE_MD5 = "136c5821a7ac4a398e86d226d5bbca7a"
PRIORS = {"claude": (7.5, 45.0), "codex": (1.0, 6.0)}  # account.go profiles: cap5, capW per unit of weight
T0 = 1790060400  # Tuesday 2026-09-22 07:00 UTC
HOUR, DAY, WEEK = 3600, 86400, 604800


class CountingRouter(W.Router):
    """winner.Router that also counts how often v4's F5 / F6 changed a placement (_place has no side effects)."""

    places = f5 = f6 = 0

    def _place(self, cands, t, b):
        best = super()._place(cands, t, b)
        c = self.cfg
        c.weekly_expire_override = False
        self.f5 += super()._place(cands, t, b) != best
        c.weekly_expire_override, c.reopen_expired_weekly = True, False
        self.f6 += super()._place(cands, t, b) != best
        c.reopen_expired_weekly = True
        self.places += 1
        return best


def session_key(sid, parent):
    if ":agent:" in sid:
        return W.SUBAGENT, sid
    if parent:
        return W.FORK, parent
    return W.MAIN, sid


def epoch_minutes(v):
    return float(v) / 60.0


def windows(prov, hdr, now):
    """(util, reset minutes) or None for the 5h and the weekly window, parsed like signals.go quotaWindows."""
    get = lambda k: hdr.get(k, [None])[0]  # noqa: E731
    if prov == "claude":
        out = []
        for n in ("5h", "7d"):
            u, reset = get(f"Anthropic-Ratelimit-Unified-{n}-Utilization"), get(f"Anthropic-Ratelimit-Unified-{n}-Reset")
            out.append(None if u is None or reset is None else (float(u), epoch_minutes(reset)))
        return tuple(out)
    w5 = wW = None
    for slot in ("Primary", "Secondary"):
        pct, minutes = get(f"X-Codex-{slot}-Used-Percent"), get(f"X-Codex-{slot}-Window-Minutes")
        if pct is None or minutes is None:
            continue
        reset = get(f"X-Codex-{slot}-Reset-At")
        w = (float(pct) / 100, epoch_minutes(reset) if reset is not None
             else (now + float(get(f"X-Codex-{slot}-Reset-After-Seconds"))) / 60.0)
        if float(minutes) < 1440:
            w5 = w
        else:
            wW = w
    return w5, wW


class Stream:
    def __init__(self, sid, parent, prov, kind, left, ctx, ttl, last=(None, -1e18)):
        self.sid, self.parent, self.prov, self.kind, self.left, self.ctx, self.ttl = sid, parent, prov, kind, left, ctx, ttl
        self.last_acct, self.last_t = last
        self.children = self.fails = 0


class Gen:
    def __init__(self, name, seed, accounts, days, arrivals_per_hour, codex_share=0.3, tok=1.0, p_sub=0.12,
                 p_fork=0.02, p_resume=0.04, p_missing=0.02, p_mixed=0.1, p_nosession=0.01, p_5xx=0.003,
                 p_unavail=0.03):
        self.name, self.seed, self.days = name, seed, days
        self.rng = rng = random.Random(seed)
        self.arrivals, self.codex_share, self.tok = arrivals_per_hour, codex_share, tok
        self.p = dict(sub=p_sub, fork=p_fork, resume=p_resume, missing=p_missing, mixed=p_mixed,
                      nosession=p_nosession, x5=p_5xx, unavail=p_unavail)
        # the plugin starts mid-window, as after a proxy restart: prior 5h usage, reset within 1-4 hours
        self.acc = [dict(id=aid, prov=prov, weight=weight, has5=prov == "claude" or plan == "plus", cap5=cap5, capW=capW,
                         u5=rng.uniform(0, 0.8), r5=T0 + rng.randint(1, 4) * HOUR, uW=uw0, rW=T0 + int(reset_in_h * HOUR),
                         cool=0.0, served=0.0, n429=0)
                    for aid, prov, weight, plan, cap5, capW, uw0, reset_in_h in accounts]
        self.R = self.router()
        self.pools = {p: [i for i, a in enumerate(self.acc) if a["prov"] == p] for p in ("claude", "codex")}
        self.events, self.heap, self.seq, self.n_sessions = [], [], 0, 0
        self.ended = []  # (end time, stream) of main sessions that can be resumed
        self.stats = dict(picks=0, handled=0, failover_picks=0, usage=0, http429=0, http5xx=0, no_candidates=0,
                          missing_all=0, missing_weekly=0, restarts=0)

    def router(self):
        return CountingRouter([W.Account(i, a["prov"], "", a["prov"] == "claude", a["weight"] * PRIORS[a["prov"]][0],
                                         a["weight"] * PRIORS[a["prov"]][1], hour_anchor=a["prov"] == "claude",
                                         weekly_first_use=a["prov"] == "codex") for i, a in enumerate(self.acc)])

    # ---------------------------------------------------------------- event queue
    def at(self, t, fn, *args):
        self.seq += 1
        heapq.heappush(self.heap, (round(t, 3), self.seq, fn, args))

    def run(self):
        self.at(T0, self.arrive)
        self.at(T0 + self.rng.uniform(0.5, 2) * DAY, self.restart)
        end = T0 + self.days * DAY
        while self.heap and self.heap[0][0] < end:
            t, _, fn, args = heapq.heappop(self.heap)
            fn(t, *args)
        return self

    def restart(self, t):
        """The proxy restarts (plugin install / upgrade): the router starts over, mid-window."""
        old, self.R = self.R, self.router()
        self.R.places, self.R.f5, self.R.f6 = old.places, old.f5, old.f6
        self.events.append(dict(op="restart", t=t))
        self.stats["restarts"] += 1
        self.at(t + self.rng.uniform(0.5, 2) * DAY, self.restart)

    # ---------------------------------------------------------------- workload
    def arrive(self, t):
        rng = self.rng
        busy = 8 <= (t - T0 + 7 * HOUR) % DAY / HOUR < 22
        if rng.random() < (1.0 if busy else 0.25):
            self.start(t)
        self.at(t + rng.expovariate(self.arrivals / HOUR), self.arrive)

    def start(self, t):
        rng, p = self.rng, self.p
        prov = "codex" if rng.random() < self.codex_share else "claude"
        old = [i for i, (end, s) in enumerate(self.ended) if HOUR < t - end < 3 * DAY and s.prov == prov]
        if old and rng.random() < p["resume"]:
            _, s = self.ended.pop(rng.choice(old))  # claude --resume: same session id, cold cache
            s.left, s.last_acct = rng.randint(3, 40), None
        else:
            self.n_sessions += 1
            sid = "" if rng.random() < p["nosession"] else f"{prov}:{self.seed}-{self.n_sessions}"
            s = Stream(sid, "", prov, W.MAIN, min(200, int(rng.lognormvariate(3.2, 0.8)) + 1),
                       int(rng.uniform(20e3, 60e3) * self.tok), HOUR if prov == "claude" else 30 * 60)
        self.at(t, self.request, s, ())

    def gap(self, s):
        rng = self.rng
        if s.kind == W.SUBAGENT:
            return rng.uniform(1, 10)
        r = rng.random()
        return rng.uniform(2, 30) if r < 0.7 else rng.uniform(30, 600) if r < 0.95 else rng.uniform(HOUR, 6 * HOUR)

    def tokens(self, s, a, t):
        rng, tok = self.rng, self.tok
        warm = a == s.last_acct and t - s.last_t <= s.ttl
        new, out = int(rng.uniform(1e3, 6e3) * tok), int(rng.uniform(200, 3000) * tok ** 0.5)
        if s.prov == "claude":
            read, write = (s.ctx, new) if warm else (0, s.ctx + new)
            toks = [rng.randint(1, 300), read, write, out]
        else:
            toks = [s.ctx + new, s.ctx if warm else 0, 0, out]
        s.ctx += new + out
        if s.ctx > 160e3 * tok:  # compaction
            s.ctx = int(30e3 * tok)
        return toks

    def units(self, prov, kind, toks):
        inp, read, write, out = toks
        if prov == "claude":
            u = inp + read * 0.05 + write * (2.0 if kind == W.MAIN else 1.25) + out * 5
        else:
            u = (inp - read) + read * 0.1 + out * 5
        return u * 1e-6 * self.rng.uniform(0.9, 1.1)

    # ---------------------------------------------------------------- meters
    def roll(self, A, t):
        if A["has5"] and t >= A["r5"]:  # a closed window opens with this request
            A["u5"] = 0.0
            A["r5"] = int(t // HOUR * HOUR) + 5 * HOUR if A["prov"] == "claude" else int(t) + 5 * HOUR
        if t >= A["rW"]:
            A["uW"] = 0.0
            if A["prov"] == "codex":
                A["rW"] = int(t) + WEEK
            while A["rW"] <= t:
                A["rW"] += WEEK

    def limited(self, A):
        return A["has5"] and A["u5"] >= 1 or A["uW"] >= 1

    def headers(self, A, t):
        rng = self.rng
        if A["prov"] == "claude":
            h = {"Anthropic-Ratelimit-Unified-5h-Utilization": [f"{A['u5']:.4f}"],
                 "Anthropic-Ratelimit-Unified-5h-Reset": [str(A["r5"])],
                 "Anthropic-Ratelimit-Unified-7d-Utilization": [f"{A['uW']:.4f}"],
                 "Anthropic-Ratelimit-Unified-7d-Reset": [str(A["rW"])]}
            weekly = "Anthropic-Ratelimit-Unified-7d"
        else:
            slots = ([(A["u5"], 300, A["r5"])] if A["has5"] else []) + [(A["uW"], 10080, A["rW"])]
            h = {}
            for slot, (u, minutes, reset) in zip(("Primary", "Secondary"), slots):
                h[f"X-Codex-{slot}-Used-Percent"] = [str(round(u * 100))]
                h[f"X-Codex-{slot}-Window-Minutes"] = [str(minutes)]
                if rng.random() < 0.1:
                    h[f"X-Codex-{slot}-Reset-After-Seconds"] = [str(reset - int(t))]
                else:
                    h[f"X-Codex-{slot}-Reset-At"] = [str(reset)]
            weekly = "X-Codex-" + ("Secondary" if A["has5"] else "Primary")
        r = rng.random()
        if r < self.p["missing"]:
            self.stats["missing_all"] += 1
            return {}
        if r < 2 * self.p["missing"]:
            self.stats["missing_weekly"] += 1
            return {k: v for k, v in h.items() if not k.startswith(weekly)}
        return h

    # ---------------------------------------------------------------- requests
    def request(self, t, s, tried):
        rng, p = self.rng, self.p
        cands = [i for i in self.pools[s.prov] if self.acc[i]["cool"] <= t and i not in tried and rng.random() > p["unavail"]]
        if not cands:  # every account cooling: the client sees an error and retries later
            self.stats["no_candidates"] += 1
            self.retry(t, s)
            return
        want = None
        if s.sid:
            kind, _ = session_key(s.sid, s.parent)
            a = want = self.R.pick(s.prov, s.sid, s.parent or s.sid, kind, 0, cands, t / 60.0)
            self.stats["handled"] += 1
        else:
            a = rng.choice(cands)  # not handled: the built-in selector picks
        self.stats["picks"] += 1
        self.stats["failover_picks"] += bool(tried)
        self.events.append(dict(op="pick", t=t, prov="" if rng.random() < p["mixed"] else s.prov, sid=s.sid,
                                parent=s.parent, cands=[self.acc[i]["id"] for i in cands],
                                want=None if want is None else self.acc[want]["id"]))
        A = self.acc[a]
        self.roll(A, t)
        if self.limited(A):
            self.stats["http429"] += 1
            self.at(t + rng.uniform(0.05, 0.8), self.usage, a, s.sid, s.parent, 429, [0, 0, 0, 0], self.headers(A, t))
            limit = A["r5"] if A["has5"] and A["u5"] >= 1 else A["rW"]
            A["cool"] = limit + rng.uniform(1, 30)
            A["n429"] += 1
            if len(tried) < 3:  # CPA fails over within the same request
                self.at(t + rng.uniform(0.01, 0.2), self.request, s, tried + (a,))
            else:
                self.retry(t, s)
        elif rng.random() < p["x5"]:
            self.stats["http5xx"] += 1
            self.at(t + rng.uniform(0.5, 5), self.usage, a, s.sid, s.parent, 500, [0, 0, 0, 0], self.headers(A, t))
            self.retry(t, s)
        else:
            self.at(t + rng.uniform(1, 40), self.complete, s, a, self.tokens(s, a, t))

    def retry(self, t, s):
        s.fails += 1
        if s.fails <= 4:
            self.at(t + self.rng.uniform(60, 600) * s.fails, self.request, s, ())
        else:  # the user gives up on this session
            s.left = s.fails = 0

    def complete(self, t, s, a, toks):
        rng, A = self.rng, self.acc[a]
        self.roll(A, t)
        units = self.units(s.prov, s.kind, toks)
        A["served"] += units
        s.fails = 0
        if A["has5"]:
            A["u5"] += units / A["cap5"]
        A["uW"] += units / A["capW"]
        self.at(t + rng.uniform(0, 0.3), self.usage, a, s.sid, s.parent, 200, toks, self.headers(A, t))
        s.last_acct, s.last_t, s.left = a, t, s.left - 1
        if s.kind != W.SUBAGENT and s.sid and rng.random() < self.p["sub"]:
            s.children += 1
            sub = Stream(f"{s.sid}:agent:a{s.children}", s.sid, s.prov, W.SUBAGENT, rng.randint(3, 15),
                         int(rng.uniform(15e3, 30e3) * self.tok), 300 if s.prov == "claude" else 30 * 60)
            self.at(t + rng.uniform(0.5, 3), self.request, sub, ())
        if s.kind == W.MAIN and s.sid and rng.random() < self.p["fork"]:
            s.children += 1
            fork = Stream(f"{s.sid}f{s.children}", s.sid, s.prov, W.FORK, rng.randint(2, 8), s.ctx, s.ttl, (a, t))
            self.at(t + self.gap(fork), self.request, fork, ())
        if s.left > 0:
            self.at(t + self.gap(s), self.request, s, ())
        elif s.kind == W.MAIN and s.sid:
            self.ended.append((t, s))

    def usage(self, t, a, sid, parent, status, toks, hdr):
        """The plugin's usage.handle: mirrors router.go observe."""
        self.stats["usage"] += 1
        self.events.append(dict(op="usage", t=t, auth=self.acc[a]["id"], prov=self.acc[a]["prov"], sid=sid,
                                parent=parent, status=status, tok=toks, hdr=hdr))
        A = self.R.acc[a]
        w5, wW = windows(A.provider, hdr, t)
        if w5 and not A.has_5h:
            A.has_5h, A.k5_hint = True, 1.0 / (self.acc[a]["weight"] * PRIORS[A.provider][0])
        u5, r5, uW, rW = A.u5, A.r5, A.uW, A.rW
        if w5:
            u5, r5 = w5
        if wW:
            uW, rW = wW
        if status == 200:
            kind, _ = session_key(sid, parent)
            inp, read, write, out = toks
            cw = inp + write if A.provider == "claude" else max(0, inp - read)
            self.R.on_response(a, t / 60.0, sid, parent or sid, kind, 0, read, cw, out, u5, r5, uW, rW)
        elif status == 429 and (w5 or wW):
            self.R.on_reject(a, t / 60.0, u5, r5, uW, rW)

    def write(self):
        R = self.R
        stats = dict(self.stats, placements=R.places, f5_changed=R.f5, f6_changed=R.f6)
        print("  units served / 429s per account:", {a["id"]: (round(a["served"]), a["n429"]) for a in self.acc})
        doc = dict(case=self.name, seed=self.seed, reference_md5=REFERENCE_MD5, stats=stats,
                   accounts=[[a["id"], a["prov"], a["weight"]] for a in self.acc], events=self.events)
        path = os.path.join(HERE, "traces", f"{self.name}.json.gz")
        with gzip.GzipFile(path, "wb", mtime=0) as f:
            f.write(json.dumps(doc, separators=(",", ":")).encode())
        print(f"{self.name:22s} {os.path.getsize(path) / 1e3:7.0f} kB  {stats}")
        return stats


# account: (id, provider, weight, plan, true 5h and weekly capacity in units, initial weekly utilization,
# hours until the weekly reset). The priors (weight x PRIORS) are deliberately off except in claude-priors-exact.
CODEX_PAIR = [("cx1", "codex", 24, "pro", 0, 60, 0.40, 50), ("cx2", "codex", 10, "prolite", 0, 50, 0.70, 15)]
CASES = [
    # name, seed, accounts, days, session arrivals per hour, keyword overrides
    ("claude-mixed-plans", 11, [("max20", "claude", 20, "", 10, 210, 0.30, 20), ("max5a", "claude", 5, "", 3, 63, 0.45, 70),
                                ("max5b", "claude", 5, "", 2.5, 55, 0.10, 130), ("pro", "claude", 1, "", 0.6, 14, 0.60, 100)]
     + CODEX_PAIR, 5, 2.0, {}),
    ("claude-priors-exact", 12, [("e4", "claude", 4, "", 30, 180, 0.20, 30), ("e1a", "claude", 1, "", 7.5, 45, 0.10, 60),
                                 ("e1b", "claude", 1, "", 7.5, 45, 0.30, 15)], 3, 2.0, dict(codex_share=0, tok=3)),
    ("claude-weekly-saturated", 13, [("w1", "claude", 5, "", 12, 60, 0.80, 30), ("w2", "claude", 5, "", 12, 60, 0.92, 80),
                                     ("w3", "claude", 1, "", 3, 15, 0.70, 140)], 8, 1.0, dict(codex_share=0)),
    ("codex-reopen", 14, [("x1", "codex", 24, "pro", 0, 90, 0.50, 3), ("x2", "codex", 10, "prolite", 0, 45, 0.30, 20),
                          ("x3", "codex", 10, "prolite", 0, 45, 0.85, 40), ("x4", "codex", 10, "prolite", 0, 45, 0.10, 60),
                          ("x5", "codex", 5, "pro", 0, 30, 0.60, 90), ("x6", "codex", 5, "pro", 0, 30, 0.20, 120)],
     12, 0.8, dict(codex_share=1)),
    ("codex-saturated-plus", 15, [("p1", "codex", 24, "pro", 0, 45, 0.60, 10), ("p2", "codex", 10, "prolite", 0, 40, 0.40, 60),
                                  ("p3", "codex", 2, "plus", 4, 20, 0.20, 100)], 8, 1.0, dict(codex_share=1)),
    ("mixed-chaos", 16, [("m20", "claude", 20, "", 8, 150, 0.5, 40), ("m5", "claude", 5, "", 2, 40, 0.2, 90),
                         ("mp", "claude", 1, "", 0.5, 8, 0.7, 10)] + CODEX_PAIR + [("cx3", "codex", 4, "plus", 3, 30, 0.2, 40)],
     4, 2.0, dict(codex_share=0.4, p_missing=0.06, p_mixed=0.3, p_nosession=0.03, p_5xx=0.02, p_sub=0.25, p_fork=0.05,
                  p_unavail=0.08)),
]

if __name__ == "__main__":
    with open(os.path.join(HERE, "reference", "winner.py"), "rb") as f:
        assert hashlib.md5(f.read()).hexdigest() == REFERENCE_MD5, "reference/winner.py was modified"
    os.makedirs(os.path.join(HERE, "traces"), exist_ok=True)
    total = {}
    for name, seed, accounts, days, rate, kw in CASES:
        for k, v in Gen(name, seed, accounts, days, rate, **kw).run().write().items():
            total[k] = total.get(k, 0) + v
    print("TOTAL", total)
