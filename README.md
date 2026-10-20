# Paced Affinity

## What it does

A [CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI) plugin that routes Claude and Codex chats across
your subscription accounts. It keeps CPA's session affinity and adds quota and cache awareness: new chats go where
their prompt is already cached and allowance is available, unless another account's weekly allowance is expiring
clearly faster, and accounts at their limit are left alone, including a Codex account that would otherwise keep
running on paid credits. It uses usage data CPA already receives, without extra requests.

[How it works](docs/how-it-works.md) has the rules: warm chats stay put, new main chats are paced by weekly usage,
and a chat's subagents stay together unless Claude Code's tool search (which defers MCP tools) is on. In Codex Pro and
ProLite pools, work goes to the account that has used the least of its week, and an account that reports no credits
is held at 98% so a 429 doesn't lock it out past an early reset.

In a replay of ten days of the author's traffic on simulated accounts at 0.9× to 1.25× the assumed plan allowances,
it used 1.0–2.1% less Claude quota per unit of work (quota spent divided by what the same requests cost as recorded)
than CPA's built-in round-robin or fill-first routing (each significant at 95%), and, at the estimated Codex capacity
(weekly allowances estimated from the logs), 27% fewer paid Codex credits than round-robin and 41% fewer than
fill-first, with no Codex 429s (22–24% fewer than round-robin, still without 429s, with a plugin restart every 6 or 24
hours); that assumes CPA passes Codex's credits header, inferred from its source but not seen live. Account limits
and plans in the replay are assumed, and under this plugin the replay finds caches warmer than the real traffic did,
so it may overstate the gain. Those figures predate the rule for expiring allowance, which in
a 27-day replay with tool search on served 0.55–0.90% more work at 0.7× to 0.9× the assumed allowances, showed no
gain at 1× that holds up when the replay's openings are estimated differently, and spent 0.021% more quota per unit of
work at 2×. On the ten-day recording it had no significant effect on work served and spent 0.032% more quota at 2×
and 0.058% more at 1× with a daily plugin restart. Over the 27 days, this version with the rule used 0.7–0.9% less
Claude quota per unit of work than round-robin and 0.3–1.2% less than fill-first, served 0.7–3.3% more work than
round-robin at 0.7× to 1×, and served as much as v0.1.1 while spending 0.14–0.19% less per unit of work at five of
six capacities (not significant at 0.8×); there it paid 12% fewer Codex credits than round-robin (9–10% with
a restart every 6 or 24 hours) ([over 27 days](docs/benchmarks.md#27-days)). See [benchmarks](docs/benchmarks.md),
including the [smaller gain](docs/benchmarks.md#expiring-allowance) with openings as recorded.

## Installation

<a id="plugin-store"></a>
<a id="compatibility"></a>

Built for **CPA v8.0.27 with native plugins enabled (CGO)**. Supported platforms:
**linux/amd64, linux/arm64, darwin/arm64**. Configure your Claude/Codex accounts in CPA first.

1. Merge this into CPA's `config.yaml`, keeping your existing settings and store sources:

   ```yaml
   plugins:
     enabled: true
     dir: "~/.cli-proxy-api/plugins"
     store-sources:
       - https://raw.githubusercontent.com/Pandoks/cpa-plugin-paced-affinity/main/registry.json
     configs:
       paced-affinity:
         enabled: true
         priority: 1

   routing:
     session-affinity: true
   ```

2. Open CPA's management panel, refresh the plugin store, and install **Paced Affinity**.
3. Restart CPA. Confirm the plugin is registered and enabled.

Use the same plugin store to install future releases. Upgrading from v0.1: `shadow: true` no longer stops routing;
set `enabled: false` instead. `shadow`, `providers` and `claude-placement` are no longer read and can be deleted, and
the management status endpoint (`/plugins/paced-affinity/status`) is removed.

## Settings

<a id="configuration"></a>

The plugin has no settings of its own; `enabled` and `priority` are CPA's. Keep `routing.session-affinity: true` so
CPA's fallback routing stays sticky. Keep `model-level-cooling` false (CPA's default): the plugin relies on CPA benching the whole account after a usage-limit 429.

Optionally, set each credential's CPA `weight` to its plan size (Claude Pro 1, Max 5x 5, Max 20x 20; Codex Pro 24,
Business ProLite 10). The plugin uses it only as a first guess at the account's size, until usage headers have moved
enough to measure it. In the replay, plan weights helped Claude slightly after plugin restarts and changed Codex
credits by more than 0.1% only at 2× Codex capacity: 31% more with the plugin kept running, 4% fewer with a daily
restart ([sensitivity](docs/benchmarks.md#sensitivity)). The Codex values follow the plugin's built-in prior of
about $60 of GPT usage a week per weight unit; the replay's estimated weekly allowances put Pro and ProLite closer to
19:10.
