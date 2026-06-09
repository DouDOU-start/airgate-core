# 插件现状(全生态)

> **现状文档** · 描述 7 个插件的**实际职责**(含混合现状)与前端机制。
> 文档中各插件"混入 provider/UI 职责"是已知的过渡态,差距与改进方向见 [`tech-debt.md`](tech-debt.md)。

## 清单

| 插件 | id | type | 核心职责 | 混合现状 |
|---|---|---|---|---|
| openai | `gateway-openai` | gateway | OpenAI 网关 + Anthropic 转换 | + ChatGPT OAuth/WebSocket(provider)+ 图像任务执行 + 账号 UI |
| claude | `gateway-claude` | gateway | Anthropic 网关 | + claude.ai OAuth/session/TLS/sidecar(provider)+ 账号 UI |
| kiro | `gateway-kiro` | gateway | Kiro(Anthropic 兼容)网关 | + AWS EventStream/OAuth/web-search(provider)+ 账号 UI |
| playground | `airgate-playground` | extension | Web 聊天 UI | + 协议转发/SSE 解析/任务编排 |
| studio | `airgate-studio` | extension | 多模态内容创作 | 生成任务 |
| epay | `payment-epay` | extension | 支付渠道 | — |
| health | `airgate-health` | extension | 提供商健康监控 | 提供 `/status` 状态页 |

## 网关插件(混合职责现状)

三个 gateway 都把**网关 + provider + UI** 三层职责合在一仓(目标愿景应拆为 `gateway-*` + `provider-*` + `ui-account-*`):

**openai**(`airgate-openai/backend/internal/gateway/`):
- 网关:`forward.go`、`request_convert.go`;Anthropic 转换 `anthropic_convert.go`/`anthropic_forward.go`
- **provider 职责**:ChatGPT OAuth `oauth.go`/`chat_completions_oauth.go`/`session_state.go`、WebSocket `ws.go`、Web 反向 `images_web_reverse.go`
- 图像任务执行:`task_image.go`/`task_runner.go`/`task_registry.go`(经 `Host.Invoke` tasks/assets)
- **UI 职责**:6 个账号 widget(`SlotAccountIdentity`/`Create`/`Edit`/`UsageWindow`/`UsageMetricDetail`/`UsageCostDetail`)

**claude**(`airgate-claude/backend/internal/gateway/`):
- 网关:`forward.go`
- **provider 职责**:`oauth.go`/`session.go`(claude.ai OAuth/session-key)、`tlsfingerprint.go`(uTLS 指纹)、`sidecar.go`
- UI:账号 widget

**kiro**(`airgate-kiro/backend/internal/gateway/`):
- 网关:`forward.go`、`converter.go`(Anthropic ↔ Kiro)
- **provider 职责**:`eventstream.go`(AWS EventStream)、`oauth.go`(device auth)、`websearch.go`
- UI:账号 widget

## 扩展插件

- **playground**:Web 聊天 UI;现状也转发协议、解析 SSE/图像、创建任务(目标愿景应为 UI-only,经 Core 编排 API)。
- **studio**:多模态内容创作;生成任务经 Core Task。
- **epay**:支付渠道;`payment-callback` 路由无鉴权(回调)。
- **health**:健康监控;`prober.go` 探测 + `aggregator.go` 聚合;Core 的 `/status` 反代至此(硬编码插件名于 `router.go:237`,路由注册于 `:253`,见 [`tech-debt.md`](tech-debt.md))。

## 前端机制

- 每个插件前端为**单 `index.js` bundle**,基于 `@doudou-start/airgate-theme`,输出至 `web/dist/index.js`。
- **挂载点**:`FrontendWidgets`(嵌入 Core 后台的 slot,如 `account-identity`)+ `FrontendPages`(独立页面)。slot 常量见 `airgate-sdk/sdkgo`(`SlotAccountIdentity` 等)。
- **资产双路径**:dev 模式 Core 从 `<plugin>/web/dist` 读(vite watch);生产从 `airgate-core/backend/data/plugins/<plugin-id>/assets` 读(`make build-plugins` 同步)。

## 插件 → Core 调用

网关/扩展经 `Host.Invoke` 调 Core 能力(契约见 [`plugin-contract.md`](plugin-contract.md)),典型:
- `tasks.create` / `tasks.update` — 创建/推进任务(图像生成)
- `assets.store` / `assets.get_url` — 存取生成的图片
- `scheduler.select_account` / `gateway.forward` — 编排转发(playground)
