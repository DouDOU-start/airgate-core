# AirGate 生态架构 v2

> ⚠️ **本文为目标愿景(to-be),大部分尚未实现。** 当前真实实现见 [`current/`](current/README.md),已知差距见 [`current/tech-debt.md`](current/tech-debt.md)。**开发以 `current/` 为准;本文用于把握演进方向。**

## 状态

草案 RFC。

本文定义 AirGate 作为多协议、多提供商、多 UI 插件 AI 网关平台的目标架构，用于指导 Core、SDK、网关插件、提供商插件、UI 插件以及任务/图像生成相关工作。

本文虽然位于 `airgate-core` 仓库，但讨论范围覆盖整个 AirGate 生态；其中的仓库名、包名和插件名用于表达目标边界，不代表必须立即完成物理拆仓。

## 一页版

先记住这 5 条即可：

1. **Core 是平台内核**：负责身份、账号、路由、任务、资产、模型目录、计费、插件生命周期。
2. **Gateway 负责外部协议兼容**：例如 OpenAI-compatible、Anthropic-compatible route、SSE、错误格式。
3. **Provider 负责上游适配**：例如 OpenAI API、ChatGPT Web、Claude OAuth、Kiro EventStream、模型发现、token/session。
4. **UI 插件只负责产品界面**：例如 chat、studio、status、account widget，通过 Core API 工作。
5. **SDK 需要拆概念**：protocol ABI、Go SDK、插件运行时、devkit、前端 SDK 不应绑在一起演进。

最小调用链：

```text
Client / UI
  -> Gateway Plugin
  -> normalized operation
  -> Core services
  -> Provider Plugin
  -> Upstream Provider
```

## 怎么读本文

- 只想判断“这段代码该放哪”：读 **一页版**、**职责速查表**、**架构决策摘要**。
- 要评审 task/image 改动：读 **任务与图像生成规则**、**当前 task/image 工作即时检查清单**。
- 要设计插件 manifest：读 **Host capability 模型**、**Manifest v2**。
- 要拆仓或迁移：读 **目标项目拆分**、**迁移计划**。
- 其余章节是审计依据和细节参考，不需要一次读完。

## 目标

AirGate Core 应当是平台内核，而不是某个协议适配器或提供商集成。生态应围绕清晰边界组织：

```text
Client / UI / CLI
    -> Gateway Plugin
    -> AirGate Internal Operation Contract
    -> Core Platform Services
    -> Provider Adapter Plugin
    -> Upstream Provider
```

典型 OpenAI 兼容调用链应如下：

```text
OpenAI-compatible client
    -> gateway-openai
    -> normalized chat/image/task request
    -> core routing / billing / task / asset / scheduler
    -> provider-chatgpt-web or provider-openai-api
    -> upstream provider
```

## 非目标

- 本 RFC 不要求立即进行多仓库拆分。
- 本 RFC 不要求一次性重写所有现有插件。
- 本 RFC 不在本文中定义每一个 protobuf 字段。
- 本 RFC 不移除对现有 OpenAI 兼容或 Anthropic 兼容客户端的兼容性。

## 职责速查表

| 组件 | 负责 | 不负责 |
| --- | --- | --- |
| Core | 身份、账号、路由、任务、资产、模型目录、计费、插件生命周期 | 外部协议格式、上游 token/session、产品页面 |
| Gateway | 外部 API 兼容：route、请求/响应格式、SSE/WebSocket、错误形态 | 上游认证、provider session、任务持久化、计费策略 |
| Provider | 上游适配：auth、token/session、HTTP/WebSocket/EventStream、模型发现 | `/v1/...` route、产品 UI、Core task 状态、平台计费 |
| UI 插件 | 页面、widget、产品状态、调用 Core public API | provider wire protocol、gateway 兼容、task 执行 |
| SDK/Protocol | 稳定 ABI、插件作者 API、运行时/开发工具分层 | 把 Go、前端、运行时、devserver 绑成一个不可分包 |

判断规则：

- 面向外部客户端的兼容性问题，放 Gateway。
- 面向上游厂商的认证、传输、响应解析，放 Provider。
- 跨 provider 共享的权限、路由、任务、资产、计费、模型索引，放 Core。
- 面向用户产品体验的页面和状态，放 UI 插件。

## 当前边界问题（审计依据，可跳读）

### Core 混入产品逻辑和协议逻辑

代表性位置：

