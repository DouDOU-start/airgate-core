package task

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/errlog"
	"github.com/DouDOU-start/airgate-core/internal/moderation"
	"github.com/DouDOU-start/airgate-core/internal/pkg/upstreamclient"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/clientid"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/errfmt"
	"github.com/DouDOU-start/airgate-core/internal/relay/outcome"
	"github.com/DouDOU-start/airgate-core/internal/relay/pipeline"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

const (
	// maxFailoverAttempts 单次提交内渠道切换上限（口径同 pipeline）。
	maxFailoverAttempts = 3
	// submitTimeout 上游任务提交请求总超时（提交是短请求，产出在后台异步进行）。
	submitTimeout = 2 * time.Minute
	// maxSubmitBodyBytes 提交请求体上限（视频参考图 multipart 留足余量）。
	maxSubmitBodyBytes = 32 << 20
	// maxUpstreamRespBytes 上游提交/查询响应体读取上限（任务元数据，非媒体内容）。
	maxUpstreamRespBytes = 4 << 20
	// defaultVideoSeconds 请求未带时长参数时的估价兜底时长（Sora 默认 4s）。
	defaultVideoSeconds = 4
	// statusClientClosedRequest 客户端断连（nginx 惯例 499）。
	statusClientClosedRequest = 499
)

// UsageSink 用量落账窄接口（*billing.Recorder 天然满足；测试注入 fake）。
type UsageSink interface {
	Record(record billing.UsageRecord)
}

// ErrSink 上游请求日志投递窄接口（*errlog.Recorder 天然满足；nil 安全）。
type ErrSink interface {
	Record(e errlog.Entry)
	CountFailure(ctx context.Context, channelID int, verdict, phase string)
}

// PriceSource 价目查询窄接口（*pricing.Cache 天然满足；测试注入 fake）。
type PriceSource interface {
	Get(model string) (pricing.Price, bool)
}

// SettingsSource gateway 设置读取窄接口（*pipeline.SettingsReader 天然满足）。
type SettingsSource interface {
	Get(ctx context.Context) pipeline.GatewaySettings
}

// CPAForwarder xAI OAuth 账号转发窄接口（*cpa.Bridge 天然满足；测试可注入 fake）。
type CPAForwarder interface {
	Forward(ctx context.Context, c *gin.Context, req cpa.ForwardRequest) cpa.ForwardResult
}

// Options 任务子系统装配依赖（registry/pricing/限流/计费组件与 pipeline 同源）。
type Options struct {
	Registry    *registry.Registry
	Pricing     PriceSource
	Concurrency *scheduler.ConcurrencyManager
	RPM         *scheduler.RPMCounter
	Calculator  *billing.Calculator
	Sink        UsageSink
	ErrLog      ErrSink
	Settings    SettingsSource
	Store       Store
	Balance     BalanceOps
	// Moderation 内容审核引擎（风控中心；nil 时全部放行）。
	Moderation pipeline.ModerationChecker
	// Accounts / CPA 为 xAI OAuth 视频账号路径依赖；nil 时原渠道任务不受影响。
	Accounts *accountreg.Registry
	CPA      CPAForwarder
}

// Flow 任务提交/查询流程。
type Flow struct {
	registry    *registry.Registry
	pricing     PriceSource
	concurrency *scheduler.ConcurrencyManager
	rpm         *scheduler.RPMCounter
	calculator  *billing.Calculator
	sink        UsageSink
	errSink     ErrSink
	settings    SettingsSource
	store       Store
	balance     BalanceOps
	moderation  pipeline.ModerationChecker
	accounts    *accountreg.Registry
	cpa         CPAForwarder
	client      *http.Client
}

// NewFlow 创建任务流程。
func NewFlow(opts Options) *Flow {
	settings := opts.Settings
	if settings == nil {
		settings = pipeline.NewSettingsReader(nil)
	}
	calculator := opts.Calculator
	if calculator == nil {
		calculator = billing.NewCalculator()
	}
	return &Flow{
		registry:    opts.Registry,
		pricing:     opts.Pricing,
		concurrency: opts.Concurrency,
		rpm:         opts.RPM,
		calculator:  calculator,
		sink:        opts.Sink,
		errSink:     opts.ErrLog,
		settings:    settings,
		store:       opts.Store,
		balance:     opts.Balance,
		moderation:  opts.Moderation,
		accounts:    opts.Accounts,
		cpa:         opts.CPA,
		client:      upstreamclient.NewClient(0),
	}
}

