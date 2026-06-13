# 插件契约(现状)

> **现状文档** · 描述 core ↔ 插件的**真实契约**(`airgate-sdk` + 运行时桥)。改动涉及契约时须同步更新本文。
> 这是日常开发的权威依据;已知差距(Manifest v2 / 版本化 capability / 规范化操作层等)见 [`tech-debt.md`](tech-debt.md)。

## gRPC 协议(ABI)

`airgate-sdk/protocol/proto/plugin.proto`,6 个 service:

| Service | 方向 | rpc |
|---|---|---|
| `PluginService` | Core→插件 | GetInfo / Init / Start / Stop / GetWebAssets / GetSchema / HealthCheck / HandleRequest |
| `GatewayService` | Core→插件 | GetPlatform / GetModels / GetRoutes / **Forward** / ForwardStream / ValidateAccount / HandleWebSocket |
| `ExtensionService` | Core→插件 | Migrate / GetBackgroundTasks / RunBackgroundTask / HandleRequest / HandleStreamRequest / **ProcessTask** / GetTaskTypes |
| `MiddlewareService` | Core→插件 | OnForwardBegin / OnForwardEnd |
| `EventService` | Core→插件 | GetEventSubscriptions / HandleEvent |
| `CoreInvokeService` | **插件→Core** | **Invoke / InvokeStream** |

所有 JSON payload 经 protobuf `bytes` 字段透传,运行时 `map[string]any ↔ json.Marshal` 转换(`runtimego/grpc/`)。

## Go 插件接口(`airgate-sdk/sdkgo/`)

```go
// 所有插件(plugin.go:11)
type Plugin interface {
    Info() PluginInfo
    Init(ctx PluginContext) error   // 此处经 HostAware 获取 Host
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
}

// 网关插件(gateway.go:43)
type GatewayPlugin interface {
    Plugin
    Platform() string
    Models() []ModelInfo
    Routes() []RouteDefinition
    Forward(ctx, req *ForwardRequest) (ForwardOutcome, error)
    ValidateAccount(ctx, credentials map[string]string) error
    HandleWebSocket(ctx, conn WebSocketConn) (ForwardOutcome, error)
}

// 扩展插件(extension.go:9)
type ExtensionPlugin interface {
    Plugin
    RegisterRoutes(r RouteRegistrar)   // 自定义 HTTP 路由
    Migrate() error                    // 插件自有表迁移
    BackgroundTasks() []BackgroundTask
}

// 中间件插件(middleware.go:26)
type MiddlewarePlugin interface {
    Plugin
    OnForwardBegin(ctx, req *MiddlewareRequest) (*MiddlewareDecision, error)
    OnForwardEnd(ctx, evt *MiddlewareEvent) error
}
```

`Forward` 入参 `ForwardRequest`(`{Account, Body(原始协议体), Headers, Model, Stream}`),返回 `ForwardOutcome`(判决模型见 [`core-runtime.md`](core-runtime.md))。
> 现状:`Body` 是**原始 OpenAI/Anthropic 请求体**,插件直接转发上游——目标愿景的"规范化操作层(chat.generate/image.generate)"未实现。见 [`tech-debt.md`](tech-debt.md)。

## Host 反向调用(`sdkgo/host.go`)

```go
type Host interface {
    Invoke(ctx, req HostInvokeRequest) (*HostInvokeResponse, error)
    InvokeStream(ctx, req HostStreamRequest) (HostStream, error)
}
type HostAware interface { Host() Host }   // host.go:33;可能返回 nil
```

- `HostInvokeRequest`:`{Method(点分式,如 "tasks.create"), Payload(map[string]any), IdempotencyKey, Metadata}`。
- 插件在 `Init` 中类型断言 `HostAware` 获取 `Host`(Core 版本不支持时为 nil)。
- 可调 method 即 Core HostService 的 19 个(见 [`core-runtime.md`](core-runtime.md))。

## Capability(扁平,现状)

`sdkgo/capability.go`:

```go
CapabilityHostInvoke       = "host.invoke"            // 允许调 Host.Invoke
CapabilityMiddlewareReadBody = "middleware.read_body" // Middleware 收到 body
// 动态:host.invoke.<method>,如 host.invoke.tasks.create
```

插件在 `PluginInfo.Capabilities` 声明(`CapabilityForHostMethod("tasks.create")` 生成 `host.invoke.tasks.create`)。SDK 仅做类型自检,**真正授权由 Core 方法注册表在启动时执行**。
> 现状:扁平方法级 capability + 单一 Invoke 通道。目标愿景的版本化分组(`host.routing@1`/`host.tasks@1`)未实现。

## Metadata 约定(声明式扩展点,现状)

SDK 的 `PluginInfo.Metadata` / `RouteDefinition.Metadata` / `ModelInfo.Metadata`(均为 `map[string]string`)是无需改 ABI 的扩展点。当前 Core 识别的约定键:

| 键 | 位置 | 含义 | Core 消费点 |
|---|---|---|---|
| `metadata_only` | RouteDefinition | `"true"` = 只读元信息路由,跳过账号调度/计费 | `Manager.IsMetadataOnlyRoute` |
| `error_format` | RouteDefinition(优先)/ PluginInfo | 对外错误体格式:`openai`(默认)/ `anthropic`;路由级用于混合协议网关(如 openai 插件的 `/v1/messages`) | `Manager.ErrorFormat` → `protocolError` |
| `family` | ModelInfo | 限流冷却的模型家族键(如 `gpt-image`) | `Manager.ModelFamily` → scheduler FamilyCooldown |
| `scheduling_model` | ModelInfo | 调度选号时的等价模型映射(精确 ID) | `internal/plugin/scheduling_model.go` |
| `scheduling_model_map` | RouteDefinition | 协议翻译路由的"请求模型 → 调度模型"前缀映射表(JSON:键=模型前缀,最长优先;值=候选列表)。如 openai 插件 `/v1/messages` 声明 claude-\*→GPT | `Manager.SchedulingModelMap` → `scheduling_model.go` |
| `account.oauth_plans` | PluginInfo | 账号 OAuth 套餐识别规则(JSON) | 账号管理 UI / 调度过滤 |

新增约定键须同步登记本表;未声明一律回退 Core 的历史默认行为(向后兼容)。

## Manifest(生成式,现状)

`plugin.yaml` 由 `backend/cmd/genmanifest` 从 `PluginInfo` + `Models()` + `Routes()` 自动生成,**禁止手改**。真实结构:

```yaml
id: gateway-openai
name: OpenAI 网关
type: gateway
min_core_version: 1.0.0
gateway:
  platform: openai
  mode: simple
  routes: [...]
  models: [...]
  account_types: [...]
```

> 现状:**无** `manifest_version` / `provides` / `requires.host` 字段(目标愿景的 Manifest v2 未实现)。capability 在 Go 代码 `PluginInfo.Capabilities` 声明,不在 manifest。

## 运行时桥(`airgate-sdk/runtimego/grpc/`)

`gateway_server.go` 把 `sdk.GatewayPlugin` 包成 gRPC server;`host_client.go` 把 `CoreInvokeServiceClient` 包成 `sdk.Host`;`go_plugin.go` 集成 hashicorp/go-plugin。

Forward 全链路:Core gRPC client → `GatewayGRPCServer.Forward(pb)` → 转 `sdk.ForwardRequest` → 插件 `Forward()` →(可能)`Host.Invoke` → `CoreInvokeServiceClient.Invoke` → Core → 返回 → 插件返回 `sdk.ForwardOutcome` → 转 `pb.ForwardOutcome` → Core 调度/计费。
