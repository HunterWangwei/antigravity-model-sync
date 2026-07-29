# Antigravity Model Sync Plugin

[English](README.md) | [简体中文](README_CN.md)

Standalone native plugin for CLIProxyAPI v7.2.104+. It discovers the current models returned by Antigravity's `fetchAvailableModels` API and merges them with the official v7.2.104 static model catalog. CLIProxyAPI's built-in Antigravity executor remains responsible for requests.

The plugin does not import CLIProxyAPI packages and can be built independently.

## Build

Windows PowerShell, from the repository root:

```powershell
.\build.ps1
```

Linux/macOS:

```sh
./build.sh
```

The binary is written to `dist/<goos>/<goarch>/antigravity-model-sync.<ext>`.

## Install from the CLIProxyAPI plugin store

Add this third-party registry to `config.yaml`:

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

The plugin then appears in the CLIProxyAPI plugin store. Install it there and restart CLIProxyAPI so the newly installed native library can be loaded.

## Configure

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    antigravity-model-sync:
      enabled: true
      priority: 100
      # Optional. The first successful endpoint wins.
      endpoints:
        - "https://daily-cloudcode-pa.googleapis.com"
        - "https://cloudcode-pa.googleapis.com"
```

Restart CLIProxyAPI after installing or replacing the binary. The plugin reads each Antigravity account's access token from host-provided auth data, but never logs or returns it. HTTP calls run through the host bridge, so the normal CLIProxyAPI proxy and request-logging policy applies.

## Compatibility limitation

The v7.2.104 plugin `ModelInfo` ABI has no `SupportsWebSearch` field. This plugin can synchronize model IDs, names, and token limits, but it cannot attach Antigravity web-search capability metadata without changing the official host ABI. The official built-in executor is otherwise used unchanged.
