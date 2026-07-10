# airgate-core — Claude 开发指南（standalone-gateway 分支）

> **本分支是独立单体网关架构**（渠道管理式），与 master 插件架构长期分叉、不合回。
> **本文件自包含**：仓库外的任何文档（monorepo 根 CLAUDE.md、skill `core-dev` / `develop-plugin` 等）描述的都是 master 插件线，对本分支一律不适用。
> 无插件进程、无 SDK 依赖、无上游账号池；架构与需求以本文件 + `README.md` 为准。

## 架构（本分支）

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
```

## 子系统边界

- `internal/relay/registry` — 渠道内存快照与调度（Pick/NextKey/Mark*，Pick 按入口协议过滤渠道 Type）；禁止 import ent 与 app 包，经 Loader/Persister 接口（由 channel service 实现）取数落库。
- `internal/relay/adaptor` — 协议适配（openai_compatible/anthropic/gemini/custom），**零翻译纯透传**：只做上游 URL 拼接、认证头、渠道模型名重写、param_override、各协议响应的 usage 提取归一化（计量不是翻译，须精确保留）；请求/响应体原样透传，不做任何跨协议翻译；调度/重试/禁用/计费一律在 pipeline。
- `internal/relay/pipeline` — 转发主循环、outcome 判定、SSE 透传（原生协议流经透传型 usage 观察器旁路计量）、错误体（按入口协议原生形态，errfmt 分发）、gateway settings 读取。
- `internal/relay/errfmt` — 错误体的协议形态渲染（openai/anthropic/gemini），pipeline 与鉴权中间件共用。网关自产错误与**上游错误**都经此包渲染：上游错误解析出语义（message/type/code）后按入口协议重建（语义保留、载体重建，非字节透传），HTTP 状态码保留上游原值。
- `internal/relay/pricing` — 价目表缓存 + token→cost 纯函数。
- `internal/billing` — 三管道计费（actual=total×billing_rate 扣余额；billed=total×sell_rate 累加 key 用量；渠道成本=total×account_rate_multiplier 快照列查询期现算、不落列）与异步记账。
- `internal/scheduler` — 仅剩 ConcurrencyManager/RPMCounter（Redis 限流原语，渠道/用户/key 维度）。

## 🚫 红线（本分支仍然有效）

- **分层五件套**：dto（server/dto）→ handler（server/handler，不写业务）→ service（app/<domain>，不碰 gin/http、不 import ent，经本包 Repository 接口）→ store（infra/store，唯一 import ent）→ ent/schema。
- **改 `ent/schema/` 后须 `make ent` 并提交生成代码**；生成代码不可手改。
- **装配两处接线**：`internal/bootstrap/http_handlers.go`（store→service→handler）+ `internal/server/router.go` `registerRoutes()`。
- **新接口走 dto + mapper**，handler 勿手拼 map 响应。
- **转发路由（/v1、/v1beta）错误一律按入口协议的原生错误形态**（`internal/relay/errfmt` 按 EntryProtocol 分发：openai/anthropic/gemini），不用 `response.*`；上游错误解析语义后按入口协议重建（状态码保留上游原值），不做字节透传；管理面照旧 `response.*`。
- **渠道 api_keys 明文永不出现在任何 API 响应**（只出 count + 尾 4 位 hint）；加解密用 `internal/auth`（AES-256-GCM），在 service 层做。
- 复用优先（新领域参照 channel 域全链路）；注释中文、不写复述代码的冗余注释；`_test.go` 同包、表驱动。
- 需求/架构变更**同步更新本文件与 `README.md`**，防止文档漂移。

## 常用命令（`airgate-core/`）

```bash
make ent            # 改 ent/schema 后重新生成
make ci             # 提交前自检
cd backend && go test ./internal/relay/... -v -count=1   # relay 子系统测试
```

## 前端（`web/`）

React 19 + Vite + TanStack Query + Tailwind。三层落点 pages/shared/app；新页面参照 `pages/admin/ChannelsPage` 与 proxy/group 域范式；i18n 键 zh/en 同步。