// HandleVideoSubmit POST /v1/videos 入口 handler（OpenAI 视频任务，Sora 形态）。
func (f *Flow) HandleVideoSubmit(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	f.handleSubmit(c, PlatformOpenAIVideo, "")
}

// HandleXAIVideoSubmit POST /v1/videos/generations 入口 handler。
// 有可用 xAI OAuth 账号时走 CPA；否则走 OpenAI 兼容渠道级联转发。
func (f *Flow) HandleXAIVideoSubmit(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	f.handleSubmit(c, PlatformXAIVideo, "")
}

// HandleSunoSubmit POST /suno/submit/:action 入口 handler（Suno 音乐任务）。
func (f *Flow) HandleSunoSubmit(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolSuno)
	f.handleSubmit(c, PlatformSuno, c.Param("action"))
}

// handleSubmit 提交入口公共段：鉴权信息 → 读体 → adaptor 解析 → 主循环。
func (f *Flow) handleSubmit(c *gin.Context, platform, action string) {
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	body, ok := readSubmitBody(c)
	if !ok {
		return
	}
	ad, err := GetAdaptor(platform)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "server_error", "internal_error", err.Error())
		return
	}
	sub, err := ad.ParseSubmit(action, c.GetHeader("Content-Type"), body)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "bad_request", err.Error())
		return
	}
	if !f.moderationCheck(c, keyInfo, platform, sub) {
		return
	}
	if platform == PlatformXAIVideo && f.hasXAIVideoAccount(keyInfo.GroupID, sub.Model) {
		f.submitXAIAccount(c, keyInfo, ad, sub)
		return
	}
	f.submit(c, keyInfo, platform, ad, sub)
}

// moderationCheck 提交前的内容审核预检（风控中心）。放行返回 true；
// 拦截时按入口协议写出错误体并落失败留痕，返回 false。
func (f *Flow) moderationCheck(c *gin.Context, keyInfo *auth.APIKeyInfo, platform string, sub *SubmitRequest) bool {
	if f.moderation == nil {
		return true
	}
	var protocol string
	switch platform {
	case PlatformOpenAIVideo, PlatformXAIVideo:
		protocol = moderation.ProtocolOpenAIVideo
	case PlatformSuno:
		protocol = moderation.ProtocolSuno
	default:
		return true
	}
	d := f.moderation.Check(c.Request.Context(), moderation.CheckRequest{
		RequestID:   requestIDOf(c),
		UserID:      keyInfo.UserID,
		UserEmail:   keyInfo.UserEmail,
		APIKeyID:    keyInfo.KeyID,
		GroupID:     keyInfo.GroupID,
		Endpoint:    c.Request.URL.Path,
		Protocol:    protocol,
		Model:       sub.Model,
		ContentType: sub.ContentType,
		Body:        sub.Body,
	})
	if d.Allowed {
		return true
	}
	status := d.StatusCode
	if status < 400 || status > 599 {
		status = http.StatusForbidden
	}
	code := "content_blocked"
	switch d.Action {
	case moderation.ActionKeywordBlock:
		code = "content_keyword_blocked"
	case moderation.ActionHashBlock:
		code = "content_hash_blocked"
	}
	writeError(c, status, "permission_error", code, d.Message)
	f.recordFailure(c, keyInfo, sub.Model, time.Now(), errlog.Entry{
		Phase: errlog.PhasePrecheckModeration, StatusCode: status,
		ErrorType: "permission_error", ErrorCode: code, Message: d.Message,
	})
	return false
}

// readSubmitBody 读取提交请求体（统一读体上限）；失败时已写出错误体。
func readSubmitBody(c *gin.Context) ([]byte, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSubmitBodyBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "request_too_large", "请求体超出大小限制")
			return nil, false
		}
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_body", "读取请求体失败")
		return nil, false
	}
	return body, true
}

