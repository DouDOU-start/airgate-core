package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/errlog"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

const (
	// maxFailoverAttempts 单请求内渠道切换上限（真实上游调用次数）。
	maxFailoverAttempts = 3
	// queueWaitTimeout 渠道容量满（RPM/并发）时的最长排队时间。
	queueWaitTimeout = 60 * time.Second
	// queuePollInterval / queueMaxPollInterval 排队退避：200ms 起指数退避，2s 封顶。
	queuePollInterval    = 200 * time.Millisecond
	queueMaxPollInterval = 2 * time.Second

	// nonStreamTimeout 非流式请求总超时；流式无总超时（连接/TLS 超时在 Transport 层）。
	nonStreamTimeout = 5 * time.Minute
	// streamSlotTTL 流式请求的渠道并发槽 TTL：流式无总超时，长流按 30min 防僵尸清理；
	// 非流式维持默认 5min（与 nonStreamTimeout 同量级）。
	streamSlotTTL = 30 * time.Minute
	// maxErrorBodyBytes 上游错误体读取上限。
	maxErrorBodyBytes = 64 << 10
	// maxResponseBodyBytes 非流式成功响应体读取上限。
	maxResponseBodyBytes = 32 << 20

	// statusClientClosedRequest 客户端断连（nginx 惯例 499）。
	statusClientClosedRequest = 499
)

// attemptResult 单次上游调用的执行结果。
type attemptResult struct {
	// netErr 网络层错误（无 HTTP 响应）。
	netErr error
	// buildErr 构建上游请求即失败（非法请求体 / 不支持端点 / 参数翻译失败）；
	// 属客户端/配置问题，未触达网络——一次性 400 终止，不 failover、不计渠道健康。
	buildErr   error
	statusCode int
	headers    http.Header
	// body 非流式成功响应体（model 已回写）或上游错误体（≤64KB）。
	body        []byte
	contentType string
	// usage 提取/捕获的用量（成功响应、SSE 旁路、4xx 错误体皆可能携带）。
	usage        *dto.Usage
	firstTokenMs int64
	// written 已向客户端写出字节（流式）——写出后不可 failover。
	written bool
	// streamErr 流式中途失败（written 恒为 true，只能终止）。
	streamErr error
	// done 流式是否收到 data: [DONE] 完成标志（仅 written=true 时有意义）。
	done bool
}

// failureSummary 记录各类失败，用于全部渠道耗尽后的响应选择。
type failureSummary struct {
	rateLimited   bool
	minRetryAfter time.Duration
	authFailed    bool
	transient     bool
	localCapacity bool
}

func (s *failureSummary) observeRetryAfter(d time.Duration) {
	if s.minRetryAfter == 0 || d < s.minRetryAfter {
		s.minRetryAfter = d
	}
}

// protocolForEndpoint 入口端点 → 入口协议（Pick 协议过滤 / errfmt 分发 / RelayInfo 传递）。
func protocolForEndpoint(endpoint string) string {
	switch endpoint {
	case adaptor.EndpointMessages, adaptor.EndpointMessagesCountTokens:
		return registry.ProtocolAnthropic
	case adaptor.EndpointGenerateContent, adaptor.EndpointPredict, adaptor.EndpointCountTokens:
		return registry.ProtocolGemini
	default:
		return registry.ProtocolOpenAI
	}
}

// forwardOptions 端点级转发选项（零值即既有默认行为）。
type forwardOptions struct {
	// rawBody / rawContentType 非 JSON 端点（multipart 等）的原样体：
	// rawBody 非 nil 时经 RelayInfo 交 adaptor 原始字节直发上游（不重组），
	// req 字段表仅承载调度所需 model/stream。
	rawBody        []byte
	rawContentType string
	// zeroBilling 零计费端点（countTokens 类）：成功/带 usage 的 4xx 均不写
	// usage_log、不扣费；余额预检与 failover/outcome 语义照常保留。
	zeroBilling bool
}

// forward 转发主循环（默认选项），语义见 forwardOpt。
func (p *Pipeline) forward(c *gin.Context, keyInfo *auth.APIKeyInfo, req *dto.ChatRequest, endpoint string) {
	p.forwardOpt(c, keyInfo, req, endpoint, forwardOptions{})
}

