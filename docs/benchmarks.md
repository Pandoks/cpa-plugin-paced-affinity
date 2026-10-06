# Benchmarks

**Paced Affinity** was evaluated in simulation and with a real CLIProxyAPI process routing to
stubbed accounts. These are controlled experiments, not measurements from live subscriptions.

The follow-up section measures the current `observed-weekly` default, which changes only the first
placement of Claude main chats. All later historical sections describe the original
`claude-placement: projected` policy. The retained Python decision replays select `projected`
explicitly; separate ABI fixtures protect the observed-weekly decision contract.

> [!IMPORTANT]
> Subscription limits and their token accounting are not published. These results show what the
> scheduler did under the tested assumptions; they do not promise a particular real-account gain.

## Current observed-weekly follow-up

A follow-up replay uses seven days of recorded chat metadata tiled into three weeks. It compares
matched seeds under the same workload, account capacities, quota meters, cache rules and retry
behavior. Raw served tokens include input, cached prompt reads/writes and output; they are not a
measure of useful output alone. The larger pool assumes four Claude and three Codex accounts; the
recorded smaller pool has three Claude and two Codex accounts. Plan capacities and subscription
metering remain modeled assumptions. Captured metadata and private local audit artifacts are not
included in this repository.

### Updated rule against the original

| Pool | Mean token change relative to original projected rule | Unadjusted 95% interval |
| --- | ---: | ---: |
| Larger, 4 Claude + 3 Codex | +0.142% | −0.040% to +0.323% |
| Smaller, 3 Claude + 2 Codex | +0.238% | −0.206% to +0.683% |
| Equal-weight mean across 39 sensitivity scenarios | +0.062% | −0.016% to +0.140% |

Across those 39 scenarios, the updated rule has a higher mean in 25, a lower mean in 13 and an
equal mean in one. Five intervals are wholly positive, two wholly negative and 32 cross zero.
This supports a small average improvement in this replay, with unresolved uncertainty; it does
not establish a gain for every workload. The production change affects previously unseen Claude
main chats only; it retains the original weekly projection guard, warm affinity, Codex, subagent,
fork, resumed-chat and failover paths. `claude-placement: projected` restores the original rule.

### Fresh usage checks before each chat

This comparison models someone checking every account's dashboard before each new human chat,
choosing the most remaining quota and keeping the chosen account until it actually returns a
429 or becomes ineligible. It checks again only when a replacement is needed. Internal agents
inherit the human chat's account. It does **not** choose anew on every model request.

Four rules were fixed before inspecting performance: maximize either the minimum or the mean of
remaining five-hour/weekly percentages, with visible forks either continuing the parent or
starting a separate chat. Weekly-only Codex accounts use their weekly percentage. Dashboard reads
are free and instantaneous but retain modeled 30-second reporting lag and rounding; they are not
an exact-meter oracle. Unlike Paced, these rules do not weight absolute plan capacity or account
for weekly pace, reset urgency or predicted usage. Unavailable readings are treated as unknown,
with zero observed usage; this is a modeling limitation.

All 960 runs completed: four rules × eight scenario assumptions × 30 matched seeds. Thirty
duplicate controls matched all 49 saved output fields and were excluded from new evidence.
The table reports **manual relative to the current observed-weekly plugin**, so negatives mean
manual switching serves fewer tokens:

| Pool | Per-chat quota score, forks continue parent | Mean manual token change | Unadjusted 95% interval |
| --- | --- | ---: | ---: |
| Larger, 4 Claude + 3 Codex | Minimum remaining percentage | −2.820% | −3.260% to −2.380% |
| Larger, 4 Claude + 3 Codex | Mean remaining percentage | −3.017% | −3.414% to −2.620% |
| Smaller, 3 Claude + 2 Codex | Minimum remaining percentage | −1.801% | −2.273% to −1.330% |
| Smaller, 3 Claude + 2 Codex | Mean remaining percentage | −1.365% | −1.841% to −0.890% |

Including the separate-fork variants, manual rules serve 2.82–3.36% fewer tokens in the larger
pool and 1.14–1.80% fewer in the smaller pool. All four clearly lose in seven of eight scenario
assumptions; the eighth has an unresolved difference. The larger-pool Codex-only difference is
unresolved; smaller-pool Codex favors the plugin. Manual rules rebuild less cache in this replay
but leave more weekly quota unused. Better allocation around resets is a plausible explanation,
not a separately isolated causal result.