- `backend/internal/plugin/host_service.go`
- `backend/internal/plugin/forwarder.go`
- `backend/internal/plugin/manager_runtime.go`
- `backend/internal/server/dynamic_router.go`
- `backend/internal/scheduler/family.go`
- `web/src/app/router.tsx`
- `web/src/app/layout/AppShell.tsx`

当前问题：

- `HostService` 通过一个过宽的服务暴露了过多互不相关的 Core 能力。
- Core 转发层包含协议特定请求解析和 OpenAI 形态错误响应。
- 调度和路由包含提供商/模型字符串特判。
- Core UI 硬编码了 chat、studio、status 等插件产品路由。
- 插件管理器混合了运行时、安装、市场、资产、数据库准备、后台任务和任务分发。

### SDK 混合协议 ABI、Go 辅助库、运行时、开发工具和前端 SDK

`airgate-sdk` 中的代表性位置：

- `proto/plugin.proto`
- `plugin.go`
- `host.go`
- `models.go`
- `billing.go`
- `task.go`
- `grpc/*`
- `devserver/*`
- `frontend/src/*`

当前问题：

- 稳定 wire ABI 和开发者便利 API 一起演进。
- 前端插件类型与 Go 插件运行时混在同一包内。
- 计费、模型、任务、前端等关注点都出现在 SDK 根层。
- 运行时实现细节很难与插件作者 API 合约分离。

### 网关插件同时承担提供商和 UI 职责

示例：

- `airgate-openai` 混合 OpenAI 兼容网关路由、ChatGPT Web 反向行为、图像任务处理、Anthropic 转换、WebSocket 上游行为、模型开关和账号 UI。
- `airgate-claude` 混合 Anthropic 兼容网关路由、Claude API key 行为、claude.ai OAuth/session-key 行为、sidecar/probe 行为、TLS 指纹和账号 UI。
- `airgate-kiro` 混合 Anthropic 兼容网关路由、Kiro/AWS 提供商转换、EventStream 解析、OAuth/device auth、web-search 策略和账号 UI。
- `airgate-playground` 主要是 UI 插件，但也转发协议请求、解析 OpenAI SSE/图像响应、创建任务并处理提供商/平台编排。

## 目标生态分类（细节参考）

Core 负责平台原语：

- 身份与授权
- 账号注册表
- 组/用户策略
- 路由
- 调度器
- 速率限制
- 插件生命周期
- 任务引擎
- 资产服务
- 模型目录
- 用量计量
- 计价与账本
- 审计/日志
- 事件总线
- 前端插件外壳

Core 不应负责：

- OpenAI 响应格式化
- Anthropic 请求格式化
- Kiro/AWS EventStream 格式化
- ChatGPT Web 反向行为
- 提供商 OAuth 实现细节
- 提供商 token 刷新逻辑
- 提供商特定模型 family 规则
- chat、studio、status 等插件产品路由
- 提供商特定任务输出解析

已有 legacy 行为可以在迁移期短期保留，但新增能力不得继续扩张这些边界；迁移期保留的逻辑应被隔离在 legacy adapter 或明确的 gateway/provider boundary 后。

### Protocol 包

`airgate-protocol` 是稳定 ABI 包。

它负责：

- protobuf schema
- 生成后的协议代码
- handshake 与协议版本常量
- manifest schema
- capability schema
- 稳定的 task/model/asset/event wire contract

它应当缓慢演进，并遵循严格兼容性规则。

### Go SDK

`airgate-sdk-go` 是插件作者 API。

它负责：

- Go 插件接口
- 类型化辅助结构
- manifest 辅助能力
- 日志辅助能力
- capability 辅助能力
- 测试辅助能力

它不应包含前端 React 代码、devserver 实现或 transport runtime 内部细节。

### Go 插件运行时

`airgate-plugin-runtime-go` 是 Go transport/runtime 适配层。

它负责：

- hashicorp/go-plugin 集成
- gRPC server/client 适配
- Host capability client
- stream bridge
- protobuf 与 Go 类型转换

它依赖 `airgate-protocol` 和 `airgate-sdk-go`。

### Devkit

`airgate-devkit-go` 负责本地插件开发工具：

- local devserver
- fake host implementation
- local scheduler/testing harness
- frontend proxy helper

### 前端插件 SDK

`@airgate/plugin-ui` 负责前端插件集成：

- UI mount API
- page/widget 类型
- route contribution 类型
- account/OAuth/import action bridge 类型
- React helper

`@doudou-start/airgate-theme` 负责设计 token 和 theme/CSS helper。

### 网关插件

网关插件暴露外部 API 面，并将协议流量规范化为 AirGate 内部操作。

它负责：