// forwardOpt 转发主循环。endpoint 为入口端点标识（adaptor.Endpoint* 常量），
// 透传到 RelayInfo 供 adaptor 选 URL/请求改写、pipeline 选流式 usage 捕获策略；
// 由 endpoint 派生的入口协议决定 Pick 只在同协议渠道集合内调度（纯透传，零翻译）。
func (p *Pipeline) forwardOpt(c *gin.Context, keyInfo *auth.APIKeyInfo, req *dto.ChatRequest, endpoint string, opts forwardOptions) {
	start := time.Now()
	ctx := c.Request.Context()
	protocol := protocolForEndpoint(endpoint)

	// 用户 / 分组 RPM 观测计数：已鉴权即计入（含后续被预检拒绝的请求），供管理端展示请求速率。
	p.rpm.IncrementUserRPM(ctx, keyInfo.UserID)
	if keyInfo.GroupID > 0 {
		p.rpm.IncrementGroupRPM(ctx, keyInfo.GroupID)
	}

	// 1. 余额预检（异步扣款模型：只挡余额已为负/零的用户）。
	if keyInfo.UserBalance <= 0 {
		writeError(c, http.StatusPaymentRequired, "insufficient_quota", "insufficient_balance", "账户余额不足")
		p.recordFailure(c, keyInfo, req, start, errlog.Entry{
			Phase: errlog.PhasePrecheckBalance, StatusCode: http.StatusPaymentRequired,
			ErrorType: "insufficient_quota", ErrorCode: "insufficient_balance", Message: "账户余额不足",
		})
		return
	}

	settings := p.settings.Get(ctx)

	// 2. 缺价预检：未配置模型价格一律 400 拒绝（无放行开关，杜绝零成本记账漏洞）。
	// 解析到的 Price 随请求传递到计费收尾复用（不二次 Get），
	// 避免请求期间缓存失效/重载失败把已定价模型静默记 0。
	price, priced := p.pricing.Get(req.Model)
	if !priced {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "model_price_not_configured",
			"模型 "+req.Model+" 未配置价格")
		p.recordFailure(c, keyInfo, req, start, errlog.Entry{
			Phase: errlog.PhasePrecheckPrice, StatusCode: http.StatusBadRequest,
			ErrorType: "invalid_request_error", ErrorCode: "model_price_not_configured",
			Message: "模型 " + req.Model + " 未配置价格",
		})
		return
	}

	// 3. user / key 并发闸门。
	releaseClient, limitCode := p.acquireClientSlots(c, keyInfo)
	if limitCode != "" {
		p.recordFailure(c, keyInfo, req, start, errlog.Entry{
			Phase: errlog.PhaseLocalLimit, StatusCode: http.StatusTooManyRequests,
			ErrorType: "rate_limit_error", ErrorCode: limitCode, Message: "并发数已达上限",
		})
		return
	}
	defer releaseClient()

	// 4. failover 主循环：
	//    hardExclude 跨循环持久（429 冷却 / 认证失败 / 配置故障），
	//    softExclude 容量满（RPM/并发）——排队退避时清空重新竞争。
	var hardExclude, softExclude []int
	summary := failureSummary{}
	// responsesNotFound 记住 Responses 端点上游 404 的原始响应（已脱敏）：
	// 部分 openai_compatible 上游只实现 /v1/chat/completions，对 /v1/responses 回 404，
	// 此时软排除换渠道重试；全渠道耗尽后把原始 404（状态码 + body）透传给客户端，
	// 而非误转成 all-failed 的 5xx。
	var responsesNotFound *attemptResult
	// hops 重试链（每次真实上游尝试一跳），随失败留痕落 attempt_chain。
	var hops []errlog.AttemptHop
	attempts := 0
	recordCanceled := func() {
		p.recordFailure(c, keyInfo, req, start, errlog.Entry{
			Phase: errlog.PhaseCanceled, StatusCode: statusClientClosedRequest,
			Message: "客户端取消请求", Attempts: attempts, Chain: hops,
		})
	}
	queueDeadline := start.Add(queueWaitTimeout)
	pollDelay := queuePollInterval

	for attempts < maxFailoverAttempts {
		if ctx.Err() != nil {
			markCanceled(c)
			recordCanceled()
			return
		}

		exclude := make([]int, 0, len(hardExclude)+len(softExclude))
		exclude = append(exclude, hardExclude...)
		exclude = append(exclude, softExclude...)
		ch, err := p.registry.Pick(keyInfo.GroupID, req.Model, protocol, exclude)
		if err != nil {
			// 排队退避：有渠道只是"暂时满"（软排除）且未超排队上限 → 清空软排除重新竞争。
			if len(softExclude) > 0 && time.Now().Before(queueDeadline) {
				softExclude = softExclude[:0]
				if !sleepOrCancel(ctx, pollDelay, queueDeadline) {
					markCanceled(c)
					recordCanceled()
					return
				}
				pollDelay = min(pollDelay*2, queueMaxPollInterval)
				continue
			}
			break
		}

		// 渠道级配置检查：适配器 / 密钥，任一缺失即硬排除（不消耗 attempt）。
		ad, err := adaptor.GetAdaptor(ch.Type)
		if err != nil {
			slog.Warn("relay_channel_type_unsupported", "channel_id", ch.ID, "type", ch.Type)
			hardExclude = append(hardExclude, ch.ID)
			continue
		}
		apiKey := p.registry.NextKey(ch.ID)
		if apiKey == "" {
			slog.Warn("relay_channel_no_api_key", "channel_id", ch.ID)
			hardExclude = append(hardExclude, ch.ID)
			continue
		}

		// 渠道 RPM + 并发闸门：满则软排除（可排队重竞争），不消耗 attempt。
		// rpmMinute 为预递增所用的分钟窗口，失败回退时对同一窗口 decrement
		//（不重取当前时间，防跨分钟边界扣穿新窗口）。
		rpmOK, rpmMinute, _ := p.rpm.TryIncrementChannelRPM(ctx, ch.ID, ch.MaxRPM)
		if !rpmOK {
			summary.localCapacity = true
			softExclude = append(softExclude, ch.ID)
			continue
		}
		requestID := uuid.New().String()
		if err := p.concurrency.AcquireChannelSlot(ctx, ch.ID, requestID, ch.MaxConcurrency, channelSlotTTL(req.Stream)); err != nil {
			p.rpm.DecrementChannelRPM(ctx, ch.ID, rpmMinute)
			summary.localCapacity = true
			softExclude = append(softExclude, ch.ID)
			continue
		}
		pollDelay = queuePollInterval // 抢到槽位即重置退避

		info := &adaptor.RelayInfo{
			Channel:        ch,
			APIKey:         apiKey,
			RequestModel:   req.Model,
			UpstreamModel:  upstreamModel(ch, req.Model),
			Stream:         req.Stream,
			Endpoint:       endpoint,
			RawBody:        opts.rawBody,
			RawContentType: opts.rawContentType,
			Client:         p.client,
		}
		attemptStart := time.Now()
		result := p.executeAttempt(c, ad, info, req, start, ch.ID, requestID, rpmMinute)
		attemptLatency := time.Since(attemptStart).Milliseconds()
		attempts++

		// 构建上游请求即失败（坏请求体/不支持端点/翻译失败）：客户端/配置问题，
		// 一次性 400 终止，不 failover、不计渠道健康信号。
		if result.buildErr != nil {
			p.rpm.DecrementChannelRPM(context.Background(), ch.ID, rpmMinute)
			msg := sanitizeKeyLeak(result.buildErr.Error(), ch.APIKeys)
			writeError(c, http.StatusBadRequest, "invalid_request_error", "bad_request", msg)
			p.recordFailure(c, keyInfo, req, start, errlog.Entry{
				Phase: errlog.PhaseBadRequest, StatusCode: http.StatusBadRequest,
				ErrorType: "invalid_request_error", ErrorCode: "bad_request",
				Message: msg, Attempts: attempts,
				ChannelID: ch.ID, ChannelName: ch.Name,
			})
			return
		}

		// 客户端已取消且未写出任何字节：直接终止（不迁怒渠道）。
		if ctx.Err() != nil && !result.written {
			p.rpm.DecrementChannelRPM(context.Background(), ch.ID, rpmMinute)
			markCanceled(c)
			recordCanceled()
			return
		}

		// 流式已写出首字节：无论成败不可切换渠道，按捕获的（部分）usage 计费后终止。
		if result.written {
			// 完成判定：chat 看 [DONE]；messages/generateContent 由观察器给出协议级
			// 完成信号（message_stop / finishReason，已回填 result.done）；
			// responses 无 [DONE] 语义，以 completed 事件（usage 捕获点）为完成信号。
			streamComplete := result.done
			if endpoint == adaptor.EndpointResponses {
				streamComplete = result.usage != nil
			}
			if result.streamErr != nil {
				slog.Warn("relay_stream_aborted",
					"channel_id", ch.ID, "model", req.Model, "error", result.streamErr)
			} else {
				p.registry.MarkRecovered(ch.ID)
				if result.usage == nil {
					// 契约要求：流式成功但未捕获 usage → 记 0 并告警（可疑的计费缺口）。
					slog.Warn("relay_stream_usage_missing",
						"channel_id", ch.ID, "model", req.Model)
				}
			}
			if !opts.zeroBilling {
				p.recordUsage(c, keyInfo, ch, req, result, start, price)
			}
			if result.streamErr != nil || !streamComplete {
				// 流式中断（含上游不发完成标志即断连的「静默不完整流」）：
				// usage_log 照旧落账（billed=true），失败日志补一行供排障。
				// Message 用固定文案：传输层原始错误串含上游 IP:port（渠道拓扑），
				// 而 Message 会透出到用户端失败视图；明细只进重试链（仅管理员可见）。
				reason := "上游未发送完成标志即断流"
				if result.streamErr != nil {
					reason = sanitizeKeyLeak(result.streamErr.Error(), ch.APIKeys)
				}
				hop := attemptHop(len(hops)+1, ch, apiKey, result.statusCode, "streamAborted", reason, 0, attemptLatency, false)
				if p.errSink != nil {
					p.errSink.CountFailure(context.Background(), ch.ID, "streamAborted", "")
				}
				p.recordFailure(c, keyInfo, req, start, errlog.Entry{
					Phase: errlog.PhaseStreamAborted, StatusCode: result.statusCode,
					Message: "上游流中断，响应未完成", Billed: true,
					Attempts: attempts, Chain: append(hops, hop),
					ChannelID: ch.ID, ChannelName: ch.Name,
				})
			}
			return
		}

		o := classifyOutcome(result.statusCode, result.headers, result.body, result.netErr, settings.BanKeywords)
		// 判定原因可能携带上游回显的渠道密钥（进日志/落库/管理端），出口前脱敏。
		o.reason = sanitizeKeyLeak(o.reason, ch.APIKeys)

		// 仅 Responses 端点：上游 404（渠道可能不支持 /v1/responses）当作可重试的软排除，
		// 换同组其他渠道尝试；透传体先脱敏并记住，failover 耗尽后透传原始 404（见循环末）。
		// chat 的 404 仍归 verdictClientError（一次性透传不重试），行为不变。
		if info.Endpoint == adaptor.EndpointResponses && result.statusCode == http.StatusNotFound && o.verdict == verdictClientError {
			result.body = []byte(sanitizeKeyLeak(string(result.body), ch.APIKeys))
			snapshot := result
			responsesNotFound = &snapshot
			o.verdict = verdictTransient
			o.reason = "responses 端点上游 404（渠道可能不支持），换渠道重试"
		}
		switch o.verdict {
		case verdictSuccess:
			p.registry.MarkRecovered(ch.ID)
			// 零计费端点（countTokens 类）：usage 归零、不写 usage_log；
			// failover/outcome/透传语义与常规端点完全一致。
			if !opts.zeroBilling {
				p.recordUsage(c, keyInfo, ch, req, result, start, price)
			}
			writeUpstreamBody(c, result)
			return

		case verdictRateLimited:
			p.rpm.DecrementChannelRPM(context.Background(), ch.ID, rpmMinute)
			p.registry.MarkCooldown(ch.ID, time.Now().Add(o.retryAfter))
			hardExclude = append(hardExclude, ch.ID)
			summary.rateLimited = true
			summary.observeRetryAfter(o.retryAfter)
			hops = append(hops, attemptHop(len(hops)+1, ch, apiKey, result.statusCode, "rateLimited", o.reason, o.retryAfter.Milliseconds(), attemptLatency, false))
			if p.errSink != nil {
				p.errSink.CountFailure(context.Background(), ch.ID, "rateLimited", "")
			}
			slog.Warn("relay_channel_rate_limited",
				"channel_id", ch.ID, "model", req.Model, "retry_after", o.retryAfter.String())
			continue

		case verdictAuthFailed:
			p.rpm.DecrementChannelRPM(context.Background(), ch.ID, rpmMinute)
			if settings.AutoBanEnabled {
				p.registry.MarkAutoDisabled(ch.ID, truncateErrorMsg(o.reason))
			}
			hardExclude = append(hardExclude, ch.ID)
			summary.authFailed = true
			hops = append(hops, attemptHop(len(hops)+1, ch, apiKey, result.statusCode, "authFailed", o.reason, 0, attemptLatency, settings.AutoBanEnabled))
			if p.errSink != nil {
				p.errSink.CountFailure(context.Background(), ch.ID, "authFailed", "")
			}
			slog.Warn("relay_channel_auth_failed",
				"channel_id", ch.ID, "model", req.Model,
				"auto_ban", settings.AutoBanEnabled, "reason", o.reason)
			continue

		case verdictTransient:
			p.rpm.DecrementChannelRPM(context.Background(), ch.ID, rpmMinute)
			softExclude = append(softExclude, ch.ID)
			summary.transient = true
			verdictName := "transient"
			if result.netErr != nil {
				verdictName = "networkError"
			}
			hops = append(hops, attemptHop(len(hops)+1, ch, apiKey, result.statusCode, verdictName, o.reason, 0, attemptLatency, false))
			if p.errSink != nil {
				p.errSink.CountFailure(context.Background(), ch.ID, verdictName, "")
			}
			slog.Warn("relay_channel_transient_failure",
				"channel_id", ch.ID, "model", req.Model, "reason", o.reason)
			continue

		default: // verdictClientError：透传终止，不重试；带 usage 仍计费（零计费端点除外）。
			billed := result.usage != nil && !opts.zeroBilling
			if billed {
				p.recordUsage(c, keyInfo, ch, req, result, start, price)
			}
			// 透传前对错误体做精确 key 替换（上游 400 可能回显凭证），其余内容不动。
			result.body = []byte(sanitizeKeyLeak(string(result.body), ch.APIKeys))
			writeUpstreamError(c, result)
			// clientError 多为调用方参数问题，不计入渠道错误率（防脏渠道健康信号）。
			hop := attemptHop(len(hops)+1, ch, apiKey, result.statusCode, "clientError", o.reason, 0, attemptLatency, false)
			p.recordFailure(c, keyInfo, req, start, errlog.Entry{
				Phase: errlog.PhaseUpstreamClientError, StatusCode: result.statusCode,
				Message: o.reason, Billed: billed,
				Attempts: attempts, Chain: append(hops, hop),
				ChannelID: ch.ID, ChannelName: ch.Name,
			})
			return
		}
	}

	// Responses 端点全渠道 404：透传首个记住的原始 404（状态码 + body），
	// 而非 writeAllFailed 的 5xx——渠道明确「不支持该端点」是可行动的客户端信息。
	if responsesNotFound != nil {
		writeUpstreamError(c, *responsesNotFound)
		p.recordFailure(c, keyInfo, req, start, errlog.Entry{
			Phase: errlog.PhaseUpstreamClientError, StatusCode: responsesNotFound.statusCode,
			Message:  "responses 端点全渠道 404（渠道均不支持该端点）",
			Attempts: attempts, Chain: hops,
		})
		return
	}

	status, errType, errCode, msg := writeAllFailed(c, summary)
	phase := errlog.PhaseUpstreamExhausted
	if attempts == 0 {
		phase = errlog.PhaseQueueTimeout
	}
	p.recordFailure(c, keyInfo, req, start, errlog.Entry{
		Phase: phase, StatusCode: status,
		ErrorType: errType, ErrorCode: errCode, Message: msg,
		Attempts: attempts, Chain: hops,
	})
}

