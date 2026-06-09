# airgate-core — Claude 开发指南

> 本文件叠加在 monorepo 根 `../CLAUDE.md` 之上，只讲 **core 内部的分层与流程**。
> 动手前务必读完「🚫 红线」，再按「分层」找落点。整套架构背景见 `docs/architecture/ecosystem-v2.md`。

## 🚫 红线（动手前必读）

- **handler 不写业务逻辑**：只做参数绑定/校验、调 `service`、用 `toXResp` 映射、`response.*` 返回。业务规则一律进 `app/<domain>/`。
- **service 不碰 gin/http**：`app/<domain>/` 里不出现 `*gin.Context`、HTTP 状态码、`response.*`。入参用本包定义的 `Input/Filter`，出参用 domain 类型 + sentinel error。
- **service 不直连 ent**：通过本包定义的 `Repository`（及 `*Reader/*Writer`）接口访问数据；实现放 `internal/infra/store/`。**不要在 handler/service 里 `import ent` 直接查库。**
- **改 `ent/schema/` 后必须 `make ent` 并提交生成代码**，否则 `make ci` 的 `verify-ent` drift 检查必挂。生成代码（`ent/` 下非 `schema/` 部分）不可手改。
- **新接口必须走 dto + mapper**，不在 handler 里手拼 `map[string]any` 当响应结构（统计/SSE 等无 DTO 的临时结构除外，沿用同域现有写法）。
- **复用优先**：动手前先读同域现成实现（首选 `account`），照它的形状改，别另起炉灶。
- 代码注释用**中文**；测试文件 `_test.go` 与被测代码**同包**、表驱动。

## 后端分层（请求自上而下）

| 层 | 位置 | 职责 |
|---|---|---|
| DTO | `internal/server/dto/<domain>.go` | 请求/响应结构，带 `json`/`binding` tag |
| Handler | `internal/server/handler/<domain>_handler{,_routes,_mapper}.go` | 绑定校验 → 调 service → `toXResp` 映射 → `response.Success/Error`；`_mapper.go` 放 `toXResp`，`_routes.go` 放更多端点方法 |
| Service | `internal/app/<domain>/{service,types,errors}.go` | 业务逻辑。`NewService(deps…)` 注入接口依赖；`types.go` 定 `Input/Filter/Result` 与 `Repository` 接口；`errors.go` 定 sentinel error |
| Store | `internal/infra/store/<domain>_store.go` | `Repository` 接口的 ent 实现（仅此层 import ent） |
| Schema | `ent/schema/<entity>.go` | DB 表/字段/索引；改完 `make ent` |

请求流向：`dto → handler → service → Repository(接口) ←实现 store → ent`。
参考样例（最完整）：`account` 全链路 —
`server/dto/account.go` · `server/handler/account_handler.go` · `app/account/service.go`(+`types.go`/`errors.go`) · `infra/store/account_store.go`。

**装配点（加了新 handler/路由必须在这两处接线）**：
- `internal/bootstrap/http_handlers.go` — `NewHTTPHandlers` 里按 `store → service → handler` 构造，挂到 `HTTPHandlers` 结构体。
- `internal/server/router.go` — `registerRoutes()` 集中注册路由（`v1`/`adminGroup`/`extGroup` 等分组），引用 `handlers.<X>.<Method>`。

`response` 包统一出口：`Success / Error / BadRequest / BindError / NotFound / Forbidden / Unauthorized / PagedData`（见 `internal/server/response/`）。

## 子系统边界（各一句话，细节进各自包/文档）

- `internal/scheduler/` — 账号调度/并发/家族冷却/sticky 路由，瞬态状态在 Redis。
- `internal/billing/` — 用量计费、费率、记账（`calculator`/`rate`/`recorder`）。
- `internal/plugin/` — 插件生命周期、转发、Host capability、资产服务。**core 经此调用插件，反向只能经 `Host.Invoke`**。
- `internal/routing/` — 模型→账号选择。
- 任务状态机见 `docs/architecture/task-state-machine.md`。

## 后端编码约定（高频、易违反）

- **错误处理**：service 返回**包内 sentinel error**（`app/<domain>/errors.go`，如 `ErrAccountNotFound`）；handler 用本类型的 `handleError(logMessage, publicMessage, err) (int, string)` 把 error `errors.Is` 映射成 HTTP code + 对外消息，再 `response.Error`。**别在 handler 硬编码状态码、别把内部 err 直接吐给前端**。
  - ⚠️ 上游账号 OAuth 失效等"业务不可处理"用 **422**，**绝不能返回 401**——前端 HTTP 客户端有 401 全局拦截，会把当前管理员踹下线（见 `account_handler.go` 的 `ErrReauthRequired`）。
- **日志**：统一 `log/slog`（`slog.Error/Warn/Info`），内部细节进日志、对外只回 `publicMessage`，不泄露堆栈/内部错误。
- **敏感凭证**：账号凭证、API key 等必须加密存储（`internal/auth/crypto.go` 的 `EncryptAPIKey`/`DecryptAPIKey`），**不明文落库**、不写日志。
- **context 透传**：service/store 方法首参 `ctx context.Context`，从 `c.Request.Context()` 一路传下去；别新建 `context.Background()`（除非确为脱离请求的后台任务）。
- **分页/时区**：分页入参用 `dto.PageReq`、出参用 `response.PagedData(...)`；时间 UTC 存储，对外展示用北京时区（参考 `account_handler.go` 的 `beijingTZ`）。

## 前端（`web/`，React 19 + Vite + TanStack Query）

| 层 | 位置 | 职责 |
|---|---|---|
| 页面 | `web/src/pages/{admin,user,setup}` | 路由页面组件 |
| 复用 | `web/src/shared/{api,hooks,components,ui,columns}` | API 封装、查询 hook、通用组件 |
| 装配 | `web/src/app/{router,providers,layout}` | 路由、Provider、布局、插件前端加载 |

约定：服务端状态用 TanStack Query，query key 统一在 `shared/queryKeys.ts`；HTTP 调用走 `shared/api`；样式用 Tailwind + `@doudou-start/airgate-theme`。新页面先抄一个现成 `pages/admin` 页面。详见 skill `core-frontend-page`。

## 常用命令（从 `airgate-core/`）

```bash
make dev            # 全量热重载（backend air + frontend vite + 插件 watch）
make dev-backend    # 仅后端
make ent            # 改 ent/schema 后重新生成
make lint && make test   # 提交前快检
make ci             # 完整 CI（lint+test+vet+verify-ent+build）
```

单包测试（从 `backend/`）：`go test ./internal/app/account/... -run TestXxx -v -count=1`

## 相关 skill

- 加/改后端接口或领域逻辑 → skill **`core-backend-feature`**（含 Ent 变更子流程）
- 加/改后台前端页面 → skill **`core-frontend-page`**
- 声称"做完"之前 → skill **`airgate-ci-check`**