// estimate 提交前估价：按次价（per_request_price）优先，其次视频按分辨率秒价。
// 返回 (估价 total, 估价用时长秒数)；两者皆未配置返回 (0, 0)。
func estimate(price pricing.Price, sub *SubmitRequest) (float64, int) {
	if price.PerRequest > 0 {
		return price.PerRequest, sub.Seconds
	}
	if perSecond, ok := pricing.VideoPriceFor(price, sub.Resolution); ok {
		seconds := sub.Seconds
		if seconds <= 0 {
			seconds = defaultVideoSeconds
		}
		return perSecond * float64(seconds), seconds
	}
	return 0, 0
}

// submitFailureSummary 各类失败汇总（全渠道耗尽后的响应选择，口径同 pipeline）。
type submitFailureSummary struct {
	rateLimited   bool
	minRetryAfter time.Duration
	authFailed    bool
	transient     bool
	localCapacity bool
}

func (s *submitFailureSummary) observeRetryAfter(d time.Duration) {
	if s.minRetryAfter == 0 || d < s.minRetryAfter {
		s.minRetryAfter = d
	}
}

// submit 提交主循环：估价 → 预扣 → failover ≤3（Pick + 渠道闸门 + 直发 + outcome 判定）
// → task 落库；全部失败退预扣。
func (f *Flow) submit(c *gin.Context, keyInfo *auth.APIKeyInfo, platform string, ad Adaptor, sub *SubmitRequest) {
	start := time.Now()
	ctx := c.Request.Context()

	// 0. 客户端限制预检（口径同 pipeline）。
	clientid.Detect(c)
	if len(keyInfo.GroupAllowedClients) > 0 {
		if !clientid.Matches(clientid.Get(c), keyInfo.GroupAllowedClients) {
			if keyInfo.GroupFallbackID != nil {
				keyInfo.GroupID = *keyInfo.GroupFallbackID
			} else {
				writeError(c, http.StatusForbidden, "permission_error", "client_restricted",
					"当前客户端类型不允许访问此分组")
				f.recordFailure(c, keyInfo, sub.Model, start, errlog.Entry{
					Phase: errlog.PhasePrecheckClientRestrict, StatusCode: http.StatusForbidden,
					ErrorType: "permission_error", ErrorCode: "client_restricted",
					Message: "当前客户端类型不允许访问此分组",
				})
				return
			}
		}
	}

	// 1. 缺价预检：任务不允许零价兜底（长任务白嫖面大），未配任务计价一律 400。
	price, priced := f.pricing.Get(sub.Model)
	estTotal, estSeconds := 0.0, 0
	if priced {
		estTotal, estSeconds = estimate(price, sub)
	}
	if !priced || estTotal <= 0 {
		msg := "模型 " + sub.Model + " 未配置任务计价（per_request_price 或 pricing_extra.video 秒价）"
		writeError(c, http.StatusBadRequest, "invalid_request_error", "model_price_not_configured", msg)
		f.recordFailure(c, keyInfo, sub.Model, start, errlog.Entry{
			Phase: errlog.PhasePrecheckPrice, StatusCode: http.StatusBadRequest,
			ErrorType: "invalid_request_error", ErrorCode: "model_price_not_configured", Message: msg,
		})
		return
	}
	sub.Seconds = estSeconds

	// 2. 倍率预检：实际扣费倍率超过密钥最高倍率时拒绝（口径同 pipeline，管理员临时调价的保护闸）。
	billingRate := billing.ResolveBillingRate(keyInfo)
	if billing.ExceedsKeyMaxRate(keyInfo, billingRate) {
		msg := fmt.Sprintf("当前计费倍率 %.2f 超过密钥最高倍率 %.2f", billingRate, keyInfo.MaxRate)
		writeError(c, http.StatusForbidden, "permission_error", "billing_rate_exceeded", msg)
		f.recordFailure(c, keyInfo, sub.Model, start, errlog.Entry{
			Phase: errlog.PhasePrecheckRate, StatusCode: http.StatusForbidden,
			ErrorType: "permission_error", ErrorCode: "billing_rate_exceeded", Message: msg,
		})
		return
	}

	// 3. 预扣：余额预检与扣款一次完成（同步动账，与同步转发的异步扣款模型不同——
	// 任务成本在提交时未知实耗，预扣防止长任务把余额打穿）。
	hold := estTotal * billingRate
	holdRemark := fmt.Sprintf("任务预扣 %s %s", platform, sub.Model)
	if err := f.balance.Hold(ctx, keyInfo.UserID, hold, holdRemark); err != nil {
		if errors.Is(err, ErrInsufficientBalance) {
			writeError(c, http.StatusPaymentRequired, "insufficient_quota", "insufficient_balance", "账户余额不足")
			f.recordFailure(c, keyInfo, sub.Model, start, errlog.Entry{
				Phase: errlog.PhasePrecheckBalance, StatusCode: http.StatusPaymentRequired,
				ErrorType: "insufficient_quota", ErrorCode: "insufficient_balance", Message: "账户余额不足",
			})
			return
		}
		slog.Error("task_hold_failed", "user_id", keyInfo.UserID, "model", sub.Model, "error", err)
		writeError(c, http.StatusInternalServerError, "server_error", "internal_error", "预扣余额失败，请稍后重试")
		return
	}
	// refund 兜底：本函数内所有未落库任务的失败路径都必须退回预扣。
	refund := func(reason string) {
		f.refundHold(keyInfo.UserID, hold, reason)
	}

	// 4. user / key 并发闸门（口径同 pipeline，任务提交同样占槽，防提交洪泛）。
	releaseClient, limitCode := f.acquireClientSlots(c, keyInfo)
	if limitCode != "" {
		refund("并发上限拒绝")
		f.recordFailure(c, keyInfo, sub.Model, start, errlog.Entry{
			Phase: errlog.PhaseLocalLimit, StatusCode: http.StatusTooManyRequests,
			ErrorType: "rate_limit_error", ErrorCode: limitCode, Message: "并发数已达上限",
		})
		return
	}
	defer releaseClient()

	settings := f.settings.Get(ctx)

	// 5. failover 主循环。
	var hardExclude, softExclude []int
	summary := submitFailureSummary{}
	var hops []errlog.AttemptHop
	attempts := 0

	for attempts < maxFailoverAttempts {
		if ctx.Err() != nil {
			refund("客户端取消")
			markCanceled(c)
			f.recordFailure(c, keyInfo, sub.Model, start, errlog.Entry{
				Phase: errlog.PhaseCanceled, StatusCode: statusClientClosedRequest,
				Message: "客户端取消请求", Attempts: attempts, Chain: hops,
			})
			return
		}

		exclude := make([]int, 0, len(hardExclude)+len(softExclude))
		exclude = append(exclude, hardExclude...)
		exclude = append(exclude, softExclude...)
		protocol := platform
		if platform == PlatformXAIVideo {
			// xAI 原生视频端点属于 OpenAI 兼容渠道的扩展能力。这里仅复用
			// 渠道选择，不进入同步 Responses 管线；实际 URL 由 xaivideo
			// 适配器固定构造为 /v1/videos/generations。
			protocol = registry.ProtocolOpenAI
		}
		ch, err := f.registry.Pick(keyInfo.GroupID, sub.Model, protocol, exclude)
		if err != nil {
			break
		}
		apiKey := ch.APIKey
		if apiKey == "" {
			slog.Warn("task_channel_key_no_api_key", "channel_key_id", ch.KeyID)
			hardExclude = append(hardExclude, ch.KeyID)
			continue
		}

		// key RPM + 并发闸门：满则软排除（任务提交不排队，客户端重试成本低）。
		rpmOK, rpmMinute, _ := f.rpm.TryIncrementKeyRPM(ctx, ch.KeyID, ch.MaxRPM)
		if !rpmOK {
			summary.localCapacity = true
			softExclude = append(softExclude, ch.KeyID)
			continue
		}
		slotID := uuid.New().String()
		if err := f.concurrency.AcquireKeySlot(ctx, ch.KeyID, slotID, ch.MaxConcurrency, 0); err != nil {
			f.rpm.DecrementKeyRPM(ctx, ch.KeyID, rpmMinute)
			summary.localCapacity = true
			softExclude = append(softExclude, ch.KeyID)
			continue
		}

		info := &Info{
			ChannelKey:    ch,
			APIKey:        apiKey,
			RequestModel:  sub.Model,
			UpstreamModel: upstreamModel(ch, sub.Model),
			Client:        f.client,
		}
		attemptStart := time.Now()
		result := f.executeSubmit(ctx, ad, info, sub)
		f.concurrency.ReleaseKeySlot(context.Background(), ch.KeyID, slotID)
		attemptLatency := time.Since(attemptStart).Milliseconds()
		attempts++

		// 构建上游请求即失败：客户端/配置问题，一次性 400 终止（不计渠道健康）。
		if result.buildErr != nil {
			f.rpm.DecrementKeyRPM(context.Background(), ch.KeyID, rpmMinute)
			refund("构建上游请求失败")
			// 用户可见消息额外抹掉上游渠道身份；管理端留痕保留渠道细节。
			adminMsg := outcome.SanitizeKeyLeak(result.buildErr.Error(), []string{apiKey})
			userMsg := outcome.SanitizeUpstreamLeak(result.buildErr.Error(), []string{apiKey}, ch.BaseURL)
			writeError(c, http.StatusBadRequest, "invalid_request_error", "bad_request", userMsg)
			f.recordFailure(c, keyInfo, sub.Model, start, errlog.Entry{
				Phase: errlog.PhaseBadRequest, StatusCode: http.StatusBadRequest,
				ErrorType: "invalid_request_error", ErrorCode: "bad_request",
				Message: adminMsg, Attempts: attempts,
				ChannelID: ch.ChannelID, ChannelName: ch.ChannelName,
			})
			return
		}

		o := outcome.Classify(result.statusCode, result.headers, result.body, result.netErr)
		o.Reason = outcome.SanitizeKeyLeak(o.Reason, []string{apiKey})

		if o.Verdict == outcome.Success {
			taskID, st, perr := ad.ParseSubmitResponse(result.body)
			if perr != nil || taskID == "" {
				// 上游 2xx 但提交响应不可解析：按 transient 换渠道。
				// 注意：上游可能已实际受理任务，此路径存在重复提交风险（与 new-api 同口径），
				// 由失败留痕携带原始片段供人工对账。
				o.Verdict = outcome.Transient
				o.Reason = "提交响应解析失败: " + outcome.BodySnippet(result.body)
			} else {
				f.registry.MarkRecovered(ch.KeyID)
				t := f.newTask(c, keyInfo, platform, ch, info, sub, taskID, st, hold, estTotal, billingRate)
				id, err := f.store.Insert(context.Background(), t)
				if err != nil {
					// 上游任务已受理但落库失败：无法跟踪即无法结算，退款并放弃跟踪。
					slog.Error("task_insert_failed_after_submit",
						"platform", platform, "task_id", taskID, "channel_key_id", ch.KeyID, "error", err)
					refund("任务落库失败")
					writeError(c, http.StatusInternalServerError, "server_error", "internal_error", "任务保存失败，费用已退回")
					return
				}
				t.ID = id
				// 用户 / 分组 RPM 观测计数：任务提交成功（已落库）才计入，口径对齐仪表盘。
				f.rpm.IncrementUserGroupRPM(context.Background(), keyInfo.UserID, keyInfo.GroupID)
				c.Data(http.StatusOK, "application/json", ad.RenderTask(t))
				return
			}
		}

		switch o.Verdict {
		case outcome.RateLimited:
			f.rpm.DecrementKeyRPM(context.Background(), ch.KeyID, rpmMinute)
			hardExclude = append(hardExclude, ch.KeyID)
			summary.rateLimited = true
			summary.observeRetryAfter(o.RetryAfter)
			hops = append(hops, attemptHop(len(hops)+1, ch, apiKey, result.statusCode, "rateLimited", o.Reason, o.RetryAfter.Milliseconds(), attemptLatency, false))
			f.countFailure(ch.ChannelID, "rateLimited")
			continue

		case outcome.AuthFailed:
			f.rpm.DecrementKeyRPM(context.Background(), ch.KeyID, rpmMinute)
			if settings.AutoBanEnabled {
				f.registry.MarkAutoDisabled(ch.KeyID, outcome.TruncateErrorMsg(o.Reason))
			}
			hardExclude = append(hardExclude, ch.KeyID)
			summary.authFailed = true
			hops = append(hops, attemptHop(len(hops)+1, ch, apiKey, result.statusCode, "authFailed", o.Reason, 0, attemptLatency, settings.AutoBanEnabled))
			f.countFailure(ch.ChannelID, "authFailed")
			continue

		case outcome.Transient:
			f.rpm.DecrementKeyRPM(context.Background(), ch.KeyID, rpmMinute)
			softExclude = append(softExclude, ch.KeyID)
			summary.transient = true
			verdictName := "transient"
			if result.netErr != nil {
				verdictName = "networkError"
			}
			hops = append(hops, attemptHop(len(hops)+1, ch, apiKey, result.statusCode, verdictName, o.Reason, 0, attemptLatency, false))
			f.countFailure(ch.ChannelID, verdictName)
			continue

		default: // outcome.ClientError：语义重建终止，不重试；预扣退回。
			refund("上游拒绝请求")
			up := errfmt.ParseUpstream(result.statusCode, result.body)
			// 出口给用户前抹掉上游渠道身份（密钥 + base_url/主机/IP）。
			up.Message = outcome.SanitizeUpstreamLeak(up.Message, []string{apiKey}, ch.BaseURL)
			writeUpstreamError(c, result.statusCode, up)
			hop := attemptHop(len(hops)+1, ch, apiKey, result.statusCode, "clientError", o.Reason, 0, attemptLatency, false)
			f.recordFailure(c, keyInfo, sub.Model, start, errlog.Entry{
				Phase: errlog.PhaseUpstreamClientError, StatusCode: result.statusCode,
				Message: o.Reason, Attempts: attempts, Chain: append(hops, hop),
				ChannelID: ch.ChannelID, ChannelName: ch.ChannelName,
			})
			return
		}
	}

	// 全部渠道耗尽：退预扣并按失败类型选响应。
	refund("全部渠道失败")
	status, errType, errCode, msg := writeSubmitAllFailed(c, summary)
	f.recordFailure(c, keyInfo, sub.Model, start, errlog.Entry{
		Phase: errlog.PhaseUpstreamExhausted, StatusCode: status,
		ErrorType: errType, ErrorCode: errCode, Message: msg,
		Attempts: attempts, Chain: hops,
	})
}

