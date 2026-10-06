# How Paced Affinity works

**Paced Affinity** chooses which subscription account serves a request. It aims to get more useful
tokens from the same accounts by keeping warm caches in place and assigning new work where allowance
is available. It uses signals already visible to CLIProxyAPI (CPA) and sends no requests of its own.

## The six rules

1. **Keep warm chats together.** A chat stays on its account for a sliding hour, unless that account
   is rate-limited. Each request refreshes the binding's lifetime.
2. **Pace new Claude main chats by observed weekly usage.** Favor the account furthest behind its
   elapsed-week spending target. Use the latest observed five-hour usage for admission.
3. **Leave a margin.** The default new-Claude rule uses a 95% observed five-hour margin and the
   existing 97% projected weekly margin, with the weekly expiry waiver below. Other placements
   retain the original projected five-hour margin.
4. **Keep projected placement for other work.** Resumed chats with retained history, subagents,
   failover and Codex still combine projected absolute room with allowance likely to expire unused.
5. **Handle weekly-only Codex plans separately.** Prefer expiring allowance, reopen expired windows
   first, and use remaining weekly room when no account has allowance projected to expire unused.
6. **Separate subagents; preserve forks.** Subagents get their own binding because they do not read
   the parent's cache. Forks follow the parent.

## Keeping a warm chat in place

A warm binding takes precedence over a better score elsewhere. Moving a Claude main chat means
writing its context on the destination account: under the benchmark's meter assumptions, a one-hour
cache write costs roughly 40 times a cached read. Claude caches are scoped to an organization, so a
move cannot assume the existing cache is available.

After more than an hour of inactivity, the next request is placed again. A rate limit also triggers
placement on an eligible account. The margin controls admission of new chats; it does not move an
existing warm chat merely because another account now looks better.

## First placement of a Claude main chat

The default `claude-placement: observed-weekly` policy changes only a Claude main chat with no
retained binding and no retained successful-usage history. All other placement paths keep the
projected policy below. Warm bindings take precedence in either mode.

For each candidate, use the elapsed fraction of its own seven-day window to set a spending target:

```text
elapsed fraction = clamp(1 − minutes until weekly reset ÷ 10080, 0, 1)
target = min(1.1 × elapsed fraction, 1)
deficit = target − observed weekly usage
```

The largest deficit wins, even if all deficits are negative. This compares fractions of each
account's allowance; it does not multiply the deficit by plan size. Equal deficits are broken by
the original projected absolute headroom, then by candidate order.

| Account | Weekly used | Reset in | Target | Deficit |
| --- | ---: | --- | ---: | ---: |
| **A** | **80%** | **1 day** | **94.3%** | **+14.3 percentage points** |
| B | 70% | 6 days | 15.7% | −54.3 percentage points |

**A gets the new main chat** if it passes the guards. A is behind the spending schedule for a week
that ends soon. B has more unused allowance but is early in its week.

Admission requires observed five-hour usage at or below 95%; an expired short window counts as
zero. The existing projected weekly usage must still be at or below 97%, unless allowance is
projected to expire unused. Known exhausted accounts are excluded while a live candidate exists.
If no candidate passes these guards, the original projected fallback supplies the choice.

Observed usage can accept work that the recent-burn forecast would reject after a short burst. It
can also admit too much simultaneous work before another response updates the headers. This rule
does not reserve capacity, guarantee room for a whole chat, poll providers or send extra requests.
Weekly projections, capacity learning and session burn estimates remain in use.

Set `claude-placement: projected` to restore the original rule for these new main chats. The
management status response reports `claude_placement`. A binding expires after one idle hour;
successful session history remains for seven idle days, so an idle root with retained history still
uses projected placement.

## Projected placement with five-hour limits

This is the original policy, preserved for Codex and other placement paths in both modes. It also
handles all new Claude main chats when `claude-placement: projected` is selected. The Python
`testdata/reference/winner.py` contract and its replay fixtures specify this policy.

For each eligible account, estimate how much allowance would remain at its next reset if its recent
burn rate continued:

```text
projected room = remaining allowance − recent burn rate × time until reset
```

Room is measured in absolute capacity units, not just percentages. A Max 20x account therefore has
about four times a Max 5x account's capacity at equal utilization. An idle account is treated as
opening a fresh five-hour window now. Weekly allowance also caps room using a five-hour look-ahead:
a fresh short window does not make an almost exhausted week usable.

The initial capacity estimate comes from the credential's existing CPA `weight` field. For example,
use `20` for Max 20x, `5` for Max 5x, and `1` for Pro. Headers and observed token usage refine that
estimate. These units describe relative capacity, not a published token entitlement.

### Adding weekly urgency

Estimate the allowance that would remain unused at the weekly reset, then divide it by the time
left. This makes a small amount expiring soon more urgent than the same amount expiring days later.

```text
weekly leftover = max(0, weekly remaining − recent weekly burn rate × time until weekly reset)
urgency = weekly leftover ÷ time until weekly reset

score = room ÷ best room + weekly weight × urgency ÷ best urgency
```

The best room and urgency are the largest values among eligible candidates. A component with no
positive value contributes zero. Weekly weight phases in as projected weekly usage rises from 40%
to 70% of capacity, reaching a maximum of `2`. With ample weekly slack, room alone decides; under
weekly pressure, expiring allowance can outweigh a larger five-hour window.

The weekly margin has an exception: an account may receive new work if some weekly allowance is
projected to expire unused anyway. This avoids stranding the last few percent of an expiring week.
It does not waive a rate limit or the five-hour margin.

