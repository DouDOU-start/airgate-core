# 技术债登记(现状 ↔ 目标差距)

> **现状文档** · 登记已知的架构差距与改进方向。
> 用途:① 让开发者知道哪些是已知债、**新代码勿加深**;② 后续决定标准/排期改造的依据。
> **本轮只登记不改代码。** 每条均可在代码定位。

## 一、Core 内的协议/产品硬编码(代码热点)

职责边界要求 Core 不碰外部协议格式、provider 特判、产品页面。以下为违反点:

| # | 位置 | 现状(代码做了什么) | 应属哪层 | 严重度 |
|---|---|---|---|---|
| 1 | `internal/plugin/error_format.go` | ~~已修复~~ 错误写出统一走 `protocolError`，按插件声明的 `Metadata["error_format"]`（路由级优先）选择格式化器（openai/anthropic）；claude/kiro 声明插件级 anthropic，openai 在 `/v1/messages*` 声明路由级 anthropic；未声明回退 OpenAI 兼容格式（历史默认） | Gateway(协议错误形态) | ~~高~~ |
| 2 | `internal/plugin/quota.go:40` | ~~已修复~~ `isMetadataOnlyPath` 优先查询插件 `RouteDefinition.Metadata["metadata_only"]` 声明的索引（`Manager.IsMetadataOnlyRoute`），硬编码列表保留为兜底；openai/claude/kiro 插件已声明 metadata_only | Gateway(协议路由) | ~~高~~ |
| 3 | `internal/scheduler/family.go:25-27` | ~~已修复~~ `Manager.ModelFamily` 从插件目录查 `Metadata["family"]`，`Scheduler.resolveModelFamily` 与 `Forwarder`/`HostService` 均优先采纳插件声明，硬编码 `gpt-image-*` 保留为兜底；openai 插件已声明 `family` | Model catalog 声明 | ~~中~~ |
| 4 | `internal/routing/selector.go` | ~~已修复~~ `GroupSupportsImageRequirement` 抽为共享函数，`forwarder.go` 与 `selector.go` 统一调用；平台硬编码保留但集中在一处并标注 TODO | capability 表达 | ~~中~~ |
| 5 | `internal/plugin/scheduling_model.go:15-19` | 硬编码 `platform=openai` + Anthropic Messages 路径时 Claude→GPT 选号映射 | Gateway(请求规范化) | 中 |
| 6 | `internal/plugin/forwarder.go` | ~~已修复~~ 图片授权特判合并至 `routing.GroupSupportsImageRequirement`，`forwarder.go` 不再重复平台判断逻辑 | capability 表达 | ~~中~~ |
| 7 | `internal/plugin/request.go` | ~~已修复~~ `isImageModel` 优先查询模型目录 `image_generation` 能力声明，回退到 `usagemodel.IsImageGen` 前缀匹配；`pickProbeModel` 直接用 `ModelInfo.HasCapability` | 声明式 capability | ~~中~~ |
| 8 | `internal/server/router.go:253` | `/status` 硬编码反代到 `airgate-health` 插件名 | (设计有意不在 core 放状态页,但耦合具体插件名) | 低 |

## 二、HostService 过宽

| 位置 | 现状 | 目标 | 严重度 |
|---|---|---|---|
| `internal/plugin/host_service.go` | 单一 `CoreInvokeService.Invoke` 暴露 19 个 method(调度/转发/资产/任务/用户/元数据混在一起) | 拆为版本化 capability service(`host.routing@1`/`host.tasks@1`/`host.assets@1` …),按 manifest `requires.host` 选择性注入 | 高 |

## 三、契约层架构缺口(目标态未实现)

| 项 | 现状 | 目标方向 | 严重度 |
|---|---|---|---|
| **规范化操作层** | 网关 `Forward` 直接收发原始 OpenAI/Anthropic 请求体,无中间层 | Gateway↔Provider 交换规范化 operation(`chat.generate`/`image.generate` …) | 架构级 |
| **Manifest v2** | `plugin.yaml` 生成式,无 `manifest_version`/`provides`/`requires.host` | 声明式 Manifest v2 | 中 |
| **版本化 capability** | 扁平 `host.invoke.<method>`(`sdkgo/capability.go`) | `host.routing@1` 等分组版本化 | 中 |
| **Provider 拆分** | openai/claude/kiro 网关仓内混 provider(OAuth/session/TLS/EventStream)与 UI(账号 widget) | 拆为 `gateway-*` + `provider-*` + `ui-account-*` | 架构级 |
| **Playground 职责** | 兼做协议转发/SSE 解析/任务编排 | UI-only,经 Core 编排 API | 中 |

详见 [`plugins.md`](plugins.md)、[`plugin-contract.md`](plugin-contract.md)。

## 维护

新增代码触碰上述热点时,**勿加深**(如勿在 Core 再加 provider 字符串特判、勿扩 HostService 无关方法)。改造排期由用户基于本表决定;改造完成后同步更新本表与对应现状文档。
