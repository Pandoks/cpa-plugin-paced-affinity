"""Extract proxied Claude Code and Codex requests from local logs for the routing simulation (sim_test.go).

    python3 -I testdata/extract.py testdata/work.tsv [since]

Writes one row per model response, in time order, tab-separated:
     0 time             completion (the response's first transcript entry), RFC 3339
     1 provider         claude | codex: the accounts that served it
     2 session          Claude Code session; Codex root session
     3 agent            subagent or workflow agent (Claude agentId, Codex thread); empty for main chats
     4 parent           Codex subagents: the direct parent thread; nested Claude Code subagents: <session>:agent:<parent
                        agent> (CPA's parent from X-Claude-Code-Parent-Agent-Id); otherwise empty
     5 model
     6 input            uncached input outside the cached prefix
     7 cache_read
     8 cache_write_5m
     9 cache_write_1h   Codex reports no writes; its uncached prompt is counted here
    10 output
    11 start            request start: the last user or attachment entry before the response; if unknown, and for
                        Codex, the completion less the latency of prefill and streamed output
    12 class            main | sub:<agentType> | wf (workflow agent) | harness (an A/B harness session) | codex |
                        codex-sub (sent by a Codex client), the harness's with the suffix :harness
    13 client           Claude Code entrypoint or Codex originator
    14 version          client version
    15 opening          the prompt opening (tools and static system prompt) this request shares with other chats
    16 opening_tokens   what the request reads when only the opening is warm on its account
    17 share            a further prefix shared with other chats: chats with the same first message (a Claude
                        chat's first request), the parent chat (a fork's first request), the sibling subagents
                        (Codex subagents), or the runs of the same variant (a Codex harness run's first request); the
                        request refreshes it
    18 share_tokens     what the request reads from it when warm; 0: refresh only
    19 deferred_opening Claude Code before tool search: the opening's size for this class with MCP tools deferred,
                        measured after 2026-10-12T22:16Z
    20 deferred_list    and the deferred tool list a chat of this class then carries in its own prefix
    21 first            1: the chat's first request, not a fork's (it reads its parent's cache); a chat that started
                        before `since` has none
and testdata/work.tsv.windows with each Codex weekly window seen through the proxy:
    plan window_start used_percent_at_first_sight has_credits
and testdata/work.tsv.bg with the successful /v1/messages requests CPA's request log (~/.cli-proxy-api/logs/main*.log)
shows but no transcript does (SIM_BG), per hour, source and model:
    hour source model requests chats
A logged request is sessionless if CPA bound it to an id it derived from the prompt (`session=derived:`), which its
selector line logs with the model and whether it opened the derived session (a chat). Each recorded request with a
known start takes the unclaimed logged request with a session id that started nearest to it, from 2.5s before its
start to 1.5s after its completion; a request without one claims after those, from its estimated start (column 11).
Of the rest, an hour's probes (the harness's `claude -p` runs (PROBES), counted by output file) are two requests
each, the probe and a short side request at launch, at PROBE_MODEL, and what remains is unknown, a one-request chat
at PROBE_MODEL. CPA rotates its log away; hours before the oldest log line keep their counts from the previous
work.tsv.bg.

Only Codex sessions using the `cliproxyapi` provider are read; every Claude Code transcript is assumed to be proxied
after `since` (default: the start of the recording this repository's benchmarks use). The A/B harness's agent
sessions are included: its test runs, calibration runs and review suites (RUNS), Claude Code's (HARNESS) as main chats
with a class of their own (and their own first-turn range) that keep their recorded openings under
SIM_OPENINGS=deferred, and Codex's (HARNESS_CODEX) as Codex chats. A response logged once per content block counts the
output of its largest entry and the rest of its usage from its first.

Openings (measured from transcripts and request captures):
- Claude Code: an opening is shared by requests with the same entrypoint, version, model, class and MCP tool set. Its
  size is tracked by value. A chat that shows its own takes its latest by then (else its first); one that doesn't
  borrows the nearest value in time to its first request of the class at the most specific level (a main chat: any; a
  subagent: its family, then any; a workflow agent: its run and phase, then its family's phase) and keeps it. A main
  chat's first request rejects a borrowed opening that leaves a first turn outside the 2nd-98th percentile, and any
  other request one larger than its prompt. An unseen workflow phase is sized from the nearest value in its run, else
  in any. Workflow phases whose openings match to the token share one: the same size means the same output schema, so
  the same prefix.
- Deferred tool list (tool search): the tool names and surfaced definitions of all of a chat's deferred_tools_delta
  attachments (a main chat's include the MCP tools listed once its servers connect), at TOKENS_PER_CHAR, the median
  per class.
- Codex: model, originator, version, base instructions and host skills, the harness's apart (its runs' first requests
  read 10,880 tokens where other codex exec chats with the same tools read 12,288). A main chat's first request reads
  what it read in the recording if the opening is warm; a later request that lost its own cache reads the opening's
  size times the share of such reads that hit (P_OPENING per originator, 1.0 for Claude models through the Codex
  client), at most what it read. A subagent's first request reads what it read if a sibling ran on the account. A
  harness run's opening is the first read the most variants show, and its first request reads past it only if a run of
  the same variant is warm: of first requests after a run of their group in the previous 10 minutes, 203 of 603 read
  past it when one of those runs had the same variant, 5 of 56 when none did.
"""
import bisect, collections, datetime, glob, hashlib, json, math, os, re, sys

