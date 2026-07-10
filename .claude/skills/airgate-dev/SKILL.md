---
name: airgate-dev
description: airgate-core（standalone-gateway 分支）开发指南：架构、子系统边界、分层范式、前端落点与常用命令。开发本仓任何后端/前端功能、改转发/计费/渠道/任务子系统前先读。
---

# airgate-dev — standalone-gateway 分支开发指南

> 本分支是独立单体网关（渠道管理式），与 master 插件架构长期分叉、不合回。
> 仓库外文档（monorepo 根 CLAUDE.md、skill `core-dev`/`develop-plugin`）描述的是 master 插件线，对本分支一律不适用。

## 🚫 红线

- **分层五件套**：dto → handler（不写业务）→ service（不碰 gin/http、不 import ent）→ store（唯一 import ent）→ ent/schema。
- **改 `ent/schema/` 后须 `make ent` 并提交生成代码**；生成代码不可手改。
- **装配两处接线**：`internal/bootstrap/http_handlers.go` + `internal/server/router.go` `registerRoutes()`。
- **新接口走 dto + mapper**，handler 勿手拼 map 响应。
- **转发路由（/v1、/v1beta、/suno）错误一律按入口协议原生形态**（`internal/relay/errfmt` 分发），不用 `response.*`；管理面照旧 `response.*`。
- **渠道 api_keys 明文永不出现在任何 API 响应**（只出 count + 尾 4 位 hint）；加解密用 `internal/auth`（AES-256-GCM），在 service 层做。
- **relay 子系统（registry/task 等）禁止 import ent 与 app 包**，经窄接口注入。
- 复用优先（新领域参照 channel 域全链路）；注释中文、不写复述代码的冗余注释；`_test.go` 同包、表驱动。
- 需求/架构变更**同步更新本 skill 与 `README.md`**，防止文档漂移。

## 架构

管理员配置**渠道**（channel = 协议类型 + base_url + api_keys + 模型列表），用户拿 sk- key 按协议调对应端点，core 内置 adaptor **纯透传直发**上游（零翻译）、按**模型价目表**计费。入站端点按协议分树，只路由到同协议渠道：

```
请求（middleware.APIKeyAuth 鉴权，入站按协议分树：
      openai    → POST /v1/chat/completions、/v1/responses、/v1/images/{generations|edits}（openai_compatible/custom 渠道）
      anthropic → POST /v1/messages、/v1/messages/count_tokens※（anthropic 渠道）
      gemini    → POST /v1beta/models/{model}:generateContent|:streamGenerateContent|:predict|:countTokens※（gemini 渠道）
      ※ countTokens 两端点零计费；images/predict 在 per_request_price>0 时按次×产出张数计费）
  → internal/relay/pipeline：余额预检 → user/key 并发闸门 → failover≤3
      { registry.Pick(分组,模型,协议)（协议过滤 + priority 分档 + weight+10 加权随机 + 多 key 轮询）
        → adaptor 透传直发 HTTP → outcome 判定（429 换渠道 / 401·403 自动禁用 / 5xx 换渠道）}
  → relay/pricing（token×价目表）→ billing.Calculate 三管道 → recorder → usage_log

异步任务分树（internal/relay/task，与 pipeline 并列的独立子系统）：
      openai_video → POST /v1/videos、GET /v1/videos/{id}、GET /v1/videos/{id}/content（openai_video 渠道）
      suno         → POST /suno/submit/{music|lyrics}、POST /suno/fetch、GET /suno/fetch/{id}（suno 渠道）
  提交：缺价预检（按次价或 pricing_extra.video.per_second×时长估价）→ 余额预扣（同步扣 user.balance）
  → failover≤3（同 outcome 判定）→ task 表落库；查询读本地快照不穿透上游；
  后台轮询器（10s tick，CAS 状态刷新 + 超时清扫 task_timeout_minutes 默认 30）终态结算：
  成功按实际用量差额多退少补 + 落 usage_log（SkipBalanceCharge 防双扣），失败/超时全额退款
  （balance_log 幂等键防双退）。
```

