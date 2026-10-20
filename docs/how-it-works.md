# How Paced Affinity works

**Paced Affinity** chooses which subscription account serves a request. It aims to get more work out of the same
accounts by reading prompt caches instead of rewriting them, and by assigning new work where allowance is available.
It uses signals already visible to CLIProxyAPI (CPA) and sends no requests of its own.

## How it picks an account

A *binding* ties a chat to an account. It is live while it has been used within the last hour. For each request with
a chat identity, the plugin takes these steps in order:

1. **Use a live binding.** A request whose binding is live goes to the bound account, unless that account is
   rate-limited or reports its limit reached, or, in a weekly-only Codex pool, is held while another account can take
   the work ([weekly-only Codex accounts](#weekly-only-codex-accounts)). A main chat has its own binding. A chat's
   subagents, its *family*, share one binding separate from the chat's, or each has its own when Claude Code's tool
   search is on (see [subagents and forks](#subagents-and-forks)). A *fork*, a new chat started from a copy of another
   chat's history, shares its parent's binding. A better score elsewhere doesn't move a chat: on another account its
   whole context is written again, and a one-hour cache write costs about 40 times a cached read under the
   [benchmark's meter](benchmarks.md#what-the-replay-does).
2. **Drop accounts that can't take the work.** An account that reports 100% usage gets no new work while another has
   room, and placement only uses accounts within the [margins](#margins), so new work doesn't push an account into
   its limit. The exception is a Codex pool without five-hour windows once every account has answered: it skips the
   margins, and instead an account that reports no credits is held from 98% weekly usage
   ([weekly-only Codex accounts](#weekly-only-codex-accounts)).
3. **Pick.** A new Claude main chat goes to the account furthest behind its weekly spending target
   ([weekly pacing](#weekly-pacing-for-new-claude-main-chats)), so each account's allowance is spent before its week
   resets. Codex work in a pool without five-hour windows, once every account has answered, goes to the account that
   has used the least of its week ([weekly-only Codex accounts](#weekly-only-codex-accounts)). Everything else uses
   [projected placement](#projected-placement): the most projected headroom, plus a bonus for weekly allowance that
   would otherwise expire unused.
4. **Claude only: prefer a warm account.** If step 3's pick hasn't recently served the same model and kind of agent,
   the plugin applies step 3's rule to the accounts that have and that pass the margins. The chat then reads its
   shared prompt prefix from cache instead of writing it again (see
   [placing a chat where its prompt is cached](#placing-a-chat-where-its-prompt-is-cached)), unless step 3's pick's
   weekly allowance is expiring clearly faster than the warm account's, relative to how fast each can spend it
   ([keeping a cold pick](#keeping-a-cold-pick-whose-allowance-is-expiring)).

## Margins

Placement only uses an account that passes these margins, except in a weekly-only Codex pool, where the 98% hold
plays that role and work goes to the least-used share among accounts neither exhausted nor held
([weekly-only Codex accounts](#weekly-only-codex-accounts)). *Projected* usage is what the account would reach if its
recent burn rate, plus the new work, continued: for the five-hour window, at its reset; for the weekly window, five
hours ahead, or at the reset if that is sooner.

| Margin | Passes when | Checked by |
| --- | --- | --- |
| Five-hour, observed | The last reported five-hour usage is at most 95%; an expired window counts as 0% | Weekly pacing of a new Claude main chat |
| Five-hour, projected | Projected five-hour usage is at most 95% | Projected placement and the warm-cache rule |
| Weekly, projected | Projected weekly usage is at most 97%, or some weekly allowance is projected to expire unused anyway | Weekly pacing, projected placement and the warm-cache rule |

If no account passes, projected placement picks the account with the most projected headroom.

## Subagents and forks

A Claude Code subagent arrives with an ID like `claude:<session>:agent:<agent-id>`. The plugin takes its family, the
chat at the top, from that ID alone, so nested subagents join their siblings' family. A Codex subagent gets an ID like
`codex:<session>:agent:<name>` only when Codex sends an `agent_name`, and then joins its family the same way. Without
one, CPA gives it `codex:<thread>` with a parent, and the plugin treats it like a fork: it follows its parent's
binding. A nested Codex subagent joins the root chat's family only if Codex sends the root's session ID; the plugin
assumes it does, and so does the replay.

How a family's Claude subagents are bound depends on Claude Code's tool search (`ENABLE_TOOL_SEARCH=true`, which
defers MCP tools instead of sending them in full):

- **Tool search off**, Claude Code's default behind a proxy: every subagent prompt starts with the full set of MCP
  tools, a large prefix its siblings share. The family's subagents share one binding, separate from the chat's. The
  first is placed like new work and the rest follow it, so they read the prefix the first one cached. If that account
  must fail over, the family's binding moves with it.
- **Tool search on**: the MCP tools are deferred and the shared prefix is small, so grouping gains little and adds load
  to one account. Each Claude subagent gets its own binding and is placed like new work, where the warm-cache rule
  still prefers an account that holds the prefix.

The plugin knows tool search is on when the request's `Anthropic-Beta` header contains `advanced-tool-use-`. Claude Code
2.1.296 sends it with tool search on and omits it with tool search off, through both entrypoints of the recorded traffic
after tool search was turned on: the Agent SDK (sdk-ts) and print mode (sdk-cli). That was checked with an API key
against a local server; OAuth logins, interactive mode, workflow agents and T3 Code's own environment weren't. If the
header lacks the token (say, after Anthropic renames the beta), the plugin uses the shared binding.

Codex subagents with a family ID always share their family's binding. Neither rule puts subagents on the chat's own
account, as CPA's `session-affinity-subagents` does, because subagents don't read their chat's cache.
[Benchmarks](benchmarks.md#subagents-and-tool-search) has the measurement and compares the rules.

Any other request with a parent is a fork. A fork reads its parent's cache, so it shares its parent's binding.

## Weekly pacing for new Claude main chats

A Claude main chat is *new* if the plugin hasn't seen it get a successful response in the last 7 days; it keeps each
chat's burn history for 7 days after its last response. A new chat uses this rule even when CPA retries it on another
account after a 429. A chat the plugin remembers is *resumed* and uses projected placement instead. For each
candidate, use the elapsed fraction of its own seven-day window to set a spending target:

```text
elapsed fraction = clamp(1 − minutes until weekly reset ÷ 10080, 0, 1)
target = min(1.1 × elapsed fraction, 1)
deficit = target − observed weekly usage
```

Among accounts that pass the observed five-hour and projected weekly margins, the largest deficit wins, even if all
deficits are negative. This compares fractions of each account's allowance; it does not multiply the deficit by plan
size. Equal deficits are broken by projected absolute headroom, then by candidate order. If no account passes,
projected placement decides.

| Account | Weekly used | Reset in | Target | Deficit |
| --- | ---: | --- | ---: | ---: |
| **A** | **80%** | **1 day** | **94.3%** | **+14.3 percentage points** |
| B | 70% | 6 days | 15.7% | −54.3 percentage points |

**A gets the new main chat** if it passes the margins. A is behind the spending schedule for a week that ends soon. B
has more unused allowance but is early in its week.

## Projected placement

Claude's resumed chats (including their retries after a 429), subagents and forks use this rule, and so does Codex work
in pools with a five-hour window or with an account not yet heard from. For each account that passes the projected
margins, estimate how much allowance would remain at its next reset if its recent burn rate continued:

```text
projected room = remaining allowance − recent burn rate × time until reset
```

Room is measured in absolute capacity units, learned from how usage headers move as tokens are served, so a Max 20x
account counts as about four times a Max 5x account at equal utilization. An account whose five-hour window has closed
is treated as opening a fresh one: for Claude, a window that started at the top of the current hour. Weekly allowance
also caps room using a five-hour look-ahead: a fresh short window does not make an almost exhausted week usable. A
credential's CPA `weight` seeds the capacity estimate until headers have moved enough to measure it.

### Adding weekly urgency

Estimate the allowance that would remain unused at the weekly reset, then divide it by the time left. This makes a
small amount expiring soon more urgent than the same amount expiring days later.

```text
weekly leftover = max(0, weekly remaining − recent weekly burn rate × time until weekly reset)
urgency = weekly leftover ÷ time until weekly reset

score = room ÷ best room + urgency factor × urgency ÷ best urgency
```

The best room and urgency are the largest values among eligible candidates. A component with no positive value
contributes zero. The urgency factor rises from 0 to 2 as the candidates' average projected weekly usage goes from
40% to 70%. With ample weekly slack, room alone decides; under weekly pressure, expiring allowance can outweigh a
larger five-hour window.

### Example: a tight week

The candidates' average projected weekly usage is about 92%, so the urgency factor is 2. "Weekly pace" means
projected total usage by the weekly reset as a percentage of the account's allowance.

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

**B gets the work.** Its 5.4 units would expire in eight hours, while C's smaller leftover has two days to find work.

## Weekly-only Codex accounts

When none of the Codex accounts the plugin has seen, including those cooling down that aren't candidates for this
request, has shown a five-hour window (Pro and Business ProLite), and every candidate has answered since the plugin
started, these rules replace projected placement for all Codex work. Only a response shows a five-hour window, so
until every candidate has answered, the pool uses projected placement with the [start-up](#codex-start-up) rule, and
no account is held. Codex pools with a five-hour plan, such as Plus, and Claude are unchanged.

- **Least-used share first.** New work goes to the account that has used the smallest share of its weekly allowance;
  an expired window counts as unused, so it is reopened first. Ties go to the most allowance left. Codex windows often
  reset days early, discarding what's left; keeping usage even is meant to waste the least of it.
- **Hold an account without credits at 98%.** The plugin reads `X-Codex-Credits-Has-Credits` from each response. An
  account that reports `false`, or hasn't reported the header, gets no new work from 98% weekly usage, and its live
  chats move while another account is usable. Past 100% it would answer with a 429, and CPA v8.0.27 then keeps it out
  until the reset it announced, even if its window resets sooner.
- **When every account is held or exhausted,** work goes to an account that reports credits rather than one that
  would answer with a 429 or eat its reserve. If every account is held, live chats stay where they are.
- **Probe for early resets.** Only a response shows an early reset. So one new main chat goes to an exhausted or held
  account that can still answer without a 429 once no request has reached it for 3 hours, at most once every 3 hours
  per account: one that reports credits, or a held one below 99%. The last 1% is for the requests a held account
  still serves after each plugin restart ([start-up](#codex-start-up)). If its window has reset, the account takes
  work again; if not, the chat moves on at its next request.

<a id="codex-start-up"></a>
**Start-up.** After a restart the plugin has heard from no account, and one not yet heard from reads as unused, so
projected placement would send it every new chat. In any Codex pool, until some account shows a five-hour window, an
account not yet heard from takes one pick and is then skipped until it answers or 3 hours pass; accounts that would
answer with a 429 (exhausted and not reporting credits) are skipped too. If that would leave no account, every
candidate stays eligible, so while every account is still waiting for its first answer, one can take more than one
pick.

Until every account has answered after a restart, the hold doesn't apply: a held account keeps serving its live chats
and takes new chats for those first seconds, while another account's first request is in flight. That is what the
last 1% of the reserve is for, and with restarts every few hours it can run out
([benchmarks](benchmarks.md#codex-restarts)). Measured in the 27-day replay at 1× with 3- or 6-hourly restarts: after
the restart at 10-17 06:19:10, ProLite, held, served requests at 06:19:10 and 06:19:16 before Pro's first answer at
06:19:20; most restarts cost a held account one or two requests.

Measured in the replay of the author's Pro and ProLite traffic with the plugin kept running, this paid 6–43% fewer
Codex credits than the previous rule at 1× to 2× the estimated capacity, with no Codex 429s. With a plugin restart
every 24 or 6 hours it also had no Codex 429s and paid 10–13% fewer credits at 1×, but 1–28% more at 2×
([benchmarks](benchmarks.md#codex)). The 98% reserve and 3-hour interval were tuned on that same traffic, and the gain
depends on windows resetting early. That CPA passes the credits header is inferred from its source and the Codex
client, not seen in live traffic. Without the header every account counts as not paying: it is held from 98% and
never preferred or probed for credits; least-used-share placement, the start-up rule and probes of held accounts
below 99% still act.

## Placing a chat where its prompt is cached

Every Claude Code request starts with an *opening*: the tools and the agent's system prompt. Chats from the same
client and version, with the same model, kind of agent and MCP tools, send the same opening, so a chat's first request
reads it from cache on an account that sent it recently. On any other account the opening is written again, as a
one-hour cache write for a main chat. Openings are large when Claude Code sends every MCP tool in full and small with
tool search on; [benchmarks](benchmarks.md#what-the-replay-does) gives the recorded sizes.

The plugin remembers when each account last answered each model, separately for main chats and subagents, whose
system prompts differ. Its key is the model name the client requested plus the kind of agent: CPA's `requested_model`
when it picks, and the usage record's alias when the response comes back. So the key still matches when an
`oauth-model-alias`, a model-router plugin or `auto` rewrites the model on its way upstream. The plugin can't see the
tool set or the client version.

The rule applies to any Claude pick without a live binding: a new or resumed chat, a subagent without a live binding,
a fork, and a retry after a 429. If step 3's pick has served the model within the cache lifetime (55 minutes for main
chats, 4 for subagents, a margin under Claude Code's one hour and five minutes), it stands. Otherwise the plugin
applies step 3's rule to the accounts that have served it and pass the projected margins; if there are none, the
original pick stands. It also stands if its weekly allowance is expiring clearly faster than the warm account's
([below](#keeping-a-cold-pick-whose-allowance-is-expiring)).

| Account | Served this model | Weekly used | Reset in | Weekly deficit | Expiry urgency | Margins | Decision |
| --- | --- | ---: | --- | ---: | ---: | --- | --- |
| A | 10 minutes ago | 70% | 6 days | −54 percentage points | 0.06 | Pass | The warm choice |
| **B** | **3 hours ago** | **20%** | **1 day** | **+74 percentage points** | **1.00** | **Pass** | **New chat goes here** |

Pacing picks B, and the warm step would move the chat to A, the only warm account. But with no recent burn, B's 80% would
expire in a day, and using it would take B's fastest pace for the whole day, while A has six days for its 30%. B's
[expiry urgency](#keeping-a-cold-pick-whose-allowance-is-expiring) (here with the assumed r = 1/6) leads A's by 0.94,
at least 0.3, so B keeps the chat. Without that rule, A would get it.

Codex is left out. It reports no cache writes, so whether it charges more for them is unknown, and in the replay a
Codex warm rule spent more credits when the Codex accounts had room
([benchmarks](benchmarks.md#alternatives-tested-and-rejected)).

### Keeping a cold pick whose allowance is expiring

Moving work to a warm account saves writing the opening again, but it can leave the cold pick's weekly allowance to
expire unused. So the plugin compares the two accounts' *expiry urgency*: the share of its fastest pace, a full
five-hour window every five hours, that an account would have to keep up until its weekly reset to use the allowance
projected to expire there. If the cold pick's urgency is at least 0.3 above the warm account's, the cold pick stands.

```text
weekly leftover = max(0, 1 − weekly usage − recent weekly burn rate × time until weekly reset)
r = a full five-hour window as a share of the weekly allowance
expiry urgency = weekly leftover ÷ (time until weekly reset ÷ 5 hours × r)
```

Weekly leftover is a share of the account's weekly allowance. The plugin learns r from how the five-hour and weekly
usage headers move together; until both have moved enough, it assumes 1/6. An account's urgency is 0 until it has
reported its weekly reset, so the rule doesn't act on accounts the plugin hasn't heard from since it started.

With r = 0.22 (a five-hour window of 110M units in a 500M-unit week, as the
[replay](benchmarks.md#what-the-replay-does) assumes), and a warm account with half its week projected unused and six
days to its reset:

| Cold pick's projected leftover | Its weekly reset in | Its urgency | Warm account's | Difference | Decision |
| ---: | --- | ---: | ---: | ---: | --- |
| 50% | 6 days | 0.08 | 0.08 | 0.00 | Move to the warm account |
| 50% | 2 days | 0.24 | 0.08 | 0.16 | Move to the warm account |
| **50%** | **1 day** | **0.47** | **0.08** | **0.39** | **Keep the cold pick** |

Half a week left with two days to go needs about a quarter of the account's fastest pace; with one day to go it needs
about half, and that pick keeps the work. Before the plugin has learned r, with its assumed 1/6, the same urgencies
are 0.10, 0.31 and 0.63, and the decisions are the same. The 0.3 threshold was chosen on the ten-day replay before any
27-day run. Against keeping any cold pick whose urgency is at least 0.5, the ten-day replay doesn't favour it on
quota; the 27-day replay favours it on work served ([benchmarks](benchmarks.md#alternatives-tested-and-rejected)).
In the 27-day replay with tool search on the rule served more work only when the accounts ran short: 0.55–0.90% more
at 0.7× to 0.9× the assumed allowances, and no robust gain at 1× ([benchmarks](benchmarks.md#expiring-allowance)).

## Accounts at their limit

With CPA's default `model-level-cooling: false`, after a 429 CPA itself benches the account until its reset, so the plugin never sees it. An account can also report
100% usage on a successful response. A Codex account with credits keeps answering past its limit and charges the
credits. The plugin treats any account that reports 100% as exhausted until its window resets: it gets no new work and
warm chats leave it, while another candidate has room. In a weekly-only Codex pool, an account that reports no
credits is held from 98% instead, so it normally doesn't reach the 429 ([above](#weekly-only-codex-accounts)).
After each plugin restart a held account still serves requests until every account has answered, usually one or two
([start-up](#codex-start-up)), so restarts every few hours can use up the reserve and reach one
([benchmarks](benchmarks.md#codex-restarts)).

## Signals it uses

| Signal | Purpose |
| --- | --- |
| `anthropic-ratelimit-unified-5h` / `7d` utilization and reset headers | Observe Claude window usage and reset times |
| `x-codex-primary` / `secondary` used-percent, window-minutes, reset-at headers | Identify Codex windows and observe usage |
| `x-codex-credits-has-credits` header | Tell which weekly-only Codex accounts pay with credits: those aren't held short of their limit, and are preferred and probed past it |
| Token usage in responses; the model the client requested | Estimate burn and capacity; remember which accounts hold each model's prompt prefix |
| Claude Code and Codex session and agent IDs | Track bindings: a chat's, its subagents', and a fork's on its parent's |
| `Anthropic-Beta` request header (`advanced-tool-use-`) | Tell whether Claude Code's tool search is on, which decides how Claude subagents are bound |

> [!NOTE]
> Subscription limits and their exact meters are not published. Projected room and weekly urgency are estimates from
> observed traffic, not guarantees about how many tokens an account can serve.

## Fallback

Missing headers leave less information for the estimates. Errors, panics, unknown providers and requests without a
chat identity fall back to CPA's built-in selector; keep `routing.session-affinity: true` so that fallback routing
stays sticky. The plugin logs each decision at debug level.
