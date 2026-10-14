# Antigravity Model Sync Plugin

[简体中文](README.md) | [English](README_EN.md)

Standalone native plugin for CLIProxyAPI v7.2.104+. It discovers models returned by Antigravity's `fetchAvailableModels` API and merges them with the official static model catalog. The built-in CLIProxyAPI Antigravity executor remains responsible for requests.

## Features

- Per-account dynamic Antigravity model discovery.
- Daily and production endpoint fallback.
- Official Antigravity User-Agent.
- Case-insensitive model deduplication with static metadata precedence.
- Skips models the account cannot use (`disabled: true` in `fetchAvailableModels`, e.g. Ultra-only `gemini-4-argon-disabled`).
- Host HTTP bridge integration for proxy and logging policy.
- CPA sidebar page showing synchronization diagnostics.
- Access tokens are never logged or returned.

## Install from the plugin store

```yaml
plugins:
  enabled: true
  dir: "plugins"
  store-sources:
    - "https://raw.githubusercontent.com/HunterWangwei/antigravity-model-sync/main/registry.json"
  configs:
    antigravity-model-sync:
      enabled: true
      priority: 100
```

Install the plugin from the CLIProxyAPI plugin store and restart CLIProxyAPI. The registry uses direct SHA-256-verified release assets and does not consume the GitHub Releases API rate limit.

## Unavailable model filtering

Since v1.0.7 the plugin reads each entry's `disabled` field from `fetchAvailableModels` and does not register entries where it is `true`.

Such entries usually carry `tagTitle: "Upgrade"` or `"Unavailable"`. For example, non-Ultra accounts receive:

```json
"gemini-4-argon-disabled": {
  "disabled": true,
  "tagTitle": "Unavailable",
  "tagDescription": "Gemini 4 Argon is currently available to Ultra plan subscribers."
}
```

The Antigravity client uses them to show an upgrade prompt; the generation API rejects these model IDs with `404 Requested entity was not found`. Earlier versions registered them, so CPA routed requests to accounts without access and eventually returned `auth_unavailable`.

Filtering is per account: the same model is still registered for accounts that list it without the `disabled` flag. The skipped count is shown in the diagnostics page.

After upgrading, remove IDs that are no longer registered (such as `gemini-4-argon-disabled`) from downstream channels such as New API.

## Diagnostics

The plugin registers an `Antigravity Model Sync` page in the CPA sidebar. Authenticated JSON diagnostics are also available at:

```text
GET /v0/management/plugins/antigravity-model-sync/status
```

## Build

```powershell
.\build.ps1
```

```sh
./build.sh
```

Artifacts are written to `dist/<goos>/<goarch>/antigravity-model-sync.<ext>`.

## Compatibility limitation

CLIProxyAPI v7.2.104 plugin `ModelInfo` does not expose `SupportsWebSearch`. Model IDs, display names, and token limits can be synchronized, but the web-search capability flag cannot be supplied without changing the official host ABI.
