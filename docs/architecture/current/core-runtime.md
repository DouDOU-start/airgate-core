# Core 运行时(现状)

> **现状文档** · 描述 `airgate-core/backend` 的实际实现。改动涉及本文所述架构时须同步更新。
> 路径均相对 `airgate-core/backend`。

## 后端分层(端口-适配器)

```
dto → handler(+_mapper/_routes) → service(app/<domain>) → Repository 接口 ← store(infra/store) → ent
```

| 层 | 位置 | 职责 |
|---|---|---|
| DTO | `internal/server/dto/<domain>.go` | 请求/响应结构 |
| Handler | `internal/server/handler/<domain>_handler{,_routes,_mapper}.go` | 绑定校验 → 调 service → `toXResp` 映射 → `response.*` |
| Service | `internal/app/<domain>/{service,types,errors}.go` | 业务逻辑;`Repository` 接口定义于本包 |
| Store | `internal/infra/store/<domain>_store.go` | `Repository` 的 ent 实现(仅此层 import ent) |
| Schema | `ent/schema/<entity>.go` | DB 表 |

**装配**:`internal/bootstrap/http_handlers.go`(`NewHTTPHandlers` 按 store→service→handler 构造)+ `internal/server/router.go`(`registerRoutes` 集中注册)。
详细开发规则见 `../../../CLAUDE.md`(core) 与 skill `core-backend-feature`。

## 转发管线

入口 `internal/plugin/forwarder.go` 的 `Forward(c *gin.Context)`(forwarder.go:81):

1. **checkBalance**(余额预检,forwarder.go:86)——不扣款,只挡余额不足。
2. **只读元信息快车道**(forwarder.go:106)——`/v1/models`、`/v1/images/tasks` 等由插件本地合成响应,**跳过**账号选号、并发闸门、failover、计费(路径列表 `quota.go:40` `isMetadataOnlyPath`)。
3. **acquireClientQuota**(forwarder.go:112)——客户端(API Key)级 RPM/并发闸门。
4. **failover 循环**(最多 `maxFailoverAttempts=3`,forwarder.go:59):
   - `pickAccount` — scheduler 选号(见下)。
   - `acquireAccountSlot` — 账号并发槽;全满则排队,最多 `queueWaitTimeout=60s`(forwarder.go:63)。
   - `Plugin.Forward()` — gRPC 调网关插件 → 上游。
   - `scheduler.Apply` — 按返回的 `ForwardOutcome.Kind` 更新账号状态/冷却。
   - 账号级失败(限流/失效/上游抽风)且未超次数 → 换账号重试。
5. **计费 + 记录** — 三管道计费(见下)+ `recorder` 写 `usage_log`。

**Middleware**:`OnForwardBegin` **只在首次 attempt** 调用(避免 failover 污染审计计数,forwarder.go:79)。

### `plugin/` 其他模块

转发管线之外,`internal/plugin/` 还包含以下子系统:

| 分组 | 文件 | 职责 |
|---|---|---|
| 资产管理 | `asset_cleanup.go`、`asset_migration.go`、`asset_storage.go` | 插件资产的存储、迁移与清理 |
| 任务服务 | `task_service.go`、`task_input_assets.go`、`task_asset_cleanup.go` | 异步任务执行、任务输入资产处理与清理 |
| 图片定价 | `image_pricing.go` | 图片固定价匹配与计费覆盖 |
| 插件管理器 | `manager_assets.go`、`manager_background.go`、`manager_catalog.go`、`manager_install.go`、`manager_runtime.go`、`manager_tasks.go`、`manager_plugin_db.go` | Manager 按职责拆分:资产同步、后台任务、模型目录、安装/卸载、运行时生命周期、任务调度、插件 DB 操作 |
| 插件市场 | `marketplace.go` | 插件发现与市场元信息 |
| 扩展代理 | `extension_proxy.go` | 扩展插件的 HTTP 代理转发 |
| 用量适配 | `usage_adapter.go` | SDK `Usage` → 计费 `CalculateInput` 的转换适配 |

## 插件判决模型 ForwardOutcome

插件 `Forward()` 返回 `sdk.ForwardOutcome`(定义见 `airgate-sdk/sdkgo/outcome.go:133`),Core 据此决策:

| 字段 | 含义 |
|---|---|
| `Kind` | 判决(见下),**插件唯一裁决依据** |
| `Upstream` | 上游响应快照(StatusCode/Headers/Body) |
| `Usage` | 计费数据(成功时必填) |
| `Duration` / `RetryAfter` / `Reason` | 耗时 / 冷却时长 / 原因 |
| `UpdatedCredentials` | 凭证刷新(如 OAuth token 轮换) |

**OutcomeKind**(outcome.go:13):`OutcomeUnknown`(零值,Core 保守处理)/ `OutcomeSuccess`(2xx,Usage 必填)/ `OutcomeClientError`(4xx,错在请求)/ `OutcomeAccountRateLimited`(429,冷却后恢复)/ `OutcomeAccountDead`(401/403,需人工)/ `OutcomeUpstreamTransient`(5xx/抖动,账号无责)/ `OutcomeStreamAborted`(流中途断)。

## HostService(插件 → Core 反向调用)