// newTask 构造落库任务行（计费快照 + 归属快照）。
func (f *Flow) newTask(c *gin.Context, keyInfo *auth.APIKeyInfo, platform string, ch *registry.ChannelKeySnapshot, info *Info, sub *SubmitRequest, taskID string, st *Status, hold, estTotal, billingRate float64) *Task {
	now := time.Now()
	t := &Task{
		TaskID:                taskID,
		Platform:              platform,
		Action:                sub.Action,
		Status:                StatusSubmitted,
		RequestModel:          sub.Model,
		UpstreamModel:         info.UpstreamModel,
		HoldAmount:            hold,
		EstTotal:              estTotal,
		RateMultiplier:        billingRate,
		SellRate:              keyInfo.SellRate,
		AccountRateMultiplier: ch.EffectiveCostRatio(),
		Seconds:               sub.Seconds,
		Resolution:            sub.Resolution,
		SubmitTime:            now,
		RequestID:             requestIDOf(c),
		UserID:                keyInfo.UserID,
		UserEmail:             keyInfo.UserEmail,
		APIKeyID:              keyInfo.KeyID,
		GroupID:               keyInfo.GroupID,
		ChannelID:             ch.ChannelID,
		ChannelKeyID:          ch.KeyID,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if st != nil {
		if st.Status != "" {
			t.Status = st.Status
		}
		t.Progress = st.Progress
		if st.Seconds > 0 {
			t.Seconds = st.Seconds
		}
		t.Data = truncateData(st.Raw)
	}
	return t
}

// submitResult 单次上游提交调用的执行结果。
type submitResult struct {
	buildErr   error
	netErr     error
	statusCode int
	headers    http.Header
	body       []byte
}

// executeSubmit 单次上游提交：构建请求 → 直发 → 读响应（≤4MB）。
func (f *Flow) executeSubmit(ctx context.Context, ad Adaptor, info *Info, sub *SubmitRequest) submitResult {
	ctx, cancel := context.WithTimeout(ctx, submitTimeout)
	defer cancel()

	httpReq, err := ad.BuildSubmitRequest(ctx, info, sub)
	if err != nil {
		return submitResult{buildErr: err}
	}
	resp, err := f.client.Do(httpReq)
	if err != nil {
		return submitResult{netErr: err}
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamRespBytes))
	if err != nil {
		return submitResult{netErr: err}
	}
	return submitResult{statusCode: resp.StatusCode, headers: resp.Header, body: body}
}

