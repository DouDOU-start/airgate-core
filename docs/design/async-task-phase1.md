# 异步任务子系统 · 第一期设计（video + music）

> 状态：设计稿（未实施）。参照 new-api 任务体系，按本分支五件套分层与 relay 子系统范式落地。
> 范围：任务骨架（表/接口/计费/轮询）+ OpenAI 风格视频（`/v1/videos`，Sora 形态）+ Suno 音乐。
> 二期（kling/jimeng/vidu 等原生协议）只加 adaptor 与路由，不动骨架——见 §14 接口形状验证。

## 1. 定位与红线边界声明

任务子系统是与同步转发 pipeline **并列的独立子系统**（`internal/relay/task`），不塞进现有 pipeline。
它与「零翻译纯透传」红线的边界如下，实施时须同步写入 `CLAUDE.md`：

- **提交请求体仍然透传**：入口协议与渠道协议同构（registry.Pick 协议过滤保证），adaptor 只做 URL 拼接、认证头、模型名重写、param_override——与同步 adaptor 契约一致。
- **查询响应不透传**：客户端查任务读的是**本地 task 表快照**（后台轮询保鲜），adaptor 必须解析上游查询响应做**状态归一化**（status/progress/fail_reason/用量），再按入口协议重建响应体。这是「计量与状态提取」，性质同现有 usage 提取——语义保留、载体重建，与 errfmt 对上游错误的处理口径一致。
- 调度/重试/禁用/计费一律在任务 flow 层（对应 pipeline 职责），adaptor 不碰。

## 2. 路由清单

```
# OpenAI 视频（Sora 形态，官方 API；渠道类型 openai_video）
POST /v1/videos                     提交视频任务（JSON 或 multipart，透传）
GET  /v1/videos/{task_id}           查任务状态（读本地快照，OpenAI video object）
GET  /v1/videos/{task_id}/content   取成片（实时代理上游，不落盘）

# Suno 音乐（Suno-API 社区形态；渠道类型 suno）
POST /suno/submit/{action}          提交（action: music / lyrics）
POST /suno/fetch                    批量查（body: {ids: [...]}，读本地快照）
GET  /suno/fetch/{task_id}          单查（读本地快照）
```

- 均挂 `middleware.APIKeyAuth`，注册在 `router.go` 转发区（与 relayGroup 并列）。
- `/v1/videos*` 错误体走 errfmt 的 OpenAI 形态；`/suno/*` 用 Suno-API 的 `{code, message, data}` 包装（errfmt 新增 suno 渲染分支）。
- 二期新增平台 = 新增路由组 + adaptor，骨架不动（如 `/kling/v1/videos/*`）。

## 3. 数据模型（`ent/schema/task.go`）

```go
field.String("task_id")            // 上游任务 ID；对外即用此 ID（OpenAI 兼容）
field.String("platform")           // "openai_video" / "suno"（= 渠道类型枚举值）
field.String("action")             // video: "generate"/"remix"；suno: "music"/"lyrics"
field.Enum("status").Values(
    "submitted", "queued", "in_progress", "success", "failure",
).Default("submitted")
field.Int("progress").Default(0)   // 0-100
field.String("fail_reason").Default("")
field.String("request_model").NotEmpty()  // 对外模型名（计费/展示）
field.String("upstream_model").Default("")
// 计费快照（口径与 usage_log 一致）
field.Float("hold_amount")   // 预扣的 actual（= 估价 total × billing_rate），decimal(20,8)
field.Float("est_total")     // 估价 total（未乘倍率），结算差额计算基准
field.Float("rate_multiplier").Default(1.0)   // 预扣时快照 billing_rate（结算复用，防结算期费率漂移）
field.Bool("settled").Default(false)           // 结算幂等闸（终态后只结一次）
// 上游原始响应快照（查询端点据此重建响应）；写入侧截断（256KB）
field.JSON("data", json.RawMessage{}).Optional()
field.Time("submit_time")    // 提交成功时刻（超时清扫基准）
field.Time("finish_time").Optional().Nillable()
// 归属（快照 + 可空 FK，口径同 usage_log）
field.Int("user_id_snapshot") / field.String("user_email_snapshot")
field.Int("user_id").Optional() / field.Int("api_key_id").Optional() /
field.Int("group_id").Optional() / field.Int("channel_id").Optional()
field.String("request_id").Default("")
field.Time("created_at") / field.Time("updated_at")
```