`internal/plugin/host_service.go`。插件经单一 `CoreInvokeService.Invoke/InvokeStream` 通道,按 method 字符串调用。**当前 18 个 method**(host_service.go:170-188):

| 分组 | method |
|---|---|
| 调度 | `scheduler.select_account`、`scheduler.report_account_result` |
| 探测 | `probe.forward`(跳过计费/限流,见下) |
| 转发 | `gateway.forward`(走完整计费管线) |
| 元数据 | `groups.list`(可选 `{public_only, user_id}` 过滤状态页可见分组,返回含 `note`/`status_visible`)、`platforms.list`、`models.list`、`users.get` |
| 资产 | `assets.store`、`assets.store_url`、`assets.get_url`、`assets.get_bytes`、`assets.delete` |
| 任务 | `tasks.create`、`tasks.update`、`tasks.get`、`tasks.list`、`tasks.delete` |

**capability 绑定时序**:`pluginHostHandle` 在 capability 未设置时拒绝所有 RPC;插件 spawn 后 `SetCapabilities` 才激活(host_service.go)。`Init()` 阶段不能调 host RPC。
> 现状:HostService 单一接口暴露全部能力(目标愿景是按 `host.routing@1` 等版本化分组注入,未实现)。见 [`tech-debt.md`](tech-debt.md)。

## 计费三管道

`internal/billing/calculator.go`,三条**独立**管道(calculator.go:72-83,Calculate 函数:86):

| 管道 | 公式 | 落点 |
|---|---|---|
| `actual_cost` | `total_cost × billing_rate` | 扣 `User.balance`(平台真实成本,reseller) |
| `billed_cost` | `total_cost × sell_rate` | 累加 `APIKey.used_quota`(终端客户可见;`sell_rate=0` 回退 `actual_cost`) |
| `account_cost` | `total_cost × account_rate` | 写 `usage_log`(仅"账号计费"统计,不影响余额) |

**图片固定价覆盖**:`ImageBillingCostOverride` / `ImageBilledCostOverride` / `OutputBillingCostOverride`,绕过倍率按固定单价计图片(calculator.go:35-51)。

### `billing/` 辅助模块

| 文件 | 职责 |
|---|---|
| `rate.go` | 费率解析(billing_rate / sell_rate / account_rate 的查找与回退逻辑) |
| `recorder.go` | 用量异步记录(写 `usage_log` 表) |
| `image_pricing.go` | 图片定价规则(按分辨率/质量匹配固定单价) |

## 调度(`internal/scheduler/`)

选号流程(`selection.go`):模型路由 → 状态过滤 → 软约束(RPM/window/session) → sticky 会话 → 负载均衡。

- **状态机**(`state.go`):账号健康/降级/冷却。
- **FamilyCooldown**(`family.go`):限流冷却按 **(account, model_family)** 维度,而非整账号——隔离某模型限流不牵连其他流量。`ModelFamily`(family.go:25)**硬编码**仅 OpenAI `gpt-image-*` 折叠为 `gpt-image`(family.go:27),其余按 model 本身。见 [`tech-debt.md`](tech-debt.md)。
- **RPM 非对称回退**(`scheduler/rpm.go`/`scheduler/scheduler.go`):调度阶段占 RPM,执行失败立即回退;仅真正成功后 `Apply` 才保留计数。
- **scheduling model 映射**(`plugin/scheduling_model.go:15`):仅当 `platform=openai` 且走 Anthropic Messages 路径时,把 Claude 模型映射为 GPT 选号(支持 env override)。见 [`tech-debt.md`](tech-debt.md)。

### `scheduler/` 辅助模块

| 文件 | 职责 |
|---|---|
| `rpm.go` | RPM 计数(Redis STRING + 分钟粒度 key) |
| `concurrency.go` | 账号并发槽管理 |
| `session.go` / `sticky.go` | 会话亲和 / sticky 路由 |
| `windowcost.go` | 滑动窗口成本统计 |
| `schedulability.go` | 账号可调度性判定 |
| `routecache.go` | 路由缓存 |
| `requirements.go` | 请求需求过滤(如 `NeedsImage`) |
| `admin.go` | 调度器管理接口 |

## 路由(`internal/routing/` + `internal/server/dynamic_router.go`)

- `routing/selector.go` 按 `Requirements`(如 `NeedsImage`)过滤候选分组。`GroupMatchesRequirements`(selector.go:105)**硬编码** OpenAI 图片授权(selector.go:109)。见 [`tech-debt.md`](tech-debt.md)。
- `server/dynamic_router.go`:Gin 不支持动态移除路由,用 catch-all `/api/v1/<path>` + 内部路由表,携带 API Key 时转发到 Forwarder。

## 隐性机制(此前文档缺口)

- **Middleware LIFO**(`middleware.go`):`OnForwardBegin` 按 Priority 升序进,`OnForwardEnd` 降序出(栈语义);Begin 返回 `DecisionDeny` 时 Core 直接拒绝。
- **ProbeForward 隔离**(`host_service.go`):探测转发跳过 `usage_log`、扣款、RPM/并发限流,**但仍** `ReportResult` 让状态机更新——使降级账号有机会被探测恢复,且探测不被限流挡掉。
- **资产双路径**(`router.go`):`/plugins/:name/assets/*` dev 从 vite watch 目录读、生产从 `data/plugins/<id>/assets` 读。