### Monday: plenty of weekly slack

Accounts A, B, and C have these relative allowances:

| Account | Plan | Five-hour allowance | Weekly allowance |
| --- | --- | ---: | ---: |
| A | Max 20x | 20 units | 120 units |
| B | Max 5x | 5 units | 30 units |
| C | Max 5x | 5 units | 30 units |

Projected weekly usage is about 37%, so weekly weight is zero. Only five-hour room contributes.

| Account | Used now | Reset in | Projected use at reset | Room | Score |
| --- | ---: | --- | ---: | ---: | ---: |
| A | 70% | 1 hour | 90% | 2.0 | 0.40 |
| **B** | **Idle** | **Fresh window opens now** | **0%** | **5.0** | **1.00** |
| C | 40% | 4 hours | 80% | 1.0 | 0.20 |

**B gets the new chat.** A has the largest plan, but its current burn leaves less projected room.

### Thursday: a tight week

Projected weekly usage is about 92%, so weekly weight is `2`. Here, "weekly pace" means projected
total usage by the weekly reset as a percentage of the account's allowance.

| Account | Weekly used | Weekly reset in | Weekly pace | Projected leftover | Five-hour room |
| --- | ---: | --- | ---: | ---: | ---: |
| A | 60% | 3 days | 100% | 0 units | 6.0 |
| **B** | **70%** | **8 hours** | **82%** | **5.4 units** | **2.0** |
| C | 50% | 2 days | 95% | 1.5 units | 3.0 |

| Account | Normalized room | Normalized urgency | Score |
| --- | ---: | ---: | ---: |
| A | 1.00 | 0.00 | 1.00 |
| **B** | **0.33** | **1.00** | **0.33 + 2 × 1.00 = 2.33** |
| C | 0.50 | ≈0.05 | ≈0.59 |

**B gets the new chat.** Its 5.4 units would expire in eight hours, while C's smaller leftover has
two days to find work. C's score uses unrounded urgency; displayed inputs are rounded.

### The weekly wall

| Account | Five-hour state | Weekly used | Weekly burn | Weekly reset in | Decision |
| --- | --- | ---: | --- | --- | --- |
| A | Fresh window | 95% | 1 percentage point/hour | 2 days | Skip for new chats |

A would consume its entire weekly allowance within five hours. The fresh short window cannot
provide usable room past that weekly wall, so the scheduler does not place a new chat there.

### The expiring-allowance waiver

| Account | Weekly used | Weekly reset in | Projected extra use | Expiring leftover | Decision |
| --- | ---: | --- | --- | --- | --- |
| B | 98% | 3 hours | ≈0.5 percentage points | ≈1.5% | Allow despite the 97% margin |
| B | 98% | 3 days | Same recent burn rate | None | Skip for new chats |

The first case has allowance that would otherwise be discarded shortly. At the same burn rate over
three days, that leftover disappears, so the normal weekly margin applies.

## Weekly-only Codex accounts

Codex Pro's seven-day window starts with the first request after the previous window expires. For
weekly-only pools, the scheduler uses weekly urgency without a five-hour room term. An expired
window is reopened first. If every active account is on pace to use its whole allowance, the account
with the most weekly room wins instead.

Codex plans with five-hour limits, including applicable Plus and Business accounts, use the
five-hour and weekly logic above. Window headers determine which limits are present.

### Codex: spend the allowance most likely to expire

| Account | Plan | Weekly allowance | Used | Reset in | Weekly pace | Projected leftover |
| --- | --- | ---: | ---: | --- | ---: | ---: |
| D | Pro | 24 units | 40% | 5 days | 100% | 0 units |
| **E** | **Business ProLite** | **10 units** | **30%** | **1 day** | **60%** | **4 units** |

**E gets the new chat.** Its four unused units are due to expire in a day. D's larger remaining
allowance is already on pace to be consumed.

| Changed condition | Decision |
| --- | --- |
| E's seven-day window has already expired | Send the next new chat to E to restart its clock |
| Both accounts are on pace to consume all allowance | Choose the account with the most weekly room |

## Signals it uses

| Signal | Purpose |
| --- | --- |
| `anthropic-ratelimit-unified-5h` / `7d` utilization and reset headers | Observe Claude window usage and reset times |
| `x-codex-primary` / `secondary` used-percent, window-minutes, reset-at headers | Identify Codex windows and observe usage |
| Token usage in responses | Estimate recent burn and refine capacity estimates |
| Claude Code and Codex session and agent IDs | Track chat bindings, independent subagents, and forks |
| Existing CPA credential `weight` | Supply the initial relative capacity estimate |

> [!NOTE]
> Subscription limits and their exact meters are not published. Projected room and weekly urgency
> are estimates from observed traffic, not guarantees about how many tokens an account can serve.

## Fallback and shadow mode

Missing headers leave less information for the estimates; the credential's plan weight supplies an
initial capacity prior. Errors, panics, and unknown providers fall back to CPA's built-in selector.
Keep `routing.session-affinity: true` so that fallback routing preserves chat affinity too.

Set `shadow: true` to log the plugin's decisions while CPA's built-in selector continues routing.
This lets you inspect the proposed choices before enabling them.

The historical benchmark benefits describe the projected policy, from simulation and a real CPA
process using stubbed accounts, not live subscriptions. They do not establish live gains for the
new observed-weekly default. See [benchmarks](benchmarks.md) for methods, results, and limitations, or
return to the [installation guide](../README.md#installation).