out_path = sys.argv[1]
since = sys.argv[2] if len(sys.argv) > 2 else "2026-10-04T07:36:09"
TOOL_SEARCH = "2026-10-12T22:16"  # Claude Code 2.1.296 with ENABLE_TOOL_SEARCH: MCP tools deferred from here
TOKENS_PER_CHAR = 0.5  # deferred tool lists: fitted on consecutive requests whose prompt growth included one
P_OPENING = {"codex_exec": 0.98, "T3 Code": 0.33}
# The A/B harness (/mnt/disk/agentsmd-trim) ran through the same proxy: its agent sessions, saved per run and also
# in the run's sandbox home, where runs stopped before saving left theirs (a copy repeats the response ids, so it is
# read once), and its probes (`claude -p --tools "" --no-session-persistence`), whose outputs are written as each
# completes.
RUNS = ["/mnt/disk/agentsmd-trim/" + d for d in ("tests/runs*/*", "tests/calibration/*/*", "reviews/*/suite/runs/*")]
HARNESS = [d + "/claude-*/" + s + "/*/*.jsonl" for d in RUNS for s in ("session", "sandbox/home/.claude/projects")]
HARNESS_CODEX = [d + "/codex-*/" + s + "/*/*/*/rollout-*.jsonl" for d in RUNS
                 for s in ("session", "sandbox/home/.codex/sessions")]
PROBES = "/mnt/disk/agentsmd-trim/reviews/**/claude-*.txt"
PROBE_MODEL = "claude-opus-5-5"  # CPA logs no model for requests with a session id; the harness used Opus


def ts(s):
    d = datetime.datetime.fromisoformat(s.replace("Z", "+00:00"))
    return (d if d.tzinfo else d.replace(tzinfo=datetime.timezone.utc)).timestamp()