索引：`(platform, task_id)` 唯一（跨渠道撞 ID 隔离）；`(status, updated_at)`（轮询扫描）；
`(user_id, created_at)`（用户查询/列表）；`request_id`。
Channel/User 边 OnDelete SET NULL，口径同 usage_log。

## 4. TaskAdaptor 接口（`internal/relay/task/adaptor.go`）

与同步 `adaptor.Adaptor` 平行的注册表（`Register(platform, factory)` / `GetAdaptor`），接口按「提交透传 + 查询归一化」两段切：

```go
// SubmitRequest 提交请求的调度视图（原始体透传，此处只提取调度与估价所需字段）。
type SubmitRequest struct {
    Model   string
    Action  string
    Seconds int      // 视频时长参数（估价用；无则 0）
    RawBody []byte   // 原始请求体（JSON 或 multipart，原样直发）
    RawContentType string
}

// TaskStatus 上游查询响应的归一化结果。
type TaskStatus struct {
    Status     string          // submitted/queued/in_progress/success/failure
    Progress   int
    FailReason string
    Seconds    int             // 实际产出时长（结算用；无则 0，保留估价）
    Raw        json.RawMessage // 上游原始响应（落 task.data）
}

type Adaptor interface {
    // ParseSubmit 从入站请求提取 model/action/估价参数；不改写原始体。
    ParseSubmit(c *gin.Context, endpoint string) (*SubmitRequest, error)
    // BuildSubmitRequest 构建上游提交请求（URL/认证/模型重写/param_override，体透传）。
    BuildSubmitRequest(ctx context.Context, info *Info, req *SubmitRequest) (*http.Request, error)
    // ParseSubmitResponse 从上游 2xx 提交响应提取 task_id 与初始状态。
    ParseSubmitResponse(body []byte) (taskID string, st *TaskStatus, err error)
    // BuildQueryRequest 构建上游单任务查询请求（轮询用）。
    BuildQueryRequest(ctx context.Context, info *Info, taskID string) (*http.Request, error)
    // ParseQueryResponse 归一化上游查询响应。
    ParseQueryResponse(body []byte) (*TaskStatus, error)
    // RenderTask 由本地 task 行重建入口协议的查询响应体。
    RenderTask(t *Task) ([]byte, error)
}

// ContentProxy 可选能力：GET /v1/videos/{id}/content 实时代理成片。
type ContentProxy interface {
    BuildContentRequest(ctx context.Context, info *Info, taskID string) (*http.Request, error)
}

// BatchQuerying 可选能力：一次查多个任务（suno 实现；轮询器优先走批量）。
type BatchQuerying interface {
    BuildBatchQueryRequest(ctx context.Context, info *Info, taskIDs []string) (*http.Request, error)
    ParseBatchQueryResponse(body []byte) (map[string]*TaskStatus, error)
}
```

`Info` 结构同现有 `adaptor.RelayInfo` 精神（Channel 快照 + APIKey + 模型名 + Client）。

第一期实现两个 adaptor：
- `task/openaivideo`：Sora 官方 API。提交 `POST {base}/v1/videos`（multipart/JSON 透传）、查询 `GET {base}/v1/videos/{id}`、成片 `GET {base}/v1/videos/{id}/content`。状态映射 queued/in_progress/completed/failed。
- `task/suno`：Suno-API 社区协议。提交 `POST {base}/suno/submit/{action}`、批量查 `POST {base}/suno/fetch`。

## 5. 渠道与 registry 扩展

- `channel.type` 枚举新增 `openai_video`、`suno`（`make ent`）。
- `registry.protocolForChannelType` 增加映射：`openai_video → ProtocolVideoOpenAI`、`suno → ProtocolSuno`（协议常量与平台标识同值，即 task.platform）。`Pick` 零改动——协议过滤天然把任务渠道与对话渠道隔离。
- 渠道 CRUD/加密/分组绑定全复用 channel 域现有链路；前端渠道表单加两个类型选项。
- 一键测试（TestChannel）第一期对任务类渠道返回「暂不支持」（测试会真实产生付费任务，不能默认发）。

## 6. 计费流转（预扣 → 结算 → 退款）

三管道口径不变（actual 扣 `user.balance` / billed 累 `api_key.used_quota` / account 快照现算），只是把「当场算」拆成两段：