// acquireClientSlots user → key 两级并发闸门（口径同 pipeline.acquireClientSlots）。
// 成功返回 (释放闭包, "")；拒绝时已写出 429 错误体，返回 (nil, 错误码)。
func (f *Flow) acquireClientSlots(c *gin.Context, keyInfo *auth.APIKeyInfo) (func(), string) {
	ctx := c.Request.Context()
	slotID := uuid.New().String()

	if keyInfo.UserMaxConcurrency > 0 {
		if err := f.concurrency.AcquireUserSlot(ctx, keyInfo.UserID, slotID, keyInfo.UserMaxConcurrency, 0); err != nil {
			writeRateLimitError(c, "user_concurrency_limit", "用户并发数已达上限", time.Second)
			return nil, "user_concurrency_limit"
		}
	}
	if keyInfo.KeyMaxConcurrency > 0 {
		if err := f.concurrency.AcquireAPIKeySlot(ctx, keyInfo.KeyID, slotID, keyInfo.KeyMaxConcurrency, 0); err != nil {
			if keyInfo.UserMaxConcurrency > 0 {
				f.concurrency.ReleaseUserSlot(context.Background(), keyInfo.UserID, slotID)
			}
			writeRateLimitError(c, "apikey_concurrency_limit", "API Key 并发数已达上限", time.Second)
			return nil, "apikey_concurrency_limit"
		}
	}
	if keyInfo.GroupID > 0 {
		f.concurrency.TrackGroupSlot(ctx, keyInfo.GroupID, slotID, 0)
	}
	return func() {
		if keyInfo.GroupID > 0 {
			f.concurrency.ReleaseGroupSlot(context.Background(), keyInfo.GroupID, slotID)
		}
		if keyInfo.KeyMaxConcurrency > 0 {
			f.concurrency.ReleaseAPIKeySlot(context.Background(), keyInfo.KeyID, slotID)
		}
		if keyInfo.UserMaxConcurrency > 0 {
			f.concurrency.ReleaseUserSlot(context.Background(), keyInfo.UserID, slotID)
		}
	}, ""
}

