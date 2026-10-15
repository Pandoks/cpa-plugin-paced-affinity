# Benchmarks

**Paced Affinity** was measured by replaying ten days of the author's recorded Claude Code and Codex traffic through
simulated accounts, with the plugin's own code making every routing decision. Against CPA's round-robin and
fill-first routing, and against v0.1.1 (the release the author ran during the recording), at 0.9× to 1.25× the assumed
plan allowances ([capacity](#capacity)):

| This version against | Round-robin | Fill-first | v0.1.1 |
| --- | ---: | ---: | ---: |
| Claude [quota per unit of work](#terms), [as recorded](#terms) | −1.16% to −2.10%* | −1.01% to −1.48%* | −2.34% to −2.61%* |
| Claude quota per unit of work, tool search on | −1.14% to −1.31%* | −1.44% to −1.55%* | −0.20% to −0.28%* |
| Work served | −0.03% to +1.00% | +0.16% to +2.74% | −0.19% to +0.55% |
| Codex credits at the estimated Codex capacity | −14% | −30% | −0.2% |

Negative quota and credits mean this version spends less. Each quota cell spans 20 paired seeds per capacity (40 at 0.9×
and 1× with tool search on), and every one is significant (`*`: its 95% paired interval excludes 0); the closest are
−1.01% (−1.18 to −0.85) against fill-first as recorded and −0.20% (−0.28 to −0.12) against v0.1.1 with tool search on.
Work served is never significantly less than round-robin's, is more than fill-first's in five of the six cells, and
against v0.1.1 is more in one cell and less in one. Codex credits are the same in every seed. Some setups narrow the
margins:

- At 0.9× with tool search on it serves less work than v0.1.1; see [the work-served gap](#work-served-gap).
- At 2×, where the accounts have room, it spent 0.4–3.1% less Claude quota than round-robin or fill-first. At 1.5×
  the estimated Codex capacity, round-robin spent 0.9% fewer Codex credits.
- In the 1.6 days after tool search was turned on, fill-first was level with it at 2×.
- With a daily plugin restart, its lead over fill-first at 1.25× as recorded shrinks to 0.33% (−0.49 to −0.17).

> [!IMPORTANT]
> Subscription limits and their token accounting are not published, and account capacity and plans are assumed.
> Replays of the periods when the plugin ran find caches warmer than the recording did: 2–17 percentage points fewer
> new main chats start cold. So these results may overstate the plugin's advantage over built-in routing; see
> [checked against the recording](#checked-against-the-recording). They show what the scheduler does with one
> person's recorded traffic under stated assumptions; they do not promise a particular gain on live accounts.

## Terms

| Term | Meaning |
| --- | --- |
| Binding | What ties a chat (or a family's subagents) to an account for a sliding hour |
| Family | A chat's subagents, nested ones included |
| Quota per unit of work | What a policy spent, under the assumed meter, divided by what the same requests cost as recorded. Tables give its change against this version; negative means this version spends less |
| Unit | One uncached input token's worth of quota under the [meter](#what-the-replay-does); allowances and credits are counted in millions of units |
| Work served | The recorded cost of the requests that found an account |
| Capacity | A multiple of the assumed plan allowances. We take 0.9× to 1.25× as the realistic band (see [capacity](#capacity)); at 2× the accounts have room to spare |
| As recorded, tool search on | Tool search is Claude Code's `ENABLE_TOOL_SEARCH=true`, which defers MCP tools instead of sending them in full. *As recorded* replays the openings as they were: tool search off for the first 8.6 days and on for the last 1.6 (from 2026-10-12 22:16). *Tool search on* replays the first 8.6 days with MCP tools deferred too |
| Round-robin, fill-first | CPA's built-in routing with session affinity and `session-affinity-subagents`, the author's configuration. Round-robin rotates through the accounts; fill-first takes the first |
| v0.1.1 | The release the author ran during the recording, which gave each subagent a binding of its own |
| This version | This branch: a chat's subagents on one shared binding, or with tool search on one each; forks on their parent's binding; Claude picks preferring warm accounts |
| Opening | The prompt prefix chats share: the tools and the agent's system prompt |
| Cold start | A chat's first request that read nothing from cache |
| Workflow agent | A subagent launched by a workflow run |
| Harness | An A/B test harness whose agent sessions ran through the same proxy, replayed as main chats of their own class |
| Background | Claude requests CPA's log shows but no transcript does, replayed as scaled copies of recorded main chats; only the [sensitivity](#sensitivity) table includes them |
| Seeds | Each cell replays 20 seeds, each with its own weekly phases and starting usage; cells marked † replay 40. Changes are paired by seed, with 95% t-intervals |
| `*` | The change's 95% paired interval excludes 0 |

## Results

### Claude

Change in Claude quota per unit of work over the whole recording, this version against each policy:

| Capacity | Tool search | vs round-robin | vs fill-first | vs v0.1.1 |
| --- | --- | ---: | ---: | ---: |
| 0.9× | as recorded | −1.16%* (−1.44 to −0.88) | −1.48%* (−1.75 to −1.20) | −2.34%* (−2.87 to −1.82) |
| 1× | as recorded | −1.51%* (−1.78 to −1.24) | −1.37%* (−1.64 to −1.09) | −2.55%* (−2.90 to −2.19) |
| 1.25× | as recorded | −2.10%* (−2.27 to −1.93) | −1.01%* (−1.18 to −0.85) | −2.61%* (−2.89 to −2.33) |
| 2× | as recorded | −3.05%* (−3.16 to −2.95) | −1.01%* (−1.13 to −0.90) | −2.60%* (−2.74 to −2.46) |
| 0.9×† | on | −1.14%* (−1.23 to −1.04) | −1.44%* (−1.53 to −1.35) | −0.20%* (−0.28 to −0.12) |
| 1×† | on | −1.20%* (−1.28 to −1.11) | −1.55%* (−1.64 to −1.46) | −0.26%* (−0.34 to −0.19) |
| 1.25× | on | −1.31%* (−1.41 to −1.21) | −1.44%* (−1.56 to −1.32) | −0.28%* (−0.37 to −0.19) |
| 2× | on | −1.24%* (−1.30 to −1.18) | −0.36%* (−0.50 to −0.22) | −0.22%* (−0.26 to −0.19) |

At 1.25× and below, demand exceeds capacity in some seeds and requests go unserved:

| Capacity | Tool search | Unserved of 69,546: this version / round-robin / fill-first / v0.1.1 | Work served vs round-robin | vs fill-first | vs v0.1.1 |
| --- | --- | --- | ---: | ---: | ---: |
| 0.9× | as recorded | 11,870 / 12,075 / 12,819 / 12,148 | +0.46% (−0.11 to +1.04) | +2.02%* (+0.91 to +3.13) | +0.44% (−0.06 to +0.94) |
| 1× | as recorded | 8,650 / 9,106 / 10,016 / 8,919 | +1.00%* (+0.06 to +1.93) | +2.74%* (+1.08 to +4.40) | +0.55%* (+0.05 to +1.06) |
| 1.25× | as recorded | 2,786 / 2,923 / 3,371 / 2,699 | +0.30% (−0.32 to +0.93) | +1.48%* (+0.95 to +2.01) | −0.13% (−0.95 to +0.70) |
| 0.9×† | on | 915 / 901 / 1,865 / 803 | −0.03% (−0.24 to +0.18) | +2.44%* (+1.84 to +3.05) | −0.19%* (−0.37 to −0.02) |
| 1×† | on | 152 / 160 / 919 / 135 | +0.02% (−0.13 to +0.17) | +1.77%* (+1.24 to +2.31) | −0.03% (−0.10 to +0.03) |
| 1.25× | on | 0 / 0 / 100 / 0 | 0.00% | +0.16% (−0.05 to +0.36) | 0.00% |

At 2× nothing goes unserved except under fill-first as recorded, 40 requests on average. Unserved counts
swing widely by seed (from 1,076 to 15,550 for this version at 1× as recorded), which is why every cell
replays at least 20 seeds.

At 1×, the share of first requests that started cold, and the 429s and the moves of a chat off an account where its
own cache was still warm, per replay:

| Policy | Tool search | New main chats | Subagents | Workflow agents | Harness sessions | 429s | Warm chats moved |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Round-robin | as recorded | 44.2% | 13.6% | 27.9% | 1.1% | 15.6 | 63 |
| Fill-first | as recorded | 30.3% | 9.4% | 25.2% | 0.5% | 19.6 | 117 |
| v0.1.1 | as recorded | 34.5% | 21.5% | 47.4% | 1.4% | 7.5 | 57 |
| **This version** | as recorded | **30.7%** | **10.9%** | **24.3%** | **0.8%** | **8.1** | **77** |
| Round-robin | on† | 32.3% | 13.0% | 31.2% | 1.2% | 9.8 | 41 |
| Fill-first | on† | 13.7% | 7.8% | 25.7% | 0.5% | 11.7 | 87 |
| v0.1.1 | on† | 22.8% | 16.9% | 42.6% | 1.2% | 0.5 | 7 |
| **This version** | on† | **14.7%** | **11.0%** | **29.0%** | **0.6%** | **0.4** | **9** |

Measured: this version's new main chats start cold about as often as fill-first's and less often than round-robin's
or v0.1.1's, and v0.1.1's subagents and workflow agents start cold about twice as often as this version's as
recorded. Fill-first has the most 429s and warm-chat moves, and under overload it serves the least. Our reading,
which the replay doesn't trace request by request: round-robin hands each new chat to the next account in turn, which
may not hold its opening, and fill-first drains the account it keeps warm. With tool search on, openings shrink from
about 185–230k tokens to 4–12k, so a cold start costs much less and the gap to v0.1.1 narrows.

### Subagents and tool search

How to bind a chat's subagents depends on tool search, so the plugin decides per request from the `Anthropic-Beta`
header ([how it works](how-it-works.md#subagents-and-forks)). Measured against this version:

- As recorded, where this version shares one binding per family until the last 1.6 days, one binding per subagent
  spends 0.19–0.85% more than this version (1×, 1.25× and 2×; every interval excludes 0).
- With tool search on, where this version binds each subagent alone, one shared binding per family spends 0.21–0.29%
  more than this version (0.9×†, 1×†, 1.25×) and at 0.9× serves 0.31% less work (−0.62 to −0.01).
- So the switch matches the better rule to within 0.01%: as recorded it is within 0.01% of always sharing, and with
  tool search on its replay is identical to always binding each subagent alone.

Each rule against this version, change in Claude quota per unit of work (+: the rule spends more):

| Subagent rule | As recorded, 1× | As recorded, 1.25× | As recorded, 2× | On, 0.9×† | On, 1×† | On, 1.25× |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Always one shared binding (no switch) | −0.005% | −0.001%* | 0.000% | +0.29%* | +0.23%* | +0.21%* |
| Always one binding per subagent (no switch) | +0.85%* | +0.53%* | +0.19%* | identical | identical | identical |
| CPA's `session-affinity-subagents`: each its own binding, starting on its parent's account | +0.97%* | +0.83%* | +0.14%* | +0.74%* | +0.58%* | +0.24%* |
| Every subagent on its parent chat's binding | +0.35%* | +0.73%* | +0.04% | +0.87%* | +0.64%* | +0.30%* |

Measured from the recording, subagents shared cached prefixes with their siblings but never with the chat that started
them; only forks read their parent's cache. So putting subagents on the chat's account gains no cache reads. What it
does instead, by our inference, is concentrate load on one account, which runs out sooner and moves the chats bound
there off their warm caches. The replay doesn't isolate that mechanism, but at 0.9× with tool search on that rule
moved 61 chats per replay off a warm cache, against 17 for this version and 18 for v0.1.1, and with that rule in
place of the switch this version spent more than v0.1.1: 0.66% at 0.9× (+0.54 to +0.79) and 0.38% at 1× (+0.27 to
+0.49; 40 seeds). Nested subagents join their family by session ID; the nested step in `placement_test.go` covers
that rule, and the replay doesn't measure it.

<a id="work-served-gap"></a>
**The work-served gap.** At 0.9× with tool search on, this version spends 0.20% less than v0.1.1 per unit of work (−0.28
to −0.12) but serves 0.19% less work (−0.37 to −0.02), and 112 more requests go unserved (+17 to +206; 40 seeds). Its
accounts reach their five-hour or weekly limit 2.40 times per replay against 1.85 for v0.1.1 (+0.55, +0.14 to +0.96; 40
seeds); the 429 counts are level (1.62 against 1.48). A request goes unserved only when every account is cooling down or
at its limit, so the extra limit episodes are the likely cause, but the replay doesn't trace each request. Without the
warm-cache rule the gap closes (+0.03% work against v0.1.1, −0.03 to +0.09), but the rule saves 0.19–0.60% across the
six cells of the [alternatives table](#alternatives-tested-and-rejected), significant in five, so it stays. At 1× the
gap is 0.03% and not significant (−0.10 to +0.03).

### Before and after tool search

The author turned tool search on at 2026-10-12 22:16. Scored on the 8.6 days before alone (replays still run from the
start), this version spent less than every other policy:

| First 8.6 days | vs round-robin | vs fill-first | vs v0.1.1 |
| --- | ---: | ---: | ---: |
| As recorded (tool search off), 0.9× to 1.25× | −1.17% to −2.19%* | −1.04% to −1.39%* | −2.67% to −2.91%* |
| As recorded (tool search off), 2× | −3.26%* | −1.14%* | −2.88%* |
| Tool search on, 0.9× to 2× | −1.16% to −1.36%* | −0.43% to −1.80%* | −0.22% to −0.30%* |

The 1.6 days after are short: 11,117 Claude requests, 7 new main chats and 117 subagents, and nothing goes unserved at
any capacity. Scored on them alone (every replay still runs from the start, so caches and quota carry over):

| Capacity | vs round-robin | vs fill-first | vs v0.1.1 |
| --- | ---: | ---: | ---: |
| 0.9× | −1.12%* (−1.50 to −0.74) | −1.98%* (−2.41 to −1.54) | −0.28%* (−0.42 to −0.14) |
| 1× | −0.89%* (−1.13 to −0.64) | −2.48%* (−3.11 to −1.85) | −0.28%* (−0.36 to −0.20) |
| 1.25× | −1.38%* (−1.82 to −0.95) | −0.80%* (−1.21 to −0.39) | −0.26%* (−0.32 to −0.20) |
| 2× | −1.33%* (−1.49 to −1.17) | +0.00% (−0.000 to +0.002) | −0.25%* (−0.33 to −0.16) |

### Codex

Units of Codex usage paid with credits over the ten days. The plugin keeps new work off an account that reports 100%
usage while the other has room. Built-in routing keeps sending it work, which an account with credits pays for: Pro,
and ProLite in one of its six recorded windows. Codex results are the same in every seed: its windows are replayed as
recorded, and its routing doesn't depend on the Claude side.

| Codex capacity | Round-robin | Fill-first | v0.1.1 | **This version** |
| --- | ---: | ---: | ---: | ---: |
| 1× (140M Pro, 75M ProLite a week) | 607.4M | 751.2M | 525.3M | **524.3M** |
| 1.5× | 317.0M | 476.1M | 320.0M | **320.0M** |
| 2× | 154.9M | 317.2M | 27.1M | **25.7M** |

At the estimated capacity (1×) this version spends 14% fewer credits than round-robin and 30% fewer than fill-first.
At 1.5× round-robin spends 0.9% fewer than this version, and at 2× this version spends 83% and 92% fewer. Against
v0.1.1 credits are unchanged at 1× and 1.5× (0.2% and 0.02% fewer) and 5.2% fewer at 2×. At 1× and 1.5× every
policy pays hundreds of millions of units in credits; we read that as demand the two weekly allowances can't hold under
any routing, which the replay doesn't show directly. Quota per unit of Codex work is within 0.12% for every policy.

Codex reports no cache writes, so its write premium is unknown and the replay charges none. If writes cost 1.25 times
uncached input, credits at the estimated capacity are 598M for this version against 675M for round-robin and 824M for
fill-first, and at 2× Codex capacity 49M against 187M and 350M.

### Sensitivity

This version against round-robin and fill-first under other assumptions, Claude quota per unit of work over the whole
recording:

| Scenario | Tool search | Capacity | vs round-robin | vs fill-first |
| --- | --- | --- | ---: | ---: |
| Background at 0.05 | as recorded | 1× | −1.47%* (−1.74 to −1.21) | −1.27%* (−1.52 to −1.01) |
| Background at 0.05 | on | 1× | −1.16%* (−1.32 to −1.01) | −2.08%* (−2.25 to −1.91) |
| Background at 0.1 | as recorded | 1× | −1.50%* (−1.80 to −1.20) | −1.50%* (−1.77 to −1.22) |
| Background at 0.1 | on | 1× | −1.15%* (−1.31 to −1.00) | −2.14%* (−2.30 to −1.99) |
| Background at 0.2 | as recorded | 1× | −1.26%* (−1.60 to −0.92) | −1.45%* (−1.72 to −1.18) |
| Background at 0.2 | on | 1× | −1.07%* (−1.26 to −0.88) | −2.06%* (−2.26 to −1.85) |
| Cold main chats read a warm opening 70% of the time | as recorded | 1× | −1.41%* (−1.66 to −1.16) | −1.09%* (−1.33 to −0.85) |
| Cold main chats read a warm opening 70% of the time | as recorded | 1.25× | −1.97%* (−2.24 to −1.69) | −1.08%* (−1.26 to −0.89) |
| Cold main chats read a warm opening 70% of the time | on | 1× | −1.24%* (−1.36 to −1.12) | −1.52%* (−1.67 to −1.36) |
| Cold main chats read a warm opening 70% of the time | on | 1.25× | −1.27%* (−1.37 to −1.16) | −1.46%* (−1.57 to −1.35) |
| Four Max 20x | as recorded | 1× | −2.54%* (−2.68 to −2.41) | −0.94%* (−1.09 to −0.80) |
| Four Max 20x | as recorded | 1.25× | −2.85%* (−2.90 to −2.79) | −0.74%* (−0.83 to −0.64) |
| Four Max 20x | on | 1× | −0.88%* (−0.91 to −0.84) | −0.67%* (−0.78 to −0.57) |
| Four Max 20x | on | 1.25× | −0.85%* (−0.90 to −0.80) | −0.67%* (−0.74 to −0.60) |
| Plugin restarts daily | as recorded | 1× | −0.98%* (−1.24 to −0.72) | −0.84%* (−1.13 to −0.54) |
| Plugin restarts daily | as recorded | 1.25× | −1.42%* (−1.57 to −1.27) | −0.33%* (−0.49 to −0.17) |
| Plugin restarts daily | on | 1× | −0.74%* (−0.88 to −0.60) | −1.00%* (−1.15 to −0.84) |
| Plugin restarts daily | on | 1.25× | −0.73%* (−0.83 to −0.63) | −0.86%* (−0.97 to −0.76) |

Background is sized relative to the recorded main chats it copies. Its real size is unknown, and 0.05 to 0.2 is the
range we chose to test, not a measured one. Across it, at 0.9× to 1.25× as recorded or with tool search on, it spent
0.95–2.00% less than round-robin and 0.97–2.14% less than fill-first, with every interval in all 18 cells against each
excluding 0. It left significantly more background requests unserved than round-robin in one cell (764 more at 0.2
and 1.25× as recorded) and fewer in four; against fill-first, never more.

A restart forgets which accounts are warm and how fast each burns. Restarting daily cost this version 0.54–0.69% per
unit of work at 1× and 1.25× (every interval excludes 0).

Setting each credential's `weight` to its plan size made no consistent difference to Claude while the plugin kept
running: quota per unit of work moved by −0.34% to +0.13% across 0.9× to 2× as recorded and with tool search on,
significant only at 0.9× (−0.34%) and 2× (−0.07%) as recorded. With a daily restart, when the plugin leans on the
weight until usage headers have moved enough to measure each account, it spent 0.11–0.26% less at 1× and 1.25×
(significant in three of four cells). Codex credits moved both ways:

| Codex credits with plan weights, vs without | 1× Codex capacity | 1.5× | 2× |
| --- | ---: | ---: | ---: |
| Plugin kept running | +0.6% | −45.5% | +56.6% |
| Daily restart | +0.5% | −5.4% | +14.4% |

### Alternatives tested and rejected

Each against this version over the whole recording, change in Claude quota per unit of work (+: the alternative
spends more). The subagent rules are in [subagents and tool search](#subagents-and-tool-search).

| Alternative | As recorded, 1× | As recorded, 1.25× | As recorded, 2× | On, 0.9×† | On, 1×† | On, 1.25× | Decision |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| No warm-cache rule | +0.19% | +0.42%* | +0.60%* | +0.19%* | +0.26%* | +0.28%* | Kept the rule: without it, new main chats start cold 2.7–8.6 points more often |
| Warm rule for main chats only | +0.03% | +0.06% | +0.05% | +0.03% | +0.07%* | +0.06% | Kept it for subagents too |
| No weekly pacing: projected placement for new main chats too | +0.20% | +0.23%* | +0.19%* | −0.02% | −0.03% | 0.00% | Kept pacing: without it, 1.58% less work served at 1× as recorded (−2.63 to −0.53) |

The same warm rule applied to Codex picks too, keyed like Claude's by requested model and agent kind, left Claude
unchanged and spent 27.9% more Codex credits at 2× Codex capacity (32.8M against 25.7M), and 0.16% and 0.27% fewer
at 1× and 1.5×. Codex stays out of the rule.

## What the replay does

`sim_test.go` replays every model request recorded on the author's machine from 2026-10-04 07:36 to 2026-10-14 13:34
UTC: 69,546 Claude requests (Claude Code main chats, subagents and workflow agents, the harness's agent sessions, and
Claude models used through Codex) and 81,091 Codex requests. `testdata/extract.py` reads them from Claude Code
transcripts, the harness's saved sessions and the Codex rollouts that went through CPA: start and completion, chat and
agent IDs, client and version, model, uncached input, cache reads and writes, and output. Each policy routes the same
requests.

CPA's behavior around the routing follows its v8.0.27 source. Session affinity binds a session and model to an
account for a sliding hour, and a subagent without a binding of its own takes its parent's. Round-robin rotates per
model through the candidates in ID order; fill-first takes the first one. An account that returns a 429 leaves the
candidates until its announced reset (CPA reads Claude's from the response headers and Codex's from the response
body), plus a 1–30 s fuzz for Claude only, and the request retries on the rest. A request
without a session ID is keyed by an ID CPA derives from it. The plugin runs unmodified behind its ABI and sees what CPA
passes it: session and parent IDs, the `Anthropic-Beta` request header (the only header it reads, and the only one the
replay sends) and usage records.

- **Accounts.** Claude: four accounts in CPA's candidate order, assumed Max 20x, Max 5x, Max 5x (joined
  2026-10-05 17:58) and Max 20x, with five-hour and weekly windows of 110M and 500M units for Max 20x and a quarter
  of that for Max 5x. Weekly phase and starting usage are unknown and drawn per seed. Codex: Pro and Business ProLite
  with the twelve weekly windows the logs show, early resets included, at 140M and 75M units a week, estimated from
  stretches when one account served everything. An account with credits keeps serving past its limit.
- **Meter.** Claude uses Opus 5.5's [API price](https://platform.claude.com/docs/en/about-claude/pricing) ratios:
  cache read 0.05, five-minute write 1.25, one-hour write 2 and output 5 times uncached input. Other models'
  multipliers are assumed: Sonnet 0.6 and Haiku 0.2 times Opus, and Fable 1, though its list price is 2.5 times Opus
  5.5's (Fable made 2,036 of the 69,546 Claude requests). Codex: cache read 0.1 and output 5.
- **Caches**, as measured from transcripts and request captures:
  - A cache belongs to one account and model. A request's writes are readable from a second after it starts (or when
    it completes, if sooner) until its lifetime after it completes: an hour for Claude Code main chats and Codex,
    five minutes for subagents and workflow agents.
  - A request whose own chat is warm on its account reads what it read in the recording. Otherwise it reads the
    largest warm prefix it shares with other chats and writes the rest.
  - Shared prefixes are the opening, shared by chats with the same client, version, model, kind of agent and MCP
    tools; a fork's parent chat; chats with the same opening that start with the same first message; and, for Codex,
    a subagent's siblings and the harness runs of the same variant. The 3 recorded forks reach the plugin with their
    parent chat as CPA's parent.
  - A later Codex request that lost its own cache reads the opening times the share of such reads that hit in the
    recording: 0.98 for `codex exec`, 0.33 for T3 Code and 0.5 for other clients.
  - Openings recorded before tool search were about 185–230k tokens for main chats and 204–212k for subagents (10th
    to 90th percentile); after it, 4–12k. With tool search on, openings shrink to the later sizes, each chat carries
    its list of deferred tools, about 6.5k tokens, in its own prefix, and Claude Code requests carry the
    `advanced-tool-use-` beta header.
- **Background.** CPA's request log, which starts at 2026-10-10 21:33, shows Claude requests that no transcript
  does: 6,411 runs of the harness's probe (two requests each), 35,020 requests in 18,188 chats without a session ID,
  which aren't Claude Code, and 33,723 requests with a session ID that nothing accounts for. Their sizes are unknown,
  so `SIM_BG` replays them as copies of recorded main chats scaled by `SIM_BG_SCALE`. The results above leave them
  out; the sensitivity table shows them.

### Checked against the recording

The cleanest check is the stretch before the plugin, 2026-10-04 07:36 to 10-05 08:45, routed by round-robin with the
three accounts CPA had then. Replayed with round-robin at 2× (20 seeds):

| Measure | Replay | Recording |
| --- | ---: | ---: |
| Claude quota | 3% high (×1.027, 1.026–1.031 across seeds) | ×1 |
| New main chats starting cold | 29.4% | 30.6% (26 of 85) |
| Subagents starting cold | 14.3% | 11.2% (18 of 161) |
| Workflow agents starting cold | 15.4% | 11.9% (15 of 126) |
| Codex quota | ×1.002 | ×1 |

<a id="capacity"></a>
**Capacity.** CPA ran out of Claude accounts for real at 2026-10-05 03:18 and 05:23 ("All credentials ... are cooling
down"), and the recorded traffic stops 03:18–03:46 and 05:23–07:00. Replaying that window with round-robin and the
default plans (40 seeds), the share of seeds that leave recorded requests unserved in the half hour before the real
events:

| Capacity | Before 03:18 | Before 03:18 or 05:23 | Any time in the window |
| --- | ---: | ---: | ---: |
| 0.9× | 39/40 | 39/40 | 39/40 |
| 0.95× | 16/40 | 16/40 | 17/40 |
| 1× | 9/40 | 9/40 | 9/40 |
| 1.1× | 2/40 | 3/40 | 3/40 |
| 1.2× | 1/40 | 2/40 | 2/40 |
| 1.3× | 1/40 | 1/40 | 1/40 |

Only about 0.9× reproduces the events reliably, and that is a lower bound, since unrecorded traffic that day is
invisible. On 2026-10-12 13:00–16:00, when CPA served everything, the replay with round-robin at 1× serves everything
in 39 of 40 seeds; the other leaves 252 requests unserved there, and 1,684 at 0.9×. So capacity can't be pinned to one
value; we take 0.9× to 1.25× as the realistic band and 2× as roomy. The plans are uncertain too. The fourth account is
a Max 20x, the first belongs to a company organization and is likely a Team seat, and the other two are unknown.

**Plugin periods.** v0.1.0 ran from 2026-10-05 08:47 and v0.1.1 from 10-06 19:55. Replaying each with the release
that ran (2×, 20 seeds):

| Period | Release | Claude quota | New main chats cold, replay / recorded | Subagents | Workflow agents |
| --- | --- | ---: | ---: | ---: | ---: |
| 10-05 08:47 to 10-06 19:55 | v0.1.0 | ×1.005 | 25.1% / 41.2% | 22.1% / 22.4% | 35.3% / 33.0% |
| 10-06 19:55 to 10-10 19:29 | v0.1.1 | ×0.975 | 48.9% / 66.1% | (one) | 42.6% / 64.6% |
| 10-10 19:29 to 10-14 13:34 | v0.1.1 | ×0.998 | 59.1% / 60.9% | 18.0% / 21.7% | 64.0% / 65.4% |

Under the plugin's routing the replay is warmer than the recording, most of all for new main chats (2–17 percentage
points), and early in v0.1.1 it spends 2.5% less than was recorded. The cause is unidentified.

## Limitations

- The replay runs warmer than the recording under the plugin's routing (see the note at the top).
- One person's ten days, with a two-day burst of very heavy use and an A/B harness running through the same proxy.
- Claude capacity and plans are unknown (above), and the meter is assumed.
- Some traffic is known only as counts in CPA's log, and only from 2026-10-10 21:33: the harness's probes, bursts of
  requests without a session ID, and about 34k requests nothing accounts for. Their sizes are guessed.
- The replay assumes every Claude Code request carries the tool-search beta header when tool search is on. That was
  checked for the Agent SDK (sdk-ts) and print mode (sdk-cli) entrypoints with an API key, not with OAuth logins,
  interactive mode, workflow agents or T3 Code's own environment ([how it works](how-it-works.md#subagents-and-forks)).
- Codex had a third Pro account from 2026-10-05 until some time before 10-11 03:15. The rollouts show one Pro window
  at a time, so the replayed Pro windows of that period may mix the two accounts.
- For a Codex credential with `websockets: true`, CPA pins a connection's turns to one account, dropping the pin on a
  401, 402, 403, 408, 429, 502, 503 or 504, a model change, or the account becoming unavailable. The replay picks per
  request, so for such credentials it overstates how quickly any policy can move Codex work off an account that
  starts paying credits mid-connection.
- An unserved request is counted once; a real client would retry later.

## Running it

```sh
make sim   # extract testdata/work.tsv* from your logs, replay once with this version
SIM_POLICY=builtin SIM_SEEDS=20 go test -tags sim -run TestSim -count=1 -v .
```

`-v` matters: without it `go test` hides the output of a passing test. Each run prints one JSON line per seed and
provider; `m` has the scored requests and `bg` the background, which is routed, billed and cached like the rest but
has no `recorded` or `rec_cold`, as its requests are copies of recorded chats. The extracted traffic stays local, and
`testdata/work.tsv*` is ignored by git.

`testdata/extract.py`'s defaults reproduce the author's setup. Its `TOOL_SEARCH` is the date the author turned tool
search on, 2026-10-12 22:16; set yours. Transcripts also carry a per-request signal, the `deferred_tools_delta`
attachment, which extract.py reads only to size the deferred tool lists, not to decide whether tool search was on.
The replay's settings:

| Variable | Default | What it sets |
| --- | --- | --- |
| `SIM_POLICY` | `plugin` | `plugin` (this version), `builtin` (round-robin) or `fillfirst`, both with CPA's session affinity |
| `SIM_SEEDS` | `1` | Replays, each with its own weekly phases and starting usage |
| `SIM_CLAUDE_SCALE` | `1` | Claude capacity, a multiple of the plan allowances |
| `SIM_PLANS` | `20x,5x,5x@2026-10-05T17:58:54Z,20x` | Claude plans in CPA's candidate order; `plan@time` joins then |
| `SIM_CODEX_CAPS` | `pro=140,self_serve_business_prolite=75` | Codex weekly capacity per plan, in millions of units |
| `SIM_OPENINGS` | `recorded` | `recorded`, or `deferred` to replay the chats before tool search with their MCP tools deferred (and Claude Code's tool-search header) |
| `SIM_OPENING_HIT` | `1` | The chance a Claude Code main chat whose own cache is cold reads a warm opening, counted as an expected value |
| `SIM_CODEX_METER` | `0.1,1` | Codex cache read and cache write, relative to uncached input |
| `SIM_WEIGHTS` | unset | `plan` sets each credential's CPA `weight` to its plan size |
| `SIM_RESTART_H` | unset | Hours between plugin restarts, each forgetting what the last one learned, counted from the plugin's start |
| `SIM_TO` | unset | Replay only requests that start before this time (RFC 3339) |
| `SIM_SCORE_FROM` | unset | Requests before it build caches, quota and plugin state but count in no metric |
| `SIM_PLUGIN_FROM` | unset | With `SIM_POLICY=plugin`, route as `builtin` until then and start a fresh plugin |
| `SIM_BG`, `SIM_BG_SCALE` | unset, `0.1` | `SIM_BG` takes a file path, e.g. `testdata/work.tsv.bg`, and adds that background at `SIM_BG_SCALE` times the size of the recorded main chats it copies |
| `SIM_LABEL` | unset | A label copied into each output line |

For example, the tool-search period on its own, at 1.25× with background:

```sh
SIM_POLICY=fillfirst SIM_SEEDS=20 SIM_CLAUDE_SCALE=1.25 SIM_SCORE_FROM=2026-10-12T22:16:00Z \
  SIM_BG=testdata/work.tsv.bg go test -tags sim -run TestSim -count=1 -v .
```
