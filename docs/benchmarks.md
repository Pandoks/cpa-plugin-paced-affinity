# Benchmarks

**Paced Affinity** was measured by replaying nine days of recorded Claude Code and Codex traffic through simulated
accounts, with the plugin's own code making every routing decision. At the capacity closest to the recording, it
spent **4.0% less Claude quota per unit of work** than CPA's built-in routing and served the same work. Codex credits
fell by **10% at the estimated capacity and by 42–100% when the accounts had more room**.

> [!IMPORTANT]
> Subscription limits and their token accounting are not published, and account capacity is assumed. These results
> show what the scheduler does with one user's recorded traffic under stated assumptions; they do not promise a
> particular gain on live accounts.

## Results

### Claude

Change in quota spent per unit of work against CPA's built-in routing; negative is better. Capacity multiplies the
assumed plan allowances. At 1× and 1.25× demand exceeds capacity and requests go unserved; the recording itself
shows about seven Claude 429s in a week, closer to 2× or 3×.

| Capacity | v0.1.1 | Subagents follow parent | **This version** | Work served by this version |
| --- | ---: | ---: | ---: | ---: |
| 1× | +1.23% (+0.88 to +1.57) | −0.72% (−1.04 to −0.40) | **−0.77% (−1.11 to −0.44)** | +0.10% (−1.49 to +1.69) |
| 1.25× | +1.09% (+0.42 to +1.77) | −0.99% (−1.40 to −0.59) | **−1.54% (−1.93 to −1.16)** | −0.27% (−0.75 to +0.20) |
| 1.5× | +0.62% (+0.11 to +1.13) | −1.79% (−2.07 to −1.51) | **−2.72% (−3.02 to −2.42)** | No change |
| 2× | −0.78% (−1.12 to −0.43) | −2.79% (−2.99 to −2.59) | **−4.03% (−4.25 to −3.81)** | No change |
| 3× | −1.37% (−1.73 to −1.01) | −2.82% (−3.08 to −2.56) | **−4.07% (−4.26 to −3.88)** | No change |

Most of the difference is how often a chat's first request found its prompt prefix cached. At 2× capacity:

| Policy | New main chats starting cold | Subagents starting cold | 429s in nine days |
| --- | ---: | ---: | ---: |
| Built-in | 34.5% | 13.3% | 4.3 |
| v0.1.1 | 20.9% | 21.1% | 0 |
| Subagents follow parent | 23.9% | 7.0% | 0 |
| **This version** | **12.8%** | **5.5%** | **0** |

Built-in round-robin hands each new chat to the next account, often one that has been idle past the cache lifetime.
v0.1.1 placed subagents by headroom, away from the accounts where their prefix was cached. Following the parent fixes
the subagents; preferring warm accounts fixes most of the new chats.

The cost is under overload. Concentrating new chats on warm accounts drains them sooner: at 1× capacity this version
served 0.81% less work than the subagent change alone (−1.56 to −0.05), and at 1.25× 0.48% less (−1.07 to +0.11).
Against the built-in routing the difference is not significant at either capacity.

### Codex

Units of Codex usage paid with credits over the nine days. The plugin keeps new work off an account that reports
100% usage while the other has room; built-in routing keeps sending it work, which Pro then pays for with credits.

| Codex capacity | Built-in | v0.1.1 | **This version** |
| --- | ---: | ---: | ---: |
| 1× (140M Pro, 75M ProLite a week) | 474.5M | 429.8M | **428.7M** |
| 1.5× | 363.4M | 209.6M | **209.3M** |
| 2× | 97.4M | 49.1M | **51.9M** |
| 3× | 50.3M | 0 | **0** |

At 1× both accounts are at their limit most of the time, and credits pay for demand no routing can place. Quota per
unit of Codex work is the same for every policy within 0.3%.

### Sensitivity

This version against built-in routing and against the subagent change alone, Claude cost per unit of work:

| Scenario | Capacity | vs built-in | vs subagents follow parent |
| --- | --- | ---: | ---: |
| Cache reads count 0.1 | 1.25× | −0.17% (−0.51 to +0.17) | +0.09% (−0.26 to +0.44) |
| Cache reads count 0.1 | 2× | −1.30% (−1.47 to −1.14) | −0.41% (−0.55 to −0.28) |
| No cache-write premium | 1.25× | −1.64% (−1.83 to −1.45) | −0.66% (−0.81 to −0.50) |
| No cache-write premium | 2× | −2.86% (−2.97 to −2.75) | −0.79% (−0.87 to −0.71) |
| Output counts 8 | 1.25× | −1.59% (−1.88 to −1.31) | −0.67% (−0.93 to −0.40) |
| Output counts 8 | 2× | −3.51% (−3.71 to −3.30) | −1.14% (−1.29 to −1.00) |
| Four Max 20x | 1.25× | −3.18% (−3.26 to −3.10) | −0.95% (−1.13 to −0.76) |
| Four Max 20x | 2× | −3.84% (−3.90 to −3.77) | −1.33% (−1.43 to −1.23) |
| Max 20x, three Max 5x | 1.25× | −1.06% (−1.57 to −0.55) | −0.50% (−0.84 to −0.15) |
| Max 20x, three Max 5x | 2× | −2.78% (−3.19 to −2.38) | −0.61% (−0.79 to −0.42) |
| Two Max 20x, one Max 5x | 1.25× | −0.77% (−0.98 to −0.55) | −0.75% (−1.02 to −0.49) |
| Two Max 20x, one Max 5x | 2× | −2.72% (−2.90 to −2.55) | −1.12% (−1.23 to −1.00) |

With cache reads counted at 0.1, this version also served 0.70% less work than the subagent change alone at 1.25×
(−1.41 to 0.00), and with output counted at 8, 0.65% less (−1.16 to −0.14).

Setting each credential's `weight` to its plan size made no consistent difference. Over 40 seeds, Claude cost per
unit of work changed by +0.02% (−0.08 to +0.12) at 1.25× and −0.15% (−0.23 to −0.07) at 2×; with the plugin
restarting daily and forgetting what it had learned, by −0.22% (−0.35 to −0.09) and −0.05% (−0.12 to +0.02). Codex
credits, one replay per scenario, rose in 11 of 12 combinations of 1×, 1.5× and 2× capacity with no restart or a
restart every 12, 24 or 48 hours, by up to 37%. The weight only seeds an account's size until its usage headers have
moved enough to measure it, so the plugin does not need it.

### Alternatives that lost

Measured with the same replay during development, against this version or its warm-cache rule:

| Alternative | Result | Decision |
| --- | --- | --- |
| No forecasting: pace and admit on observed usage only | 0.05–0.47% more quota per unit of work, 0.6% less work served under overload, 15% more Codex credits | Kept forecasting |
| Projected placement for new Claude main chats | Same cost within 0.15 points, 0.5 points less work served at 1× | Removed `claude-placement` |
| Warm-cache preference only above 50% or 80% of the best account's room, only under low weekly pressure, or only below 80% five-hour use | None recovered the overload loss; they gave back up to 0.7 points of the gain | Kept the plain rule |
| Warm-cache preference for Codex too | 14% more credits at 2× Codex capacity | Claude only |
| Keeping the plugin's own 429 handling | Identical results in every scenario: CPA already holds the account back | Removed |

## What the replay does

`sim_test.go` replays every model response recorded on the author's machine from 2026-10-04 07:36 to 2026-10-13
08:12 UTC: 53,184 Claude Code responses (main chats, subagents and workflow agents) and 59,847 Codex responses.
`testdata/extract.py` reads them from Claude Code transcripts and Codex session logs: time, chat and subagent IDs,
model, uncached input, cache reads and writes, and output. Each policy routes the same requests:

| Policy | What it is |
| --- | --- |
| Built-in | CPA's round-robin with session affinity and `session-affinity-subagents`, as configured during the recording |
| v0.1.1 | The release that ran from 2026-10-06 19:55, with independent subagent bindings |
| Subagents follow parent | v0.1.1 with subagents on their parent's binding |
| This version | Also places new Claude chats on warm caches, without its own 429 handling |

The plugin runs unmodified behind its ABI, with CPA's behavior around it: candidates exclude an account that returned
a 429 until the reset it announced (plus CPA's 1–30 s fuzz), a failed attempt retries on the remaining accounts, and
round-robin rotates per model.

- **Accounts.** Claude: two Max 20x and two Max 5x accounts with five-hour and weekly windows, at 110M and 500M units
  for Max 20x and a quarter of that for Max 5x (an earlier calibration). Weekly phase and starting usage are unknown
  and drawn per seed. Codex: Pro and Business ProLite with the twelve weekly windows the logs show, early resets
  included, at 140M and 75M units a week, estimated from stretches when one account served everything. An account
  with credits, as the logs show for Pro, keeps serving past its limit.
- **Meter.** API price ratios: cache read 0.05, five-minute write 1.25, one-hour write 2 and output 5 times uncached
  input for Claude, with Sonnet at 0.6 and Haiku at 0.2 times Opus; cache read 0.1 and output 5 for Codex.
- **Caches.** An account keeps a chat's cache for its lifetime after the chat's last request there: an hour for main
  chats, five minutes for subagents, an hour for Codex. A chat without its own cache on the account reads only the
  prefix shared by chats of the same model and kind, if one of them ran there within that lifetime, and writes the
  rest. A request starts before its recorded completion by an estimated latency, and its writes become readable when
  it completes, so parallel subagents cannot read each other's first writes.
- **Measures.** *Quota per unit of work* is what a policy spent divided by what the same requests cost as recorded;
  *work served* is the recorded cost of the requests that found an account. Each scenario runs 15 seeds, and changes
  are paired by seed with 95% t-intervals.

### Checked against the recording

Replaying each period with the routing that actually ran:

| Period | Policy | Claude quota, simulated vs recorded | New main chats cold, simulated / recorded | Subagents cold, simulated / recorded |
| --- | --- | ---: | ---: | ---: |
| 10-04 to 10-06 | Built-in | −0.6% | 20% / 34% | 15% / 22% |
| 10-06 to 10-13 | v0.1.1 | −8.2% | 38% / 67% | 17% / 60% |

Codex usage matches within 1% in both periods. The replay reproduces the built-in period's cost but finds caches
warm more often than the recording did, and it reproduces only part of the cold starts v0.1.1 caused. Real accounts
seem to lose the shared prefix more often than the model assumes, so the replay likely understates what warm
placement and following the parent save.

## Limitations

- One person's nine days, in a period with a two-day burst of very heavy use.
- Claude plan sizes, capacities and the meter are assumptions; the tables show how the results move with them.
- CPA keeps a Codex websocket on one account for all its turns. The replay picks per request, so it overstates how
  quickly any policy can move Codex work off an account that starts paying credits mid-connection.
- An unserved request is counted once; a real client would retry later.
- The shared prompt prefix is keyed by model and main chat versus subagent; real prefixes also vary with tool sets
  and project instructions.

## Running it

```sh
make sim                                          # extract testdata/work.tsv from your logs, replay once
SIM_POLICY=builtin SIM_SEEDS=15 go test -tags sim -run TestSim -count=1 .
```

Each run prints one JSON line per seed and provider. `SIM_PLANS` (for example `20x,5x`), `SIM_CLAUDE_SCALE`,
`SIM_CODEX_CAPS`, `SIM_METER`, `SIM_WEIGHTS=plan`, `SIM_RESTART_H` (hours between plugin restarts), `SIM_FROM` and
`SIM_TO` change the assumptions. The extracted traffic stays local; `testdata/work.tsv*` is ignored by git.
