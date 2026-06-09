# airgate-core — Claude 开发指南

> 叠加于根 `../CLAUDE.md`，仅述 core 内部分层与流程。开发前先读「🚫 红线」，再按「分层」定位。架构背景见 `docs/architecture/ecosystem-v2.md`。

## 🚫 红线

- **handler 不写业务逻辑**：仅做参数绑定/校验、调 `service`、`toXResp` 映射、`response.*` 返回；业务规则一律进 `app/<domain>/`。
- **service 不碰 gin/http**：`app/<domain>/` 内不出现 `*gin.Context`、HTTP 状态码、`response.*`；入参用本包 `Input/Filter`，出参用 domain 类型 + 哨兵错误。
- **service 不直连 ent**：经本包定义的 `Repository`（及 `*Reader/*Writer`）接口访问数据，实现置于 `internal/infra/store/`；handler/service 禁止 `import ent`。
- **改 `ent/schema/` 后须 `make ent` 并提交生成代码**，否则 `make ci` 的 `verify-ent` 失败；生成代码（`ent/` 非 `schema/` 部分）不可手改。
- **新接口须走 dto + mapper**，handler 内勿手拼 `map[string]any` 作响应（统计/SSE 等无 DTO 的临时结构除外，沿用同域写法）。
- **复用优先**：开发前先读同域现有实现（首选 `account`），沿用其结构。
- 注释用中文；`_test.go` 与被测代码同包、表驱动。

## 后端分层（请求自上而下）

| 层 | 位置 | 职责 |
|---|---|---|
| DTO | `internal/server/dto/<domain>.go` | 请求/响应结构，带 `json`/`binding` tag |
| Handler | `internal/server/handler/<domain>_handler{,_routes,_mapper}.go` | 绑定校验 → 调 service → `toXResp` 映射 → `response.Success/Error`；`_mapper.go` 置 `toXResp`，`_routes.go` 置其余端点 |
| Service | `internal/app/<domain>/{service,types,errors}.go` | 业务逻辑；`NewService(deps…)` 注入接口依赖；`types.go` 定义 `Input/Filter/Result` 与 `Repository` 接口；`errors.go` 定义哨兵错误 |
| Store | `internal/infra/store/<domain>_store.go` | `Repository` 接口的 ent 实现（仅此层 import ent） |
| Schema | `ent/schema/<entity>.go` | DB 表/字段/索引；改后 `make ent` |

请求流向：`dto → handler → service → Repository（接口）←实现 store → ent`。
完整样例：`account` 全链路——`server/dto/account.go` · `server/handler/account_handler.go` · `app/account/service.go`(+`types.go`/`errors.go`) · `infra/store/account_store.go`。

**装配点（新增 handler/路由须在两处接线）**：
- `internal/bootstrap/http_handlers.go` — `NewHTTPHandlers` 内按 `store → service → handler` 构造，挂载至 `HTTPHandlers`。
- `internal/server/router.go` — `registerRoutes()` 集中注册路由（`v1`/`adminGroup`/`extGroup` 分组），引用 `handlers.<X>.<Method>`。

`response` 包统一出口：`Success / Error / BadRequest / BindError / NotFound / Forbidden / Unauthorized / PagedData`（`internal/server/response/`）。

## 子系统边界

- `internal/scheduler/` — 账号调度/并发/家族冷却/sticky 路由，瞬态状态在 Redis。
- `internal/billing/` — 用量计费、费率、记账（`calculator`/`rate`/`recorder`）。
- `internal/plugin/` — 插件生命周期、转发、宿主能力、资产服务；core 经此调用插件，反向仅经 `Host.Invoke`。
- `internal/routing/` — 模型 → 账号选择。
- 任务状态机见 `docs/architecture/task-state-machine.md`。

## 后端编码约定（高频）

- **错误处理**：service 返回包内哨兵错误（`app/<domain>/errors.go`，如 `ErrAccountNotFound`）；handler 经本类型 `handleError(logMessage, publicMessage, err) (int, string)` 将 error `errors.Is` 映射为 HTTP code + 对外消息，再 `response.Error`。handler 勿硬编码状态码、勿将内部 err 直接返回前端。
  - ⚠️ 上游账号 OAuth 失效等"业务不可处理"用 **422**，禁止返回 401——前端有 401 全局拦截，会登出当前管理员（见 `account_handler.go` 的 `ErrReauthRequired`）。
- **日志**：统一 `log/slog`，内部细节进日志、对外仅回 `publicMessage`，不泄露堆栈/内部错误。
- **敏感凭证**：账号凭证、API key 须加密存储（`internal/auth/crypto.go` 的 `EncryptAPIKey`/`DecryptAPIKey`），不明文落库、不写日志。
- **context 透传**：service/store 方法首参 `ctx context.Context`，自 `c.Request.Context()` 透传；勿新建 `context.Background()`（脱离请求的后台任务除外）。
- **分页/时区**：分页入参 `dto.PageReq`、出参 `response.PagedData(...)`；时间 UTC 存储，对外展示用北京时区（参考 `account_handler.go` 的 `beijingTZ`）。

## 前端（`web/`，React 19 + Vite + TanStack Query）

| 层 | 位置 | 职责 |
|---|---|---|
| 页面 | `web/src/pages/{admin,user,setup}` | 路由页面组件 |
| 复用 | `web/src/shared/{api,hooks,components,ui,columns}` | API 封装、查询 hook、通用组件 |
| 装配 | `web/src/app/{router,providers,layout}` | 路由、Provider、布局、插件前端加载 |

约定：服务端状态用 TanStack Query，query key 统一于 `shared/queryKeys.ts`；HTTP 经 `shared/api`；样式用 Tailwind + `@doudou-start/airgate-theme`。新页面参照现有 `pages/admin` 页面。详见 skill `core-frontend-page`。

## 常用命令（`airgate-core/`）

```bash
make dev            # 全量热重载（后端 air + 前端 vite + 插件 watch）
make dev-backend    # 仅后端
make ent            # 改 ent/schema 后重新生成
make lint && make test   # 提交前快检
make ci             # 完整 CI（lint + test + vet + verify-ent + build）
```

单包测试（`backend/`）：`go test ./internal/app/account/... -run TestXxx -v -count=1`

## 相关 skill

- 后端接口/领域逻辑 → `core-backend-feature`（含 Ent 变更）
- 后台前端页面 → `core-frontend-page`
- 提交前自检 → `airgate-ci-check`
