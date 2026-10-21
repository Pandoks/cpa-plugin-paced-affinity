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
| Codex credits at the estimated Codex capacity | −27% | −41% | −16% |

Negative quota and credits mean this version spends less. Each quota cell spans 20 paired seeds per capacity (40 at 0.9×
and 1× with tool search on), and every one is significant (`*`: its 95% paired interval excludes 0); the closest are
−1.01% (−1.18 to −0.85) against fill-first as recorded and −0.20% (−0.28 to −0.12) against v0.1.1 with tool search on.
Work served is never significantly less than round-robin's, is more than fill-first's in five of the six cells, and
against v0.1.1 is more in one cell and less in one. Codex credits are the same in every seed. Some setups narrow the
margins:

- At 0.9× with tool search on it serves less work than v0.1.1; see [the work-served gap](#work-served-gap).
- At 2×, where the accounts have room, it spent 0.4–3.1% less Claude quota than round-robin or fill-first.
- In the 1.6 days after tool search was turned on, fill-first was level with it at 2×.
- With a daily plugin restart, its lead over fill-first at 1.25× as recorded shrinks to 0.33% (−0.49 to −0.17).
- With a plugin restart every 24 or 6 hours, its Codex credits at the estimated capacity are 24% and 22% below
  round-robin's instead of 27%, still without Codex 429s ([restarts](#codex-restarts)).

The ten-day results were measured before the [expiring-allowance rule](#expiring-allowance) was added. On the ten-day
recording, with tool search on, its effect on work served (0.9× and 1×) and quota per unit of work (0.9× to 1.25×) is
not significant; it spends slightly more at 2× (0.032%*) and, with a daily restart, 0.058%* more at 1× (+0.010 to
+0.107). On a 27-day recording with tool search on it serves 0.55–0.90% more work at 0.7× to 0.9×; at 1× its small
gain doesn't hold up when that recording's openings are estimated differently. With openings as recorded, it serves
0.058% more at 0.9× (not significant after correction for multiple comparisons) and no significant change at 1×.

On that 27-day recording with tool search on, this version with the rule spent 0.68–0.90% less Claude quota per unit of
work than round-robin, 0.26–1.23% less than fill-first at 0.7× to 2×, and 0.14–0.19% less than v0.1.1 at each of
those capacities except 0.8×, where the 0.05% gap isn't significant. It served 0.73–3.34% more work than round-robin
at 0.7× to 1× and as much as v0.1.1, with no gap at 0.9×. It paid 12% fewer Codex credits than round-robin.
See [over 27 days](#27-days).

> [!IMPORTANT]
> Subscription limits and their token accounting are not published, and account capacity and plans are assumed.
> Replays of the periods when the plugin ran find caches warmer than the recording did: 2–17 percentage points fewer
> new main chats start cold. So these results may overstate the plugin's advantage over built-in routing; see
> [checked against the recording](#checked-against-the-recording). They show what the scheduler does with one
> person's recorded traffic under stated assumptions; they do not promise a particular gain on live accounts.

## Terms

| Term | Meaning |
| --- | --- |
| Binding | What ties a chat (or a family's subagents) to an account: for a sliding hour (Claude) or 7 days (Codex) |
| Family | A chat's subagents, nested ones included |
| Quota per unit of work | What a policy spent, under the assumed meter, divided by what the same requests cost as recorded. Tables give its change against this version; negative means this version spends less |
| Unit | One uncached input token's worth of quota under the [meter](#what-the-replay-does); allowances and credits are counted in millions of units |
| Work served | The recorded cost of the requests that found an account |
| Capacity | A multiple of the assumed plan allowances. We take 0.9× to 1.25× as the realistic band (see [capacity](#capacity)); at 2× the accounts have room to spare |
| As recorded, tool search on | Tool search is Claude Code's `ENABLE_TOOL_SEARCH=true`, which defers MCP tools instead of sending them in full. *As recorded* replays the openings as they were: tool search off for the first 8.6 days and on for the last 1.6 (from 2026-10-12 22:16). *Tool search on* replays the first 8.6 days with MCP tools deferred too |
| Round-robin, fill-first | CPA's built-in routing with session affinity and `session-affinity-subagents`, the author's configuration. Round-robin rotates through the accounts; fill-first takes the first |
| v0.1.1 | The release the author ran during the recording, which gave each subagent a binding of its own |
| This version | This branch: a chat's subagents on one shared binding, or with tool search on one each; forks on their parent's binding; Claude picks preferring warm accounts, unless the cold pick's weekly allowance is expiring clearly faster (the [expiring-allowance rule](#expiring-allowance), added after the ten-day tables were measured) |
| Opening | The prompt prefix chats share: the tools and the agent's system prompt |
| Cold start | A chat's first request that read nothing from cache |
| Workflow agent | A subagent launched by a workflow run |
| Harness | An A/B test harness whose agent sessions ran through the same proxy, replayed as main chats of their own class |
| Background | Claude requests CPA's log shows but no transcript does, replayed as scaled copies of recorded main chats; only the [sensitivity](#sensitivity) table includes them |
| Seeds | Each cell replays 20 seeds, each with its own weekly phases and starting usage; cells marked † replay 40. The main ten-day tables use seeds 1–20, or 1–40 where marked †; the [expiring-allowance rule](#expiring-allowance) and [over 27 days](#27-days) use 40 seeds, 101–140. The rule was chosen on the ten-day recording's seeds 101–140, so only its 27-day runs are out of sample. Changes are paired by seed, with 95% t-intervals |
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
accounts reach their five-hour or weekly limit more often (2.40 times per replay against 1.85). Measured: 91% of its
unserved requests at 0.9× come while all four accounts have used up their week, so the gap most likely comes from
weekly allowance that expired earlier rather than from five-hour limits; that is our inference, since the checks below
compare this version with and without the warm-cache step, not against v0.1.1. Comparing this version with and without the warm-cache step on seeds 1–16 at 0.9×, the work the step costs
tracks the weekly allowance that expired before demand peaked (correlation −0.98), but two seeds carry that: with the
step, seed 14 served 17.8M fewer units of work and seed 15 22.7M fewer, and 9 of the 16 seeds served exactly the same.
At 1×, seeds 115 and 117 served 68.2M and 17.7M fewer. In those four seeds the loss starts when the plugin starts and
has observed no account: weekly pacing would pick an account it hasn't observed, but the warm-cache step moves the
chat to one that has served the model, and an account whose week reset hours later with 75–460M units unused got less
work. That this also makes up the gap to v0.1.1 is our inference; the replay doesn't trace it. Letting a pick of an
unobserved account stand fixed seed 15 but hurt seed 7 and spent 0.34% more quota with daily restarts, so it was
rejected. The [expiring-allowance rule](#expiring-allowance) doesn't cover this case: an account's urgency is 0 until it
has reported its weekly reset. Without the warm-cache rule the gap closes (+0.03% work against v0.1.1, −0.03 to
+0.09), but the rule saves 0.19–0.60% across the six cells of the
[alternatives table](#alternatives-tested-and-rejected), significant in five, so it stays. At 1× the gap is 0.03% and
not significant (−0.10 to +0.03).

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

<a id="expiring-allowance"></a>
### The expiring-allowance rule

The warm-cache step keeps a cold pick whose weekly allowance is expiring clearly faster than the warm account's ([how it
works](how-it-works.md#keeping-a-cold-pick-whose-allowance-is-expiring)). Replayed over 27 days with tool search on, it
serves 0.55–0.90% more Claude work when the accounts run short (0.7× to 0.9×). At 0.9× a smaller gain stays significant
when the weeks before the proxy are left out (+0.386%, +0.091 to +0.681; 676 fewer requests unserved, against 1,129),
and with the ten-day recording's openings on the overlap it is +0.635% (+0.355 to +0.914). At 1× it is not
robust: +0.213% here, which doesn't survive correction for multiple comparisons, and not significant in either of those
checks. At 1.25× there is no gain, and at 2× it spends 0.021% more quota per unit of work. With openings as recorded
the gain is about 0.06% at 0.9× and not significant at 1×. The effect is modest, how it serves more work is inferred
rather than measured, and the realistic capacity on this recording is uncertain (below).

The 0.3 lead was chosen on the ten-day recording, screened on seeds 1–16 and then picked on its seeds 101–140,
before any run on this recording, and the 27-day runs below played no part in the choice. Compared directly, the
ten-day runs don't favour it over an urgency of at least 0.5 on quota; the 27-day runs favour it on work served
([other forms](#alternatives-tested-and-rejected)). This recording adds about 17 days that choice didn't see, but its
10-04 to 10-14 span overlaps the ten-day recording, where its re-estimated openings make the work heavier with tool
search on (below).

Measured change with the rule against this version without it, 40 paired seeds (101–140), tool search on unless stated:

| Capacity | Work served | Unserved per replay | Quota per unit of work | 429s | Expired allowance | Cold picks kept / warm-step moves |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 0.7× | +0.553%* (+0.342 to +0.764) | −866* | −0.015% (−0.068 to +0.038) | −0.2 | −16.0M* | 16 / 998 |
| 0.8× | +0.899%* (+0.653 to +1.145) | −1,579* | +0.030% (−0.028 to +0.088) | −0.2 | −37.9M* | 22 / 1,280 |
| 0.9× | +0.644%* (+0.361 to +0.927) | −1,129* | −0.025% (−0.071 to +0.020) | −0.9* | −44.0M* | 25 / 1,510 |
| 1× | +0.213%* (+0.048 to +0.378) | −336* | −0.015% (−0.048 to +0.017) | −0.5* | −40.2M* | 30 / 1,709 |
| 1.25× | +0.008% (−0.008 to +0.025) | −13 | 0.000% | −0.1 | −47.1M* | 42 / 1,912 |
| 2× | 0 | 0 | +0.021%* (+0.018 to +0.024) | 0 | −43.1M* | 44 / 1,808 |
| 1×, daily restart | +0.183%* (+0.018 to +0.349) | −171* | 0.000% | −0.6* | −24.4M* | 18 / 1,326 |
| 0.9×, openings as recorded | +0.058%* (+0.006 to +0.110) | −123* | −0.008% | −0.1 | −6.5M* | 4 / 493 |
| 1×, openings as recorded | +0.023% (−0.005 to +0.050) | −79* | +0.013% | −0.1 | −10.1M* | 6 / 640 |

Expired allowance is the Claude weekly allowance left unused at the weekly resets, per replay; the last column counts,
per replay, how often the warm step would have moved a pick and how often the rule kept it. Both openings-as-recorded
rows route by round-robin until 2026-10-05 08:47, when the plugin started, and by the plugin after.

Separate checks reran the 0.9× and 1× cells with the same seeds on three other versions of the replay. The last takes
the opening sizes for the rows that overlap the ten-day recording from that recording (below):

| Replay | 0.9× work served | 0.9× unserved | 1× work served | 1× unserved |
| --- | ---: | ---: | ---: | ---: |
| Round-robin until 2026-10-05 08:47, the plugin after | +0.450%* (+0.176 to +0.725) | −940* | not run | not run |
| Proxied requests only, from 2026-10-04 07:36 | +0.386%* (+0.091 to +0.681) | −676* (−1,202 to −151) | +0.017% (−0.028 to +0.061) | −34 (−115 to +47) |
| Overlap's openings from the ten-day recording | +0.635%* (+0.355 to +0.914) | −1,119* (−1,683 to −556) | +0.144% (−0.034 to +0.323) | −226 (−538 to +86) |

Without the rule, the proxied-only replay leaves 3,613 requests unserved at 0.9× and 785 at 1×. The round-robin start
alone lowers the 0.9× gain from +0.644% to +0.450%, and adding openings as recorded takes it to +0.058%, so most of
that drop comes with the openings rather than the round-robin start; splitting it this way is inferred, since the two
effects need not add. Quota per unit of work changed significantly in no rerun; with the
ten-day openings it moved −0.008% at 0.9× (−0.050 to +0.034) and −0.027% at 1× (−0.057 to +0.002). The 0.7× to 0.9×
cells (p ≤ 4.3e-5) survive correction for multiple comparisons; the 1× cell (p = 0.013), the 1× daily-restart cell
(p = 0.031) and the 0.9× openings-as-recorded cell (p = 0.029) don't survive Holm correction. So with tool search on
the 0.9× gain holds in every rerun, while the 1× gain fails correction and both reruns that test it. Keeping the cold
pick whenever its own urgency is at least 0.25 serves about as much more work (level with this rule from 0.7× to 1×),
so picking this rule from several didn't inflate the work gain; at least 0.5 serves less (below).

**Why it helps.** Measured: with the rule, 16–47M fewer units of weekly allowance expire unused per replay, and the
work it adds lands on crunch days: at 0.9×, 143 fewer requests unserved on 10-06 and 738 fewer on 10-18 (both `*`). The
27 days have two weekly crunches, 10-05 to 10-08 and 10-16 to 10-18, matching CPA's real "All credentials ... cooling
down" events on 10-05 (03:18, 05:23 and 17:58), 10-08 04:36 and 10-16 21:46. Inferred, not measured: allowance saved
from expiring at a weekly reset becomes served work when a shortage follows, but that isn't the only route. On the
ten-day recording at 0.9×, seed 107 left 6M fewer units unused at a 10-06 04:42 reset before its 10-07 crunch and
served 5.5M more units of work, while seed 101 served 2.3M more with no reset saving allowance before its crunch.
Across its 40 seeds the rule's effect there is not significant: work served +0.060% at 0.9× (−0.045 to +0.166)
and +0.127% at 1× (−0.052 to +0.307); quota per unit of work −0.002%, +0.024% and −0.008% at 0.9×, 1× and 1.25×, but
+0.032%* at 2× and +0.058%* at 1× with a daily restart.

**The 27-day recording.** `testdata/extract.py` with `since` set to 2026-09-19 treats every Claude Code transcript
from then on as proxied. Its requests run from 2026-09-21 23:55 to 2026-10-19 00:00 UTC: 174,887 Claude requests,
3,382M units of work with tool search on. The author's Claude Code didn't go through CPA before 2026-10-04 07:36, where
the ten-day recording starts, so the 39,740 Claude requests before then are transcripts replayed as if they had: the
demand is real, but those accounts never served it through the proxy. They include no harness sessions; the harness's
6,335 Claude requests all come later. Most of the harness's agent sessions from 10-11 to 10-13 have since been deleted,
so only 362 of the ten-day recording's 15,120 harness rows could still be extracted; the 27-day recording takes all
15,120 from the ten-day recording instead. Its other rows from 10-04 07:36 to 10-14 13:34 match the ten-day
recording's one for one, but extracting over 27 days re-estimates their openings. Taking the openings from the ten-day
recording changes the opening columns of 39,674 rows, mostly the size of the deferred tool list, and the opening size
itself of 5,985, 3,632 of them Codex. On the 1,958 Claude main-chat rows among those, the median opening is about 25k
tokens here and 216k in the ten-day recording, which matches the 185–230k [recorded before tool
search](#what-the-replay-does). With tool search on, deferring the tools takes about 197k off a 216k opening but only
about 7k off a 25k one, so the smaller estimates leave the overlap's Claude work about 0.9% heavier (1,595.0M units
against 1,580.7M with the ten-day openings). The check above that takes those openings from the ten-day recording keeps
the 0.9× gain (+0.635%*) and loses the 1× one (+0.144%, not significant). The replay assumes the same accounts before
10-04 as after (the default
`SIM_PLANS`), and the plugin routes throughout unless a row says otherwise.

**Calibration caveat.** With openings as recorded, the replay of this recording reproduces the real 10-05 03:18
run-out in most seeds only at about 0.7× (33 of 40 seeds; at 1×, 4 of 40), unlike the ten-day
[capacity check](#capacity) (0.9×: 39 of 40). Yet in the same runs 0.9× runs out on 10-06 in 38 of 40 seeds, when no
real all-credentials event happened. With tool search on, 0.7× reproduces the 03:18 run-out in only 14 of 40 seeds. So
which capacities are realistic on this recording is uncertain.

<a id="27-days"></a>
### Over 27 days

On the [27-day recording](#expiring-allowance) with tool search on, this version, with the expiring-allowance rule,
spent less Claude quota per unit of work than round-robin, fill-first and v0.1.1 at every capacity, significantly in
17 of 18 cells. It served more work than round-robin and fill-first whenever the accounts ran short, and about as much
as v0.1.1. Its first 12 days are transcripts from before the proxy, replayed as if routed, and which capacities are
realistic here is uncertain (the calibration caveat above). v0.1.1 is that release's plugin code, built against the
current replay. All cells use 40 paired seeds (101–140).

| Capacity | Quota per unit of work vs round-robin | vs fill-first | vs v0.1.1 |
| --- | ---: | ---: | ---: |
| 0.7× | −0.68%* (−0.77 to −0.58) | −1.23%* (−1.36 to −1.10) | −0.16%* (−0.22 to −0.10) |
| 0.8× | −0.80%* (−0.89 to −0.72) | −1.23%* (−1.32 to −1.14) | −0.05% (−0.12 to +0.01) |
| 0.9× | −0.86%* (−0.92 to −0.79) | −1.20%* (−1.28 to −1.13) | −0.14%* (−0.18 to −0.10) |
| 1× | −0.86%* (−0.91 to −0.81) | −1.17%* (−1.25 to −1.10) | −0.15%* (−0.18 to −0.12) |
| 1.25× | −0.90%* (−0.95 to −0.86) | −1.05%* (−1.13 to −0.98) | −0.19%* (−0.21 to −0.16) |
| 2× | −0.80%* (−0.84 to −0.77) | −0.26%* (−0.31 to −0.22) | −0.15%* (−0.17 to −0.13) |

| Capacity | Unserved of 174,887: this version / round-robin / fill-first / v0.1.1 | Work served vs round-robin | vs fill-first | vs v0.1.1 |
| --- | --- | ---: | ---: | ---: |
| 0.7× | 23,977 / 28,751 / 33,360 / 24,103 | +3.32%* (+2.44 to +4.20) | +6.89%* (+5.59 to +8.20) | +0.07% (−0.10 to +0.25) |
| 0.8× | 10,127 / 15,344 / 19,638 / 10,143 | +3.34%* (+2.71 to +3.98) | +6.71%* (+5.55 to +7.86) | +0.02% (−0.14 to +0.18) |
| 0.9× | 2,542 / 6,137 / 9,187 / 2,663 | +2.36%* (+1.86 to +2.85) | +4.80%* (+4.00 to +5.60) | +0.08% (−0.09 to +0.25) |
| 1× | 268 / 1,393 / 2,996 / 308 | +0.73%* (+0.37 to +1.10) | +2.12%* (+1.57 to +2.66) | +0.02% (−0.04 to +0.08) |
| 1.25× | 0 / 0 / 356 / 0 | 0.00% | +0.24%* (+0.11 to +0.38) | 0.00% |

At 2× nothing goes unserved. The ten-day [work-served gap](#work-served-gap) to v0.1.1 at 0.9× doesn't appear here.
Measured: without the rule, this version serves less than v0.1.1 on this recording too, by 0.47–0.86% at 0.7× to
0.9× (0.55% at 0.9×, each `*`), so here the rule's 0.55–0.90% gain closes the gap. How the rule adds that work is
inferred, as above. Against round-robin the quota saving is smaller than on the ten-day recording with tool search on:
0.68–0.90% here, against 1.14–1.31% there. Codex is replayed the same in every seed and at every Claude capacity. At
the estimated Codex capacity this version paid 941.8M units in credits over the 27 days: 12% fewer than round-robin
(1,067.9M), 29% fewer than fill-first (1,323.5M) and 14% fewer than v0.1.1 (1,097.7M). Before the
[weekly-only Codex rule](#codex) it paid 1,096.7M, 2.7% more than round-robin; [Codex](#codex) traces why.

### Codex

Units of Codex usage paid with credits. With the plugin kept running, this version pays the fewest at every capacity
over both recordings, with no Codex 429s; with restarts every 6 or 24 hours it pays more than before at 2×
([restarts](#codex-restarts)). The recorded pool is Pro, which has credits, and Business ProLite, which has them in
only one recorded window (10-09 22:39 → 10-13 05:52), so the
[weekly-only Codex rule](how-it-works.md#weekly-only-codex-accounts) applies: least-used share of the week first,
ProLite held from 98% weekly usage, and a probe for early resets every 3 hours. *Before* is this version without that
rule or its [start-up](how-it-works.md#codex-start-up) rule (commit 7b8eff0), which kept new work off an account at
100% while the other had room. Codex results are the same in every seed: its windows are replayed as recorded, early
resets included, and its routing doesn't depend on the Claude side.

| Codex capacity | Round-robin | Fill-first | v0.1.1 | Before | **This version** |
| --- | ---: | ---: | ---: | ---: | ---: |
| Ten days, 1× (140M Pro, 75M ProLite a week) | 607.4M | 751.2M | 525.3M | 524.3M | **442.8M** |
| Ten days, 1.5× | 317.0M | 476.1M | 320.0M | 320.0M | **182.2M** |
| Ten days, 2× | 154.9M | 317.2M | 27.1M | 25.7M | **14.7M** |
| 27 days, 1× | 1,067.9M | 1,323.5M | 1,097.7M | 1,096.7M | **941.8M** |
| 27 days, 1.5× | 595.1M | 925.1M | – | 662.9M | **417.2M** |
| 27 days, 2× | 258.0M | 631.1M | – | 132.3M | **124.4M** |

Measured: at 1× this version pays 27% fewer credits than round-robin, 41% fewer than fill-first and 16% fewer than
v0.1.1 over ten days, and 12%, 29% and 14% fewer over 27 days. Before had Codex 429s (2, 1 and 0 at 1×, 1.5× and 2×
over ten days; 2, 2 and 1 over 27); this version has none, also with restarts every 6 or 24 hours (more frequent
restarts can still cause one; see [restarts](#codex-restarts)). No Codex request goes unserved under
any policy. At 1× and 1.5× every policy pays hundreds of millions of units in credits; we read that as demand the two
weekly allowances can't hold under any routing, which the replay doesn't show directly. Quota per unit of Codex work
is 0.04–0.30% higher than before and 0.07–0.39% above round-robin's and fill-first's.

Why, from the 27-day replay's traces at 1×: before, Pro paying credits at 100% counted as exhausted just like a full
ProLite. With both exhausted, the plugin picked ProLite for its larger projected headroom, which answered with a 429 at
10-14 13:21. CPA v8.0.27 then keeps an account out until the reset it announced (10-20 05:52, after the recording
ends) and never lets it back early. ProLite's window reset early, on 10-15 06:27, and the old rule used none of that
window's 75M units while Pro paid credits; round-robin used all of it. Early resets are the norm here: in the
replayed windows, taken from the Codex rollouts, Pro's weekly windows from 10-04 on lasted 0.4–3.9 days and ProLite's
0.5–3.3, against the seven each announced.

<a id="codex-restarts"></a>
With plugin restarts (`SIM_RESTART_H`; seed 101, [openings](#terms) deferred as with tool search on), credits in
millions of units and Codex 429s, before → this version:

| Restart | Ten days, 1× | Ten days, 2× | 27 days, 1× | 27 days, 2× |
| --- | ---: | ---: | ---: | ---: |
| None | 524.3 (2) → 442.8 (0) | 25.7 (0) → 14.7 (0) | 1,096.7 (2) → 941.8 (0) | 132.3 (1) → 124.4 (0) |
| Every 24 hours | 524.6 (1) → 462.9 (0) | 40.3 (1) → 40.7 (0) | 1,101.2 (2) → 973.2 (0) | 140.0 (2) → 157.4 (0) |
| Every 6 hours | 527.7 (2) → 476.1 (0) | 41.2 (1) → 52.8 (0) | 1,102.1 (2) → 957.4 (0) | 141.6 (2) → 159.4 (0) |

Measured: with 6- and 24-hourly restarts this version still has no Codex 429s and pays 10–13% fewer credits at 1×, but 1–28% more at
2×. The traces put the 2× rise on Pro's early reset at 10-04 21:06, which ended its 10-02 window: restarts reveal it
sooner (between 10-04 21:06 and 10-05 07:37, against 10-05 22:36 without restarts), least-used share then sends work
to Pro, and the extra credits fall on 10-07 and 10-08 in the window that reset opened, before its own early reset at
10-08 17:23 (ten days at 2× with 6-hourly restarts: 52.5M in that window against 14.2M without restarts). Over 27 days at 1× with 6-hourly
restarts, ProLite's last window without credits ends at 74.93M of its 75M units (99.9%), near the edge: it is held
from 98% on 10-16 at 05:53 until the recording ends about 2.75 days later. Until every account has answered after a
restart the hold doesn't apply, so a held account keeps serving its live chats and new chats for those first seconds
([start-up](how-it-works.md#codex-start-up)). Measured in the 27-day 1× traces with 3- and 6-hourly restarts: after
the restart at 10-17 06:19:10, ProLite, held at 98.5–99.1% weekly usage, served requests at 06:19:10 and
06:19:16 before Pro's first answer at 06:19:20; most restarts cost a held account one or two requests. Inferred: a
hold longer than 2.75 days with 6-hourly restarts could use up the last 1% and reach a 429. Measured with more
frequent restarts: every 3 hours, this version had 1 Codex 429 in one of four runs (27-day 1×), and hourly, 1–2 in
three of four, against 1–2 in every one of those runs for the previous version. Traced cause in that 3-hourly run:
ProLite crosses 98% on 10-16 at 05:40 and reaches 100.3% on 10-17 at 15:01, about 1.4 days and 12 requests later,
most of them served just after a restart; its next request, at 17:55, gets the 429. With hourly restarts this version
paid more credits than before on ten days at 1× (540.4M against 531.3M) and fewer on 27 days at 2× (175.4M against
291.7M), so with hourly restarts the comparison is mixed. With restarts every 30 minutes it had no more Codex 429s
than before (6 against 7 over the four runs) and paid no more credits. Probing every account not heard from for 3 hours, not only exhausted or held ones,
raised credits in every cell without restarts (2×: 14.7M → 47.7M over ten days, 124.4M → 156.3M over 27) and was
rejected. Claude results are unchanged in every cell.

Limits:

- The 2% reserve and the 3-hour probe interval were tuned on these two recordings. Measured without restarts: shorter
  intervals cost more at 1×: 30 minutes 64.5M more over ten days and 137.7M more over 27 (507.3M and 1,079.5M, against
  442.8M and 941.8M), and 1 hour 64.3M more over each, with no 429s; 6 hours paid within 5M of 3 hours in every cell.
  A 1% reserve paid slightly fewer credits in all six cells without restarts (440.4M and 938.6M at 1×), also with no
  429s, but with 6-hourly restarts it ran out: one and two Codex 429s at 1× and 111M and 66M more credits than 2%. So
  2% is kept for restarts. A 3% reserve paid 2.5–5.3M more than 2% without restarts.
- With early resets removed from the replayed windows, this version pays 0.1–0.7% more credits than before. Its
  saving depends on windows resetting early.
- The plugin reads `X-Codex-Credits-Has-Credits` from the response. That CPA passes it is inferred from CPA's source and
  test fixtures, and the Codex client reads the same header, but it wasn't captured from live traffic. Without it every
  account counts as not paying: each is held from 98% and none is preferred or probed for credits; least-used-share
  placement, the start-up rule and probes of held accounts below 99% still act.
- Plus and mixed Codex pools weren't tested: the recording has none. They keep projected placement, with the start-up
  rule until an account shows a five-hour window.

Codex reports no cache writes, so its write premium is unknown and the replay charges none. If writes cost 1.25 times
uncached input, credits over ten days at the estimated capacity are 515M for this version against 675M for
round-robin and 824M for fill-first, and at 2× Codex capacity 48M against 187M and 350M.

<a id="codex-cache"></a>
### Codex cache kept indefinitely

On 2026-10-21 OpenAI [announced](https://x.com/thsottiaux/status/2112712984644784534) that Codex keeps a logged-in
user's prompt cache indefinitely, so resuming an old conversation reads it from cache. The cache belongs to the
account (inferred; the announcement doesn't say), so a chat that moves still reads its whole history uncached on its
new account. This version keeps a Codex chat bound to its account for 7 days of inactivity instead of an hour; it still
moves off an account that is exhausted, or held in a weekly-only pool, while another account can take it.

Measured on a different recording from the tables above: everything extracted on the author's machine from 2026-10-04
07:36 to 2026-10-21, 135,837 Codex requests in 13,905 chats, without the A/B harness's sessions, which weren't on that
machine. The replay keeps Codex caches indefinitely (`SIM_CODEX_TTL`, default `inf`). Credits in millions of units,
with warm chats moved in brackets; *before* is this version with Codex bindings expiring after an hour:

| Codex capacity | Restart | Round-robin | Fill-first | Before | **This version** |
| --- | --- | ---: | ---: | ---: | ---: |
| 1× | none | 1,193.4 (26) | 1,441.9 (0) | 1,059.7 (108) | **1,059.9 (105)** |
| 1.5× | none | 774.3 (36) | 1,066.1 (0) | 558.4 (88) | **558.7 (83)** |
| 2× | none | 371.6 (44) | 732.2 (0) | 234.6 (73) | **234.8 (61)** |
| 1× | 24 h | | | 1,079.6 (125) | **1,079.8 (121)** |
| 1.5× | 24 h | | | 580.6 (135) | **580.6 (106)** |
| 2× | 24 h | | | 260.7 (110) | **259.7 (74)** |
| 1× | 6 h | | | 1,089.2 (178) | **1,075.9 (168)** |
| 1.5× | 6 h | | | 589.5 (174) | **593.7 (172)** |
| 2× | 6 h | | | 270.4 (170) | **269.5 (163)** |

Measured: the change moves fewer warm chats (3–36% fewer) but leaves credits within 0.7% of before, except 1.2% fewer
at 1× with 6-hourly restarts, and Codex quota per unit of work within 0.035%; neither has Codex 429s without restarts,
and both have one at 1× with 6-hourly restarts. The reason is in the traffic: only 140 Codex requests start more than
an hour after their chat's previous one, carrying 18M of the 14,382M prompt tokens, and before already sent most of
them back to the same account. The cache change itself, with routing unchanged, cut Codex quota per unit of work by
0.18% at 1× (2,420.3M units with one-hour caches, 2,415.9M indefinite). Interactive use that resumes chats after hours
would gain more than this mostly automated traffic; that wasn't replayed. A restart still forgets every binding, so
a chat resumed after one is placed afresh.

Also tested against this version, all at the same capacities and restarts, and rejected:

| Alternative | Credits at 1× / 1.5× / 2× | Decision |
| --- | --- | --- |
| Keep a live chat on a held account; only new work avoids it | 1,221.6 / 800.0 / 491.1 (+15% / +43% / +109%) | Rejected: the held account reaches its limit, CPA benches it after the 429 past its early reset, and the other pays credits; 2 Codex 429s with restarts. It saves 0.06% quota |
| Also keep a live chat on an account paying credits | 1,223.7 / 802.0 / 493.2 | Rejected, as above |
| Warm rule for Codex: a new chat prefers an account that has served its model | 1,092.3 / 592.8 / 273.3 (+3% / +6% / +16%) | Rejected: cold first requests unchanged (1,475 against 1,476), since every account soon holds every opening |

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
(significant in three of four cells). Codex credits moved by more than 0.1% only at 2×, up with the plugin kept
running and down with a daily restart:

| Codex credits with plan weights, vs without | 1× Codex capacity | 1.5× | 2× |
| --- | ---: | ---: | ---: |
| Plugin kept running | 0.0% | −0.01% | +31% (19.3M against 14.7M) |
| Daily restart | −0.03% | −0.04% | −4.0% (39.1M against 40.7M) |

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
at 1× and 1.5×, measured before the [weekly-only Codex rule](#codex). Codex stays out of the rule.

**Other forms of the expiring-allowance rule.** A screen on the ten-day recording with tool search on (seeds 1–16; 0.9×,
1× and 1.25×) tried fifteen forms: keeping the cold pick whenever its own urgency is at least 0.15, 0.25, 0.35, 0.5 or
1.0; at least 0.25 or 0.5 for new main chats only; at least 0.25 only when its projected weekly leftover is at least 10%
or 20%, or only when the candidates' average projected weekly usage is at least 55%; any leftover when its weekly reset
is within 12, 24 or 48 hours; and a lead of 0.15 or 0.3. Against no rule, ten spent significantly more quota per unit of
work at 1× or 1.25× (at least 1.0 by only 0.007%*). Of the other five, main chats only at 0.5 and the 20% floor cut
expired allowance by no significant amount, and a reset within 12 hours, which cut it, wasn't carried forward either.
That left the 0.3 lead and at least 0.5, with no significant quota change at any capacity and less allowance expiring
(−19.8M* and −18.3M* at 0.9×). Both went on to the ten-day recording's seeds 101–140 (0.9× to 2×), and so did at least
0.15 and at least 0.25, although on the screen they had spent more at 1× (+0.236%* and +0.146%*), and at least 0.15 also
at 1.25× (+0.130%*). On those seeds the 0.3 lead was the only form with no significant quota increase over no rule at
0.9× to 1.25× (−0.002%, +0.024%, −0.008%): at 1.25× at least 0.15, 0.25 and 0.5 spent 0.133%*, 0.064%* and 0.035%* more.
At 2× all four spent more, the lead least (+0.032%*). That picked the lead, before any 27-day run; with the ten-day
recording replayed twice over, it was again the only form tested there without a significant increase at 0.9× to 1.25×,
on both seed sets. With a daily restart it was also the only one level with no rule at 1.25×, but at 1× it spent 0.058%*
more, where at least 0.5 was level (+0.004%). Compared directly with the lead, at least 0.25 spent 0.071%* more at 1.25×
(+0.031 to +0.112), but at least 0.5 didn't: +0.043% at 1.25× (−0.004 to +0.091, not significant) and 0.047%* less at 1×
(−0.085 to −0.009). So the ten-day runs don't favour the lead over at least 0.5 on quota. The 27-day runs (seeds
101–140), which played no part in the choice, favour the lead on work served, the point of the rule, since the shortage
is what loses work: it served more than at least 0.5 at 0.8× (+0.132%*) and 0.9× (+0.167%*). At least 0.25 was level
with it in work served from 0.7× to 1×, but spent 0.053%* more quota than the lead at 1.25× and 0.046%* more at 2×.

**Avoiding forced moves.** A *forced move* takes a chat off an account where its cache is still warm because that
account reached its five-hour or weekly limit. Measured on the ten-day recording with tool search on (seeds 101–140):

| Capacity | Forced moves per replay | Main / subagent / workflow / Claude via Codex | Five-hour / weekly | Cost | Share of quota |
| --- | ---: | --- | --- | ---: | ---: |
| 0.9× | 22.2 | 5.4 / 8.9 / 6.5 / 1.5 | 11.3 / 10.9 | 7.02M | 0.461% |
| 1× | 13.0 | 3.2 / 5.8 / 3.9 / 0.2 | 6.9 / 6.1 | 4.30M | 0.278% |
| 1.25× | 6.6 | 0.9 / 4.6 / 1.1 / <0.1 | 5.3 / 1.3 | 2.00M | 0.126% |
| 2× | 0.5 | | | 0.28M | 0.018% |
| 1×, daily restart | 27.5, of which 14.4 at no limit: the restart forgot the binding | | | 11.45M | 0.728% |

These are the same moves the [Claude table](#claude) counts as warm chats moved, without the expiring-allowance rule
in both, but on seeds 101–140 rather than 1–40, and the count varies widely by seed: 13.0 here at 1× against 9 there
(seeds 1–16 alone average 7.3).

43 variants were screened on seeds 1–16: a separate five-hour margin for the warm step (projected usage at most 60% to
90%), a weekly margin, room relative to the pick, scarcity gates, burn rates that react to bursts (the larger of the
30-minute and 5- or 10-minute rates), room reserved for live chats (at 0.5× or 1× their burn, per chat, or for chats
just placed), and combinations. The best, held out on seeds 101–140, change in Claude quota per unit of work with tool
search on (+: the variant spends more):

| Variant | 0.9× | 1× | 1.25× | 2× | 1×, daily restart | Decision |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| Warm step needs projected five-hour usage at most 85% | +0.023% | −0.041% | −0.032% | +0.004%* | +0.034% | Rejected |
| Warm step's margin check adds 0.5× the account's burn | +0.025% | +0.008% | +0.007% | +0.006% | +0.048% | Rejected |
| Both | +0.032% | +0.010% | +0.016% | +0.014%* | +0.053%* | Rejected |
| No warm-cache rule | +0.249%* | +0.230%* | +0.236%* | +0.224%* | +0.372%* | Kept the rule |

None did significantly better. Stricter warm-step margins avoid 1–4 forced moves per replay but cause about as many
cold starts, and cautious burn projections concentrate work and add moves.

## What the replay does

`sim_test.go` replays every model request recorded on the author's machine from 2026-10-04 07:36 to 2026-10-14 13:34
UTC: 69,546 Claude requests (Claude Code main chats, subagents and workflow agents, the harness's agent sessions, and
Claude models used through Codex) and 81,091 Codex requests. `testdata/extract.py` reads them from Claude Code
transcripts, the harness's saved sessions and the Codex rollouts that went through CPA: start and completion, chat and
agent IDs, client and version, model, uncached input, cache reads and writes, and output. Each policy routes the same
requests. The [expiring-allowance rule](#expiring-allowance) and [over 27 days](#27-days) also use a 27-day recording, described with the rule.

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
  stretches when one account served everything. An account with credits keeps serving past its limit, and each
  response reports whether the account has credits in `X-Codex-Credits-Has-Credits`.
- **Meter.** Claude uses Opus 5.5's [API price](https://platform.claude.com/docs/en/about-claude/pricing) ratios:
  cache read 0.05, five-minute write 1.25, one-hour write 2 and output 5 times uncached input. Other models'
  multipliers are assumed: Sonnet 0.6 and Haiku 0.2 times Opus, and Fable 1, though its list price is 2.5 times Opus
  5.5's (Fable made 2,036 of the 69,546 Claude requests). Codex: cache read 0.1 and output 5.
- **Caches**, as measured from transcripts and request captures:
  - A cache belongs to one account and model. A request's writes are readable from a second after it starts (or when
    it completes, if sooner) until its lifetime after it completes: an hour for Claude Code main chats, five minutes
    for subagents and workflow agents, and indefinitely for Codex (`SIM_CODEX_TTL`). The Codex tables before
    [Codex cache kept indefinitely](#codex-cache) were measured with an hour.
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
- One person's ten days (27 for the expiring-allowance rule and over 27 days, its first 12 not proxied), with a two-day burst of very
  heavy use and an A/B harness running through the same proxy.
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
provider; `m` has the scored requests and `bg` the background, which is routed, billed and cached like the rest but has
no `recorded` or `rec_cold`, as its requests are copies of recorded chats. Claude's `expired_units` is the weekly
allowance the accounts left unused at their weekly resets from `SIM_SCORE_FROM` on. The extracted traffic stays local,
and `testdata/work.tsv*` is ignored by git.

`testdata/extract.py`'s defaults reproduce the author's setup. Its `TOOL_SEARCH` is the date the author turned tool
search on, 2026-10-12 22:16; set yours. Transcripts also carry a per-request signal, the `deferred_tools_delta`
attachment, which extract.py reads only to size the deferred tool lists, not to decide whether tool search was on.
The replay's settings:

| Variable | Default | What it sets |
| --- | --- | --- |
| `SIM_POLICY` | `plugin` | `plugin` (this version), `builtin` (round-robin) or `fillfirst`, both with CPA's session affinity |
| `SIM_SEEDS` | `1` | Replays, each with its own weekly phases and starting usage |
| `SIM_SEED_FROM` | `1` | The first seed, e.g. `101` for seeds outside the 1–16 used to screen rules |
| `SIM_CLAUDE_SCALE` | `1` | Claude capacity, a multiple of the plan allowances |
| `SIM_PLANS` | `20x,5x,5x@2026-10-05T17:58:54Z,20x` | Claude plans in CPA's candidate order; `plan@time` joins then |
| `SIM_CODEX_CAPS` | `pro=140,self_serve_business_prolite=75` | Codex weekly capacity per plan, in millions of units |
| `SIM_OPENINGS` | `recorded` | `recorded`, or `deferred` to replay the chats before tool search with their MCP tools deferred (and Claude Code's tool-search header) |
| `SIM_OPENING_HIT` | `1` | The chance a Claude Code main chat whose own cache is cold reads a warm opening, counted as an expected value |
| `SIM_CODEX_TTL` | `inf` | Seconds a Codex request's cache stays readable after it completes; `3600` replays the cache before 2026-10-21 |
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