// attemptHop 构造重试链一跳（reason 已由调用方 sanitizeKeyLeak 脱敏，
// errlog sink 端还会再过通用凭证正则与截断）。
func attemptHop(seq int, ch *registry.ChannelSnapshot, apiKey string, upstreamStatus int, verdict, reason string, retryAfterMs, latencyMs int64, autoDisabled bool) errlog.AttemptHop {
	return errlog.AttemptHop{
		Seq:          seq,
		ChannelID:    ch.ID,
		ChannelName:  ch.Name,
		KeyHint:      keyHint(apiKey),
		UpstreamStat: upstreamStatus,
		Verdict:      verdict,
		Reason:       reason,
		RetryAfterMs: retryAfterMs,
		LatencyMs:    latencyMs,
		AutoDisabled: autoDisabled,
	}
}

// keyHint 渠道密钥尾 4 位提示（明文永不落库）。
// 口径与 sanitize.go 的 maskAPIKey 一致：长度 >4 保留尾 4 位。
func keyHint(key string) string {
	if len(key) <= 4 {
		return "…"
	}
	return "…" + key[len(key)-4:]
}

// recordFailure 上游请求日志失败留痕（relay 来源公共字段填充）；errSink 未注入时 no-op。
// phase 维度计数器随留痕恒 INCR（错误率事实源与采样落库解耦）。
func (p *Pipeline) recordFailure(c *gin.Context, keyInfo *auth.APIKeyInfo, req *dto.ChatRequest, start time.Time, e errlog.Entry) {
	if p.errSink == nil {
		return
	}
	e.RequestID = requestIDOf(c)
	e.Source = errlog.SourceRelay
	if e.Model == "" {
		e.Model = req.Model
	}
	e.Endpoint = c.Request.URL.Path
	e.Stream = req.Stream
	e.UserID = keyInfo.UserID
	e.UserEmail = keyInfo.UserEmail
	e.APIKeyID = keyInfo.KeyID
	e.GroupID = keyInfo.GroupID
	e.IPAddress = c.ClientIP()
	e.UserAgent = c.Request.UserAgent()
	e.DurationMs = time.Since(start).Milliseconds()
	p.errSink.CountFailure(context.Background(), 0, "", e.Phase)
	p.errSink.Record(e)
}

