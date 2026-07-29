# Antigravity 动态模型同步插件

[简体中文](README.md) | [English](README_EN.md)

这是一个适用于 CLIProxyAPI v7.2.104 及以上版本的独立原生插件。插件会调用 Antigravity 的 `fetchAvailableModels` 接口获取当前账号可用模型，并与 CLIProxyAPI v7.2.104 的官方静态模型清单合并。

插件只负责模型发现和注册，实际请求仍由 CLIProxyAPI 官方内置 Antigravity 执行器处理。插件不导入 CLIProxyAPI 内部包，可以独立构建。

## 功能

- 按 Antigravity 账号动态获取可用模型。
- Daily 和 Prod 接口自动回退。
- 支持账号自定义 `base_url`。
- 与官方静态模型合并，并按模型 ID 忽略大小写去重。
- 官方静态元数据优先，远程数据只补全缺失字段。
- 通过宿主 HTTP 桥接访问上游，继承 CLIProxyAPI 的代理和请求日志策略。
- 不记录、不返回 Antigravity Access Token。
- 提供中文同步状态接口，可查看最后同步时间、HTTP 状态和模型数量。

## 从插件商店安装

在 CLIProxyAPI `config.yaml` 中添加第三方插件源：

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

重启 CLIProxyAPI 后，在插件商店中安装“Antigravity 动态模型同步”。原生动态库安装完成后需要再次重启 CLIProxyAPI 才能加载。

插件源使用带 SHA-256 校验的直接下载清单，安装过程不依赖 GitHub Release API 额度，也无需配置 GitHub Token。

Docker 部署时，请确保插件目录可写且已持久化：

```yaml
volumes:
  - ./plugins:/app/plugins
```

对应配置：

```yaml
plugins:
  dir: "/app/plugins"
```

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

构建结果位于：

```text
dist/<goos>/<goarch>/antigravity-model-sync.<ext>
```

## 可选接口配置

```yaml
plugins:
  configs:
    antigravity-model-sync:
      enabled: true
      priority: 100
      endpoints:
        - "https://daily-cloudcode-pa.googleapis.com"
        - "https://cloudcode-pa.googleapis.com"
```

插件会按列表顺序尝试接口，并使用第一个成功返回模型的地址。

## 查看同步状态

```text
GET /v0/management/plugins/antigravity-model-sync/status
```

该接口需要 Management Key，且不会返回 Access Token。

## 兼容性限制

CLIProxyAPI v7.2.104 的插件 `ModelInfo` ABI 没有 `SupportsWebSearch` 字段，因此插件可以同步模型 ID、显示名称和 Token 限制，但无法在不修改官方宿主 ABI 的情况下写入 Antigravity Web Search 能力标记。