// refundHold 退回预扣（后台超时上下文，不受请求断连影响）；失败仅记日志。
func (f *Flow) refundHold(userID int, amount float64, reason string) {
	if amount <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.balance.Adjust(ctx, userID, amount, "任务预扣退回: "+reason, ""); err != nil {
		slog.Error("task_hold_refund_failed", "user_id", userID, "amount", amount, "reason", reason, "error", err)
	}
}

// countFailure 渠道×verdict 分钟桶计数；errSink 未注入时 no-op。
func (f *Flow) countFailure(channelID int, verdict string) {
	if f.errSink == nil {
		return
	}
	f.errSink.CountFailure(context.Background(), channelID, verdict, "")
}

// recordFailure 上游请求日志失败留痕（task 提交路径公共字段填充）。
func (f *Flow) recordFailure(c *gin.Context, keyInfo *auth.APIKeyInfo, model string, start time.Time, e errlog.Entry) {
	if f.errSink == nil {
		return
	}
	e.RequestID = requestIDOf(c)
	e.Source = errlog.SourceRelay
	if e.Model == "" {
		e.Model = model
	}
	e.Endpoint = c.Request.URL.Path
	e.UserID = keyInfo.UserID
	e.UserEmail = keyInfo.UserEmail
	e.APIKeyID = keyInfo.KeyID
	e.GroupID = keyInfo.GroupID
	e.IPAddress = c.ClientIP()
	e.UserAgent = c.Request.UserAgent()
	e.DurationMs = time.Since(start).Milliseconds()
	f.errSink.CountFailure(context.Background(), 0, "", e.Phase)
	f.errSink.Record(e)
}