- public route 定义
- 所属协议的请求校验
- 协议本地错误形态
- SSE/WebSocket wire 语义
- 协议请求到内部请求的转换
- 内部响应到协议响应的转换

它不应负责：

- 上游提供商认证
- 提供商 token 刷新
- 提供商特定 header 或 TLS 行为
- 任务持久化
- 资产存储策略
- 账号获取 UI
- 计费策略
- 调度策略

示例：

- `gateway-openai`
- `gateway-anthropic`
- `gateway-realtime`

### 提供商适配插件

提供商插件负责将 AirGate 内部操作适配到上游提供商。

它负责：

- provider auth 与 credential 生命周期
- provider token 刷新
- 上游 HTTP/WebSocket/EventStream client
- 内部请求到 provider 请求的转换
- provider 响应到内部响应的转换
- provider model catalog 发布
- provider capability metadata
- provider quota/health probe

它不应负责：

- public `/v1/...` route 兼容
- 面向用户的产品 UI 页面
- Core task 状态
- Core 计费策略
- 外部 API 之间的通用协议转换

提供商插件的公开边界应停留在规范化 operation handler 与 capability metadata；provider 原始请求/响应、cookie/session、token refresh、网页 DOM/API 细节不得暴露给 gateway 或 Core。

示例：

- `provider-openai-api`
- `provider-chatgpt-web`
- `provider-anthropic-api`
- `provider-claude-oauth`
- `provider-kiro`
- `provider-gemini`

### UI 插件

UI 插件提供产品表面。

它负责：

- 页面
- widget
- UI 特定状态
- UI 特定持久化
- 调用 Core public orchestration API

它不应负责：

- provider wire protocol 解析
- gateway route 兼容
- 直接账号调度
- provider token 生命周期
- task 执行

示例：

- `ui-playground-chat`
- `ui-image-studio`
- `ui-status`
- `ui-account-openai`
- `ui-account-claude`
- `ui-account-kiro`

## Core 服务边界（细节参考）

Core service 拆分的重点不是立刻改代码，而是避免一个 `HostService` 或 plugin manager 继续吸收所有职责。

| 服务 | 负责 | 不负责 |
| --- | --- | --- |
| Plugin Runtime | 进程生命周期、handshake、health check、capability binding、runtime config | marketplace、frontend asset、task dispatch、plugin DB 产品策略 |
| Plugin Registry | install/update/remove、source metadata、official list、version/provenance、未来 signature/checksum | 插件运行时进程、前端静态资产、业务任务执行 |
| Plugin Asset | 前端资产发布、静态资产服务、开发模式资产代理、CDN/cache | 插件安装来源、插件进程生命周期 |
| Model Catalog | 跨 provider 的 model identity、modality、operation、pricing/routing tags | provider 私有 settings、字符串特判 |
| Routing | 基于 operation、modality、capability、model hint 选择 provider/account | path 检查、`model contains image`、provider 特定开关 |
| Task Engine | create/lease/dispatch/retry/cancel/progress/result/ownership/event | provider 私有任务协议、provider 响应体解析 |
| Asset Service | object storage、授权访问、retention、metadata、ownership | 解析 `b64_json` 等 provider 特定响应 |
| Usage/Rating/Ledger | usage event、定价策略、余额变更、audit trail | 把 provider 特定 usage dimension 硬编码进平台 |
| Frontend Plugin Shell | manifest routes、layout、action bridge、auth policy、theme/runtime isolation | 硬编码 chat/studio/status 等产品页面 |

Provider 插件发布 model entry，Core 消费方查询 model catalog，而不是读取 plugin manager cache 或 provider 特定 settings。

插件可以实现 task validation、processing 和 result normalization，但 Core 负责 task lifecycle 与安全模型。

## Host capability 模型

当前过宽的 HostService 应演进为版本化 capability service。插件在 manifest 中声明需要哪些 host capability，Core 在启动时只注入已声明能力。

建议分组：

```text
host.identity, host.accounts, host.routing, host.forwarding, host.probe,
host.models, host.tasks, host.assets, host.events, host.billing
```

每个 capability 至少定义 name、version、methods、permissions、ownership model、compatibility rules；涉及生命周期的能力还要定义 lifecycle model。

```yaml
requires:
  host:
    - host.routing@1
    - host.tasks@1
    - host.assets@1
```

Capability 是 host 向插件授予的服务接口，不等同于插件对外提供的 operation；插件的 `provides.operations` 描述其可被编排调用的业务能力，`requires.host` 描述其运行时需要 Core 注入的宿主能力。

## Manifest v2