// channelSlotTTL 渠道并发槽 TTL：流式 30min（无总超时的长流防僵尸清理），
// 非流式用默认值（传 0 → concurrency 层 5min）。
func channelSlotTTL(stream bool) time.Duration {
	if stream {
		return streamSlotTTL
	}
	return 0
}

// executeAttempt 执行单次上游调用，并保证渠道并发槽/RPM 在 panic 时也正确回收：
// 槽位恒经 defer 释放；panic 时回退 RPM 预递增后继续向上抛（由 Recovery 中间件转 500）。
func (p *Pipeline) executeAttempt(c *gin.Context, ad adaptor.Adaptor, info *adaptor.RelayInfo, req *dto.ChatRequest, start time.Time, channelID int, requestID string, rpmMinute int64) attemptResult {
	defer func() {
		p.concurrency.ReleaseChannelSlot(context.Background(), channelID, requestID)
		if rec := recover(); rec != nil {
			p.rpm.DecrementChannelRPM(context.Background(), channelID, rpmMinute)
			panic(rec)
		}
	}()
	return p.execute(c, ad, info, req, start)
}

// acquireClientSlots user → key 两级并发闸门。成功返回 (释放闭包, "")；
// 拒绝时已写出 429 错误体，返回 (nil, 错误码) 供失败留痕。
// 顺带记录分组维度在途槽位（纯观测口径，不限流），供管理端展示分组实时并发。
func (p *Pipeline) acquireClientSlots(c *gin.Context, keyInfo *auth.APIKeyInfo) (func(), string) {
	ctx := c.Request.Context()
	slotID := uuid.New().String()

	if keyInfo.UserMaxConcurrency > 0 {
		if err := p.concurrency.AcquireUserSlot(ctx, keyInfo.UserID, slotID, keyInfo.UserMaxConcurrency, 0); err != nil {
			writeRateLimitError(c, "user_concurrency_limit", "用户并发数已达上限", time.Second)
			return nil, "user_concurrency_limit"
		}
	}
	if keyInfo.KeyMaxConcurrency > 0 {
		if err := p.concurrency.AcquireAPIKeySlot(ctx, keyInfo.KeyID, slotID, keyInfo.KeyMaxConcurrency, 0); err != nil {
			if keyInfo.UserMaxConcurrency > 0 {
				p.concurrency.ReleaseUserSlot(context.Background(), keyInfo.UserID, slotID)
			}
			writeRateLimitError(c, "apikey_concurrency_limit", "API Key 并发数已达上限", time.Second)
			return nil, "apikey_concurrency_limit"
		}
	}
	if keyInfo.GroupID > 0 {
		p.concurrency.TrackGroupSlot(ctx, keyInfo.GroupID, slotID, 0)
	}
	return func() {
		if keyInfo.GroupID > 0 {
			p.concurrency.ReleaseGroupSlot(context.Background(), keyInfo.GroupID, slotID)
		}
		if keyInfo.KeyMaxConcurrency > 0 {
			p.concurrency.ReleaseAPIKeySlot(context.Background(), keyInfo.KeyID, slotID)
		}
		if keyInfo.UserMaxConcurrency > 0 {
			p.concurrency.ReleaseUserSlot(context.Background(), keyInfo.UserID, slotID)
		}
	}, ""
}

