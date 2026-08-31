---
name: core-dev
description: airgate-core（standalone-gateway 分支）开发指南：架构、子系统边界、分层范式、前端落点与常用命令。开发本仓任何后端/前端功能、改转发/计费/渠道/任务子系统前先读。
---

# core-dev — standalone-gateway 分支开发指南

> 本分支是独立单体网关（渠道管理式），与 master 插件架构长期分叉、不合回。
> 仓库外文档（monorepo 根 CLAUDE.md、skill `develop-plugin`）描述的是 master 插件线，对本分支一律不适用。

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

**双路径转发**（渠道 + 订阅账号池统一调度）：

| 路径 | 单元 | 协议 | 说明 |
|---|---|---|---|
| A 渠道 | ChannelKey | 零翻译透传 | API Key 上游；adaptor 只做 URL/认证/model 重写 |
| B 账号 | Account | **允许翻译** | OAuth/订阅账号（Codex/Claude/Antigravity/Kimi/xAI/Gemini…）；经 CLIProxyAPI 公开 SDK executor + translator |

管理员配置**渠道**和/或**账号**（均有 priority+weight，绑分组后混合选路）。用户 sk- key 按协议调对应端点，按**模型价目表**计费。

```
请求（middleware.APIKeyAuth 鉴权，入站按协议分树：
      openai    → POST /v1/chat/completions、/v1/responses、/v1/alpha/search※、/v1/images/{generations|edits}
      anthropic → POST /v1/messages、/v1/messages/count_tokens※
      gemini    → POST /v1beta/models/{model}:generateContent|...
  → pipeline：余额预检 → moderation → 并发闸门 → failover≤3
      { 统一选路：ChannelKey 候选 ∪ Account 候选
        → priority 分档 + weight 加权随机
        → 渠道：adaptor 透传 | 账号：cpa.Forward（CPA 翻译+executor）
        → outcome 判定 }
    rate_limited 账号恢复：原子单飞探测；单请求至多一次；完整 2xx 才恢复 active；
    失败按 30s→1m→2m→4m→8m→15m 指数冷却，并拒绝插件旧 allow，保留 Core fallback
  → pricing → billing → usage_log（channel_key_id 或 account_id；无计量 / 中断且无 token 不落库，只 WARN + 失败留痕）

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

- `internal/relay/registry` — 渠道密钥端点内存快照与调度；禁止 import ent 与 app 包。
- `internal/relay/accountreg` — 订阅账号内存快照、状态机、与渠道混合候选；禁止 import ent。限流恢复探测由本层原子 claim + 指数退避，探测失败时所有候选入口（含 Relay Hook 显式 allow）统一隐藏，只有本代完整成功可恢复 active；普通旧在途成功不得覆盖较新的 rate_limited。`ModelsForGroup` 供模型目录聚合。**(账号, 模型) 级 429 冷却**（`model_cooldown.go`，对齐 CLIProxyAPI）：单模型限流只冷却该模型（每窗口最多升一级退避 + 抖动、成功即清、纯运行时不落库），账号全部模型冷却时才升级账号级 rate_limited 进入恢复探测。路由目录只按 EffectivePriority 分桶，rate_limited/disabled 由 `RouteCandidate` 每次选取实时复核，状态抖动**不**递增 routeVersion（防全量目录失效风暴）。
- `internal/relay/cpa` — CLIProxyAPI 桥接（固定模块 `github.com/router-for-me/CLIProxyAPI/v7`，**禁止 go.mod replace 本地路径**）；仅用公开 SDK 做转发；账号 OAuth 登录在 `app/account` 交互式实现（不走 `sdk/auth` 阻塞 Login）。
- `internal/relay/adaptor` — 渠道路径协议适配，**零翻译纯透传**。
- `internal/relay/pipeline` — 转发主循环、双路径选路、outcome、SSE、errfmt；单请求最多消耗一次限流账号恢复探测，避免插件账号吃满三次 failover 预算而饿死健康 Core fallback。**粘性会话**（`session_affinity.go`，对齐 CLIProxyAPI）：按显式会话标识（Claude Code header / `session_id` / `prompt_cache_key` / anthropic metadata）或「system+首条 user 输入」派生哈希，把同一会话粘到同一路由目标（TTL 1h，key 含 userID 防串扰）；绑定优先于优先级，目标失效自动重绑；Relay Hook plan 存在时粘性让位。`GET /v1/models` / `/v1beta/models` 按分组聚合 **渠道 models ∪ 账号池 models**（账号路径经 CPA 翻译，protocols 标 openai/anthropic/gemini）；**未标价模型不进目录**，与 forward/task 缺价预检 fail-closed 一致（渠道与账号均不可调用未标价模型）。
- `app/account` / `app/proxy` — 账号/代理管理面；凭证 AES-GCM；导出明文；账号类型仅 **oauth / api_key**；用量窗口（Codex `/wham/usage`、Claude `/api/oauth/usage`，快照存 `extra.usage`，`POST /accounts/:id/usage/refresh`）；OAuth 交互式授权 + Codex 三路导入（浏览器授权 / RT / Session，**无设备码**）；**重新授权**（`account_id` 写入已有账号凭证，保留 ID/分组/调度）；**可服务模型白名单**（`extra.models` / `models` 字段，空=平台默认；支持单账号与 bulk 覆盖）。
- `internal/relay/task` — 异步任务子系统（视频/音乐「提交-轮询」型转发）：flow 提交主循环、poller 后台轮询与结算、平台 adaptor（openaivideo/suno）。**与零翻译红线的边界**：提交体仍透传（入口协议=渠道协议，adaptor 只做 URL/认证/模型重写）；查询响应不透传——读本地 task 表快照、adaptor 做状态归一化后按入口协议重建（计量与状态提取，口径同 errfmt 的「语义保留、载体重建」）。禁止 import ent 与 app 包：落库经 Store、余额动账经 BalanceOps 窄接口注入（server 层适配 app/user）。
- `internal/relay/outcome` — 上游 attempt 判定总表与出口脱敏（唯一事实源，见 `outcome.go` 包注释）：401/402/403→AuthFailed 禁用切号；429→RateLimited 冷却；408/5xx→Transient 软换号；其余 4xx→ClientError 终止。禁止 body 关键词触发禁用。
- `internal/relay/errfmt` — 错误体的协议形态渲染（openai/anthropic/gemini/suno），pipeline、task 与鉴权中间件共用。网关自产错误与**上游错误**都经此包渲染：上游错误解析出语义（message/type/code）后按入口协议重建（语义保留、载体重建，非字节透传），HTTP 状态码保留上游原值。
- `internal/relay/pricing` — 价目表缓存 + token→cost 纯函数。
- `internal/moderation` — 风控中心判定核心（与 billing/errlog 同级顶层包）：输入抽取（gjson 按协议抽最后一条 user 消息）、关键词 Aho-Corasick、外部审核 API 客户端（多 key round-robin + 按状态分级冻结熔断）、observe 异步 worker 池、命中哈希 Redis 缓存、滑窗计数自动封禁（管理员豁免）与邮件通知副作用、日志双保留期 TTL 清理。**不 import ent**——落库/封禁/配置经窄接口（LogStore/UserBanner/ConfigSource/Notifier）注入；配置存 settings 表 `risk_control` 组（总开关 `risk_control_enabled` + 单 JSON `content_moderation_config`，审核 key AES-256-GCM 密文，加解密在 `app/riskcontrol`）；挂点：pipeline `forwardOpt` 并发闸门前 + task `handleSubmit` 提交前，拦截按入口协议 errfmt 渲染、errlog phase=`precheck_moderation`；引擎 fail-open（任何内部故障放行）。管理面 `/admin/risk-control/*`（app/riskcontrol + riskcontrol_handler 三件套）。注意：转发鉴权校验 `user.status`（禁用用户 sk- key 5s 缓存内失效）；管理员不可被禁用（手动与自动封禁双防线）。
- `internal/probe` — 渠道密钥主动健康探针 / 余额同步；状态变化经 `probe.Notifier` 外推。当前实现：`bootstrap.channelBarkNotifier` + `infra/bark`（settings 组 `bark`，单 device key，10 分钟去重）。**恢复语义**：探测集含 `disabled_auto` 的 key/凭证（自动禁用正是探针要救回的对象；`disabled_manual` 不探不恢复）；探针连续成功经状态机 `ActionRecover` → `MarkRecovered` 自动回 enabled；429 只进凭证冷却**不进健康失败计数**（防临时限流被升级成永久禁用）；401/403 的健康信号与自动禁用统一受 `channel_auto_ban_enabled` 总开关约束；管理端手动测试恢复会重置健康计数（`HealthResetter`）。
- `internal/notify` — 管理员外推通知窄抽象（`Message` / `Channel` / `Multi`），供 Bark 等通道复用；后续扩 Telegram/Webhook 时优先挂这里，不必再绑具体业务。
- `internal/billing` — 三管道计费（actual=total×billing_rate 扣余额；billed=total×sell_rate 累加 key 用量；渠道成本=total×account_rate_multiplier 快照列查询期现算、不落列）与异步记账；billing_rate 优先级链 user.group_rates > tier.rates（用户等级批量分层）> group.rate_multiplier > 1.0（rate.go，鉴权时经 APIKeyInfo 预装载）。**无计量（usage_missing）与中断且无 token（stream_aborted_usage_missing）不落 usage_log**，避免 $0 行污染使用记录；有部分 token 的流中断仍落账。
- `internal/scheduler` — 仅剩 ConcurrencyManager/RPMCounter（Redis 限流原语，渠道/用户/key 维度）。
- `internal/pluginruntime` — 独立进程插件（go-plugin gRPC）。能力驱动：`relay_hook.v1` 在选路前整包替换 JSON 请求体（fail-open，不得改 model/stream），`account_test_transform.v1` 仅服务账号连接测试（fail-closed）。配置表单控件：`multi_select` / `single_select`（`data_source=groups` 或静态 `options`）/ `text` / `number` / `textarea` / `switch`。Codex 增强实现在仓外 `airgate-codex-overage`，插件 ID `airgate-codex-enhance`（超额 + instruction 注入）。

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
