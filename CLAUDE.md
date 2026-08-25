# airgate-core — Claude 开发指南

> 叠加于根 `../CLAUDE.md`。开发前先读「生态边界」「🚫 红线」。
> 分层落点、错误映射、鉴权、编码约定的**权威细则见 skill `core-dev`**（后端 `backend.md` / 前端 `frontend.md` / 转发·调度·计费 `forwarding.md` / 任务 `task.md` / 插件契约 `plugin-contract.md`）——本文件只保留边界与红线，不复制细则。

## 生态边界（动手前先归位）

全生态职责速查表见根 `../CLAUDE.md`「生态边界」，本节为 core 视角。

**Core 负责**：身份/用户、账号、API Key、分组与路由、账号调度、转发管线（鉴权/限流/failover）、计费、任务/资产、模型目录、插件生命周期、后台 UI 框架。

**Core 不负责（出现即越界）**：

| 不写什么 | 归谁 | core 的正确做法 |
|---|---|---|
| 外部协议格式（OpenAI/Anthropic 的请求/响应/SSE/错误体形态） | Gateway 插件 | 转发层只认 `ForwardOutcome`；对外错误格式按插件 `Metadata["error_format"]` 声明选择格式化器，未声明回退 OpenAI 兼容默认 |
| 上游认证（OAuth/token/session/TLS 指纹） | Provider（现混于网关插件） | 凭证只加密存取、不解释；刷新经 `ForwardOutcome.UpdatedCredentials` 回写 |
| 插件产品页面 | UI 插件 | core 仅提供挂载点（FrontendWidgets slot / FrontendPages）与资产服务 |

**边界纪律（新增/改动代码必须遵守）**：

1. **禁止新增 provider/模型字符串特判**。协议/平台差异一律经插件 Metadata 约定键声明，Core 只留与厂商无关的默认兜底。约定键登记于 skill `core-dev` 的 Metadata 约定表；**现存硬编码越界**（`scheduling_model.go` 的 claude/openai 翻译映射、`selector.go`/`billing/image_pricing.go`/`asset_cleanup.go` 的图像 provider 假定）已逐条登记 skill `core-dev`「技术债」，**勿加深**，越界判定标准见该节。
2. **HostService 是插件调 core 的唯一通道**（`internal/plugin/host_service.go`，现 19 个 method），已登记"单通道过宽"债务。新增 method 前先确认属**跨插件的平台能力**，单插件业务勿入；新增后同步登记 skill `core-dev`。
3. **core 禁止 import 插件包**，识别插件仅经 SDK 接口 + manifest；core 代码勿绑定具体插件名（`/status` 反代目标经 config `plugins.status_plugin` 指定即为此例）。
4. 触碰技术债登记的热点时**勿加深**，治理按排期，无需顺手重构。
5. 改动涉及转发/契约/计费/调度/任务，**同步更新 skill `core-dev`**（防漂移红线）。

## 🚫 红线

- **分层**：handler 不写业务逻辑；service 不碰 gin/http（不出现 `*gin.Context`/HTTP 状态码/`response.*`）；service 不直连 ent——经本包 `Repository` 接口，实现置于 `internal/infra/store/`。落点表与标准改动顺序见 skill `core-dev`「backend」。
- **改 `ent/schema/` 后须 `make ent` 并提交生成代码**，否则 `make ci` 的 `verify-ent` 失败；生成代码（`ent/` 非 `schema/` 部分）不可手改。
- **新接口走 dto + mapper**，handler 勿手拼 `map[string]any` 作响应（统计/SSE 等沿用同域写法除外）。
- **上游账号失效用 422，禁止返回 401**——前端 401 全局拦截会登出当前管理员（见 `ErrReauthRequired`）。
- **API Key 路由错误用 `abortWithOpenAIError()`**，不用 `response.*`。
- **复用优先**：开发前先读同域现有实现（首选 `account` 全链路）。注释中文；`_test.go` 同包、表驱动。

## 装配点（新增 handler/路由须两处接线）

- `internal/bootstrap/http_handlers.go` — `NewHTTPHandlers` 内按 `store → service → handler` 构造，挂载至 `HTTPHandlers`。
- `internal/server/router.go` — `registerRoutes()` 选对分组（`v1`/`userGroup`/`adminGroup`/`extGroup`）注册路由。

## 子系统边界

- `internal/scheduler/` — 账号调度/并发/家族冷却/sticky 路由，瞬态状态在 Redis。
- `internal/billing/` — 用量计费、费率、记账（`calculator`/`rate`/`recorder`）。
- `internal/plugin/` — 插件生命周期、转发管线、HostService 宿主能力、任务执行、资产服务；core 调插件经此，反向仅经 `Host.Invoke`。
- `internal/routing/` — 模型 → 账号选择。
- 任务状态机见 skill `core-dev`「任务状态机」（`task.md`）。

## 前端（`web/`）

React 19 + Vite + TanStack Query + Tailwind + `@doudou-start/airgate-theme`。三层落点（pages/shared/app）、数据流、路由守卫与懒加载约定见 skill `core-dev`「frontend」；新页面参照 `pages/admin` 现有页面。

## 常用命令（`airgate-core/`）

```bash
make dev            # 全量热重载（后端 air + 前端 vite + 插件 watch）
make ent            # 改 ent/schema 后重新生成
make ci             # 提交前完整自检（链路见 skill airgate-ci-check）
```

单包测试（`backend/`）：`go test ./internal/app/account/... -run TestXxx -v -count=1`

## 相关 skill / 文档

- core 全栈开发（后端/前端/子系统） → skill `core-dev`
- 提交前自检 → skill `airgate-ci-check`