// execute 单次上游调用：构建请求 → 直发 → 按流式/非流式分派响应处理。
func (p *Pipeline) execute(c *gin.Context, ad adaptor.Adaptor, info *adaptor.RelayInfo, req *dto.ChatRequest, start time.Time) attemptResult {
	ctx := c.Request.Context()
	cancel := context.CancelFunc(func() {})
	if !info.Stream {
		ctx, cancel = context.WithTimeout(ctx, nonStreamTimeout)
	}
	defer cancel()

	httpReq, err := ad.BuildRequest(ctx, info, req)
	if err != nil {
		// BuildRequest 发生在触网之前：坏请求体/不支持端点/翻译失败/坏 URL 都是
		// 客户端或配置问题，failover 重试无益，且不应污染渠道健康。归 buildErr。
		return attemptResult{buildErr: err}
	}
	resp, err := info.Client.Do(httpReq)
	if err != nil {
		return attemptResult{netErr: err}
	}
	defer func() { _ = resp.Body.Close() }()

	result := attemptResult{
		statusCode:  resp.StatusCode,
		headers:     resp.Header,
		contentType: resp.Header.Get("Content-Type"),
	}

	// 上游非 2xx：读错误体（≤64KB）供判定/透传；错误体带 usage 仍计费。
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		result.body = body
		if u, found := dto.ExtractUsage(body); found {
			result.usage = &u
		}
		return result
	}

	// 流式仅在上游确按 SSE 响应时走透传；2xx 但 Content-Type 非 text/event-stream
	//（上游忽略 stream 参数 / param_override 关闭了 stream）按非流式路径处理，
	// 保证 usage 提取、model 回写与计费语义不丢。
	if info.Stream {
		if isSSEContentType(result.contentType) {
			// 按端点选流式 usage 捕获策略（纯透传：入口协议==渠道协议，Pick 已保证，
			// 不存在任何跨协议翻译路径）：
			//   - chat：顶层 usage；客户端未请求 include_usage 时吞掉网关注入的 usage-only chunk。
			//   - responses：usage 只在 completed 事件的 data.response.usage，
			//     且无 usage-only chunk 概念（completed 是正常内容事件），全透传不吞。
			//   - messages / generateContent / images：原生协议流字节级透传，适配器
			//     提供透传型观察器旁路解析 usage / 完成信号（images 另计产出张数）。
			extractUsage := dto.ExtractUsage
			forwardUsageChunk := req.IncludeUsageRequested()
			isFirstContentLine := chatFirstContentLine
			maxLineBytes := sseMaxLineBytes
			switch info.Endpoint {
			case adaptor.EndpointResponses:
				extractUsage = dto.ExtractResponsesUsage
				forwardUsageChunk = true
				// first_token 只认内容增量事件（跳过 response.created 等 ack/preamble）；
				// completed 事件内嵌完整 response，单行放宽到 64MB 防 scanner 截断。
				isFirstContentLine = responsesFirstContentLine
				maxLineBytes = sseMaxLineBytesResponses
			case adaptor.EndpointMessages:
				// first_token 只认 content_block_delta（跳过 message_start 等 ack）。
				isFirstContentLine = anthropicFirstContentLine
			case adaptor.EndpointImagesGenerations, adaptor.EndpointImagesEdits:
				// 图像流（观察器路径，内联 extractUsage 不参与）：partial/completed
				// 事件内嵌整幅 b64 图，单行可达数十 MB，放宽上限防 scanner 截断。
				maxLineBytes = sseMaxLineBytesResponses
			}
			var observer adaptor.StreamObserver
			if so, ok := ad.(adaptor.StreamObserving); ok {
				observer = so.NewStreamObserver(info)
			}
			sr := relaySSE(c.Writer, resp, start, extractUsage, forwardUsageChunk, isFirstContentLine, maxLineBytes, observer)
			result.usage = sr.usage
			result.firstTokenMs = sr.firstTokenMs
			result.written = sr.written
			result.streamErr = sr.err
			// done 标志回传：streamErr==nil 但未收到完成信号的「静默不完整流」
			//（上游不发完成标志即断连）借此可被失败日志捕获。
			result.done = sr.done
			return result
		}
		slog.Warn("relay_stream_content_type_mismatch",
			"channel_id", info.Channel.ID, "model", info.RequestModel,
			"content_type", result.contentType)
	}

	// 多读 1 字节探测超限：超过上限时按错误处理（可 failover），
	// 绝不把截断的半截 JSON 静默透传给客户端。
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		// 非流式读体失败：未向客户端写出，按网络错误处理（可 failover）。
		return attemptResult{netErr: err}
	}
	if len(body) > maxResponseBodyBytes {
		return attemptResult{netErr: fmt.Errorf("上游响应体超过 %d 字节上限", maxResponseBodyBytes)}
	}
	rewritten, usage := ad.ParseNonStreamResponse(info, body)
	result.body = rewritten
	result.usage = usage
	return result
}