Means average 30 paired-seed percentage ratios; two-sided intervals use Student-t with 29 degrees
of freedom. Aggregate intervals first average correlated scenario assumptions within each seed.
These intervals describe seed variation, not calibration error, uncertain capacities or the
representativeness of one recorded history. Neither these manual choices nor live performance
were observed. This comparison does not establish a universally best router. The older
fill-one-account hand-switching policy below is a different comparator.

## Methodology

The baseline is CLIProxyAPI's built-in round-robin with session affinity: a one-hour binding, with
subagents inheriting the parent's account. It represents a well-configured default.

| Test bed | Coverage | What it checks |
| --- | --- | --- |
| Simulation | 112 situations × 30 runs | Tokens served, cache movement, and allowance use across workloads |
| Heavy mixed-plan simulation | 16 situations × 30 runs | Claude Max 20x + Max 5x + Max 5x, alongside Codex Pro + Business ProLite |
| Real CLIProxyAPI v8.0.12 with stubbed accounts | Eight paired runs per case; compressed time | Plugin loading, routing, failures, headers, and cache movement through the proxy |
| Decision replay | 128,000 picks | Agreement between the Go implementation and the Python reference |

The main simulation covers Claude pools of two to five accounts, mixed plan sizes, Codex Pro and
Plus, light through overloaded demand, five-hour and weekly bottlenecks, staggered and aligned
resets, long contexts, subagent swarms, and uncertainty in token accounting.

The mixed-plan suite regularly exhausts both five-hour and weekly allowances. Its cache-movement
counts cover three-week runs. The proxy test bed uses real CLIProxyAPI against simulated Claude and
Codex endpoints, with time compressed to exercise resets. Neither test bed uses real subscription
accounts.

Token gains below are relative to the baseline, unless another comparison is named. Counts of
situations are separate from counts of repeated runs. Lower cache waste, forced moves, and expired
allowance are better.

## Overall simulation results

| Metric | Paced Affinity | Baseline |
| --- | ---: | ---: |
| Tokens served, all situations | +0.51% | Reference |
| Tokens served, when accounts run out | +0.66% | Reference |
| Tokens served, Codex | +0.96% | Reference |
| Situations with more / fewer / equal tokens | 45 / 0 / 67 | Reference |
| Forced warm-cache moves per week | 9.4 | 12.9 |
| Usage spent rebuilding caches | 1.61% | 2.07% |
| Claude weekly allowance expiring unused | 0.98% | 1.42% |

The gain is modest because session affinity already does much of the work. The plugin improves
where new chats land and how accounts approach resets, while keeping warm chats in place.

### Gain by load

| Provider | Workload | Token gain |
| --- | --- | ---: |
| Claude | Five-hour windows maxed, 1.1× demand | +0.52% |
| Claude | Five-hour windows heavily maxed, 1.6× demand | +1.54% |
| Claude | Weekly demand right at capacity | +0.79% |
| Claude | Weekly demand at 1.4× capacity | +0.37% |
| Claude | Weekly saturated | +0.15% |
| Claude | Both five-hour and weekly limits maxed, 1.5–2.5× demand | +0.12% |
| Codex | At capacity | +1.50% |
| Codex | At 1.4× capacity | +1.06% |
| Codex | Saturated | +0.40% |

The largest gains appear near capacity or when five-hour windows constrain throughput. Far beyond
all limits, every strategy drains every account. The +0.12% Claude result when both limits are
maxed is within noise; routing cannot create more allowance.

### Heavy mixed plans

| Metric | Paced Affinity | Baseline |
| --- | ---: | ---: |
| Tokens served, overall | +0.64% ±0.11 | Reference |
| Tokens served, Claude | +0.55% | Reference |
| Tokens served, Codex | +0.79% | Reference |
| Situations with more / fewer / equal tokens | 10 / 0 / 6 | Reference |
| Forced warm-cache moves per three-week run | 36.2 | 43.8 |
| Usage spent rebuilding caches | 1.51% | 1.80% |
| Weekly allowance expiring unused | 1.09% | 1.80% |

The reported ±0.11 has no specified uncertainty method; it should not be read as a confidence
interval. In this historical suite, Paced Affinity served 2.7% more tokens than the fill-one-account
hand-switching rule, which moves all chats together, and approximately 2.7% more than fill-first.
The best tested built-in configuration, weighted
round-robin with independent subagents, improved on the baseline by only 0.16%.

For scale, a hypothetical workload serving about eight billion tokens per week would gain about
50 million tokens per week at +0.64%. This is arithmetic, not a measured production workload.

