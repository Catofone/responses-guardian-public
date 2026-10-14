# Responses Guardian

当前版本：**0.4.1**。提供 Windows 64 位程序、PowerShell 管理脚本及 Go 源码，更新记录见 [CHANGELOG.md](CHANGELOG.md)。

Responses Guardian 是放在 Codex 与 CCSwitch 之间的本地兼容代理：

```text
Codex -> 127.0.0.1:15722 -> Responses Guardian -> 127.0.0.1:15721 -> CCSwitch -> 上游
```

Guardian 始终保持同一个本地端口，运行中可在 `off`、`on-error` 和 `always` 三种模式之间切换。模式选择会保存在 `runtime/mode.json`，下次启动继续使用。

## 目录

```text
responses-guardian/
  cmd/responses-guardian/  Go 源码与测试
    ui/panel.html         嵌入程序的控制面板
  scripts/                构建、启动、停止、状态与模式切换脚本
  licenses/               第三方依赖与 Go 运行时的许可声明
  启动Guardian.cmd        双击启动并打开控制面板
  go.mod / go.sum         Go 依赖及校验
  README.md               项目与运行说明
  CHANGELOG.md            版本更新记录
  LICENSE                 项目 MIT 许可证
  THIRD_PARTY_NOTICES.md  第三方组件说明
  responses-guardian.exe  Windows 分享包中提供；源码包可自行构建
  runtime/                首次运行后生成的设置、状态与日志
```

从项目根目录执行脚本，例如 `./scripts/status.ps1`。脚本会定位项目根目录，因此也可用脚本的绝对路径从其他目录启动。运行设置、日志和编译产物由各自的使用环境生成。

## 快速开始（Windows）

1. 解压到可写目录，进入包含 `README.md` 的项目根目录。Windows 分享包已包含程序；源码包先按下方“从源码构建”执行构建。
2. 启动 CCSwitch 并启用其本地代理，确认 Codex 当前提供商已经配置为通过 CCSwitch 访问上游。默认 CCSwitch 地址为 `http://127.0.0.1:15721`。
3. 完全关闭 Codex，双击根目录的 `启动Guardian.cmd`。启动成功后会自动打开控制面板；窗口自动关闭，失败时保留错误并等待按键。
4. 重新打开 Codex。首次双击启动且没有保存模式时，使用 `off` 原样转发；已保存的模式会被继承。程序已经运行时，启动器保留当前模式并打开面板。

也可以在 PowerShell 中显式选择 `on-error` 启动：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\start-ccswitch.ps1 -Mode on-error
```

查看状态或打开控制面板：

```powershell
.\scripts\status.ps1
.\scripts\open-panel.ps1
```

管理脚本支持 Windows PowerShell 5.1。预编译程序为 Windows amd64，运行它不需要安装 Go；从源码构建需要 Go 1.25 或更高版本。

Windows 分享包中的 `SHA256SUMS.txt` 记录可执行文件的 SHA256。可用 `Get-FileHash .\responses-guardian.exe -Algorithm SHA256` 核对。

## 本地控制面板（0.4.1）

启动 Guardian 后运行：

```powershell
.\scripts\open-panel.ps1
```

默认页面地址是 `http://127.0.0.1:15722/__responses_guardian/panel`。可以在页面上切换模式、查看上游连通性与请求计数，并修改 Guardian 端口、CCSwitch 端口、Codex 提供商 ID、配置文件路径以及两种重试次数。

| 设置 | 生效方式 |
| --- | --- |
| off / on-error / always 模式 | 立即作用于后续请求；正在输出的流继续完成 |
| 请求 / 流重试次数 | 写入当前 Codex 配置，重启 Codex 后读取 |
| 端口、上游地址、提供商 ID、配置路径 | 保存到下次启动；关闭 Codex 后重启 Guardian，再启动 Codex |

“Codex 提供商 ID”对应 `[model_providers.custom]` 中的 `custom`，不是 CCSwitch 中的供应商账号名称。实际账号路由仍在 CCSwitch 内管理。