// isSSEContentType 判断响应 Content-Type 是否为 SSE（容忍 charset 等参数与大小写）。
func isSSEContentType(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream")
}

// recordUsage 计费收尾：ComputeCosts → Calculate 三管道 → UsageRecord 落账。
// price 为转发前缺价预检解析的快照（每请求解析一次，不二次 Get，
// 避免请求期间缓存失效把已定价请求静默记 0）；缺价请求在预检已被拒绝，进不到这里。
func (p *Pipeline) recordUsage(c *gin.Context, keyInfo *auth.APIKeyInfo, ch *registry.ChannelSnapshot, req *dto.ChatRequest, result attemptResult, start time.Time, price pricing.Price) {
	if p.sink == nil {
		return
	}

	var usage dto.Usage
	if result.usage != nil {
		usage = *result.usage
	}
	tier := serviceTierOf(req)
	costs := pricing.ComputeCosts(price, pricing.Usage{
		PromptTokens:          usage.PromptTokens,
		CompletionTokens:      usage.CompletionTokens,
		CachedTokens:          usage.CachedTokens,
		CacheCreationTokens:   usage.CacheCreationTokens,
		CacheCreation5mTokens: usage.CacheCreation5mTokens,
		CacheCreation1hTokens: usage.CacheCreation1hTokens,
		Calls:                 usage.Calls,
	}, tier)
	calc := p.calculator.Calculate(billing.CalculateInput{
		InputCost:         costs.Input,
		OutputCost:        costs.Output,
		CachedInputCost:   costs.Cached,
		CacheCreationCost: costs.CacheCreation5m + costs.CacheCreation1h,
		BillingRate:       billing.ResolveBillingRate(keyInfo),
		SellRate:          keyInfo.SellRate,
		AccountRate:       ch.CostRatio,
	})

	// 展示口径与成本口径对齐：input 记扣除 cached 后的部分（cached 单列）。
	inputTokens := usage.PromptTokens - usage.CachedTokens
	if inputTokens < 0 {
		inputTokens = 0
	}

	// 按次计费时把 per_request 单价写入 InputPrice 快照位，成本整单落在
	// InputCost = per_request × 计次数（图像端点计次数=响应产出张数，其余端点恒 1），
	// 保证 usage_log 的「单价 × 用量 = 成本」对账口径成立。
	inputPrice := price.Input
	if price.PerRequest > 0 {
		inputPrice = price.PerRequest
	}

	p.sink.Record(billing.UsageRecord{
		UserID:                keyInfo.UserID,
		UserEmail:             keyInfo.UserEmail,
		APIKeyID:              keyInfo.KeyID,
		ChannelID:             ch.ID,
		GroupID:               keyInfo.GroupID,
		Model:                 req.Model,
		InputTokens:           inputTokens,
		OutputTokens:          usage.CompletionTokens,
		CachedInputTokens:     usage.CachedTokens,
		CacheCreationTokens:   usage.CacheCreationTokens,
		CacheCreation5mTokens: usage.CacheCreation5mTokens,
		CacheCreation1hTokens: usage.CacheCreation1hTokens,
		Calls:                 usage.Calls,
		InputPrice:            inputPrice,
		OutputPrice:           price.Output,
		CachedInputPrice:      price.CachedInput,
		CacheCreationPrice:    price.CacheCreation5m,
		CacheCreation1hPrice:  price.CacheCreation1h,
		ServiceTier:           tier,
		InputCost:             calc.InputCost,
		OutputCost:            calc.OutputCost,
		CachedInputCost:       calc.CachedInputCost,
		CacheCreationCost:     calc.CacheCreationCost,
		TotalCost:             calc.TotalCost,
		ActualCost:            calc.ActualCost,
		BilledCost:            calc.BilledCost,
		RateMultiplier:        calc.RateMultiplier,
		SellRate:              calc.SellRate,
		AccountRateMultiplier: calc.AccountRateMultiplier,
		Stream:                req.Stream,
		DurationMs:            time.Since(start).Milliseconds(),
		FirstTokenMs:          result.firstTokenMs,
		UserAgent:             truncateRunes(c.Request.UserAgent(), maxUserAgentLen),
		IPAddress:             c.ClientIP(),
		Endpoint:              c.Request.URL.Path,
		Source:                billing.SourceRelay,
		RequestID:             requestIDOf(c),
	})
}