Plugin manifest 应成为 plugin kind、capability、route、UI surface、account flow、task handler 和 host requirement 的单一事实来源。

`manifest_version` 表示 manifest schema 版本，不表示插件自身版本；插件自身版本应使用独立的 `plugin_version`。

最小模板：

```yaml
id: gateway-openai | provider-chatgpt-web | ui-image-studio
kind: gateway | provider | ui
manifest_version: 2
plugin_version: 0.1.0

provides:
  protocols: [...]
  providers: [...]
  operations: [...]
  modalities: [...]

routes: [...]
account: ...
models: ...
tasks: ...

requires:
  host:
    - host.routing@1
    - host.models@1
    - host.tasks@1
    - host.assets@1
```

Gateway manifest 重点声明外部协议和 route；Provider manifest 重点声明可处理的规范化 operation、modality、account flow 和 model source；UI manifest 重点声明页面 route、layout 和所需 host capability。

## 内部操作合约

网关插件和提供商插件不应以原始 OpenAI、Anthropic、Claude、Kiro 或其他 provider 特定请求体作为主要交换合约。

AirGate 应定义规范化操作，例如：

```text
chat.generate
chat.stream
image.generate
image.edit
embedding.create
audio.transcribe
model.list
token.count
```

网关插件将外部协议请求转换为内部操作请求。

提供商适配插件将内部操作请求转换为上游 provider 请求。

### Chat request 示例

```ts
ChatGenerateRequest {
  model?: string
  messages: Message[]
  tools?: Tool[]
  reasoning?: ReasoningOptions
  responseFormat?: ResponseFormat
  stream?: boolean
  metadata?: Record<string, unknown>
}
```

### Image request 示例

```ts
ImageGenerateRequest {
  model?: string
  prompt: string
  size?: string
  quality?: string
  count?: number
  inputAssets?: AssetRef[]
  outputMode?: "url" | "asset" | "base64"
}
```

### 转换职责归属

网关插件：

```text
external protocol request -> internal operation request
internal operation response -> external protocol response
```

提供商插件：

```text
internal operation request -> provider request
provider response -> internal operation response
```

## 任务与图像生成规则

图像生成是一等内部操作，不是 OpenAI 网关的副作用。

规则：

1. 同步外部 API 默认保持同步，除非协议明确选择异步行为。
2. 异步图像生成必须在 gateway compatibility metadata 中声明。
3. Core 负责 task lifecycle 和安全模型。
4. Provider 插件负责 provider 特定图像执行。
5. Asset ingestion 通过 Asset Service API 显式完成。
6. Core 不应解析 provider 特定图像响应体。
7. UI 插件消费 task 与 asset API，而不是 provider wire response。

Core task 只保存规范化 task metadata、状态、权限、进度、asset references 与审计信息；provider 特定 job id、polling cursor、stream offset 等执行状态由 provider 插件通过 task metadata 扩展字段或私有存储管理，Core 不解释其语义。

## 兼容性与版本

### Protocol ABI

- 只能通过新 field number 添加字段。
- 绝不复用 field number。
- 删除的 protobuf 字段必须 reserve。
- 同一 protocol major version 内不得不兼容地改变 request/response 语义。
- 破坏性 wire 变更需要新的 protocol major version。
- Handshake version 变更只用于 runtime/wire 不兼容场景。

### Go SDK

- minor version 可以添加可选接口、字段、helper 和 capability。
- 不要向已有稳定接口添加必需方法。
- 优先使用新增可选接口或新的 interface version。

### Capability

- Capability 名称和语义是稳定合约。
- 添加 capability 是兼容变更。
- 移除 capability 或改变 capability 语义是破坏性变更。
- 未知 future capability 应被可预测地忽略或拒绝，而不是导致 Core 崩溃。

### Frontend SDK

- 前端包独立于 Go SDK 版本化。
- Theme token 增加属于 minor。
- Theme token 删除或重命名属于 major。
- React peer dependency 变化只对 `@airgate/plugin-ui` 构成 major。

## 目标项目拆分

### `airgate-openai`

目标组件：

- `gateway-openai`
- `provider-openai-api`
- `provider-chatgpt-web`
- `ui-account-openai` 或 provider 声明式 account widget

移出：

- Anthropic conversion 进入 `gateway-anthropic` 或 protocol adapter module。
- ChatGPT OAuth/WebSocket/web-reverse image code 进入 `provider-chatgpt-web`。
- Image task execution 进入 provider task handler 与 Core task engine。
- Account React component 进入 account UI widget 或声明式 account flow。

