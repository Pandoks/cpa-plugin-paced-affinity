"""Extract proxied Claude Code and Codex requests from local logs for the routing simulation (sim_test.go).

    python3 -I testdata/extract.py testdata/work.tsv [since]

Writes one row per model response, in time order:
    time provider session agent - model input cache_read cache_write_5m cache_write_1h output project
and testdata/work.tsv.windows with each Codex weekly window seen through the proxy:
    plan window_start used_percent_at_first_sight has_credits
Only Codex sessions using the `cliproxyapi` provider are read; every Claude Code transcript is assumed to be proxied
after `since` (default: the start of the recording this repository's benchmarks used).
"""
import glob, hashlib, json, os, sys

out_path = sys.argv[1]
since = sys.argv[2] if len(sys.argv) > 2 else "2026-10-04T07:36:09"
rows, windows = [], {}


def project(cwd):
    return hashlib.sha1((cwd or "").encode()).hexdigest()[:8]


for f in glob.glob(os.path.expanduser("~/.claude/projects/**/*.jsonl"), recursive=True):
    seen, sub = set(), "/subagents/" in f
    for line in open(f, errors="replace"):
        if '"usage"' not in line or '"assistant"' not in line:
            continue
        try:
            o = json.loads(line)
        except ValueError:
            continue
        m = o.get("message") or {}
        u = m.get("usage")
        if o.get("type") != "assistant" or not u or "claude" not in (m.get("model") or ""):
            continue
        mid = m.get("id") or o.get("requestId")
        if mid in seen or o.get("timestamp", "") < since:  # a response is logged once per content block
            continue
        seen.add(mid)
        cc = u.get("cache_creation") or {}
        w1h, w5m = cc.get("ephemeral_1h_input_tokens"), cc.get("ephemeral_5m_input_tokens")
        if w1h is None and w5m is None:
            w = u.get("cache_creation_input_tokens") or 0
            w1h, w5m = (0, w) if sub else (w, 0)
        rows.append((o["timestamp"], "claude", o.get("sessionId", ""), o.get("agentId", "") if sub else "", "-",
                     m["model"], u.get("input_tokens") or 0, u.get("cache_read_input_tokens") or 0, w5m or 0,
                     w1h or 0, u.get("output_tokens") or 0, project(o.get("cwd"))))

for f in glob.glob(os.path.expanduser("~/.codex/sessions/*/*/*/rollout-*.jsonl")):
    meta, model, usage = None, "", []
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
        elif o.get("type") == "token_usage_record":  # one per response
            usage.append((o.get("timestamp", ""), p.get("usage") or {}, model))
        elif o.get("type") == "event_msg" and p.get("type") == "token_count":
            rl = p.get("rate_limits") or {}
            w = rl.get("primary") or {}
            if rl.get("plan_type") and w.get("resets_at") and w.get("window_minutes"):
                start = w["resets_at"] - 60 * w["window_minutes"]
                key = (rl["plan_type"], start // 600)
                if key not in windows or o["timestamp"] < windows[key][0]:
                    windows[key] = (o["timestamp"], rl["plan_type"], start, w.get("used_percent") or 0,
                                    bool((rl.get("credits") or {}).get("has_credits")))
    if not meta or meta.get("model_provider") != "cliproxyapi":
        continue
    own = meta.get("id") or meta.get("session_id")
    session = meta.get("session_id") or own
    for ts, u, mdl in usage:
        if ts >= since and u:
            inp, cached = u.get("input_tokens") or 0, u.get("cached_input_tokens") or 0
            rows.append((ts, "codex", session, own if own != session else "", "-", mdl, max(inp - cached, 0), cached,
                         0, 0, u.get("output_tokens") or 0, project(meta.get("cwd"))))

rows.sort()
with open(out_path, "w") as w:
    for r in rows:
        w.write("\t".join(map(str, r)) + "\n")
with open(out_path + ".windows", "w") as w:
    for _, plan, start, used, credits in sorted(windows.values(), key=lambda x: x[2]):
        w.write(f"{plan}\t{start}\t{used}\t{int(credits)}\n")
print(f"{len(rows)} requests, {len(windows)} Codex windows")