// attemptHop 构造重试链一跳（reason 已由调用方脱敏）。
func attemptHop(seq int, ch *registry.ChannelKeySnapshot, apiKey string, upstreamStatus int, verdict, reason string, retryAfterMs, latencyMs int64, autoDisabled bool) errlog.AttemptHop {
	return errlog.AttemptHop{
		Seq:          seq,
		ChannelID:    ch.ChannelID,
		ChannelName:  ch.ChannelName,
		KeyID:        ch.KeyID,
		KeyName:      ch.KeyName,
		KeyHint:      outcome.KeyHint(apiKey),
		UpstreamStat: upstreamStatus,
		Verdict:      verdict,
		Reason:       reason,
		RetryAfterMs: retryAfterMs,
		LatencyMs:    latencyMs,
		AutoDisabled: autoDisabled,
	}
}

// writeSubmitAllFailed 全部渠道耗尽后的响应选择（口径同 pipeline.writeAllFailed）。
func writeSubmitAllFailed(c *gin.Context, summary submitFailureSummary) (int, string, string, string) {
	switch {
	case summary.rateLimited:
		retryAfter := summary.minRetryAfter
		if retryAfter <= 0 {
			retryAfter = time.Second
		}
		msg := "所有可用渠道均被限流，请稍后重试"
		writeRateLimitError(c, "upstream_rate_limited", msg, retryAfter)
		return http.StatusTooManyRequests, "rate_limit_error", "upstream_rate_limited", msg
	case summary.localCapacity:
		msg := "渠道容量已满，请稍后重试"
		writeError(c, http.StatusServiceUnavailable, "server_error", "all_channels_busy", msg)
		return http.StatusServiceUnavailable, "server_error", "all_channels_busy", msg
	case summary.authFailed:
		msg := "上游认证失败，请联系管理员检查渠道密钥"
		writeError(c, http.StatusBadGateway, "server_error", "upstream_auth_failed", msg)
		return http.StatusBadGateway, "server_error", "upstream_auth_failed", msg
	case summary.transient:
		msg := "上游服务暂时不可用"
		writeError(c, http.StatusBadGateway, "server_error", "upstream_error", msg)
		return http.StatusBadGateway, "server_error", "upstream_error", msg
	default:
		msg := "无可用渠道"
		writeError(c, http.StatusServiceUnavailable, "server_error", "no_available_channel", msg)
		return http.StatusServiceUnavailable, "server_error", "no_available_channel", msg
	}
}