## 子系统边界

- `internal/relay/registry` — 渠道内存快照与调度（Pick/NextKey/Mark*，Pick 按入口协议过滤渠道 Type）；禁止 import ent 与 app 包，经 Loader/Persister 接口（由 channel service 实现）取数落库。
- `internal/relay/adaptor` — 协议适配（openai_compatible/anthropic/gemini/custom），**零翻译纯透传**：只做上游 URL 拼接、认证头、渠道模型名重写、param_override、各协议响应的 usage 提取归一化（计量不是翻译，须精确保留）；请求/响应体原样透传，不做任何跨协议翻译；调度/重试/禁用/计费一律在 pipeline。
- `internal/relay/pipeline` — 转发主循环、outcome 判定、SSE 透传（原生协议流经透传型 usage 观察器旁路计量）、错误体（按入口协议原生形态，errfmt 分发）、gateway settings 读取。
- `internal/relay/task` — 异步任务子系统（视频/音乐「提交-轮询」型转发）：flow 提交主循环、poller 后台轮询与结算、平台 adaptor（openaivideo/suno）。**与零翻译红线的边界**：提交体仍透传（入口协议=渠道协议，adaptor 只做 URL/认证/模型重写）；查询响应不透传——读本地 task 表快照、adaptor 做状态归一化后按入口协议重建（计量与状态提取，口径同 errfmt 的「语义保留、载体重建」）。禁止 import ent 与 app 包：落库经 Store、余额动账经 BalanceOps 窄接口注入（server 层适配 app/user）。
- `internal/relay/outcome` — 上游 attempt 判定表与出口脱敏（429/401·403/5xx → 换渠道/禁用），pipeline 与 task 共用的唯一事实源。
- `internal/relay/errfmt` — 错误体的协议形态渲染（openai/anthropic/gemini/suno），pipeline、task 与鉴权中间件共用。网关自产错误与**上游错误**都经此包渲染：上游错误解析出语义（message/type/code）后按入口协议重建（语义保留、载体重建，非字节透传），HTTP 状态码保留上游原值。
- `internal/relay/pricing` — 价目表缓存 + token→cost 纯函数。
- `internal/billing` — 三管道计费（actual=total×billing_rate 扣余额；billed=total×sell_rate 累加 key 用量；渠道成本=total×account_rate_multiplier 快照列查询期现算、不落列）与异步记账。
- `internal/scheduler` — 仅剩 ConcurrencyManager/RPMCounter（Redis 限流原语，渠道/用户/key 维度）。

## 新增后端领域套路

1. `ent/schema/` 建表 → `make ent`；
2. `infra/store/<domain>_store.go`（唯一 import ent）→ `app/<domain>`（Repository 接口 + service）→ `server/dto` + `server/handler`（dto + mapper，不手拼 map）；
3. 两处接线：`internal/bootstrap/http_handlers.go`（store→service→handler）+ `internal/server/router.go` `registerRoutes()`；
4. 参照 channel 域全链路复用现有范式；数据库迁移/修复归 `bootstrap.Migrate` 的定点修复清单（preMigrationFixups/legacyFixups），不进 main.go。

## 前端（web/）

React 19 + Vite + TanStack Query + Tailwind。三层落点 pages/shared/app；新页面参照 `pages/admin/ChannelsPage` 与 proxy/group 域范式；统一 `useCrudMutation`；i18n 键 zh/en 同步（`src/i18n/{zh,en}.json`）。

## 常用命令

```bash
make ent            # 改 ent/schema 后重新生成
make ci             # 提交前自检（lint + test + ent 漂移检查 + 编译）
make dev            # 前后端热重载
cd backend && go test ./internal/relay/... -count=1   # relay 子系统测试
```
