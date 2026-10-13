# How Paced Affinity works

**Paced Affinity** chooses which subscription account serves a request. It aims to get more work out of the same
accounts by reading prompt caches instead of rewriting them, and by assigning new work where allowance is available.
It uses signals already visible to CLIProxyAPI (CPA) and sends no requests of its own.

## The rules

1. **Keep warm chats together.** A chat stays on its account for a sliding hour, like CPA's session affinity. It moves
   only when the account is rate-limited or reports its limit reached.
2. **Start subagents and forks on their parent's account.** CPA's `session-affinity-subagents` does the same; the
   plugin has to apply it itself because CPA's selector never sees the plugin's bindings.
3. **Place new Claude chats where their prompt prefix is cached.** Among accounts with room, prefer one that served
   the same model and kind of agent within the cache lifetime.
4. **Pace new Claude main chats by observed weekly usage.** Favor the account furthest behind its elapsed-week
   spending target.
5. **Use projected placement for other work.** Resumed chats, failover and Codex combine projected headroom with
   weekly allowance likely to expire unused.
6. **Stay off accounts at their limit.** An account that reports 100% usage gets no new work while another has room.

## Keeping a warm chat in place

A warm binding takes precedence over a better score elsewhere. Moving a Claude main chat means writing its context on
the destination account: under the benchmark's meter assumptions, a one-hour cache write costs 40 times a cached
read. Claude caches are scoped to an organization, so a move cannot assume the existing cache is available.

After more than an hour of inactivity, the chat's own cache has expired and its next request is placed again. A rate
limit also triggers placement on an eligible account. The placement margins control new work; they do not move a
warm chat because another account now looks better.

## Subagents and forks

A Claude Code subagent arrives with an ID like `claude:<session>:agent:<agent-id>`, a Codex subagent with
`codex:<session>:agent:<name>`. Both use the binding of the chat at the top of their family, so a nested subagent
follows the main chat. Any other request with a parent is a fork and follows the parent. If a subagent must fail
over, the family's binding moves with it.

## Placing a chat where its prompt is cached

Every Claude Code request starts with the model's tools and the agent's system prompt. In the recorded traffic, a
chat's first request read a median of about 212k tokens of that prefix from cache whenever the account had sent it
recently, the same for every chat. On any other account the same prefix is written again: for a main chat that is a
one-hour cache write, about 40 times the cost of the read.

The plugin remembers when each account last answered each model, separately for main chats and subagents, whose
system prompts differ. When it has to place a Claude chat, it first picks as below. If that account has not served
the model within the cache lifetime (55 minutes for main chats, 4 for subagents, a margin under Claude Code's one
hour and five minutes) but another account has, the same rule is applied to the warm accounts alone, and the result
is used if it passes the margins below. Otherwise the original choice stands.

| Account | Served this model | Weekly deficit | Margins | Decision |
| --- | --- | ---: | --- | --- |
| A | 10 minutes ago | −54 percentage points | Pass | **New chat goes here** |
| B | 3 hours ago | +74 percentage points | Pass | Would win on pacing alone |

Codex is left out: it has no cache-write premium, so a cold first request costs little, and concentrating Codex chats
spent more credits in the benchmark.

## First placement of a Claude main chat

This rule applies to a Claude main chat with no retained binding and no retained successful-usage history. For each
candidate, use the elapsed fraction of its own seven-day window to set a spending target:

```text
elapsed fraction = clamp(1 − minutes until weekly reset ÷ 10080, 0, 1)
target = min(1.1 × elapsed fraction, 1)
deficit = target − observed weekly usage
```

The largest deficit wins, even if all deficits are negative. This compares fractions of each account's allowance; it
does not multiply the deficit by plan size. Equal deficits are broken by projected absolute headroom, then by
candidate order.

| Account | Weekly used | Reset in | Target | Deficit |
| --- | ---: | --- | ---: | ---: |
| **A** | **80%** | **1 day** | **94.3%** | **+14.3 percentage points** |
| B | 70% | 6 days | 15.7% | −54.3 percentage points |

**A gets the new main chat** if it passes the guards. A is behind the spending schedule for a week that ends soon. B
has more unused allowance but is early in its week.

