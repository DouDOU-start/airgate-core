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
	"github.com/DouDOU-start/airgate-core/internal/relay/clientid"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/errfmt"
	"github.com/DouDOU-start/airgate-core/internal/relay/outcome"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/relay/relayhook"
)

const (
	// maxFailoverAttempts 单请求内渠道切换上限（真实上游调用次数）。
	maxFailoverAttempts = 3
	// queueWaitTimeout 渠道容量满（RPM/并发）时的最长排队时间。
	queueWaitTimeout = 60 * time.Second
	// queuePollInterval / queueMaxPollInterval 排队退避：200ms 起指数退避，2s 封顶。
	queuePollInterval    = 200 * time.Millisecond
	queueMaxPollInterval = 2 * time.Second
	// maxQueueWaiters 排队退避的全局在途上限。每个排队请求在最长 60s 的等待期内
	// 整段持有完整请求体（上限 32MB）与 user/key 并发槽，过载时无上限排队会把内存
	// 撑爆（万级排队 × 平均百 KB 请求体即 GB 级）；超限按渠道容量满快速失败泄压。
	maxQueueWaiters = 4096

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

// resolveAlphaSearchPrice 解析 codex 联网搜索的按次单价（USD/次）：
// 分组覆盖价（Group.alpha_search_price，非 nil 即生效，含 0=免费）优先，
// 否则用全局 gateway 设置 alpha_search_price。负值钳 0（防脏配置写入负成本）。
// 返回值仅为基础单价，实际扣费在 recordUsage 再叠加分组倍率（billing_rate）。
func resolveAlphaSearchPrice(settings GatewaySettings, keyInfo *auth.APIKeyInfo) float64 {
	price := settings.AlphaSearchPrice
	if keyInfo != nil && keyInfo.GroupAlphaSearchPrice != nil {
		price = *keyInfo.GroupAlphaSearchPrice
	}
	if price < 0 {
		price = 0
	}
	return price
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

	// 0. 客户端识别 + 分组客户端限制预检。
	clientid.Detect(c)
	if len(keyInfo.GroupAllowedClients) > 0 {
		if !clientid.Matches(clientid.Get(c), keyInfo.GroupAllowedClients) {
			if keyInfo.GroupFallbackID != nil {
				keyInfo.GroupID = *keyInfo.GroupFallbackID
			} else {
				writeError(c, http.StatusForbidden, "permission_error", "client_restricted",
					"当前客户端类型不允许访问此分组")
				p.recordFailure(c, keyInfo, req, start, errlog.Entry{
					Phase: errlog.PhasePrecheckClientRestrict, StatusCode: http.StatusForbidden,
					ErrorType: "permission_error", ErrorCode: "client_restricted",
					Message: "当前客户端类型不允许访问此分组",
				})
				return
			}
		}
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

	// 2. 倍率预检：实际扣费倍率超过密钥最高倍率时拒绝（管理员临时调价的保护闸）；
	// 零计费端点（countTokens 类）不产生消费，不拦。
	if !opts.zeroBilling {
		if rate := billing.ResolveBillingRate(keyInfo); billing.ExceedsKeyMaxRate(keyInfo, rate) {
			msg := fmt.Sprintf("当前计费倍率 %.2f 超过密钥最高倍率 %.2f", rate, keyInfo.MaxRate)
			writeError(c, http.StatusForbidden, "permission_error", "billing_rate_exceeded", msg)
			p.recordFailure(c, keyInfo, req, start, errlog.Entry{
				Phase: errlog.PhasePrecheckRate, StatusCode: http.StatusForbidden,
				ErrorType: "permission_error", ErrorCode: "billing_rate_exceeded", Message: msg,
			})
			return
		}
	}

	settings := p.settings.Get(ctx)

	// 3. 缺价预检：未配置模型价格一律 400 拒绝（无放行开关，杜绝零成本记账漏洞）。
	// 解析到的 Price 随请求传递到计费收尾复用（不二次 Get），
	// 避免请求期间缓存失效/重载失败把已定价模型静默记 0。
	price, priced := p.pricing.Get(req.Model)
	if endpoint == adaptor.EndpointAlphaSearch {
		// 联网搜索按次计费：单价取分组覆盖价 ?? 全局 gateway 设置，与模型价目表解耦
		//（SearchResponse 无 token usage，仅 2xx 成功时按次×1 计价，实际扣费再叠加分组倍率）。
		price = pricing.Price{PerRequest: resolveAlphaSearchPrice(settings, keyInfo)}
	} else if !priced {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "model_price_not_configured",
			"模型 "+req.Model+" 未配置价格")
		p.recordFailure(c, keyInfo, req, start, errlog.Entry{
			Phase: errlog.PhasePrecheckPrice, StatusCode: http.StatusBadRequest,
			ErrorType: "invalid_request_error", ErrorCode: "model_price_not_configured",
			Message: "模型 " + req.Model + " 未配置价格",
		})
		return
	}

	// 4. 外部 Relay Hook：只处理 JSON 请求。插件可返回完整替换体和本次请求的
	// 有序账号计划；Core 会校验 model/stream 与候选账号，失败时沿用原逻辑。
	var routePlan *relayhook.RoutePlan
	if opts.rawBody == nil {
		req, routePlan = p.applyRelayHook(c, keyInfo, req, endpoint, protocol)
	}

	// 5. 内容审核预检（风控中心）：触网前判定，被拦截的请求不占并发槽。
	if !p.moderationCheck(c, keyInfo, req, endpoint, opts, start) {
		return
	}

	// 6. user / key 并发闸门。
	releaseClient, limitCode := p.acquireClientSlots(c, keyInfo)
	if limitCode != "" {
		p.recordFailure(c, keyInfo, req, start, errlog.Entry{
			Phase: errlog.PhaseLocalLimit, StatusCode: http.StatusTooManyRequests,
			ErrorType: "rate_limit_error", ErrorCode: limitCode, Message: "并发数已达上限",
		})
		return
	}
	defer releaseClient()

	// 7. failover 主循环：
	//    hardExclude 跨循环持久（429 限流 / 认证失败 / 配置故障，仅本次请求内生效），
	//    softExclude 容量满（RPM/并发）——排队退避时清空重新竞争。
	var hardExcludeKeys, softExcludeKeys []int
	var hardExcludeAccounts, softExcludeAccounts []int
	summary := failureSummary{}
	// responsesNotFound 记住 Responses 端点上游 404 的语义（已解析脱敏）：
	// 部分 openai_compatible 上游只实现 /v1/chat/completions，对 /v1/responses 回 404，
	// 此时软排除换渠道重试；全渠道耗尽后按 404 原状态码语义重建渲染给客户端，
	// 而非误转成 all-failed 的 5xx。
	var responsesNotFound *errfmt.UpstreamError
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

		excludeKeys := make([]int, 0, len(hardExcludeKeys)+len(softExcludeKeys))
		excludeKeys = append(excludeKeys, hardExcludeKeys...)
		excludeKeys = append(excludeKeys, softExcludeKeys...)
		excludeAccounts := make([]int, 0, len(hardExcludeAccounts)+len(softExcludeAccounts))
		excludeAccounts = append(excludeAccounts, hardExcludeAccounts...)
		excludeAccounts = append(excludeAccounts, softExcludeAccounts...)

		target, ok := p.pickRoute(keyInfo.GroupID, req.Model, protocol, excludeKeys, excludeAccounts, routePlan)
		if !ok {
			// 排队退避：有目标只是"暂时满"（软排除）且未超排队上限 → 清空软排除重新竞争。
			if (len(softExcludeKeys) > 0 || len(softExcludeAccounts) > 0) && time.Now().Before(queueDeadline) {
				if p.queueWaiters.Add(1) > maxQueueWaiters {
					p.queueWaiters.Add(-1)
					summary.localCapacity = true
					break
				}
				softExcludeKeys = softExcludeKeys[:0]
				softExcludeAccounts = softExcludeAccounts[:0]
				waited := sleepOrCancel(ctx, pollDelay, queueDeadline)
				p.queueWaiters.Add(-1)
				if !waited {
					markCanceled(c)
					recordCanceled()
					return
				}
				pollDelay = min(pollDelay*2, queueMaxPollInterval)
				continue
			}
			break
		}

		// ---------- 账号路径（CPA）----------
		if target.kind == routeAccount {
			acc := target.account
			if p.cpa == nil {
				slog.Warn("relay_account_cpa_unavailable", "account_id", acc.ID)
				hardExcludeAccounts = append(hardExcludeAccounts, acc.ID)
				continue
			}
			requestID, rpmMinute, soft, slotOK := p.acquireAccountSlots(ctx, acc, req.Stream)
			if !slotOK {
				summary.localCapacity = true
				if soft {
					softExcludeAccounts = append(softExcludeAccounts, acc.ID)
				} else {
					hardExcludeAccounts = append(hardExcludeAccounts, acc.ID)
				}
				continue
			}
			pollDelay = queuePollInterval
			payload, perr := prepareAccountPayload(req, opts)
			if perr != nil {
				p.concurrency.ReleaseAccountSlot(context.Background(), acc.ID, requestID)
				p.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
				writeError(c, http.StatusBadRequest, "invalid_request_error", "bad_request", "序列化请求体失败")
				p.recordFailure(c, keyInfo, req, start, errlog.Entry{
					Phase: errlog.PhaseBadRequest, StatusCode: http.StatusBadRequest,
					ErrorType: "invalid_request_error", ErrorCode: "bad_request",
					Message: perr.Error(), Attempts: attempts,
					AccountID: acc.ID, AccountName: acc.Name,
				})
				return
			}
			attemptStart := time.Now()
			result := p.executeAccountAttempt(c, acc, req, endpoint, protocol, payload, start, requestID, rpmMinute)
			attemptLatency := time.Since(attemptStart).Milliseconds()
			attempts++
			if p.handleAccountOutcome(c, keyInfo, acc, req, result, start, price, settings, opts,
				rpmMinute, attempts, &hops, &summary, &hardExcludeAccounts, &softExcludeAccounts, attemptLatency) {
				return
			}
			continue
		}

		// ---------- 渠道 key 路径（零翻译透传）----------
		ch := target.channel
		// key 级配置检查：适配器 / 密钥，任一缺失即硬排除（不消耗 attempt）。
		ad, err := adaptor.GetAdaptor(ch.Type)
		if err != nil {
			slog.Warn("relay_channel_key_type_unsupported", "channel_key_id", ch.KeyID, "type", ch.Type)
			hardExcludeKeys = append(hardExcludeKeys, ch.KeyID)
			continue
		}
		apiKey := ch.APIKey
		if apiKey == "" {
			slog.Warn("relay_channel_key_no_api_key", "channel_key_id", ch.KeyID)
			hardExcludeKeys = append(hardExcludeKeys, ch.KeyID)
			continue
		}
		capacityID := ch.CredentialID
		if capacityID <= 0 {
			capacityID = ch.KeyID
		}

		// 物理凭证 RPM + 并发闸门：同一 API Key 的多个协议端点共享限额。
		// rpmMinute 为预递增所用的分钟窗口，失败回退时对同一窗口 decrement
		//（不重取当前时间，防跨分钟边界扣穿新窗口）。
		rpmOK, rpmMinute, _ := p.rpm.TryIncrementKeyRPM(ctx, capacityID, ch.MaxRPM)
		if !rpmOK {
			summary.localCapacity = true
			softExcludeKeys = append(softExcludeKeys, ch.KeyID)
			continue
		}
		requestID := uuid.New().String()
		if err := p.concurrency.AcquireKeySlot(ctx, capacityID, requestID, ch.MaxConcurrency, channelSlotTTL(req.Stream)); err != nil {
			p.rpm.DecrementKeyRPM(ctx, capacityID, rpmMinute)
			summary.localCapacity = true
			softExcludeKeys = append(softExcludeKeys, ch.KeyID)
			continue
		}
		pollDelay = queuePollInterval // 抢到槽位即重置退避

		info := &adaptor.RelayInfo{
			ChannelKey:     ch,
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
		result := p.executeAttempt(c, ad, info, req, start, capacityID, requestID, rpmMinute)
		attemptLatency := time.Since(attemptStart).Milliseconds()
		attempts++

		// 构建上游请求即失败（坏请求体/不支持端点/翻译失败）：客户端/配置问题，
		// 一次性 400 终止，不 failover、不计渠道健康信号。
		if result.buildErr != nil {
			p.rpm.DecrementKeyRPM(context.Background(), capacityID, rpmMinute)
			// 用户可见消息额外抹掉上游渠道身份（base_url/主机/IP）；管理端留痕保留渠道细节。
			adminMsg := outcome.SanitizeKeyLeak(result.buildErr.Error(), []string{apiKey})
			userMsg := outcome.SanitizeUpstreamLeak(result.buildErr.Error(), []string{apiKey}, ch.BaseURL)
			writeError(c, http.StatusBadRequest, "invalid_request_error", "bad_request", userMsg)
			p.recordFailure(c, keyInfo, req, start, errlog.Entry{
				Phase: errlog.PhaseBadRequest, StatusCode: http.StatusBadRequest,
				ErrorType: "invalid_request_error", ErrorCode: "bad_request",
				Message: adminMsg, Attempts: attempts,
				ChannelID: ch.ChannelID, ChannelName: ch.ChannelName,
			})
			return
		}

		// 客户端已取消且未写出任何字节：直接终止（不迁怒 key）。
		if ctx.Err() != nil && !result.written {
			p.rpm.DecrementKeyRPM(context.Background(), capacityID, rpmMinute)
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
					"channel_key_id", ch.KeyID, "model", req.Model, "error", result.streamErr)
			} else {
				p.registry.MarkRecovered(ch.KeyID)
				if p.healthTracker != nil {
					p.healthTracker.RecordSuccess(ch.KeyID)
				}
				if result.usage == nil {
					// 契约要求：流式成功但未捕获 usage → 记 0 并告警（可疑的计费缺口）。
					slog.Warn("relay_stream_usage_missing",
						"channel_key_id", ch.KeyID, "model", req.Model)
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
					reason = outcome.SanitizeKeyLeak(result.streamErr.Error(), []string{apiKey})
				}
				hop := attemptHop(len(hops)+1, ch, apiKey, result.statusCode, "streamAborted", reason, 0, attemptLatency, false)
				if p.errSink != nil {
					p.errSink.CountFailure(context.Background(), ch.ChannelID, "streamAborted", "")
				}
				p.recordFailure(c, keyInfo, req, start, errlog.Entry{
					Phase: errlog.PhaseStreamAborted, StatusCode: result.statusCode,
					Message: "上游流中断，响应未完成", Billed: true,
					Attempts: attempts, Chain: append(hops, hop),
					ChannelID: ch.ChannelID, ChannelName: ch.ChannelName,
				})
			}
			return
		}

		o := outcome.Classify(result.statusCode, result.headers, result.body, result.netErr)
		// 判定原因可能携带上游回显的渠道密钥（进日志/落库/管理端），出口前脱敏。
		o.Reason = outcome.SanitizeKeyLeak(o.Reason, []string{apiKey})

		// 仅 Responses 端点：上游 404（渠道可能不支持 /v1/responses）当作可重试的软排除，
		// 换同组其他渠道尝试；错误语义先解析脱敏并记住，failover 耗尽后按 404 原状态码
		// 语义重建渲染（见循环末）。chat 的 404 仍归 verdictClientError（一次性终止不重试）。
		if info.Endpoint == adaptor.EndpointResponses && result.statusCode == http.StatusNotFound && o.Verdict == outcome.ClientError {
			up := errfmt.ParseUpstream(result.statusCode, result.body)
			// 上游错误 message 出口给用户前抹掉渠道身份（含 base_url/主机/IP），不止密钥。
			up.Message = outcome.SanitizeUpstreamLeak(up.Message, []string{apiKey}, ch.BaseURL)
			responsesNotFound = &up
			o.Verdict = outcome.Transient
			o.Reason = "responses 端点上游 404（渠道可能不支持），换渠道重试"
		}
		switch o.Verdict {
		case outcome.Success:
			p.registry.MarkRecovered(ch.KeyID)
			if p.healthTracker != nil {
				p.healthTracker.RecordSuccess(ch.KeyID)
			}
			// 零计费端点（countTokens 类）：usage 归零、不写 usage_log；
			// failover/outcome/透传语义与常规端点完全一致。
			if !opts.zeroBilling {
				p.recordUsage(c, keyInfo, ch, req, result, start, price)
			}
			writeUpstreamBody(c, result)
			return

		case outcome.RateLimited:
			// 仅本次请求内硬排除换 key 重试；不设冷却状态，下次请求照常调度。
			p.rpm.DecrementKeyRPM(context.Background(), capacityID, rpmMinute)
			if p.healthTracker != nil {
				p.healthTracker.RecordFailure(ch.KeyID)
			}
			hardExcludeKeys = append(hardExcludeKeys, ch.KeyID)
			summary.rateLimited = true
			summary.observeRetryAfter(o.RetryAfter)
			hops = append(hops, attemptHop(len(hops)+1, ch, apiKey, result.statusCode, "rateLimited", o.Reason, o.RetryAfter.Milliseconds(), attemptLatency, false))
			if p.errSink != nil {
				p.errSink.CountFailure(context.Background(), ch.ChannelID, "rateLimited", "")
			}
			slog.Warn("relay_channel_key_rate_limited",
				"channel_key_id", ch.KeyID, "model", req.Model, "retry_after", o.RetryAfter.String())
			continue

		case outcome.AuthFailed:
			p.rpm.DecrementKeyRPM(context.Background(), capacityID, rpmMinute)
			if p.healthTracker != nil {
				p.healthTracker.RecordAuthFailure(ch.KeyID)
			}
			if settings.AutoBanEnabled {
				if result.statusCode == http.StatusUnauthorized {
					p.registry.MarkCredentialAutoDisabled(ch.KeyID, outcome.TruncateErrorMsg(o.Reason))
				} else {
					p.registry.MarkAutoDisabled(ch.KeyID, outcome.TruncateErrorMsg(o.Reason))
				}
			}
			hardExcludeKeys = append(hardExcludeKeys, ch.KeyID)
			summary.authFailed = true
			hops = append(hops, attemptHop(len(hops)+1, ch, apiKey, result.statusCode, "authFailed", o.Reason, 0, attemptLatency, settings.AutoBanEnabled))
			if p.errSink != nil {
				p.errSink.CountFailure(context.Background(), ch.ChannelID, "authFailed", "")
			}
			slog.Warn("relay_channel_key_auth_failed",
				"channel_key_id", ch.KeyID, "model", req.Model,
				"auto_ban", settings.AutoBanEnabled, "reason", o.Reason)
			continue

		case outcome.Transient:
			p.rpm.DecrementKeyRPM(context.Background(), capacityID, rpmMinute)
			if p.healthTracker != nil {
				p.healthTracker.RecordFailure(ch.KeyID)
			}
			softExcludeKeys = append(softExcludeKeys, ch.KeyID)
			summary.transient = true
			verdictName := "transient"
			if result.netErr != nil {
				verdictName = "networkError"
			}
			hops = append(hops, attemptHop(len(hops)+1, ch, apiKey, result.statusCode, verdictName, o.Reason, 0, attemptLatency, false))
			if p.errSink != nil {
				p.errSink.CountFailure(context.Background(), ch.ChannelID, verdictName, "")
			}
			slog.Warn("relay_channel_key_transient_failure",
				"channel_key_id", ch.KeyID, "model", req.Model, "reason", o.Reason)
			continue

		default: // verdictClientError：语义重建终止，不重试；带 usage 仍计费（零计费端点除外）。
			billed := result.usage != nil && !opts.zeroBilling
			if billed {
				p.recordUsage(c, keyInfo, ch, req, result, start, price)
			}
			// 语义保留、载体重建：解析上游错误体提取 (message/type/code)，按入口协议
			// 渲染（HTTP 状态码保留上游原值）；message 出口给用户前抹掉渠道身份
			// （密钥 + base_url/主机/IP），确保用户看不到实际上游渠道。
			// 原始响应体不透传，仅经 o.Reason 片段进失败留痕。
			up := errfmt.ParseUpstream(result.statusCode, result.body)
			up.Message = outcome.SanitizeUpstreamLeak(up.Message, []string{apiKey}, ch.BaseURL)
			writeUpstreamError(c, result.statusCode, up)
			// clientError 多为调用方参数问题，不计入渠道错误率（防脏渠道健康信号）。
			hop := attemptHop(len(hops)+1, ch, apiKey, result.statusCode, "clientError", o.Reason, 0, attemptLatency, false)
			p.recordFailure(c, keyInfo, req, start, errlog.Entry{
				Phase: errlog.PhaseUpstreamClientError, StatusCode: result.statusCode,
				Message: o.Reason, Billed: billed,
				Attempts: attempts, Chain: append(hops, hop),
				ChannelID: ch.ChannelID, ChannelName: ch.ChannelName,
			})
			return
		}
	}

	// Responses 端点全渠道 404：按首个记住的 404 语义重建渲染（保留 404 原状态码），
	// 而非 writeAllFailed 的 5xx——渠道明确「不支持该端点」是可行动的客户端信息。
	if responsesNotFound != nil {
		writeUpstreamError(c, http.StatusNotFound, *responsesNotFound)
		p.recordFailure(c, keyInfo, req, start, errlog.Entry{
			Phase: errlog.PhaseUpstreamClientError, StatusCode: http.StatusNotFound,
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
func (p *Pipeline) executeAttempt(c *gin.Context, ad adaptor.Adaptor, info *adaptor.RelayInfo, req *dto.ChatRequest, start time.Time, channelKeyID int, requestID string, rpmMinute int64) attemptResult {
	defer func() {
		p.concurrency.ReleaseKeySlot(context.Background(), channelKeyID, requestID)
		if rec := recover(); rec != nil {
			p.rpm.DecrementKeyRPM(context.Background(), channelKeyID, rpmMinute)
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
			"channel_key_id", info.ChannelKey.KeyID, "model", info.RequestModel,
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
func (p *Pipeline) recordUsage(c *gin.Context, keyInfo *auth.APIKeyInfo, ch *registry.ChannelKeySnapshot, req *dto.ChatRequest, result attemptResult, start time.Time, price pricing.Price) {
	if p.sink == nil {
		return
	}

	// 用户 / 分组 RPM 观测计数：与 usage_log 同源，仅成功计费的请求计入，口径对齐仪表盘。
	// 用 Background ctx，避免请求收尾（尤其流式结束）ctx 已取消导致漏计。
	p.rpm.IncrementUserGroupRPM(context.Background(), keyInfo.UserID, keyInfo.GroupID)

	var usage dto.Usage
	if result.usage != nil {
		usage = *result.usage
	}
	usage = enrichImageBillingUsage(c.Request.URL.Path, req, result.body, usage)
	tier := serviceTierOf(req)
	reasoningEffort := reasoningEffortOf(c, req)
	costs := pricing.ComputeCosts(price, pricing.Usage{
		PromptTokens:          usage.PromptTokens,
		CompletionTokens:      usage.CompletionTokens,
		CachedTokens:          usage.CachedTokens,
		CacheCreationTokens:   usage.CacheCreationTokens,
		CacheCreation5mTokens: usage.CacheCreation5mTokens,
		CacheCreation1hTokens: usage.CacheCreation1hTokens,
		Calls:                 usage.Calls,
		ImageSize:             usage.ImageSize,
		ImageQuality:          usage.ImageQuality,
	}, tier)
	calc := p.calculator.Calculate(billing.CalculateInput{
		InputCost:         costs.Input,
		OutputCost:        costs.Output,
		CachedInputCost:   costs.Cached,
		CacheCreationCost: costs.CacheCreation5m + costs.CacheCreation1h,
		BillingRate:       billing.ResolveBillingRate(keyInfo),
		SellRate:          keyInfo.SellRate,
		AccountRate:       ch.EffectiveCostRatio(),
	})

	// 展示口径与成本口径对齐：input 记扣除 cached 后的部分（cached 单列）。
	inputTokens := usage.PromptTokens - usage.CachedTokens
	if inputTokens < 0 {
		inputTokens = 0
	}

	// 按次/按张计费时把生效单价写入 InputPrice 快照位，成本整单落在
	// InputCost = 单价 × 计次数（图像端点计次数=响应产出张数，其余端点恒 1），
	// 保证 usage_log 的「单价 × 用量 = 成本」对账口径成立。
	// 单价来源与 ComputeCosts 同一优先级链：分辨率表命中 > per_request。
	inputPrice := price.Input
	// billedCalls 落账计次：按次计费下上游未给张数（如联网搜索）时按 1 次记，
	// 保证 usage_log「单价 × 计次 = 成本」对账口径成立（ComputeCosts 内部同样把 <1 钳为 1）。
	billedCalls := usage.Calls
	if perImage, ok := pricing.ImagePriceFor(price, usage.ImageQuality, usage.ImageSize); ok {
		inputPrice = perImage
		if billedCalls < 1 {
			billedCalls = 1
		}
	} else if price.PerRequest > 0 {
		inputPrice = price.PerRequest
		if billedCalls < 1 {
			billedCalls = 1
		}
	}

	p.sink.Record(billing.UsageRecord{
		UserID:                keyInfo.UserID,
		UserEmail:             keyInfo.UserEmail,
		APIKeyID:              keyInfo.KeyID,
		ChannelID:             ch.ChannelID,
		ChannelKeyID:          ch.KeyID,
		GroupID:               keyInfo.GroupID,
		Model:                 req.Model,
		InputTokens:           inputTokens,
		OutputTokens:          usage.CompletionTokens,
		CachedInputTokens:     usage.CachedTokens,
		CacheCreationTokens:   usage.CacheCreationTokens,
		CacheCreation5mTokens: usage.CacheCreation5mTokens,
		CacheCreation1hTokens: usage.CacheCreation1hTokens,
		Calls:                 billedCalls,
		InputPrice:            inputPrice,
		OutputPrice:           price.Output,
		CachedInputPrice:      price.CachedInput,
		CacheCreationPrice:    price.CacheCreation5m,
		CacheCreation1hPrice:  price.CacheCreation1h,
		ServiceTier:           tier,
		ReasoningEffort:       reasoningEffort,
		ImageSize:             usage.ImageSize,
		ImageQuality:          usage.ImageQuality,
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

// writeUpstreamError 不可重试 4xx：语义保留、载体重建——上游错误体解析出的语义字段
// （已由调用方脱敏）经 errfmt 按入口协议渲染，HTTP 状态码保留上游原值；
// 原始响应体不透传（只进失败留痕）。
func writeUpstreamError(c *gin.Context, status int, up errfmt.UpstreamError) {
	c.JSON(status, errfmt.RenderUpstream(entryProtocolOf(c), status, up, requestIDOf(c)))
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

// reasoningEffortOf 从请求体读取推理强度档位（low/medium/high/xhigh/max）：
// OpenAI Chat Completions 顶层 reasoning_effort、Responses 嵌套 reasoning.effort、
// Anthropic 嵌套 output_config.effort，三协议取值域一致，原样展示不做归一化。
func reasoningEffortOf(c *gin.Context, req *dto.ChatRequest) string {
	if entryProtocolOf(c) == registry.ProtocolAnthropic {
		return nestedStringField(req, "output_config", "effort")
	}
	if raw, ok := req.Get("reasoning_effort"); ok {
		var effort string
		if err := json.Unmarshal(raw, &effort); err == nil {
			return effort
		}
	}
	return nestedStringField(req, "reasoning", "effort")
}

// nestedStringField 读取请求体里形如 {"<parent>": {"<child>": "..."}} 的嵌套字符串字段；
// parent 缺失/非对象或 child 缺失/非字符串一律返回 ""。
func nestedStringField(req *dto.ChatRequest, parent, child string) string {
	raw, ok := req.Get(parent)
	if !ok {
		return ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	v, ok := obj[child]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return ""
	}
	return s
}

// upstreamModel 经 key 的 model_mapping 解析上游模型名；无映射用对外名。
func upstreamModel(ch *registry.ChannelKeySnapshot, model string) string {
	if mapped, ok := ch.ModelMapping[model]; ok && mapped != "" {
		return mapped
	}
	return model
}