```
提交时（同步）：
  est_total = 估价（见 §7）
  hold      = est_total × billing_rate（ResolveBillingRate，快照进 task.rate_multiplier）
  余额预检：balance ≥ hold（复用 pipeline 预检口径）
  同步扣款：UserStore.UpdateBalance(-hold)，balance_log type="task_hold"
  ——重试换渠道不重复扣；全部渠道失败 → 同步退 hold（type="task_refund"）+ errfmt 报错

终态时（轮询器内，settled=false 才结算，CAS 置 settled 保证幂等）：
  success：
    final_total  = 实际用量计价（上游返回实际时长；无则保留 est_total）
    final_actual = final_total × task.rate_multiplier
    差额 = hold - final_actual → 同步 UpdateBalance 补/退（balance_log type="task_settle"）
    recorder.Record(UsageRecord{ Source:"task", SkipBalanceCharge:true, calls=1,
        total/actual/billed/rate 快照齐全, endpoint=platform, model=request_model, ... })
        → usage_log 落一条，key 用量、统计、渠道成本全走现有管道
  failure / 超时：
    全额退 hold（type="task_refund"）；不落 usage_log；
    失败留痕走 upstream_request_logs（复用 errlog recorder，source 标 task）
```

recorder 改动一处：`UsageRecord` 增加 `SkipBalanceCharge bool`，`applyUsageCharges` 对该记录跳过 `user.balance` 扣减（余额动账已在预扣/结算同步完成），其余（key used_quota、统计口径）照常。

## 7. 估价与价目表扩展

`model_prices` 复用，两种计价模式，按已有字段优先级取：

1. **按次**：`per_request_price > 0` → `est_total = per_request_price`（suno、按次视频模型）。
2. **按秒**：`pricing_extra.video = {"per_second": 0.05}` → `est_total = per_second × seconds`（seconds 取请求参数，缺省按模型默认时长 4s；结算用上游实际时长）。

分辨率/档位差价第一期**不做倍率表**，用模型名区分（如 `sora-2` / `sora-2-pro`），与「模型即价格」的现有心智一致。`pricing.Cache` 增加 video 字段解析；两者皆未配置 → 提交直接报「模型未配置价格」（任务不允许零价兜底，防白嫖长任务）。

## 8. 提交流程（`internal/relay/task/flow.go`）

对齐 pipeline.forward 的骨架，复用其组件：

```
HandleVideoSubmit / HandleSunoSubmit:
  requireKeyInfo → adaptor.ParseSubmit（读体上限复用 32MB）
  → pricing 估价 + 余额预检（balance ≥ hold + 未结任务 hold 之和不做，第一期单任务预检即可）
  → 同步预扣 hold
  → user/key 并发闸门照挂（任务提交是短请求，闸门 TTL 用非流式档）
  → failover ≤3：registry.Pick(group, model, platform) → NextKey
      → BuildSubmitRequest → 直发 → classifyOutcome 复用
        （429 换渠道 / 401·403 MarkAutoDisabled / 5xx 换渠道 / 2xx 成功）
  → 成功：ParseSubmitResponse 拿 task_id → task 落库（含 data 快照）
      → RenderTask 返回入口协议响应（201/200）
  → 全败：退 hold → writeAllFailed 口径渲染错误
```

`classifyOutcome`/`keyHint`/失败留痕等从 pipeline 包内提出到 `internal/relay/relaycore`（或导出复用），不复制。

## 9. 轮询器（`internal/relay/task/poller.go`）

- 挂载点：`Server.StartBackground` 起 goroutine，ctx 取消即停（同 PaymentService.StartExpireLoop 范式）。
- 节拍：10s tick；先 `COUNT` 未完成任务，为 0 则本轮空转（廉价短路）。
- 扫描：`ListUnfinished(limit 100, order by updated_at asc)` → 按 channel_id 分组：
  - 组间并发（errgroup，上限 4）；组内 suno 走 `BatchQuerying` 一次查完，openai_video 逐个查、任务间 sleep 200ms 限速。
- 更新：**CAS 条件更新**（`UPDATE ... SET status=new WHERE id=? AND status=old`，ent 谓词实现），防「客户端触发的实时查询」与轮询并发覆盖；行没更新到就跳过本轮。
- 终态：按 §6 结算/退款（settled CAS 幂等）。
- 超时清扫：`submit_time` 超过 `task_timeout_minutes`（gateway settings，默认 30）→ 置 failure（fail_reason="任务超时"）+ 退款。
- 轮询失败处理：查询请求 401/403 → MarkAutoDisabled（渠道口径一致）；网络错/5xx 只记日志等下轮，**不**据此判任务失败。
- 多副本：本分支按单体部署设计，第一期不做分布式租约；`infra/store/lock.go` 已有锁原语，若将来多副本，poller 起始处加一行租约即可（设计预留，不实现）。