`runtime/settings.json` 保存下次启动设置，`runtime/instance.json` 记录活动实例的设置。修改端口后，停止脚本仍会定位旧端口的活动进程。初次升级会读取现有 Codex 的提供商 ID 和重试次数，后续保存以页面选择为准，包括 0 和较小的次数。

0.4.1 在保存前检查所选配置文件是否存在、可读写，以及提供商的 `base_url` 和重试字段是否可处理。无效路径、缺少提供商或不受信任的路由会被拒绝，原设置保持不变。更改上游端口时，同一配置文件、同一提供商的旧实例地址仅用于启动迁移；运行中的守护不会持续接受旧地址。

监听地址支持 `127.0.0.1:15722`、`localhost:15722` 和 `[::1]:15722`。健康检查、进程核验及模式切换均使用活动监听地址；`set-mode.ps1 -Port` 只覆盖端口，保留地址。HTTP 端口 80 的面板支持浏览器省略 Host 和 Origin 中的默认端口。

面板仅接受回环连接，写入需要当前页面的令牌及同源请求；未使用外部脚本、字体或资源，也不会显示请求正文或凭据。

```text
off       原样转发，不检查、不清理、不重试
on-error  原样发送；命中特定历史错误后清理并重试一次
always    每个 Responses 请求发送前先清理
```

## 运行模式

`off` 是空模式。所有路径、请求正文、压缩正文及上游响应均直接转发；不读取 Responses JSON，不净化，也不因 400 重试：

```powershell
.\scripts\set-mode.ps1 off
```

`on-error` 是默认模式。请求先完整转发，仅在明确的历史密文或 resource mismatch 400 后净化并重试一次：

```powershell
.\scripts\set-mode.ps1 on-error
```

`always` 会在每个 Responses 请求第一次转发前运行净化：

```powershell
.\scripts\set-mode.ps1 always
```

也可以重复运行启动脚本来切换，已运行时不会重启进程：

```powershell
.\scripts\start-ccswitch.ps1 -Mode off
```

切换不停止或重启 Guardian，也不改 `base_url`。每个请求在开始时固定自己的模式，因此切换时已开始的请求和 SSE 流会按原模式继续完成，之后的新请求使用新模式。

`always` 会持续删除 reasoning、compaction、item reference、旧 response/conversation 引用和服务器 item ID。它保留明文消息、工具调用对及可读 reasoning summary，但会损失加密推理连续性和只存在于 compaction 密文中的早期上下文。

健康检查的 `mode`、`pid`、`mode_changes`、`passthrough_requests`、`proactive_checks` 和 `proactive_changes` 字段可确认实际运行状态。

## 限流重试

面板可将 Codex 的两种重试次数设置为 0–100。初始默认值为：

```toml
request_max_retries = 20
stream_max_retries = 20
```

前者配置响应开始前的 HTTP 请求重试，后者配置 SSE 流的重试次数。面板允许两项设置在 0–100 之间。Guardian 不另外复制一套 429 重试，避免两层重试相乘造成请求风暴。

Guardian 的配置守护会在 CCSwitch 重写选定提供商的配置后恢复已保存的重试次数。0.4.0 可明确调低重试次数，不再强制至少为 20。若上游返回的是配额耗尽、账单限制或永久不可用错误，增加重试次数不会解决问题。

## 恢复策略

触发条件仅包括：

- `invalid_encrypted_content`
- `encrypted content ... could not be decrypted`
- `encrypted content ... could not be verified`
- `encrypted content ... could not be parsed`
- `requested item was created under a different OpenAI resource`
- `use the same resource that created the item`

触发后：

- 保留用户/助手明文消息、图片和文件引用。
- 成对保留 `function_call` 与 `function_call_output`，包括 `call_id`。
- 将可读的 reasoning summary 转为普通 assistant 上下文。
- 删除不可迁移的 reasoning、compaction 和 `item_reference` 条目。
- 删除旧上游的 `previous_response_id`、`conversation`、item `id` 与 `status`。
- 只重试一次；普通 400、500、超时和已经开始的流不会触发恢复。