## Other routing policies

These are simulated policies, including built-in-style strategies. The table does not imply that
each policy is a separately selectable CLIProxyAPI feature. Comparisons are against the same
built-in round-robin plus session-affinity baseline.

| Policy | Token change vs. baseline |
| --- | ---: |
| Paced Affinity | +0.51% |
| Same decision rules with perfect information | +0.49% |
| Weighted round-robin by plan size | +0.04% |
| Pacing without weekly urgency | −0.25% |
| Switching by hand: fill one account, then move everything | −1.47% |
| Soonest-reset-first | −1.52% |
| Fill-first | −1.76% |
| No stickiness: split requests evenly | −9.68% |

The perfect-information variant uses the same decision rules, so it is not an oracle for the
best possible scheduling policy. Its +0.49% result was no better than the measured-input version.

### What lost and why

The following explanations describe the scheduling tradeoffs behind these tested policies; the
aggregate results do not isolate a separate causal effect for each tradeoff.

- **Fill-first** concentrates chats on one account until it hits a limit. Moving those warm chats
  then consumes allowance rebuilding caches on another account.
- **Soonest-reset-first** considers the clock without enough regard for available capacity or burn
  rate. An early reset does not necessarily make an account a good home for another chat.
- **No stickiness** repeatedly changes accounts. Claude caches are per organization, so spreading a
  warm chat across accounts can turn cheap reads into expensive cache writes.
- **Switching by hand** moves all chats when the current account fills, causing a wave of warm-cache
  rebuilds rather than placing new chats before the account reaches its limit.
- **Pacing without weekly urgency** can leave weekly allowance unused at reset even when its
  five-hour placement decisions look reasonable.

## Real proxy, stubbed accounts

CLIProxyAPI v8.0.12 loaded the plugin and the plugin routed every request in this test bed. The
accounts and provider responses were stubbed. Each case used eight paired runs with compressed
time, so these results validate integration under controlled conditions.

| Case | Result vs. baseline |
| --- | --- |
| Three Claude accounts | +2.0% finished requests; −65% failed client attempts |
| Claude Max 20x + Max 5x + Max 5x | −36% forced warm moves; cache-rebuild waste 3.6% → 2.1% |
| Subagent-heavy Claude workload | −53% forced warm moves; cache-rebuild waste 2.6% → 0.9% |
| Codex weekly expiry cases | Weekly allowance expiring unused 5.3–7.9% → ≤0.5% |
| Saturated weekly workload | Tie |
| Go implementation vs. Python reference | Identical decisions on 128,000 replayed picks |

Finished requests and failed client attempts are different measures from simulated token
throughput; their percentages should not be combined into a single gain.

The harness caught a bug in an earlier version: a strict 3% weekly safety margin stranded the last
≤3% of allowance in saturated weeks. The expiring-allowance waiver lets an account use that margin
when allowance would otherwise expire unused. Both test beds confirmed the fix.

## Meter assumptions and sensitivity

The experiments use API price ratios as a proxy for subscription usage. These ratios are model
assumptions, not a claim that subscriptions meter tokens this way.

| Token category | Claude Opus 5.5 | Codex |
| --- | ---: | ---: |
| Ordinary input | 1× | 1× |
| Cached input read | 0.05× | 0.1× |
| One-hour cache write | 2× | No write premium |
| Five-minute cache write | 1.25× | No write premium |
| Output | 5× | 5× |

For Claude main chats, these assumptions make a one-hour cache write about 40 times as costly as a
cached read. The subscription-meter uncertainty is why sensitivity testing matters.

Sensitivity runs varied cache reads to 0.10× and 0.25×, output to 3× and 8×, and removed the cache
write premium. Paced Affinity beat the baseline in all 16 sensitivity runs.

## Caveats

- These results come from simulation and stubbed provider accounts, not live subscriptions.
- Real subscription accounting, header availability, workload mix, and provider behavior may differ.
- Time compression tests reset behavior but does not reproduce every production timing effect.
- The baseline already has one-hour affinity. Comparing with an unconfigured or nonsticky setup
  would overstate the benefit over a well-configured default.
- The reported aggregates do not provide a confidence interval or prove that every future workload
  will improve. The heaviest dual-limit result is within noise.
- Capacity remains fixed. With light demand there may be nothing to recover; with overwhelming
  demand there may be no unused allowance left for better routing to find.

See [how it works](how-it-works.md) for the placement rules and worked examples, or return to the
[README](../README.md) for installation and configuration.