Admission requires observed five-hour usage at or below 95%; an expired short window counts as zero. Projected weekly
usage must stay at or below 97%, unless allowance is projected to expire unused. If no candidate passes, the projected
rule below supplies the choice.

## Projected placement

Resumed chats, subagents without a live family binding, failover and Codex use this rule. For each eligible account,
estimate how much allowance would remain at its next reset if its recent burn rate continued:

```text
projected room = remaining allowance − recent burn rate × time until reset
```

Room is measured in absolute capacity units, learned from how usage headers move as tokens are served, so a Max 20x
account counts as about four times a Max 5x account at equal utilization. An idle account is treated as opening a
fresh five-hour window now. Weekly allowance also caps room using a five-hour look-ahead: a fresh short window does not
make an almost exhausted week usable. A credential's CPA `weight` seeds the capacity estimate until headers have
moved enough to measure it.

### Adding weekly urgency

Estimate the allowance that would remain unused at the weekly reset, then divide it by the time left. This makes a
small amount expiring soon more urgent than the same amount expiring days later.

```text
weekly leftover = max(0, weekly remaining − recent weekly burn rate × time until weekly reset)
urgency = weekly leftover ÷ time until weekly reset

score = room ÷ best room + weekly weight × urgency ÷ best urgency
```

The best room and urgency are the largest values among eligible candidates. A component with no positive value
contributes zero. Weekly weight phases in as projected weekly usage rises from 40% to 70% of capacity, reaching a
maximum of `2`. With ample weekly slack, room alone decides; under weekly pressure, expiring allowance can outweigh a
larger five-hour window.

The weekly margin has an exception: an account may receive new work if some weekly allowance is projected to expire
unused anyway. It does not waive a rate limit or the five-hour margin.

### Example: a tight week

Projected weekly usage is about 92%, so weekly weight is `2`. "Weekly pace" means projected total usage by the weekly
reset as a percentage of the account's allowance.

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

**B gets the new chat.** Its 5.4 units would expire in eight hours, while C's smaller leftover has two days to find
work.

## Weekly-only Codex accounts

Codex Pro's seven-day window starts with the first request after the previous window expires. For weekly-only pools,
the scheduler uses weekly urgency without a five-hour room term. An expired window is reopened first. If every active
account is on pace to use its whole allowance, the account with the most weekly room wins instead. Codex plans with
five-hour limits use the five-hour and weekly logic above.

| Account | Weekly used | Reset in | Weekly pace | Projected leftover |
| --- | ---: | --- | ---: | ---: |
| D | 40% | 5 days | 100% | 0 units |
| **E** | **30%** | **1 day** | **60%** | **4 units** |

**E gets the new chat.** Its four unused units are due to expire in a day; D's remaining allowance is already on pace
to be consumed.

## Accounts at their limit

When an account answers with a 429, CPA reads the reset from its headers and leaves the account out of the candidates
until then, so the plugin never sees it. An account can also report 100% usage on a successful response. A Codex
account with credits keeps answering past its limit and charges the credits. The plugin treats any account that
reports 100% as exhausted until its window resets: it gets no new work and warm chats leave it, while another
candidate has room.

## Signals it uses

| Signal | Purpose |
| --- | --- |
| `anthropic-ratelimit-unified-5h` / `7d` utilization and reset headers | Observe Claude window usage and reset times |
| `x-codex-primary` / `secondary` used-percent, window-minutes, reset-at headers | Identify Codex windows and observe usage |
| Token usage and model in responses | Estimate burn and capacity; remember which accounts hold each model's prompt prefix |
| Claude Code and Codex session and agent IDs | Track chat bindings and keep subagents and forks with their parent |

> [!NOTE]
> Subscription limits and their exact meters are not published. Projected room and weekly urgency are estimates from
> observed traffic, not guarantees about how many tokens an account can serve.

## Fallback

Missing headers leave less information for the estimates. Errors, panics, unknown providers and requests without a
chat identity fall back to CPA's built-in selector; keep `routing.session-affinity: true` so that fallback routing
stays sticky. The plugin logs each decision at debug level. See [benchmarks](benchmarks.md) for the replay of recorded
traffic, or return to the [installation guide](../README.md#installation).