// writeRateLimitError 写出 429 错误体并携带 Retry-After 头（秒向上取整，最小 1）。
func writeRateLimitError(c *gin.Context, code, message string, retryAfter time.Duration) {
	seconds := int(retryAfter.Seconds())
	if retryAfter > time.Duration(seconds)*time.Second {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	c.Header("Retry-After", fmt.Sprintf("%d", seconds))
	writeError(c, http.StatusTooManyRequests, "rate_limit_error", code, message)
}

// markCanceled 客户端断连：未写出过字节时补 499 状态。
func markCanceled(c *gin.Context) {
	if !c.Writer.Written() {
		c.Status(statusClientClosedRequest)
	}
	c.Abort()
}

// requireKeyInfo 从 gin ctx 取 APIKeyAuth 写入的 keyInfo；缺失（装配错误）写 401。
func requireKeyInfo(c *gin.Context) (*auth.APIKeyInfo, bool) {
	value, exists := c.Get(middleware.CtxKeyKeyInfo)
	if !exists {
		writeError(c, http.StatusUnauthorized, "authentication_error", "missing_api_key", "缺少 API Key")
		return nil, false
	}
	keyInfo, ok := value.(*auth.APIKeyInfo)
	if !ok || keyInfo == nil {
		writeError(c, http.StatusUnauthorized, "authentication_error", "invalid_api_key", "API Key 信息无效")
		return nil, false
	}
	return keyInfo, true
}

// upstreamModel 经 key 的 model_mapping 解析上游模型名；无映射用对外名。
func upstreamModel(ch *registry.ChannelKeySnapshot, model string) string {
	if mapped, ok := ch.ModelMapping[model]; ok && mapped != "" {
		return mapped
	}
	return model
}

// maxTaskDataBytes task.data 快照落库上限（超限不存，查询端点回退最小重建）。
const maxTaskDataBytes = 256 << 10

// truncateData 上游原始响应超限时不落库（jsonb 截断会产生非法 JSON）。
func truncateData(raw []byte) []byte {
	if len(raw) == 0 || len(raw) > maxTaskDataBytes {
		return nil
	}
	return raw
}
