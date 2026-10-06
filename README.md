# Paced Affinity

## What it does

A [CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI) plugin that routes Claude and
Codex chats across your subscription accounts. It keeps warm chats on the same account and
balances five-hour and weekly quotas. It uses usage data CPA already receives, without extra requests.

## Installation

<a id="plugin-store"></a>
<a id="compatibility"></a>

Built for **CPA v8.0.12 with native plugins enabled (CGO)**. Supported platforms:
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
         shadow: false
         providers: [claude, codex]
         claude-placement: observed-weekly

   routing:
     session-affinity: true
   ```

2. Open CPA's management panel, refresh the plugin store, and install **Paced Affinity v0.1.1**.
3. Restart CPA. Confirm the plugin is registered and enabled, with `shadow: false`.

Use the same plugin store to install future releases.

## How it works

- **Warm chats:** keep their account for one hour after the latest request; move when the account
  becomes unavailable or rate-limited.
- **New Claude main chats:** choose their first account using `claude-placement`:

  | Mode | Placement rule |
  | --- | --- |
  | **`observed-weekly` (default)** | Check observed five-hour usage and projected weekly usage, then favor the account furthest behind its elapsed-week spending target. |
  | `projected` | Forecast five-hour and weekly usage; favor projected remaining capacity and weekly allowance at risk of expiring unused. |

- **Codex, resumed Claude chats, subagents and failover:** use projected placement in either mode.
  Weekly-only Codex accounts prioritize allowance at risk of expiring unused.
- **Subagents** get independent bindings; **forks** follow their parent.
- Unsupported providers, missing chat identity or plugin errors fall back to CPA's selector.

Both modes keep warm bindings and projected weekly checks. If no account passes the
`observed-weekly` checks, the original projected rule supplies the fallback.

## Settings

<a id="configuration"></a>

Unprefixed settings below belong under `plugins.configs.paced-affinity`.
The values shown match the installation example.

| Setting | Value shown | What it does |
| --- | --- | --- |
| `plugins.enabled` | `true` | Enables CPA's plugin system. |
| `plugins.dir` | `~/.cli-proxy-api/plugins` | Where CPA installs and loads plugin libraries. |
| `plugins.store-sources` | Registry URL above | Adds this plugin to CPA's store catalog. |
| `enabled` | `true` | Enables Paced Affinity; `false` disables it. |
| `priority` | `1` | Order among scheduler plugins; higher numbers run first. |
| `shadow` | `false` | `false`: route requests. `true`: compute/debug-log choices while CPA routes. |
| `providers` | `[claude, codex]` | Providers this plugin handles. |
| `claude-placement` | `observed-weekly` | New Claude main-chat placement: `observed-weekly` or `projected`. |
| `routing.session-affinity` | `true` | Keeps CPA's fallback routing sticky. |

Each credential's CPA **`weight`** seeds its relative capacity: for example, Claude Pro `1`,
Max 5x `5`, Max 20x `20`. Use relative plan sizes for Codex. Observed usage refines these estimates.
