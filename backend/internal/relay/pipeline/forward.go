package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

const (
	// entryProtocolOpenAI 入口协议标识（UsageRecord.Platform / RelayInfo.EntryProtocol）。
	entryProtocolOpenAI = "openai"

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
	netErr     error
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

// forward 转发主循环。endpoint 为入口端点标识（adaptor.EndpointChatCompletions /
// EndpointResponses），透传到 RelayInfo 供 adaptor 选 URL/请求改写、pipeline 选流式 usage 提取。
func (p *Pipeline) forward(c *gin.Context, keyInfo *auth.APIKeyInfo, req *dto.ChatRequest, endpoint string) {
	start := time.Now()
	ctx := c.Request.Context()

	// 1. 余额预检（异步扣款模型：只挡余额已为负/零的用户）。
	if keyInfo.UserBalance <= 0 {
		writeError(c, http.StatusPaymentRequired, "insufficient_quota", "insufficient_balance", "账户余额不足")
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
		return
	}

	// 3. user / key 并发闸门。
	releaseClient, ok := p.acquireClientSlots(c, keyInfo)
	if !ok {
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
	attempts := 0
	queueDeadline := start.Add(queueWaitTimeout)
	pollDelay := queuePollInterval

	for attempts < maxFailoverAttempts {
		if ctx.Err() != nil {
			markCanceled(c)
			return
		}

		exclude := make([]int, 0, len(hardExclude)+len(softExclude))
		exclude = append(exclude, hardExclude...)
		exclude = append(exclude, softExclude...)
		ch, err := p.registry.Pick(keyInfo.GroupID, req.Model, exclude)
		if err != nil {
			// 排队退避：有渠道只是"暂时满"（软排除）且未超排队上限 → 清空软排除重新竞争。
			if len(softExclude) > 0 && time.Now().Before(queueDeadline) {
				softExclude = softExclude[:0]
				if !sleepOrCancel(ctx, pollDelay, queueDeadline) {
					markCanceled(c)
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
			Channel:       ch,
			APIKey:        apiKey,
			RequestModel:  req.Model,
			UpstreamModel: upstreamModel(ch, req.Model),
			Stream:        req.Stream,
			Endpoint:      endpoint,
			EntryProtocol: entryProtocolOpenAI,
			Client:        p.client,
		}
		result := p.executeAttempt(c, ad, info, req, start, ch.ID, requestID, rpmMinute)
		attempts++

		// 客户端已取消且未写出任何字节：直接终止（不迁怒渠道）。
		if ctx.Err() != nil && !result.written {
			p.rpm.DecrementChannelRPM(context.Background(), ch.ID, rpmMinute)
			markCanceled(c)
			return
		}

		// 流式已写出首字节：无论成败不可切换渠道，按捕获的（部分）usage 计费后终止。
		if result.written {
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
			p.recordUsage(c, keyInfo, ch, req, result, start, price)
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
			p.recordUsage(c, keyInfo, ch, req, result, start, price)
			writeUpstreamBody(c, result)
			return

		case verdictRateLimited:
			p.rpm.DecrementChannelRPM(context.Background(), ch.ID, rpmMinute)
			p.registry.MarkCooldown(ch.ID, time.Now().Add(o.retryAfter))
			hardExclude = append(hardExclude, ch.ID)
			summary.rateLimited = true
			summary.observeRetryAfter(o.retryAfter)
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
			slog.Warn("relay_channel_auth_failed",
				"channel_id", ch.ID, "model", req.Model,
				"auto_ban", settings.AutoBanEnabled, "reason", o.reason)
			continue

		case verdictTransient:
			p.rpm.DecrementChannelRPM(context.Background(), ch.ID, rpmMinute)
			softExclude = append(softExclude, ch.ID)
			summary.transient = true
			slog.Warn("relay_channel_transient_failure",
				"channel_id", ch.ID, "model", req.Model, "reason", o.reason)
			continue

		default: // verdictClientError：透传终止，不重试；带 usage 仍计费。
			if result.usage != nil {
				p.recordUsage(c, keyInfo, ch, req, result, start, price)
			}
			// 透传前对错误体做精确 key 替换（上游 400 可能回显凭证），其余内容不动。
			result.body = []byte(sanitizeKeyLeak(string(result.body), ch.APIKeys))
			writeUpstreamError(c, result)
			return
		}
	}

	// Responses 端点全渠道 404：透传首个记住的原始 404（状态码 + body），
	// 而非 writeAllFailed 的 5xx——渠道明确「不支持该端点」是可行动的客户端信息。
	if responsesNotFound != nil {
		writeUpstreamError(c, *responsesNotFound)
		return
	}

	writeAllFailed(c, summary)
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

// acquireClientSlots user → key 两级并发闸门；返回反向释放闭包（Background ctx 保证断连也能释放）。
func (p *Pipeline) acquireClientSlots(c *gin.Context, keyInfo *auth.APIKeyInfo) (func(), bool) {
	ctx := c.Request.Context()
	slotID := uuid.New().String()

	if keyInfo.UserMaxConcurrency > 0 {
		if err := p.concurrency.AcquireUserSlot(ctx, keyInfo.UserID, slotID, keyInfo.UserMaxConcurrency, 0); err != nil {
			writeRateLimitError(c, "user_concurrency_limit", "用户并发数已达上限", time.Second)
			return nil, false
		}
	}
	if keyInfo.KeyMaxConcurrency > 0 {
		if err := p.concurrency.AcquireAPIKeySlot(ctx, keyInfo.KeyID, slotID, keyInfo.KeyMaxConcurrency, 0); err != nil {
			if keyInfo.UserMaxConcurrency > 0 {
				p.concurrency.ReleaseUserSlot(context.Background(), keyInfo.UserID, slotID)
			}
			writeRateLimitError(c, "apikey_concurrency_limit", "API Key 并发数已达上限", time.Second)
			return nil, false
		}
	}
	return func() {
		if keyInfo.KeyMaxConcurrency > 0 {
			p.concurrency.ReleaseAPIKeySlot(context.Background(), keyInfo.KeyID, slotID)
		}
		if keyInfo.UserMaxConcurrency > 0 {
			p.concurrency.ReleaseUserSlot(context.Background(), keyInfo.UserID, slotID)
		}
	}, true
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
		return attemptResult{netErr: err}
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
			// 按端点选流式 usage 提取策略：
			//   - responses：usage 只在 completed 事件的 data.response.usage，
			//     且无 usage-only chunk 概念（completed 是正常内容事件），全透传不吞。
			//   - chat：顶层 usage；客户端未请求 include_usage 时吞掉网关注入的 usage-only chunk。
			extractUsage := dto.ExtractUsage
			forwardUsageChunk := req.IncludeUsageRequested()
			isFirstContentLine := chatFirstContentLine
			maxLineBytes := sseMaxLineBytes
			if info.Endpoint == adaptor.EndpointResponses {
				extractUsage = dto.ExtractResponsesUsage
				forwardUsageChunk = true
				// first_token 只认内容增量事件（跳过 response.created 等 ack/preamble）；
				// completed 事件内嵌完整 response，单行放宽到 64MB 防 scanner 截断。
				isFirstContentLine = responsesFirstContentLine
				maxLineBytes = sseMaxLineBytesResponses
			}
			sr := relaySSE(c.Writer, resp, start, extractUsage, forwardUsageChunk, isFirstContentLine, maxLineBytes)
			result.usage = sr.usage
			result.firstTokenMs = sr.firstTokenMs
			result.written = sr.written
			result.streamErr = sr.err
			return result
		}
		slog.Warn("relay_stream_content_type_mismatch",
			"channel_id", info.Channel.ID, "model", info.RequestModel,
			"content_type", result.contentType)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		// 非流式读体失败：未向客户端写出，按网络错误处理（可 failover）。
		return attemptResult{netErr: err}
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

	// 按次计费时把 per_request 单价写入 InputPrice 快照位（成本整单落在 InputCost），
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
		Platform:              entryProtocolOpenAI,
		Model:                 req.Model,
		InputTokens:           inputTokens,
		OutputTokens:          usage.CompletionTokens,
		CachedInputTokens:     usage.CachedTokens,
		CacheCreationTokens:   usage.CacheCreationTokens,
		CacheCreation5mTokens: usage.CacheCreation5mTokens,
		CacheCreation1hTokens: usage.CacheCreation1hTokens,
		ReasoningOutputTokens: usage.ReasoningTokens,
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
		AccountCost:           calc.AccountCost,
		RateMultiplier:        calc.RateMultiplier,
		SellRate:              calc.SellRate,
		AccountRateMultiplier: calc.AccountRateMultiplier,
		Stream:                req.Stream,
		DurationMs:            time.Since(start).Milliseconds(),
		FirstTokenMs:          result.firstTokenMs,
		UserAgent:             c.Request.UserAgent(),
		IPAddress:             c.ClientIP(),
		Endpoint:              c.Request.URL.Path,
	})
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
func writeAllFailed(c *gin.Context, summary failureSummary) {
	switch {
	case summary.rateLimited:
		retryAfter := summary.minRetryAfter
		if retryAfter <= 0 {
			retryAfter = time.Second
		}
		writeRateLimitError(c, "upstream_rate_limited", "所有可用渠道均被限流，请稍后重试", retryAfter)
	case summary.localCapacity:
		writeError(c, http.StatusServiceUnavailable, "server_error", "all_channels_busy", "渠道容量已满，请稍后重试")
	case summary.authFailed:
		// 渠道存在但上游认证失败（401/403）：与「无可用渠道」区分，指向密钥问题。
		writeError(c, http.StatusBadGateway, "server_error", "upstream_auth_failed", "上游认证失败，请联系管理员检查渠道密钥")
	case summary.transient:
		writeError(c, http.StatusBadGateway, "server_error", "upstream_error", "上游服务暂时不可用")
	default:
		writeError(c, http.StatusServiceUnavailable, "server_error", "no_available_channel", "无可用渠道")
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
