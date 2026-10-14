# Antigravity 动态模型同步插件

[简体中文](README.md) | [English](README_EN.md)

这是一个适用于 CLIProxyAPI v7.2.104 及以上版本的独立原生插件。插件会调用 Antigravity 的 `fetchAvailableModels` 接口获取当前账号可用模型，并与 CLIProxyAPI 官方静态模型清单合并。

插件只负责模型发现和注册，实际请求仍由 CLIProxyAPI 官方内置 Antigravity 执行器处理。

## 功能

- 按 Antigravity 账号动态获取可用模型。
- Daily 和 Prod 接口自动回退。
- 支持账号自定义 `base_url`。
- 与官方静态模型合并，并按模型 ID 忽略大小写去重。
- 官方静态元数据优先，远程数据只补全缺失字段。
- 自动跳过账号无权使用的模型：`fetchAvailableModels` 中标记 `disabled: true` 的条目（例如 Ultra 专属模型对普通账号显示的 `gemini-4-argon-disabled`）不会注册，避免请求被路由到该账号后返回 404。
- 使用官方 Antigravity User-Agent。
- 通过宿主 HTTP 桥接访问上游，继承 CLIProxyAPI 的代理和日志策略。
- CPA 左侧菜单提供中文同步状态页面。
- 不记录、不返回 Antigravity Access Token。

## 从插件商店安装

在 `config.yaml` 中添加第三方插件源：

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

在插件商店中安装后重启 CLIProxyAPI。插件源使用带 SHA-256 校验的直接下载清单，不消耗 GitHub Release API 额度，也无需 GitHub Token。

Docker 部署时，确保插件目录可写且已持久化：

```yaml
volumes:
  - ./plugins:/app/plugins
```

## 状态页面

v1.0.3 起，插件会在 CPA 左侧菜单注册“Antigravity 模型同步”页面，展示：

- 插件版本和 User-Agent。
- 最后同步时间。
- 实际请求端点和 HTTP 状态码。
- 远程模型数量、跳过的不可用模型数量和合并后模型数量。
- 中文成功或失败原因。

也可以通过 Management API 获取 JSON：

```text
GET /v0/management/plugins/antigravity-model-sync/status
```

## 不可用模型过滤

v1.0.7 起，插件会读取 `fetchAvailableModels` 中每个模型的 `disabled` 字段，值为 `true` 的条目不会注册到 CLIProxyAPI。

这类条目通常带有 `tagTitle: "Upgrade"` 或 `"Unavailable"`，例如普通账号会看到：

```json
"gemini-4-argon-disabled": {
  "disabled": true,
  "tagTitle": "Unavailable",
  "tagDescription": "Gemini 4 Argon is currently available to Ultra plan subscribers."
}
```

Antigravity 客户端用它们展示“需要升级”的入口，生成接口并不接受这些模型 ID，直接调用会返回 `404 Requested entity was not found`。旧版本会把它们注册为可用模型，导致 CPA 把请求路由到无权限账号，最终返回 `auth_unavailable`。

过滤按账号进行：同一个模型在有权限的账号上（无 `disabled` 标记）仍会正常注册。被跳过的数量会显示在状态页面的“跳过不可用”一栏。

升级到 v1.0.7 后，建议在下游（如 New API）渠道中移除 `gemini-4-argon-disabled` 等已不再注册的模型 ID。

## 手动构建

Windows PowerShell：

```powershell
.\build.ps1
```

Linux/macOS：

```sh
chmod +x build.sh
./build.sh
```

构建结果位于 `dist/<goos>/<goarch>/antigravity-model-sync.<ext>`。

## 兼容性限制

CLIProxyAPI v7.2.104 的插件 `ModelInfo` ABI 没有 `SupportsWebSearch` 字段，因此插件可以同步模型 ID、显示名称和 Token 限制，但无法在不修改官方宿主 ABI 的情况下写入 Antigravity Web Search 能力标记。