// maxUserAgentLen user_agent 落库长度上限：UA 为攻击者可控头部（HTTP 头上限约 1MB），
// 不截断会让每条计费记录被塞入无界文本。
const maxUserAgentLen = 512

// truncateRunes 按 rune 截断字符串，避免把多字节字符切碎。
func truncateRunes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}

// writeUpstreamBody 非流式成功：透传上游状态码 / Content-Type / 响应体（model 已回写）。
func writeUpstreamBody(c *gin.Context, result attemptResult) {
	contentType := result.contentType
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(result.statusCode, contentType, result.body)
}

// writeUpstreamError 不可重试 4xx：原样透传上游错误体；空体时合成 OpenAI 错误体。
func writeUpstreamError(c *gin.Context, result attemptResult) {
	if len(result.body) == 0 {
		writeError(c, result.statusCode, "invalid_request_error", "upstream_error", "上游返回错误")
		return
	}
	contentType := result.contentType
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(result.statusCode, contentType, result.body)
}

// writeAllFailed 全部渠道耗尽后的响应选择
// （优先级：429 > 容量满 > 上游认证失败 > 上游故障 > 无渠道）。
// 返回写下的 (状态码, error_type, error_code, message) 供失败留痕复用同一事实源。
func writeAllFailed(c *gin.Context, summary failureSummary) (int, string, string, string) {
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
		// 渠道存在但上游认证失败（401/403）：与「无可用渠道」区分，指向密钥问题。
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

// markCanceled 客户端断连：未写出过字节时补 499 状态。
func markCanceled(c *gin.Context) {
	if !c.Writer.Written() {
		c.Status(statusClientClosedRequest)
	}
	c.Abort()
}

// sleepOrCancel 等待 delay（不超过 deadline），期间请求取消返回 false。
func sleepOrCancel(ctx context.Context, delay time.Duration, deadline time.Time) bool {
	if remaining := time.Until(deadline); delay > remaining {
		delay = remaining
	}
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// serviceTierOf 从请求体读取 OpenAI service_tier 字段（priority/flex/standard/auto）；
// 缺失或非字符串返回 ""（计费按标准档，不套服务档倍率）。
func serviceTierOf(req *dto.ChatRequest) string {
	raw, ok := req.Get("service_tier")
	if !ok {
		return ""
	}
	var tier string
	if err := json.Unmarshal(raw, &tier); err != nil {
		return ""
	}
	return tier
}

// upstreamModel 经渠道 model_mapping 解析上游模型名；无映射用对外名。
func upstreamModel(ch *registry.ChannelSnapshot, model string) string {
	if mapped, ok := ch.ModelMapping[model]; ok && mapped != "" {
		return mapped
	}
	return model
}
