# Paced Affinity

## What it does

A [CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI) plugin that routes Claude and Codex chats across
your subscription accounts. It keeps CPA's session affinity and adds quota and cache awareness: new chats go where
their prompt is already cached and allowance is available, and accounts at their limit are left alone, including a
Codex account that would otherwise keep running on paid credits. It uses usage data CPA already receives, without
extra requests.

[How it works](docs/how-it-works.md) has the rules: warm chats stay put, new main chats are paced by weekly usage,
and a chat's subagents stay together unless Claude Code's tool search (which defers MCP tools) is on.

In a replay of ten days of the author's traffic on simulated accounts at 0.9× to 1.25× the assumed plan allowances,
it used 1.0–2.1% less Claude quota per unit of work (quota spent divided by what the same requests cost as recorded)
than CPA's built-in round-robin or fill-first routing (each significant at 95%), and, at the estimated Codex capacity
(weekly allowances estimated from the logs), 14% fewer paid Codex credits than round-robin and 30% fewer than
fill-first. Account limits and plans in the replay are assumed, and under this plugin the replay finds caches warmer
than the real traffic did, so it may overstate the gain. See [benchmarks](docs/benchmarks.md).

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
enough to measure it. In the replay, plan weights helped Claude slightly after plugin restarts and moved Codex credits
both ways ([sensitivity](docs/benchmarks.md#sensitivity)). The Codex values follow the plugin's built-in prior of
about $60 of GPT usage a week per weight unit; the replay's estimated weekly allowances put Pro and ProLite closer to
19:10.
