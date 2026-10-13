# Paced Affinity

## What it does

A [CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI) plugin that routes Claude and Codex chats across
your subscription accounts. It keeps CPA's session affinity and adds quota and cache awareness: new chats go where
their prompt is already cached and allowance is available, and accounts at their limit are left alone, including a
Codex account that would otherwise keep running on paid credits. It uses usage data CPA already receives, without
extra requests.

In a replay of nine days of recorded traffic, it used 4% less Claude quota for the same work than CPA's built-in
routing, and 10–100% fewer Codex credits depending on how much room the Codex accounts had; see
[benchmarks](docs/benchmarks.md).

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

Use the same plugin store to install future releases. Upgrading from v0.1: `shadow`, `providers` and
`claude-placement` are no longer read and can be deleted.

## How it works

- **Warm chats** keep their account for an idle hour, like CPA's session affinity, and move only when the account
  is rate-limited or reports its limit reached.
- **Subagents and forks** start on their parent's account.
- **New Claude chats** go to an account that served the same model within the cache lifetime, when one has room:
  every chat begins with the same tools and system prompt, often 200k tokens, which such an account reads from cache
  instead of writing again.
- Among accounts with room, a new Claude main chat goes to the account furthest behind its weekly spending pace;
  other placements weigh projected headroom against weekly allowance that would otherwise expire unused.
- An account reporting 100% usage gets no new work while another has room. CPA already holds back accounts that
  returned a 429; a Codex account paying with credits never does.
- Unsupported providers, requests without a chat identity, and plugin errors fall back to CPA's selector.

See [how it works](docs/how-it-works.md) for the rules and worked examples.

## Settings

<a id="configuration"></a>

The plugin has no settings of its own; `enabled` and `priority` are CPA's. `routing.session-affinity: true` keeps
CPA's fallback routing sticky.

A credential's CPA **`weight`** seeds the plugin's guess at the account's size until its usage headers have moved
enough to measure it. Leave it unset: in the replay, plan-size weights changed Claude quota use by at most 0.22% and
raised Codex credits in most scenarios.