def med(v):
    return sorted(v)[len(v) // 2]


def latency(uncached, out):
    """A request's latency, for estimating its start when the logs don't show it: prefill of the uncached prompt plus
    the streamed output."""
    return 2 + uncached / 40000 + min(out, 4000) / 60


def iso(t):
    return datetime.datetime.fromtimestamp(t, datetime.timezone.utc).isoformat()


def h(x):
    s = x if isinstance(x, str) else json.dumps(x, sort_keys=True)
    return hashlib.sha1(s.encode()).hexdigest()[:10]


def text_of(c):
    if isinstance(c, list):
        return "\n".join(b.get("text", "") for b in c if isinstance(b, dict) and b.get("type") == "text")
    return c if isinstance(c, str) else ""


def nearest(pts, t):
    """The (t, value) in pts (sorted by t) nearest to t, or None."""
    i = bisect.bisect_left(pts, (t,))
    return min(pts[max(0, i - 1):i + 1], key=lambda p: abs(p[0] - t), default=None)


def variant(run):
    """A harness run's AGENTS.md and global CLAUDE.md or AGENTS.md (none in mode none), by the hashes in its meta.json;
    a run stopped before writing it has its directory's name less the repeat (agent, variant and mode)."""
    if not os.path.exists(run + "/meta.json"):
        return os.path.basename(run).rsplit("-", 1)[0]
    m = json.load(open(run + "/meta.json"))
    return [m["variant_sha256"], m.get("home", {}).get("global_file_sha256")]


# ---- Claude Code transcripts: one chat per file
def read_chat(f, harness=False):
    meta = {}
    if os.path.exists(f[:-6] + ".meta.json"):
        try:
            meta = json.load(open(f[:-6] + ".meta.json"))
        except ValueError:
            pass
    c = dict(file=f, meta=meta, rows=[], forked=None, first_user=None, sub="/subagents/" in f, harness=harness,
             wfrun=f.split("/workflows/")[1].split("/")[0] if "/subagents/workflows/" in f else None, dlist=0)
    if harness:
        c["variant"] = variant(re.split("/session/|/sandbox/", f)[0])
    seen, last_in, compacted = {}, None, False
    for line in open(f, errors="replace"):
        try:
            o = json.loads(line)
        except ValueError:
            continue
        t = o.get("type")
        if o.get("forkedFrom") and c["forked"] is None:
            c["forked"] = o["forkedFrom"].get("sessionId")
        if t == "system" and o.get("subtype") == "compact_boundary":
            compacted = True
        if t in ("user", "attachment") and o.get("timestamp"):
            last_in = o["timestamp"]
        if t == "user" and c["first_user"] is None and not o.get("isMeta"):
            c["first_user"] = h(text_of((o.get("message") or {}).get("content")))
        a = o.get("attachment") or {}
        if t == "attachment" and a.get("type") == "deferred_tools_delta":
            c["dlist"] += len("\n".join(a.get("addedLines") or [])) + \
                sum(len(json.dumps(d.get("definition"))) for d in a.get("surfacedDefinitions") or [])
        m = o.get("message") or {}
        u = m.get("usage")
        if t != "assistant" or not u or "claude" not in (m.get("model") or ""):
            continue
        mid = m.get("id") or o.get("requestId")
        if mid in seen:  # a response is logged once per content block, with its output so far
            seen[mid]["out"] = max(seen[mid]["out"], u.get("output_tokens") or 0)
            continue
        cc = u.get("cache_creation") or {}
        w1h, w5m = cc.get("ephemeral_1h_input_tokens"), cc.get("ephemeral_5m_input_tokens")
        write = u.get("cache_creation_input_tokens") or 0
        if w1h is None and w5m is None:
            w1h, w5m = (0, write) if c["sub"] else (write, 0)
        r = dict(mid=mid, t=o["timestamp"], pre=last_in, compacted=compacted, model=m["model"],
                 inp=u.get("input_tokens") or 0, read=u.get("cache_read_input_tokens") or 0, w5=w5m or 0,
                 w1h=w1h or 0, write=write, out=u.get("output_tokens") or 0, session=o.get("sessionId", ""),
                 agent=o.get("agentId", "") if c["sub"] else "", entry=o.get("entrypoint") or "",
                 ver=o.get("version") or "", cwd=o.get("cwd"))
        r["prompt"] = r["read"] + r["inp"] + r["write"]
        c["rows"].append(r)
        seen[mid] = r
        compacted = False
    return c


since_epoch = ts(since)
chats = [read_chat(f, harness) for harness, pats in ((False, ["~/.claude/projects/**/*.jsonl"]), (True, HARNESS))
         for pat in pats for f in glob.glob(os.path.expanduser(pat), recursive=True)
         if not f.endswith("journal.jsonl") and os.path.getmtime(f) >= since_epoch - 86400]
# A fork copies its parent's entries, timestamps and response ids included: keep them only in the parent, and
# remember the last copied response as the fork point.
by_mid = {}
for c in sorted(chats, key=lambda c: c["forked"] is not None):
    own = []
    for r in c["rows"]:
        if r["mid"] in by_mid:
            if c["forked"]:
                c["fork_at"] = by_mid[r["mid"]]
        else:
            by_mid[r["mid"]] = r
            own.append(r)
    c["rows"] = own
chats = [c for c in chats if c["rows"]]

TTL = {"main": 3600.0, "harness": 3600.0, "sub": 300.0, "wf": 300.0}
for c in chats:
    meta = c["meta"]
    c["cls"] = "harness" if c["harness"] else "main" if not c["sub"] else \
        "wf" if meta.get("workflowPhase") is not None else "sub:" + str(meta.get("agentType"))
    c["base"] = c["cls"].split(":")[0]
    c["ttl"] = TTL[c["base"]]
    c["fam"], c["phase"] = c["rows"][0]["session"], meta.get("workflowPhase")
    for i, r in enumerate(c["rows"]):
        r["done"] = ts(r["t"])
        r["start"] = ts(r["pre"]) if r["pre"] and r["pre"] <= r["t"] else None
        r["s"] = r["start"] if r["start"] is not None else r["done"] - latency(r["inp"] + r["write"], r["out"])
        r["first"] = i == 0 and not c["forked"]
        r["in"] = r["t"] >= since  # only the window counts: before it, tool search was on and caches differed
        r["K"] = (r["entry"], r["ver"], r["model"], c["cls"])

# A main chat's first request sent before its MCP servers connected carries a much smaller tool set.
first_main = collections.defaultdict(list)
for c in chats:
    r = c["rows"][0]
    if r["first"] and r["in"] and c["cls"] == "main":
        first_main[(r["entry"], r["ver"])].append(r["prompt"])
for c in chats:
    r = c["rows"][0]
    if r["first"] and r["in"] and c["cls"] == "main" and r["prompt"] < 0.6 * med(first_main[(r["entry"], r["ver"])]):
        r["K"] += ("pre-mcp",)

# Chats whose first request repeats an earlier chat's first message (same model, directory and opening group, as
# chats with other tools or versions share no prefix, and for the harness the same AGENTS.md and global CLAUDE.md:
# they're in the request, not the transcript) within the TTL share that chat's first prompt.
by_msg0 = collections.defaultdict(list)
for c in chats:
    r = c["rows"][0]
    if r["first"] and c["first_user"]:
        c["msg0"] = h([r["model"], c["first_user"], r["cwd"] or "", r["K"]] + ([c["variant"]] if c["harness"] else []))
        by_msg0[c["msg0"]].append(c)
for c in chats:
    r, c["msg0_src"] = c["rows"][0], None
    if c.get("msg0"):
        src = [q["rows"][0] for q in by_msg0[c["msg0"]] if q is not c and q["rows"][0]["s"] < r["s"]
               and r["s"] < q["rows"][0]["done"] + q["ttl"]]
        c["msg0_src"] = max(src, key=lambda q: q["s"], default=None)


def shows_opening(c, r):
    """Whether chat c's request r is a first request whose cache read stopped at the opening: it read the cache and
    wrote at least 3,000 tokens, and is neither a fork nor a repeated first message (nor a harness session's that
    read past the opening)."""
    return r["first"] and r["in"] and r["read"] > 0 and r["write"] >= 3000 and not c["msg0_src"] and \
        not c.get("past_opening")


warm = collections.defaultdict(list)  # top-level chats' first requests that show an opening, per opening group
for c in chats:
    r = c["rows"][0]
    if c["base"] in ("main", "harness") and shows_opening(c, r):
        warm[r["K"]].append(c)
# The harness runs one tool set per opening group, so a first request that read more than the group's most common
# opening by over its shortest first turn read on into context shared with other chats (the global CLAUDE.md). A main
# chat's openings differ by MCP tool set, so its group has no one opening to test against.
for k, cs in warm.items():
    if k[3] == "harness":
        top = collections.Counter(c["rows"][0]["read"] for c in cs).most_common(1)[0][0]
        shortest = min(c["rows"][0]["prompt"] - c["rows"][0]["read"] for c in cs)
        for c in cs:
            c["past_opening"] = c["rows"][0]["read"] > top + shortest


def turn_range(b, v):
    """The plausible first turns from warm first turns v of class b: a main chat's 2nd to 98th percentile, as the
    shortest also read context shared with other chats. The harness's past-opening test drops those, and its turns
    cluster tightly (the same cases rerun), where a percentile would cut through a cluster: all of them."""
    v = sorted(v)
    return (v[0], v[-1]) if b == "harness" else (v[len(v) * 2 // 100], v[len(v) * 98 // 100])


# Plausible first turns after a warm opening, per top-level class and per opening group; a group's are widened to its
# class's, as a group with few warm first requests can't narrow them.
warm_turn = {}
for b in ("main", "harness"):
    if v := [c["rows"][0]["prompt"] - c["rows"][0]["read"] for cs in warm.values() for c in cs
             if c["base"] == b and shows_opening(c, c["rows"][0])]:
        warm_turn[b] = turn_range(b, v)
for k, cs in warm.items():
    if v := [c["rows"][0]["prompt"] - c["rows"][0]["read"] for c in cs if shows_opening(c, c["rows"][0])]:
        (lo, hi), b = turn_range(cs[0]["base"], v), warm_turn[cs[0]["base"]]
        warm_turn[k] = min(lo, b[0]), max(hi, b[1])


def turn_bounds(c, r):
    """The plausible first turns for chat c's request r, or None if not a top-level chat's first request."""
    if r["first"] and c["base"] in warm_turn:
        return warm_turn.get(r["K"], warm_turn[c["base"]])


# Opening sizes by value: points are warm first requests that read only the opening (a top-level chat's leaving at
# least a short first turn), and requests after a compaction.
points = collections.defaultdict(list)
for c in chats:
    for r in c["rows"]:
        if shows_opening(c, r) and (turn_bounds(c, r) is None or r["prompt"] - r["read"] >= turn_bounds(c, r)[0]) or \
                r["in"] and r["read"] > 0 and r["compacted"]:
            points[r["K"]].append((r["done"], r["read"], c))
canon = {}  # values within 4 tokens are one opening (token-count noise); a workflow agent's must match to the token
for k, ps in points.items():
    cnt = collections.Counter(p[1] for p in ps)
    vals, group, gap = sorted(cnt), [], 0 if k[3] == "wf" else 4
    for v in vals + [None]:
        if group and (v is None or v - group[-1] > gap):
            rep = max(group, key=lambda x: cnt[x])
            canon.update({(k, x): rep for x in group})
            group = []
        group.append(v)
index = collections.defaultdict(list)
for k, ps in points.items():
    for t, v, c in sorted(ps, key=lambda p: p[0]):
        p = (t, canon[(k, v)])
        for sub in [(), ("chat", c["file"]), ("fam", c["fam"]), ("run", c["wfrun"], c["phase"]),
                    ("famph", c["fam"], c["phase"]), ("runany", c["wfrun"])]:
            index[(k,) + sub].append(p)
first_turn = collections.defaultdict(list)  # the first prompt beyond the opening, for openings never seen warm
for c in chats:
    r = c["rows"][0]
    if r["first"] and r["in"] and 0 < r["read"] < r["prompt"]:
        first_turn[c["base"]].append(r["prompt"] - r["read"])
first_turn = {k: med(v) for k, v in first_turn.items()}


def fits(c, r, v):
    """Whether chat c's request r can have an opening of v tokens."""
    if b := turn_bounds(c, r):
        return b[0] <= r["prompt"] - v <= b[1]
    return v <= r["prompt"]


def borrow(c, r):
    """(opening id, tokens) for chat c, which shows no opening of its own, from its request r."""
    k = r["K"]
    levels = {"main": [()], "harness": [()], "sub": [("fam", c["fam"]), ()],
              "wf": [("run", c["wfrun"], c["phase"]), ("famph", c["fam"], c["phase"])]}[c["base"]]
    for lv in levels:
        p = nearest(index.get((k,) + lv, []), r["done"])
        if p and fits(c, r, p[1]):
            return h([k, p[1]]), p[1]
    oid = h([k, "unseen", c["file"]])
    if c["base"] == "wf":  # each phase has its own output schema: an unseen phase shares with nobody
        p = nearest(index.get((k, "runany", c["wfrun"]), []), r["done"]) or nearest(index.get((k,), []), r["done"])
        if p:
            return oid, p[1]
    return oid, max(0, c["rows"][0]["prompt"] - first_turn.get(c["base"], 0))


borrowed = {}


def opening_for(c, r):
    """(opening id, tokens) for request r of chat c."""
    k = r["K"]
    own = index.get((k, "chat", c["file"]))
    if own:
        _, v = own[max(bisect.bisect_right(own, (r["done"], math.inf)) - 1, 0)]
        return h([k, v]), v
    if (c["file"], k) not in borrowed:
        borrowed[(c["file"], k)] = borrow(c, r)
    return borrowed[(c["file"], k)]


deferred, dlist = collections.defaultdict(list), collections.defaultdict(list)  # by class, with tool search on
for k, ps in points.items():
    for t, v, c in ps:
        if t >= ts(TOOL_SEARCH) and "pre-mcp" not in k and not c["harness"]:
            for key in {c["cls"], c["base"]}:
                deferred[key].append(canon[(k, v)])
for c in chats:
    if c["rows"][0]["t"] >= TOOL_SEARCH and not c["harness"]:
        for key in {c["cls"], c["base"]}:
            dlist[key].append(round(TOKENS_PER_CHAR * c["dlist"]))
deferred, dlist = ({k: med(v) for k, v in x.items()} for x in (deferred, dlist))
rows = []
for c in chats:
    for i, r in enumerate(c["rows"]):
        if not r["in"]:
            continue
        r["oid"], r["O"] = opening_for(c, r)
        share, share_tokens = "", 0
        if i == 0 and c.get("fork_at"):  # a fork's first request reads its parent's prompt at the fork point
            share, share_tokens = "claude:" + c["forked"], c["fork_at"]["prompt"]
        elif r["first"] and c.get("msg0"):
            share, q = c["msg0"], c["msg0_src"]
            if q:  # the recorded read when it went beyond the opening, else the earlier chat's first prompt
                share_tokens = r["read"] if r["read"] > r["O"] + 64 else min(q["prompt"], r["prompt"])
        d = l = ""
        if r["t"] < TOOL_SEARCH and not c["harness"]:
            d = deferred.get(c["cls"], deferred.get(c["base"], ""))
            l = dlist.get(c["cls"], dlist.get(c["base"], ""))
        pa = c["meta"].get("parentAgentId")
        parent = r["session"] + ":agent:" + pa if r["agent"] and pa and pa not in ("main", r["agent"]) else ""
        rows.append((r["t"], "claude", r["session"], r["agent"], parent, r["model"], r["inp"], r["read"], r["w5"],
                     r["w1h"], r["out"], iso(r["s"]), c["cls"], r["entry"], r["ver"], r["oid"], r["O"], share,
                     share_tokens, d, l, int(r["first"])))
print("tool-search openings:", dict(sorted(deferred.items())), "deferred lists:", dict(sorted(dlist.items())),
      "warm first turns:", warm_turn, file=sys.stderr)

# ---- Codex rollouts
windows, threads, crow, seen_resp = {}, {}, collections.defaultdict(list), set()
for f, harness in [(f, False) for f in glob.glob(os.path.expanduser("~/.codex/sessions/*/*/*/rollout-*.jsonl"))] + \
        [(f, True) for pat in HARNESS_CODEX for f in glob.glob(pat)]:
    if os.path.getmtime(f) < since_epoch - 86400:
        continue
    meta, model, ws = None, "", None
    for line in open(f, errors="replace"):
        try:
            o = json.loads(line)
        except ValueError:
            continue
        p = o.get("payload") if isinstance(o.get("payload"), dict) else {}
        if o.get("type") == "session_meta" and meta is None:
            meta = p
            if meta.get("model_provider") != "cliproxyapi":
                break
        elif o.get("type") == "turn_context":
            model = p.get("model") or model
        elif o.get("type") == "world_state" and ws is None:
            ws = h((p.get("state") or {}).get("host_skills"))
        elif o.get("type") == "token_usage_record" and p.get("response_id") not in seen_resp:  # one per response
            if p.get("response_id"):
                seen_resp.add(p["response_id"])
            crow[(f, p.get("thread_id"))].append((o["timestamp"], p.get("session_id"), p.get("usage") or {}, model))
        elif o.get("type") == "event_msg" and p.get("type") == "token_count":
            rl = p.get("rate_limits") or {}
            w = rl.get("primary") or {}
            if rl.get("plan_type") and w.get("resets_at") and w.get("window_minutes"):
                start = w["resets_at"] - 60 * w["window_minutes"]
                key = (rl["plan_type"], start // 600)
                if key not in windows or o["timestamp"] < windows[key][0]:
                    windows[key] = (o["timestamp"], rl["plan_type"], start, w.get("used_percent") or 0,
                                    bool((rl.get("credits") or {}).get("has_credits")))
    if meta and meta.get("model_provider") == "cliproxyapi":
        threads[f] = dict(meta=meta, ws=ws, bi=h((meta.get("base_instructions") or {}).get("text", "")),
                          variant=harness and variant(re.split("/session/|/sandbox/", f)[0]))
codex = collections.defaultdict(list)  # thread -> requests
for (f, tid), us in crow.items():
    if f not in threads:
        continue
    th = threads[f]
    m = th["meta"]
    tid = tid or m.get("id")
    for t, sid, u, mdl in us:
        if not u:
            continue
        inp, cached = u.get("input_tokens") or 0, u.get("cached_input_tokens") or 0
        codex[tid].append(dict(t=t, session=m.get("session_id") or sid or tid, model=mdl, read=cached,
                               write=max(inp - cached, 0), out=u.get("output_tokens") or 0,
                               parent=m.get("parent_thread_id") or "", origin=m.get("originator") or "",
                               ver=m.get("cli_version") or "", variant=th["variant"],
                               K=(mdl, m.get("originator"), m.get("cli_version"), th["bi"], th["ws"]) +
                               (("harness",) if th["variant"] else ())))
# The opening: the most common read of main chats' warm first requests; the harness's, the read the most variants show,
# as a run often reads on into its variant's prefix.
opening, variants = collections.defaultdict(collections.Counter), collections.defaultdict(set)
for tid, rs in codex.items():
    rs.sort(key=lambda r: r["t"])
    r = rs[0]
    if not r["parent"] and r["read"] > 0 and r["t"] >= since:
        if r["variant"]:
            variants[(r["K"], r["read"])].add(h(r["variant"]))
        else:
            opening[r["K"]][r["read"]] += 1
for (k, read), v in variants.items():
    opening[k][read] = len(v)
for tid, rs in codex.items():
    for i, r in enumerate(rs):
        if r["t"] < since:
            continue
        claude = r["model"].startswith("claude")  # T3 using Claude through the Codex client, served by Claude accounts
        share, share_tokens = ("sib:" + r["parent"], r["read"] if i == 0 else 0) if r["parent"] else ("", 0)
        top = opening[r["K"]].most_common(1)
        if i == 0:
            o = 0 if r["parent"] else r["read"]
            if r["variant"] and not r["parent"]:  # a harness run reads past the opening from its variant's runs
                o = min(o, top[0][0]) if top else 0
                share, share_tokens = "codex:" + h([list(r["K"]), r["variant"]]), r["read"] if r["read"] > o else 0
        else:
            p = 1.0 if claude else P_OPENING.get(r["origin"], 0.5)
            o = min(round(p * top[0][0]), r["read"]) if top else 0
        cls = "codex-sub" if r["parent"] else "codex"
        rows.append((r["t"], "claude" if claude else "codex", r["session"], tid if r["parent"] else "", r["parent"],
                     r["model"], 0, r["read"], 0, r["write"], r["out"], iso(ts(r["t"]) - latency(r["write"], r["out"])),
                     cls + ":harness" if r["variant"] else cls,
                     r["origin"], r["ver"], h(list(r["K"])), o, share, share_tokens, "", "", int(i == 0)))

rows.sort()
with open(out_path, "w") as w:
    for r in rows:
        w.write("\t".join(map(str, r)) + "\n")
with open(out_path + ".windows", "w") as w:
    for _, plan, start, used, credits in sorted(windows.values(), key=lambda x: x[2]):
        w.write(f"{plan}\t{start}\t{used}\t{int(credits)}\n")
print(f"{len(rows)} requests, {len(windows)} Codex windows")

# ---- Traffic CPA served that no transcript shows
head = re.compile(r'^\[(\S+ \S+)\] \[(\w+)\] \[\w+\s*\] \[(\w+\.go):\d+\] (.*)$')
gin = re.compile(r'^200 \|\s*(\S+)\s*\|.*\| POST\s+"/v1/messages(\?[^"]*)?"$')
logged_from, derived, starts = math.inf, {}, []
for f in sorted(glob.glob(os.path.expanduser("~/.cli-proxy-api/logs/main*.log"))):
    for line in open(f, errors="replace"):
        m = head.match(line.rstrip("\n"))
        if not m:
            continue
        t, rid, src, msg = m.groups()
        logged_from = min(logged_from, ts(t.replace(" ", "T")))
        if src == "selector.go" and (sel := re.search(r"session=derived:.* model=(\S+)", msg)):
            derived[rid] = (sel[1], "new binding" in msg)  # the model, and whether it opened a derived session
        elif src == "gin_logger.go" and (g := gin.match(msg)):
            d = re.fullmatch(r"(?:(\d+)h)?(?:(\d+)m)?([\d.]+)(µs|ms|s)", g.group(1))
            lat = float(d[3]) * {"µs": 1e-6, "ms": 1e-3, "s": 1}[d[4]] + 60 * int(d[2] or 0) + 3600 * int(d[1] or 0)
            starts.append((ts(t.replace(" ", "T")) + 0.5 - lat, rid))  # logged at completion, to the second
served = sorted(((s, derived.get(rid)) for s, rid in starts), key=lambda x: x[0])
at, claimed = [s for s, _ in served], [False] * len(served)


# Claude Code's requests, those with a known start first (a Codex client's go to /v1/responses)
for r in sorted((r for c in chats for r in c["rows"] if r["in"]), key=lambda r: (r["start"] is None, r["s"])):
    st, best = r["s"], None
    i = bisect.bisect_left(at, st - 2.5)
    while i < len(at) and at[i] <= r["done"] + 1.5:
        if not claimed[i] and not served[i][1] and (best is None or abs(at[i] - st) < abs(at[best] - st)):
            best = i
        i += 1
    if best is not None:
        claimed[best] = True


def hour_of(t):
    return datetime.datetime.fromtimestamp(t - t % 3600, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


bg = collections.defaultdict(lambda: [0, 0])  # (hour, source, model) -> [requests, chats]
with_id = collections.Counter()
for (s, sessionless), c in zip(served, claimed):
    if sessionless:
        model, opened = sessionless
        bg[(hour_of(s), "sessionless", model)][0] += 1
        bg[(hour_of(s), "sessionless", model)][1] += opened
    elif not c:
        with_id[hour_of(s)] += 1
probes = collections.Counter(hour_of(os.path.getmtime(f)) for f in glob.glob(PROBES, recursive=True))
for hour, n in with_id.items():  # each probe is two requests; the rest is unknown, one request per chat
    p = min(n, 2 * probes[hour])
    if p:
        bg[(hour, "probe", PROBE_MODEL)] = [p, (p + 1) // 2]
    if n - p:
        bg[(hour, "unknown", PROBE_MODEL)] = [n - p, n - p]
if os.path.exists(out_path + ".bg"):
    for line in open(out_path + ".bg"):
        hour, source, model, n, chats = line.split()
        if ts(hour) < logged_from:
            bg[(hour, source, model)] = [int(n), int(chats)]
with open(out_path + ".bg", "w") as w:
    for (hour, source, model), (n, chats) in sorted(bg.items()):
        w.write(f"{hour}\t{source}\t{model}\t{n}\t{chats}\n")
total = collections.Counter()
for (_, source, _), (n, _) in bg.items():
    total[source] += n
print("background requests:", dict(sorted(total.items())))