## 10. 查询端点行为

- `GET /v1/videos/{id}` / `/suno/fetch*`：按 `(platform, task_id)` + user_id 归属查本地 task 表 → `RenderTask` 重建响应。**不穿透上游**（轮询保鲜，最长滞后一个节拍）。
- 查不到或非本人 → 404（入口协议错误形态）。
- `GET /v1/videos/{id}/content`：本地确认 status=success 后，实时构建上游 content 请求流式代理（`io.Copy`，透传 Content-Type/Length），不落盘不缓存。渠道 key 用该任务落库的 channel_id（成片必须回原渠道取）。

## 11. 分层与装配

- `internal/relay/task` 禁止 import ent（同 registry 范式）：定义 `Store` 窄接口
  （Insert / GetByTaskID / ListUnfinished / CountUnfinished / UpdateStatusCAS / MarkSettledCAS），
  由 `app/task` service 实现桥接 → `infra/store/task_store.go`（唯一 import ent）。
- 余额动账复用 `app/user` 现有 `UpdateBalance`（含 balance_log），task flow 经接口注入。
- 装配：`bootstrap/http_handlers.go` 接线 store→service；`server.go` 构造 task.Flow/Poller（注入 registry/pricing/calculator/recorder/errlog/settings，与 pipeline 同源组件），`registerRoutes` 注册 §2 路由，`StartBackground` 启动 poller。
- 管理面（后台任务列表页/API）**第一期不做**，排障走 DB + upstream_request_logs；用户可自查任务。

## 12. 前端改动（web/）

1. 渠道表单：type 下拉加「OpenAI 视频」「Suno 音乐」（i18n zh/en）。
2. 模型价目：编辑器支持 `pricing_extra.video.per_second`（按秒价输入框，仅任务类模型展示）。
3. 使用日志页：source=task 的记录正常展示（endpoint 列显示 platform），无需新页面。

## 13. PR 切分与估算

| # | 内容 | 估算 |
|---|---|---|
| PR1 | ent schema（task + channel 枚举）+ task store + registry 协议映射 | 1.5d |
| PR2 | TaskAdaptor 接口 + openaivideo adaptor + 提交 flow + 预扣 + /v1/videos 路由 + errfmt | 3d |
| PR3 | 轮询器 + 结算/退款 + recorder SkipBalanceCharge + content 代理 | 2.5d |
| PR4 | suno adaptor + /suno 路由 + 批量查询 | 1.5d |
| PR5 | 前端（渠道类型/价目/i18n） | 1.5d |
| PR6 | 文档同步（CLAUDE.md/README）+ 集成测试补齐 | 1d |

合计约 **11 人天**。测试：flow/poller 表驱动 + httptest 假上游集成测试（同 pipeline integration_test 范式）；真上游联调需 Sora 兼容中转 + Suno-API 各一个账号。

## 14. 二期接口形状验证（纸面，不实现）

- **kling（可灵）**：认证是 AK/SK 算 JWT——`Info.APIKey` 存 `ak|sk` 复合值，`BuildSubmitRequest` 内自签 JWT，接口无需改（认证构造本就是 adaptor 职责）。提交/查询是标准「create → get by task_id」，`ParseSubmit/BuildQueryRequest` 直接容纳。原生路由 `/kling/v1/videos/*` 是新路由组注册，骨架不动。
- **jimeng（即梦）**：火山签名 v4 同理封装在 BuildSubmitRequest；其「提交与查询同一端点按 Action 参数区分」也只是 URL 构造差异。
- **异步图片（MJ 类）**：buttons/交互动作超出本接口（action 语义复杂），确认走独立立项，不硬塞。

结论：接口形状对二期平台成立，二期成本锁定为「每平台一个 adaptor + 一组路由」。

## 15. 文档同步清单（实施时随 PR6 落）

- `CLAUDE.md`：架构图加任务分支；子系统边界加 `internal/relay/task`（含 §1 边界声明）；红线「零翻译透传」补任务子系统例外条款；常用命令补 task 测试路径。
- `README.md`：端点列表加 §2 路由；功能清单加视频/音乐任务。