这比每次请求都删除 reasoning 的模式损失更小，但无法恢复只存在于加密 compaction 中、且已经没有明文副本的早期上下文。

## 启动

先启动 CCSwitch 并确认其本地代理已启用，然后运行：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\start-ccswitch.ps1
```

首次启动也可直接指定模式：

```powershell
.\scripts\start-ccswitch.ps1 -Mode off
```

脚本会启动本地代理，并只把 `~/.codex/config.toml` 中 `[model_providers.custom]` 的：

```toml
base_url = "http://127.0.0.1:15721/v1"
```

改为：

```toml
base_url = "http://127.0.0.1:15722/v1"
```

首次修改前会创建 `config.toml.responses-guardian.bak`。CCSwitch 热切换供应商时如果写回直接路由，Guardian 会恢复代理地址和已保存的重试次数；模型与认证配置保持不变。写入使用同目录临时文件与替换，并在替换前检查原文件是否被其他程序修改。

0.4.1 启动失败时会撤回本次对 Codex 配置的修改，不再用旧 `instance.json` 恢复新的监听地址。若文件同时被其他程序修改，只在路由仍属于本次启动时撤回该路由，保留并发修改的凭据和其他字段；遇到新的路由选择或写入权限错误，会保留文件并报告恢复错误。

启动后需要完全退出并重新启动 Codex，让新进程读取 `base_url`。

### 激活待更新版本

0.4.1 修复了启动回滚、上游地址迁移、配置路径校验、IPv6 和默认 HTTP 端口处理，详见 [更新记录](CHANGELOG.md)。

若目录中存在 `responses-guardian.next.exe`，说明新版已暂存但旧进程仍在服务。不要在活动 Codex 任务中停止 Guardian。完成当前任务后：

1. 完全关闭 Codex。
2. 在 Guardian 目录运行：

```powershell
.\scripts\activate-update.ps1 -Mode on-error
```

3. 脚本显示更新已生效后再启动 Codex。升级二进制或改变端口时需要关闭 Codex；日常模式切换可直接使用面板或 `set-mode.ps1`。

## 状态与停止

```powershell
.\scripts\status.ps1
.\scripts\stop-ccswitch.ps1
```

Codex 不会在运行中重新加载 `base_url`。正常停止顺序是：先关闭 Codex，再运行停止脚本。停止脚本会终止 Guardian，并把 Codex `base_url` 恢复为活动实例使用的 CCSwitch 地址，默认是 `127.0.0.1:15721/v1`。

如果脚本检测到 Codex 仍运行或 `15722` 仍有活动连接，它不会修改进程或配置。完全关闭 Codex 后再执行：

```powershell
.\scripts\stop-ccswitch.ps1
```

健康检查：

```text
http://127.0.0.1:15722/__responses_guardian/health
```

## 安全边界

- 只允许监听和转发到回环地址。
- 不记录请求正文、Authorization、API key、工具输出或 reasoning summary。
- 支持 Codex Desktop 使用的 zstd 请求压缩；解压结果受 64 MiB 上限保护。
- 日志只包含恢复次数、删除条目数量和 HTTP 状态。
- 不修改 CCSwitch 数据库或供应商凭据。
- 在线模式切换写入本地 `runtime/mode.json`，并核对健康检查中的 PID 和最终模式。
- 连接设置写入 `runtime/settings.json`，活动配置单独记在 `runtime/instance.json`。
- HTTP 客户端不会自动跟随上游重定向。
- 不会自动注册开机启动。

## 从源码构建

需要 Go 1.25 或更高版本：

```powershell
.\scripts\build.ps1
```

源代码只使用一个固定版本的纯 Go zstd 解码依赖；具体版本和校验值记录在 `go.mod` 与 `go.sum`。

运行测试与静态检查：

```powershell
go test ./...
go vet ./...
```

Windows 上的回归测试包含临时配置、模拟 CCSwitch 和 PowerShell 5.1 生命周期测试，测试使用独立的程序副本和临时端口。

## 许可证

项目代码按 [MIT License](LICENSE) 提供。第三方组件保留各自的许可证，详见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
