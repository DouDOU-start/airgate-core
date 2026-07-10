---
name: airgate-dev
description: airgate-core（standalone-gateway 分支）开发指南：红线、架构、子系统边界、开发套路。开发本仓任何后端/前端功能前先读。
---

# airgate-dev

独立单体网关：管理员配渠道（协议类型 + base_url + api_keys + 模型列表），用户拿 sk- key 按协议调对应端点，内置 adaptor 零翻译透传直发上游，按模型价目表计费。与 master 插件线长期分叉，master 线文档一律不适用。

## 🚫 红线

- 分层五件套：dto → handler（不写业务）→ service（app/<domain>，不碰 gin/http、不 import ent）→ store（infra/store，业务链路唯一 import ent 层）→ ent/schema。
- 改 `ent/schema/` 后须 `make ent` 并提交生成代码；生成代码不可手改。
- 装配两处接线：`internal/bootstrap/http_handlers.go` + `internal/server/router.go` `registerRoutes()`。
- 转发路由（/v1、/v1beta、/suno）错误一律经 `internal/relay/errfmt` 按入口协议出原生形态；管理面用 `response.*`。
- 渠道 api_keys 明文永不出现在任何 API 响应（只出 count + 尾 4 位 hint）；加解密用 `internal/auth`，在 service 层做。
- relay 子系统禁止 import ent 与 app 包，依赖经窄接口注入（参照 registry 的 Loader/Persister、task 的 Store/BalanceOps）。
- 复用优先；注释中文；`_test.go` 同包、表驱动（import cycle 时才用外部 `_test` 包）。
- 架构变更同步更新本 skill 与 `README.md`。

## 请求链路

- **同步转发**（internal/relay/pipeline）：APIKeyAuth 鉴权 → 余额预检 → user/key 并发闸门 → failover≤3｛registry.Pick（协议过滤 + priority 分档 + weight 加权随机 + 多 key 轮询）→ adaptor 透传直发 → outcome 判定（429 换渠道 / 401·403 自动禁用 / 5xx 换渠道）｝→ pricing 计费 → recorder 异步落账。
  端点：openai `/v1/chat/completions`、`/v1/responses`、`/v1/images/*`；anthropic `/v1/messages*`；gemini `/v1beta/models/{model}:{动词}`。countTokens 零计费；images/predict 按次×张数。
- **异步任务**（internal/relay/task，与 pipeline 并列）：视频 `/v1/videos*`（openai_video 渠道）、音乐 `/suno/*`（suno 渠道）。提交时按估价预扣余额（按次价或 pricing_extra.video.per_second×时长）→ task 表落库；查询读本地快照不穿透上游；轮询器 10s tick CAS 刷新状态 + 超时清扫（task_timeout_minutes 默认 30），终态结算：成功差额多退少补 + 落 usage_log（SkipBalanceCharge 防双扣），失败/超时全额退款（幂等键防双退）。

## 子系统边界

- `relay/registry` — 渠道内存快照与调度（Pick 按入口协议过滤渠道 Type）。
- `relay/adaptor` — 协议适配，零翻译：仅 URL 拼接、认证头、模型名重写、param_override、usage 提取归一化；调度/重试/禁用/计费一律在 pipeline。
- `relay/task` — 任务子系统：提交体透传；查询响应读本地快照 + 状态归一化重建（唯一允许的非透传面）。
- `relay/outcome` — 上游 attempt 判定表与出口脱敏，pipeline 与 task 共用。
- `relay/errfmt` — 错误体协议形态渲染（openai/anthropic/gemini/suno）；上游错误解析语义后按入口协议重建，状态码保留原值。
- `relay/pricing` — 价目表缓存（token 单价 / 按次价 / 视频按秒价）。
- `billing` — 三管道计费（actual 扣余额 / billed 累 key 用量 / 渠道成本快照现算）+ 异步记账。
- `scheduler` — Redis 限流原语（并发闸门 / RPM）。

## 开发套路

- 新后端领域：ent/schema → `make ent` → store → app service（Repository 接口）→ dto + handler（mapper，勿手拼 map）→ 两处接线。参照 channel 域。
- 数据库迁移/修复归 `bootstrap.Migrate` 修复清单（preMigrationFixups / legacyFixups），不进 main.go。
- 前端（web/）：React 19 + Vite + TanStack Query；落点 pages/shared/app；参照 `pages/admin/ChannelsPage`（复杂域）或 `GroupsPage`（简单域）；统一 `useCrudMutation`；i18n 键 zh/en 同步。

## 命令

```bash
make ent   # 改 schema 后重新生成
make ci    # 提交前自检（lint + test + ent 漂移 + 编译）
make dev   # 前后端热重载
```