### `airgate-claude`

目标组件：

- `gateway-anthropic`
- `provider-anthropic-api`
- `provider-claude-oauth`
- `ui-account-claude`

移出：

- OAuth/session-key/setup-token/uTLS/sidecar 行为进入 `provider-claude-oauth`。
- Anthropic Messages/count_tokens route 行为进入 `gateway-anthropic`。
- Account UI 进入 provider account widget。

### `airgate-kiro`

目标组件：

- 复用 `gateway-anthropic`，或在必要时使用专用 Anthropic-compatible gateway
- `provider-kiro`
- `ui-account-kiro`

移出：

- AWS EventStream、Kiro ConversationState、BuilderID auth、usage limit 进入 `provider-kiro`。
- Anthropic-compatible route 行为进入 gateway layer。
- Web search routing 进入 provider capability/tool policy。

### `airgate-playground`

目标组件：

- `ui-playground-chat`
- `ui-image-studio`

移出：

- Protocol forwarding 改为调用 Core orchestration API；需要对外协议兼容时通过 gateway 插件完成。
- 从 backend product logic 中移除 OpenAI SSE/image response parsing。
- Task execution 进入 Core task engine 与 provider task handler。
- Forwarder 只能作为 UI/product bridge 或 legacy adapter 存在，不应继续承担 gateway 或 provider 职责。

## 迁移计划

先做边界和逻辑拆分，再决定是否物理拆仓。

| 阶段 | 目标 | 验收标准 |
| --- | --- | --- |
| Phase 0 | 停止边界继续扩张 | 不再新增 broad HostService；新 Host API 必须 versioned capability；task API 强制 ownership；Core 不新增 provider image response 解析 |
| Phase 1 | 用本文评审新增工作 | 每个变更能回答：属于 Core/Gateway/Provider/SDK/UI 哪一层；capability 是否声明；Host API 是否版本化；ownership 是否由 Core 执行；协议兼容是否保持 |
| Phase 2 | 拆分 SDK 概念 | 逻辑上区分 `airgate-protocol`、`airgate-sdk-go`、`airgate-plugin-runtime-go`、`airgate-devkit-go`、`@airgate/plugin-ui`、`@doudou-start/airgate-theme` |
| Phase 3 | 引入 Core service interface | plugin runtime、registry、asset、model catalog、routing、task、asset service、metering/rating/ledger、frontend shell 有清晰内部边界 |
| Phase 4 | 以 OpenAI 作为样板 | gateway 不知道 ChatGPT OAuth；provider 不拥有 `/v1/...` route；task lifecycle 由 Core 负责；OpenAI-compatible image sync 语义不回退 |
| Phase 5 | 迁移 Claude、Kiro、Playground | Claude/Kiro 成为 provider adapter 或复用 gateway；Playground 转为 UI-only 产品插件 |

## 当前 task/image 工作即时检查清单

任何进行中的 task 或 image-generation 变更都应使用此清单：

- [ ] 新 task API 是 user-scoped。
- [ ] 新 task API 是 group-scoped。
- [ ] 新 task API 是 plugin-scoped。
- [ ] Task type 已声明并授权。
- [ ] 适用时保留 provider/account ownership。
- [ ] Core 不解析 provider 特定 image response body。
- [ ] Provider 原始 job id、poll cursor、stream offset 不成为 Core 语义。
- [ ] Asset ingestion 使用类型化 Asset Service API。
- [ ] OpenAI-compatible sync image route 默认保持同步。
- [ ] Async behavior 是 opt-in，并在 manifest/capability metadata 中声明。
- [ ] Host API 是 versioned capability，而不是 broad HostService 扩张。
- [ ] SDK/proto 变更保持 ABI 兼容。
- [ ] Generated protobuf files 与 schema changes 一起更新。
- [ ] Gateway code 只负责 protocol wire compatibility。
- [ ] Provider code 只负责 upstream-specific auth 与 transport。
- [ ] UI code 消费 Core task/asset/model API，而不是 provider wire response。

## 架构决策摘要

1. Core 是平台内核，不是协议适配器。
2. SDK protocol 是稳定 ABI，不是混合工具包。
3. Gateway 插件和 provider adapter 插件是不同职责。
4. Task、asset、model catalog、routing、billing 是 Core 平台原语。
5. Plugin manifest 是 operation、capability 和 route 声明的单一事实来源。
6. HostService 演进为 versioned capability services。
7. UI 插件不直接处理 provider wire protocol。
8. 图像生成是内部操作，并具有明确的 sync/async 兼容规则。
