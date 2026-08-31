package pipeline

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/clientid"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/errfmt"
	"github.com/DouDOU-start/airgate-core/internal/relay/outcome"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/relay/relayhook"
	"github.com/DouDOU-start/airgate-core/internal/relay/streamlife"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/DouDOU-start/airgate-core/internal/requestaudit"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
)

const (
	// maxFailoverAttempts 单请求内渠道切换上限（真实上游调用次数）。
	maxFailoverAttempts = 3
	// maxRateLimitProbesPerRequest 单请求最多执行一次限流账号恢复探测，
	// 避免插件计划中的坏账号吃光 failover 预算，给健康 Core 候选保留机会。
	maxRateLimitProbesPerRequest = 1
	// queueWaitTimeout 渠道容量满（RPM/并发）时的最长排队时间。
	queueWaitTimeout = 60 * time.Second
	// queuePollInterval / queueMaxPollInterval 排队退避：200ms 起指数退避，2s 封顶。
	queuePollInterval    = 200 * time.Millisecond
	queueMaxPollInterval = 2 * time.Second
	// maxQueueWaiters 排队退避的全局在途上限。每个排队请求在最长 60s 的等待期内
	// 整段持有完整请求体（上限 32MB）与 user/key 并发槽，过载时无上限排队会把内存
	// 撑爆（万级排队 × 平均百 KB 请求体即 GB 级）；超限按渠道容量满快速失败泄压。
	maxQueueWaiters = 4096

	// nonStreamTimeout 非流式请求总超时；流式请求使用 streamlife.MaxDuration 硬上限。
	nonStreamTimeout = 5 * time.Minute
	// streamSlotTTL 流式请求的渠道并发槽 TTL：长流按 30min 防僵尸清理；
	// 非流式维持默认 5min（与 nonStreamTimeout 同量级）。
	streamSlotTTL = streamlife.MaxDuration
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
	usage *dto.Usage
	// firstTokenMs 是从请求进入转发主循环到内容首字的请求级总耗时，继续用于用量和审计。
	firstTokenMs int64
	// attemptFirstTokenMs 是当前渠道 attempt 自身的内容首字耗时，只用于渠道调度 EWMA。
	attemptFirstTokenMs int64
	// requestFirstTokenMs 为包含发包前处理和前序故障转移的请求级真实首字耗时。
	// 账号调度 EWMA 仍使用 firstTokenMs，避免把前序账号失败惩罚算到最终成功账号。
	requestFirstTokenMs int64
	// written 已向客户端写出字节（流式）——写出后不可 failover。
	written bool
	// dataReceived 已从 provider 收到数据，但未必已向客户端写出。与
	// written 分开建模：缓冲中的 partial/error body 也意味着请求可能已
	// 触达上游，不能再切换账号或渠道重放。
	dataReceived bool
	// responseStarted records that the provider response boundary was observed
	// even when no body bytes/status/header values were available. This is a
	// stronger replay-safety marker than dataReceived for native transports.
	responseStarted bool
	// streamErr 流式中途失败（written 恒为 true，只能终止）。
	streamErr error
	// done 流式是否收到 data: [DONE] 完成标志（仅 written=true 时有意义）。
	done bool
	// auditErr 审计写入失败；该错误发生在真实触网前，调用方必须以 500 终止。
	auditErr error
	// auditAttempt 是普通渠道路径在发包前创建的上游审计行。
	auditAttempt *requestaudit.AttemptHandle
}

// unreplayableProviderFailure reports an indeterminate attempt that crossed
// the provider response boundary. Nothing may have reached the downstream
// client yet, but the provider may already have consumed (and billed) the
// request. Retrying on another account or channel would therefore risk
// duplicate execution.
func (r attemptResult) unreplayableProviderFailure() error {
	if (!r.dataReceived && !r.responseStarted) || r.written {
		return nil
	}
	switch {
	case r.netErr != nil:
		return r.netErr
	case r.streamErr != nil:
		return r.streamErr
	case r.buildErr != nil:
		// BuildErr normally means the request never touched the network. If a
		// provider also reports DataReceived, the latter is the stronger safety
		// signal and must suppress failover.
		return r.buildErr
	default:
		return nil
	}
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

// channelRoutingProtocolForEndpoint 返回渠道候选池使用的内部协议。
// CPA 已覆盖的文本端点允许三类文本渠道混合调度；生图、视频、音乐、搜索等
// 未纳入翻译白名单的端点仍按入口协议只选择原生渠道。
func channelRoutingProtocolForEndpoint(endpoint string) string {
	switch endpoint {
	case adaptor.EndpointMemoriesTraceSummarize, adaptor.EndpointRealtimeSideband,
		adaptor.EndpointGuardian, adaptor.EndpointGuardianClassifier,
		adaptor.EndpointHistoryListWindows, adaptor.EndpointHistoryListItems,
		adaptor.EndpointHistoryReadItem, adaptor.EndpointHistorySearchContents,
		adaptor.EndpointNotesListFilesByPrefix, adaptor.EndpointNotesReadFile,
		adaptor.EndpointNotesSearchContents, adaptor.EndpointNotesAppendToFile,
		adaptor.EndpointNotesWriteFile, adaptor.EndpointNotesThreadHint,
		adaptor.EndpointAnalyticsEvents, adaptor.EndpointFilesCreate,
		adaptor.EndpointFilesFinalize,
		adaptor.EndpointCodexUsage, adaptor.EndpointCodexThreadUsage,
		adaptor.EndpointCodexRateLimitResetCredits,
		adaptor.EndpointCodexRateLimitResetCreditsConsume,
		adaptor.EndpointCodexAccountsCheck, adaptor.EndpointCodexAccountsNudge,
		adaptor.EndpointCodexProfilesMe, adaptor.EndpointCodexConfigBundle,
		adaptor.EndpointCodexSettingsUser, adaptor.EndpointCodexTasks,
		adaptor.EndpointCodexTasksList, adaptor.EndpointCodexTaskDetails,
		adaptor.EndpointCodexTaskSiblingTurns,
		adaptor.EndpointCodexEnvironments, adaptor.EndpointCodexEnvironmentsByRepo,
		adaptor.EndpointCodexWorkspaceMessages, adaptor.EndpointCodexPSMCP,
		adaptor.EndpointCodexPluginsList, adaptor.EndpointCodexPluginsSearch,
		adaptor.EndpointCodexPluginsSuggested, adaptor.EndpointCodexPluginsInstalled,
		adaptor.EndpointCodexPluginsWorkspaceShared,
		adaptor.EndpointCodexPluginsWorkspaceCreated, adaptor.EndpointCodexPluginDetail,
		adaptor.EndpointCodexPluginSkillDetail, adaptor.EndpointCodexPluginInstall,
		adaptor.EndpointCodexPluginUninstall, adaptor.EndpointCodexPluginShares,
		adaptor.EndpointCodexConnectorsDirectoryList,
		adaptor.EndpointCodexConnectorsDirectoryListWorkspace,
		adaptor.EndpointCodexAppsBatch,
		adaptor.EndpointCodexPluginsFeatured,
		adaptor.EndpointCodexPluginLegacyEnable,
		adaptor.EndpointCodexPluginLegacyUninstall,
		adaptor.EndpointCodexPluginsWorkspaceUploadURL,
		adaptor.EndpointCodexPluginsWorkspaceCreate,
		adaptor.EndpointCodexPluginsWorkspaceUpdate,
		adaptor.EndpointCodexPluginsWorkspaceDetail,
		adaptor.EndpointCodexPluginsWorkspaceDelete,
		adaptor.EndpointCodexRemoteControlEnroll, adaptor.EndpointCodexRemoteControlRefresh,
		adaptor.EndpointCodexRemoteControlPair, adaptor.EndpointCodexRemoteControlPairStatus,
		adaptor.EndpointCodexRemoteControlClientsList, adaptor.EndpointCodexRemoteControlClientRevoke,
		adaptor.EndpointCodexRemoteControlServerWebSocket,
		adaptor.EndpointCodexTurnCosts:
		// No registry channel implements these native-only Codex wire
		// contracts. An internal protocol key keeps ordinary CPA channels out of
		// the candidate index; forwardOptions adds the account-family guard.
		return "codex_native"
	case adaptor.EndpointChatCompletions,
		adaptor.EndpointResponses,
		adaptor.EndpointMessages,
		adaptor.EndpointMessagesCountTokens,
		adaptor.EndpointGenerateContent,
		adaptor.EndpointCountTokens:
		return registry.ProtocolTranslatedText
	default:
		return protocolForEndpoint(endpoint)
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
	// method overrides the upstream HTTP method for native/raw account
	// contracts. The zero value preserves the historical POST behavior used by
	// model-generation endpoints.
	method string
	// rawBody / rawContentType 非 JSON 端点（multipart 等）的原样体：
	// rawBody 非 nil 时经 RelayInfo 交 adaptor 原始字节直发上游（不重组），
	// req 字段表仅承载调度所需 model/stream。
	rawBody        []byte
	rawContentType string
	// providerPath overrides the endpoint-derived native provider path. Codex
	// realtime call creation uses /live for its Frameless wire shape.
	providerPath string
	// nativeCodexAccountsOnly prevents native Codex contracts from ever
	// entering an ordinary channel or a non-Codex account translation path.
	nativeCodexAccountsOnly bool
	// channelRoutingProtocol overrides the endpoint's default channel candidate
	// protocol for shared aliases. Native Codex requests retain the internal
	// codex_native pool; ordinary shared Realtime requests use OpenAI channels.
	channelRoutingProtocol string
	// nativeCodexAPIKeyOnly restricts a native control-plane call to API-key
	// Codex accounts. The official turn-cost reconciliation endpoint is
	// available only on the public API-key contract; selecting a ChatGPT OAuth
	// account would produce an invalid backend path and needlessly consume a
	// failover attempt.
	nativeCodexAPIKeyOnly bool
	// nativeCodexOAuthOnly restricts a native control-plane call to ChatGPT
	// OAuth accounts. Remote plugin catalog/mutation APIs reject API-key auth
	// and must not silently fall through to a CPA channel.
	nativeCodexOAuthOnly bool
	// preferNativeCodex makes native Codex accounts win over ordinary CPA
	// channels for a Codex CLI request while retaining the normal CPA route as
	// a safe fallback when the plugin/account family is unavailable. The
	// handler decides this from request signals and the plugin-wide policy.
	preferNativeCodex bool
	// allowUnpriced is limited to zero-billing native control-plane calls whose
	// provider response has no billable usage (realtime bootstrap/memories).
	allowUnpriced bool
	// passthroughResponse preserves the native endpoint's response status,
	// headers, content type, and body even for non-2xx client errors.
	passthroughResponse bool
	// providerHeaders overrides the safe inbound header projection for a
	// request-scoped native control-plane contract. A nil value deletes a
	// projected header; selected-account credentials are still applied by the
	// transport layer.
	providerHeaders http.Header
	// providerQueryDefaults supplies wire-required query parameters for native
	// control-plane contracts. Defaults are added only when the caller did not
	// provide that key, so explicit client values always win.
	providerQueryDefaults map[string][]string
	// zeroBilling 零计费端点（countTokens 类）：成功/带 usage 的 4xx 均不写
	// usage_log、不扣费；余额预检与 failover/outcome 语义照常保留。
	zeroBilling bool
	// nativeCodexAccountID pins a native-only control request to the account
	// selected by an earlier step (for example, Files finalize must stay on
	// the account that created the file). Zero keeps normal weighted routing.
	nativeCodexAccountID int
	// responseTransform runs on a successful native account response before
	// it is written to the caller. It is used by the Files create contract to
	// replace the provider upload URL with an AirGate opaque upload token.
	responseTransform func(*accountreg.Snapshot, *attemptResult) error
	// Remote Control metadata is kept separate from ordinary provider headers.
	// The native Codex executor uses the enrolled server token only for pair,
	// pair/status and websocket contracts; the OAuth lease remains authoritative
	// for enroll/refresh and client-management calls. These values are populated
	// by the Remote Control entry handler (or a future token middleware).
	remoteControlToken           string
	remoteControlServerID        string
	remoteControlName            string
	remoteControlProtocolVersion string
	installationID               string
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
	channelRoutingProtocol := channelRoutingProtocolForEndpoint(endpoint)
	if override := strings.TrimSpace(opts.channelRoutingProtocol); override != "" {
		channelRoutingProtocol = override
	}

	// 0. 客户端识别 + 分组客户端限制预检。
	clientid.Detect(c)

	// 完整请求审计在所有业务预检之前同步创建骨架，客户端原始 Header/Body
	// 由有界工作池异步补写；队列满或内存超限时自动同步兜底。
	var auditRequest *requestaudit.Handle
	if p.requestAudit != nil {
		inboundBody := relayInboundRequestBody(c)
		var err error
		auditRequest, err = p.requestAudit.StartFast(ctx, requestaudit.RequestInput{
			RequestID: requestIDOf(c), UserID: keyInfo.UserID, UserEmail: keyInfo.UserEmail,
			APIKeyID: keyInfo.KeyID, GroupID: keyInfo.GroupID, Client: relayClientType(c),
			Protocol: protocol, Endpoint: endpoint, Model: req.Model, Stream: req.Stream,
			Method: c.Request.Method, Path: c.Request.URL.Path, RawQuery: c.Request.URL.RawQuery,
			Host: c.Request.Host, RequestProto: c.Request.Proto, RemoteAddr: c.Request.RemoteAddr,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
			ContentType: c.Request.Header.Get("Content-Type"), ContentLen: c.Request.ContentLength,
			Headers: c.Request.Header, Body: inboundBody, BodyImmutable: true,
		})
		if err != nil {
			slog.Error("request_audit_start_failed", "request_id", requestIDOf(c), "error", err)
			writeError(c, http.StatusInternalServerError, "server_error", "audit_write_failed", "请求审计写入失败，已阻止转发")
			return
		}
		defer func() {
			status := c.Writer.Status()
			if ctx.Err() != nil && !c.Writer.Written() {
				status = statusClientClosedRequest
			} else if !c.Writer.Written() {
				// 无响应直接退出只可能来自 panic；Recovery 会随后向客户端写 500。
				status = http.StatusInternalServerError
			}
			responseBytes := int64(c.Writer.Size())
			if responseBytes < 0 {
				responseBytes = 0
			}
			auditRequest.Finish(status, responseBytes, true)
		}()
	}
	if len(keyInfo.GroupAllowedClients) > 0 {
		if !clientid.Matches(relayClientType(c), keyInfo.GroupAllowedClients) {
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
	price := pricing.Price{}
	priced := opts.allowUnpriced
	if !opts.allowUnpriced && p.pricing != nil {
		price, priced = p.pricing.Get(req.Model)
	}
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
	releaseClient, limitCode := p.acquireClientSlots(c, keyInfo, channelSlotTTL(req.Stream))
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
	rateLimitProbes := 0
	// 账号路径的 CPA 输入在同一请求的故障转移间不变。请求被插件改写后可能需要
	// 重新序列化大 JSON，这里按需只构建一次，避免每次切换账号重复编码。
	var accountPayload []byte
	var accountPayloadErr error
	accountPayloadReady := false
	recordCanceled := func() {
		p.recordFailure(c, keyInfo, req, start, errlog.Entry{
			Phase: errlog.PhaseCanceled, StatusCode: statusClientClosedRequest,
			Message: "客户端取消请求", Attempts: attempts, Chain: hops,
		})
	}
	queueDeadline := start.Add(queueWaitTimeout)
	pollDelay := queuePollInterval

	// 粘性会话：提取会话身份（显式标识或派生哈希）。路由粘性和 Cursor
	// 状态复用使用同一稳定身份，但 Relay Hook plan 存在时只关闭前者，不能
	// 改变 Hook 指定的账号探测/故障转移顺序；Cursor executor 仍按账号 ID
	// 隔离 conversation/checkpoint，账号切换时不会串用会话状态。
	sessionKey := ""
	cursorSessionKey := ""
	if sessionID := sessionIDForRequest(c, req); sessionID != "" {
		cursorSessionKey = affinityKey(keyInfo.UserID, keyInfo.GroupID, req.Model, protocol, sessionID)
		if routePlan == nil {
			sessionKey = cursorSessionKey
		}
	}
	unbindAffinity := func(kind routeKind, id int) {
		if sessionKey != "" && id > 0 {
			p.sessionAffinity.unbind(sessionKey, kind, id)
		}
	}

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

		var target routeTarget
		var ok bool
		if opts.nativeCodexAccountsOnly || opts.preferNativeCodex {
			if opts.nativeCodexAccountID > 0 {
				target, ok = p.pickBoundNativeCodexAccount(keyInfo.GroupID, req.Model, opts.nativeCodexAccountID)
				if ok {
					for _, excluded := range excludeAccounts {
						if excluded == opts.nativeCodexAccountID {
							ok = false
							break
						}
					}
				}
			} else {
				target, ok = p.pickNativeCodexRouteWithPlan(
					keyInfo.GroupID, req.Model, excludeAccounts, routePlan,
				)
			}
		}
		// A Codex request may have an existing native-account affinity. Reuse it
		// before weighted native selection, but never let a channel affinity beat
		// the native plane. Relay Hook plans intentionally disable sessionKey
		// above, so an explicit account order remains authoritative.
		if opts.preferNativeCodex && !opts.nativeCodexAccountsOnly && !ok && sessionKey != "" {
			if kind, id, bound := p.sessionAffinity.lookup(sessionKey); bound && kind == routeAccount {
				target, ok = p.resolveAffinityTarget(kind, id, keyInfo.GroupID, req.Model, channelRoutingProtocol, excludeKeys, excludeAccounts)
				if ok && (target.account == nil || !isNativeCodexAccount(target.account)) {
					ok = false
				}
				if !ok {
					unbindAffinity(kind, id)
				}
			}
		}
		if ok && opts.preferNativeCodex && !opts.nativeCodexAccountsOnly && sessionKey != "" && target.kind == routeAccount && target.account != nil {
			p.sessionAffinity.bind(sessionKey, routeAccount, target.account.ID)
		}
		if !opts.preferNativeCodex && !opts.nativeCodexAccountsOnly && sessionKey != "" {
			if kind, id, bound := p.sessionAffinity.lookup(sessionKey); bound {
				// 绑定优先于优先级：目标仍可调度（且未被本次 failover 排除）就复用。
				target, ok = p.resolveAffinityTarget(kind, id, keyInfo.GroupID, req.Model, channelRoutingProtocol, excludeKeys, excludeAccounts)
				if !ok {
					// 目标已失效或已被当前请求排除，立即解除旧绑定；若没有
					// 可替代目标，也不能让下一次请求继续粘回这个失败目标。
					unbindAffinity(kind, id)
				} else if target.kind == routeAccount {
					decision := p.rebalanceAffinityAccount(target, keyInfo.GroupID, req.Model, excludeAccounts)
					if decision.action != affinityAccountKeep {
						fromAccountID := target.account.ID
						target = decision.target
						if decision.action == affinityAccountRebind {
							p.sessionAffinity.bind(sessionKey, routeAccount, target.account.ID)
							slog.Info("relay_affinity_account_rebound",
								"from_account_id", fromAccountID,
								"to_account_id", target.account.ID,
								"model", req.Model,
								"from_first_token_ms", decision.boundLatencyMs,
								"to_first_token_ms", decision.targetLatencyMs,
								"from_inflight", decision.boundInflight,
								"to_inflight", decision.targetInflight,
							)
						} else {
							// 并发临时分流不修改粘性绑定，避免后续串行请求丢失 prompt cache。
							slog.Debug("relay_affinity_account_spilled",
								"bound_account_id", fromAccountID,
								"target_account_id", target.account.ID,
								"model", req.Model,
								"bound_first_token_ms", decision.boundLatencyMs,
								"target_first_token_ms", decision.targetLatencyMs,
								"bound_inflight", decision.boundInflight,
								"target_inflight", decision.targetInflight,
							)
						}
					}
				}
			}
		}
		if ok && opts.preferNativeCodex && !opts.nativeCodexAccountsOnly && sessionKey != "" && target.kind == routeAccount && target.account != nil {
			p.sessionAffinity.bind(sessionKey, routeAccount, target.account.ID)
		}
		// Some native control-plane contracts are specific to API-key auth. In
		// particular, the official turn-cost worker calls the public
		// `/v1/analytics/codex/turn-costs` endpoint; routing that request through
		// a ChatGPT OAuth account would select the wrong backend path. Reject the
		// mismatched native candidate before any account slot/probe is acquired.
		if ok && opts.nativeCodexAPIKeyOnly {
			if target.kind != routeAccount || target.account == nil ||
				!providertransport.IsCodexAPIKeyAuthType(target.account.Type) {
				if target.kind == routeAccount && target.account != nil {
					hardExcludeAccounts = append(hardExcludeAccounts, target.account.ID)
					unbindAffinity(routeAccount, target.account.ID)
				}
				target = routeTarget{}
				ok = false
				// Re-run the native picker with the mismatched account excluded so
				// a later API-key account in the same group can still serve the
				// request. No account slot has been acquired at this point.
				continue
			}
		}
		if ok && opts.nativeCodexOAuthOnly {
			if target.kind != routeAccount || target.account == nil ||
				!providertransport.IsCodexOAuthAuthType(target.account.Type) {
				if target.kind == routeAccount && target.account != nil {
					hardExcludeAccounts = append(hardExcludeAccounts, target.account.ID)
					unbindAffinity(routeAccount, target.account.ID)
				}
				target = routeTarget{}
				ok = false
				// Continue the native picker with the mismatched account excluded;
				// no account slot or upstream request has been acquired yet.
				continue
			}
		}
		if !ok && !opts.nativeCodexAccountsOnly {
			target, ok = p.pickRouteWithChannelConfig(
				keyInfo.GroupID,
				req.Model,
				channelRoutingProtocol,
				excludeKeys,
				excludeAccounts,
				routePlan,
				settings.ChannelLatency,
			)
			if ok && sessionKey != "" {
				p.sessionAffinity.bind(sessionKey, target.kind, routeTargetID(target))
			}
		}
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
				waited := p.waitForCapacity(ctx, pollDelay, queueDeadline)
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
			if p.cpa == nil && p.providerTransport == nil {
				slog.Warn("relay_account_cpa_unavailable", "account_id", acc.ID)
				hardExcludeAccounts = append(hardExcludeAccounts, acc.ID)
				unbindAffinity(routeAccount, acc.ID)
				continue
			}
			// 本请求已探测过一个限流账号时，跳过其余限流账号，继续寻找插件计划中
			// 的活跃账号或 Core fallback，避免三个坏账号恰好吃满全部重试预算。
			if acc.State == accountreg.StateRateLimited && rateLimitProbes >= maxRateLimitProbesPerRequest {
				hardExcludeAccounts = append(hardExcludeAccounts, acc.ID)
				unbindAffinity(routeAccount, acc.ID)
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
				unbindAffinity(routeAccount, acc.ID)
				continue
			}
			pollDelay = queuePollInterval
			if !accountPayloadReady {
				accountPayload, accountPayloadErr = prepareAccountPayload(req, opts)
				accountPayloadReady = true
			}
			payload, perr := accountPayload, accountPayloadErr
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

			probeDecision := accountreg.RateLimitProbeNotNeeded
			var probeLease accountreg.RateLimitProbeLease
			if p.accounts != nil {
				probeDecision, probeLease = p.accounts.BeginRateLimitProbe(acc.ID)
			}
			if probeDecision == accountreg.RateLimitProbeBlocked ||
				(probeDecision == accountreg.RateLimitProbeAcquired && rateLimitProbes >= maxRateLimitProbesPerRequest) {
				if probeDecision == accountreg.RateLimitProbeAcquired {
					p.accounts.CancelRateLimitProbe(acc.ID, probeLease)
				}
				p.concurrency.ReleaseAccountSlot(context.Background(), acc.ID, requestID)
				p.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
				hardExcludeAccounts = append(hardExcludeAccounts, acc.ID)
				unbindAffinity(routeAccount, acc.ID)
				continue
			}
			rateLimitProbe := probeLease != 0
			if rateLimitProbe {
				rateLimitProbes++
			}
			releaseLocalLoad := p.trackAccountAttempt(acc.ID)
			attemptStart := time.Now()
			result := func() (result attemptResult) {
				defer releaseLocalLoad()
				if rateLimitProbe {
					defer func() {
						if recovered := recover(); recovered != nil {
							p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, 0, "限流恢复探测执行异常，结果未知")
							panic(recovered)
						}
					}()
				}
				return p.executeAccountAttempt(c, acc, req, endpoint, protocol, payload, start, requestID, rpmMinute, auditRequest, cursorSessionKey, opts)
			}()
			attemptLatency := time.Since(attemptStart).Milliseconds()
			attempts++
			if result.auditErr != nil {
				if rateLimitProbe {
					p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, 0, "限流恢复探测审计失败，结果未知")
				}
				writeError(c, http.StatusInternalServerError, "server_error", "audit_write_failed", "请求审计写入失败，已阻止转发")
				p.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
				return
			}
			accountStreamAborted := result.written && (result.streamErr != nil || !result.done)
			if p.handleAccountOutcome(c, keyInfo, acc, req, endpoint, result, start, price, settings, opts,
				rpmMinute, attempts, &hops, &summary, &hardExcludeAccounts, &softExcludeAccounts, attemptLatency, probeLease) {
				if accountStreamAborted {
					unbindAffinity(routeAccount, acc.ID)
				}
				return
			}
			// handleAccountOutcome 返回 false 表示本次账号失败并将切换目标。
			unbindAffinity(routeAccount, acc.ID)
			continue
		}

		// ---------- 渠道 key 路径（同协议直发，跨协议文本走 CPA）----------
		ch := target.channel
		needsCPATranslation := channelNeedsCPATranslation(endpoint, protocol, ch.Type)
		if needsCPATranslation && p.cpa == nil && p.providerTransport == nil {
			slog.Warn("relay_channel_cpa_unavailable", "channel_key_id", ch.KeyID, "type", ch.Type)
			hardExcludeKeys = append(hardExcludeKeys, ch.KeyID)
			unbindAffinity(routeChannel, ch.KeyID)
			continue
		}
		// key 级配置检查：适配器 / 密钥，任一缺失即硬排除（不消耗 attempt）。
		ad, err := adaptor.GetAdaptor(ch.Type)
		if err != nil {
			slog.Warn("relay_channel_key_type_unsupported", "channel_key_id", ch.KeyID, "type", ch.Type)
			hardExcludeKeys = append(hardExcludeKeys, ch.KeyID)
			unbindAffinity(routeChannel, ch.KeyID)
			continue
		}
		apiKey := ch.APIKey
		if apiKey == "" {
			slog.Warn("relay_channel_key_no_api_key", "channel_key_id", ch.KeyID)
			hardExcludeKeys = append(hardExcludeKeys, ch.KeyID)
			unbindAffinity(routeChannel, ch.KeyID)
			continue
		}
		capacityID := ch.CredentialID
		if capacityID <= 0 {
			capacityID = ch.KeyID
		}

		// 单协议物理凭证 RPM + 并发闸门：同一 API Key 的请求共享限额。
		// rpmMinute 为预递增所用的分钟窗口，失败回退时对同一窗口 decrement
		//（不重取当前时间，防跨分钟边界扣穿新窗口）。
		requestID := uuid.New().String()
		rpmMinute, err := p.concurrency.AcquireKeyCapacity(
			ctx, capacityID, requestID, ch.MaxRPM, ch.MaxConcurrency, channelSlotTTL(req.Stream),
		)
		if err != nil {
			summary.localCapacity = true
			softExcludeKeys = append(softExcludeKeys, ch.KeyID)
			unbindAffinity(routeChannel, ch.KeyID)
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
			ProviderPath:   opts.providerPath,
			ProviderQuery:  providerQueryWithOverrideForEndpoint(c, endpoint, opts.providerQueryDefaults),
			RequestHeaders: accountProviderHeaders(c),
			Client:         p.client,
		}
		releaseLocalLoad := p.trackChannelAttempt(ch.KeyID)
		attemptStart := time.Now()
		auditTarget := requestaudit.Target{
			RouteKind: "channel", ChannelID: ch.ChannelID, ChannelName: ch.ChannelName,
			ChannelKeyID: ch.KeyID, ChannelKeyName: ch.KeyName,
		}
		result := func() (result attemptResult) {
			defer releaseLocalLoad()
			if needsCPATranslation {
				return p.executeChannelCPAAttempt(
					c, ch, req, endpoint, protocol, start, requestID, rpmMinute, capacityID, auditRequest,
				)
			}
			return p.executeAttempt(c, ad, info, req, start, attemptStart, capacityID, requestID, rpmMinute, auditRequest, auditTarget)
		}()
		attemptLatency := time.Since(attemptStart).Milliseconds()
		attempts++
		finishChannelAuditAttempt(result, attemptLatency, apiKey)
		if result.auditErr != nil {
			p.rpm.DecrementKeyRPM(context.Background(), capacityID, rpmMinute)
			writeError(c, http.StatusInternalServerError, "server_error", "audit_write_failed", "请求审计写入失败，已阻止转发")
			return
		}

		if partialErr := result.unreplayableProviderFailure(); partialErr != nil {
			reason := outcome.SanitizeKeyLeak(partialErr.Error(), []string{apiKey})
			billed := result.usage != nil && !opts.zeroBilling
			if billed {
				p.recordUsage(c, keyInfo, ch, req, endpoint, result, start, price)
			}
			writeError(c, http.StatusBadGateway, "upstream_error", "upstream_response_interrupted", "上游响应中断，已停止重试以避免重复执行请求")
			hop := attemptHop(len(hops)+1, ch, apiKey, result.statusCode, "streamAborted", reason, 0, attemptLatency, false)
			if p.errSink != nil {
				p.errSink.CountFailure(context.Background(), ch.ChannelID, "streamAborted", "")
			}
			p.recordFailure(c, keyInfo, req, start, errlog.Entry{
				Phase: errlog.PhaseStreamAborted, StatusCode: http.StatusBadGateway,
				ErrorType: "upstream_error", ErrorCode: "upstream_response_interrupted",
				Message: "上游已返回部分数据后中断，已阻止渠道 failover", Billed: billed,
				Attempts: attempts, Chain: append(hops, hop),
				ChannelID: ch.ChannelID, ChannelName: ch.ChannelName,
			})
			unbindAffinity(routeChannel, ch.KeyID)
			return
		}

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
			if result.streamErr != nil || !streamComplete {
				slog.Warn("relay_stream_aborted",
					"channel_key_id", ch.KeyID, "model", req.Model,
					"complete", streamComplete, "error", result.streamErr)
			} else {
				if result.attemptFirstTokenMs > 0 {
					p.recordChannelFirstTokenWithConfig(
						ch.KeyID, req.Model, result.attemptFirstTokenMs, time.Now(), settings.ChannelLatency,
					)
				} else {
					p.recordChannelFastSuccessWithConfig(ch.KeyID, req.Model, attemptLatency, settings.ChannelLatency)
				}
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
				p.recordUsage(c, keyInfo, ch, req, endpoint, result, start, price)
			}
			if result.streamErr != nil || !streamComplete {
				unbindAffinity(routeChannel, ch.KeyID)
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
			p.recordChannelFastSuccessWithConfig(ch.KeyID, req.Model, attemptLatency, settings.ChannelLatency)
			p.registry.MarkRecovered(ch.KeyID)
			if p.healthTracker != nil {
				p.healthTracker.RecordSuccess(ch.KeyID)
			}
			// 零计费端点（countTokens 类）：usage 归零、不写 usage_log；
			// failover/outcome/透传语义与常规端点完全一致。
			if !opts.zeroBilling {
				p.recordUsage(c, keyInfo, ch, req, endpoint, result, start, price)
			}
			writeUpstreamBodyForMode(c, result, opts.passthroughResponse)
			return

		case outcome.RateLimited:
			p.recordChannelSlowFailureWithConfig(ch.KeyID, req.Model, attemptLatency, settings.ChannelLatency)
			// 本次请求内硬排除，并按 Retry-After 对物理凭证做跨请求短冷却，
			// 避免下游重试时立即再次命中同一把已限流的 API Key。
			p.rpm.DecrementKeyRPM(context.Background(), capacityID, rpmMinute)
			// 429 只做短冷却，不喂健康失败计数：临时限流经健康状态机连续累计
			// 会被升级成 disabled_auto 永久禁用，违反 outcome「限流→冷却」铁律。
			if p.registry != nil {
				p.registry.MarkRateLimited(ch.KeyID, time.Now().Add(o.RetryAfter))
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
			unbindAffinity(routeChannel, ch.KeyID)
			continue

		case outcome.AuthFailed:
			p.rpm.DecrementKeyRPM(context.Background(), capacityID, rpmMinute)
			if settings.AutoBanEnabled {
				// 健康信号也归入开关内：RecordAuthFailure 会经探针状态机单次
				// suspend + MarkAutoDisabled，放在开关外等于绕过自动封禁总开关。
				if p.healthTracker != nil {
					p.healthTracker.RecordAuthFailure(ch.KeyID)
				}
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
			unbindAffinity(routeChannel, ch.KeyID)
			continue

		case outcome.Transient:
			p.recordChannelSlowFailureWithConfig(ch.KeyID, req.Model, attemptLatency, settings.ChannelLatency)
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
			unbindAffinity(routeChannel, ch.KeyID)
			continue

		default: // verdictClientError：语义重建终止，不重试；带 usage 仍计费（零计费端点除外）。
			billed := result.usage != nil && !opts.zeroBilling
			if billed {
				p.recordUsage(c, keyInfo, ch, req, endpoint, result, start, price)
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
	// 末次跳的 channel_key_id 快照：健康监测 key 级失败率依赖此列。
	if e.ChannelKeyID == 0 {
		for i := len(e.Chain) - 1; i >= 0; i-- {
			if e.Chain[i].KeyID > 0 {
				e.ChannelKeyID = e.Chain[i].KeyID
				break
			}
		}
	}
	p.errSink.CountFailure(context.Background(), 0, "", e.Phase)
	p.errSink.Record(e)
}

// channelSlotTTL 渠道并发槽 TTL：流式 30min（与上游流硬超时一致，防僵尸清理），
// 非流式用默认值（传 0 → concurrency 层 5min）。
func channelSlotTTL(stream bool) time.Duration {
	if stream {
		return streamSlotTTL
	}
	return 0
}

// executeAttempt 执行单次上游调用，并保证渠道并发槽/RPM 在 panic 时也正确回收：
// 槽位恒经 defer 释放；panic 时回退 RPM 预递增后继续向上抛（由 Recovery 中间件转 500）。
func (p *Pipeline) executeAttempt(c *gin.Context, ad adaptor.Adaptor, info *adaptor.RelayInfo, req *dto.ChatRequest, start, attemptStart time.Time, channelKeyID int, requestID string, rpmMinute int64, auditRequest *requestaudit.Handle, auditTarget requestaudit.Target) attemptResult {
	defer func() {
		// 槽位释放异步化：ZREM 幂等，不必阻塞请求收尾/下一次 failover 尝试。
		go p.concurrency.ReleaseKeySlot(context.Background(), channelKeyID, requestID)
		if rec := recover(); rec != nil {
			p.rpm.DecrementKeyRPM(context.Background(), channelKeyID, rpmMinute)
			panic(rec)
		}
	}()
	return p.execute(c, ad, info, req, start, attemptStart, auditRequest, auditTarget)
}

// acquireClientSlots user → key 两级并发闸门。成功返回 (释放闭包, "")；
// 拒绝时已写出 429 错误体，返回 (nil, 错误码) 供失败留痕。
// 顺带记录分组维度在途槽位（纯观测口径，不限流），供管理端展示分组实时并发。
func (p *Pipeline) acquireClientSlots(c *gin.Context, keyInfo *auth.APIKeyInfo, slotTTL time.Duration) (func(), string) {
	ctx := c.Request.Context()
	slotID := uuid.New().String()
	err := p.concurrency.AcquireClientCapacity(
		ctx, keyInfo.UserID, keyInfo.KeyID, keyInfo.GroupID, slotID,
		keyInfo.UserMaxConcurrency, keyInfo.KeyMaxConcurrency, slotTTL,
	)
	if errors.Is(err, scheduler.ErrUserConcurrencyLimit) {
		writeRateLimitError(c, "user_concurrency_limit", "用户并发数已达上限", time.Second)
		return nil, "user_concurrency_limit"
	}
	if errors.Is(err, scheduler.ErrAPIKeyConcurrencyLimit) {
		writeRateLimitError(c, "apikey_concurrency_limit", "API Key 并发数已达上限", time.Second)
		return nil, "apikey_concurrency_limit"
	}
	return func() {
		// 异步释放：观测/闸门口径允许亚毫秒级延迟，不阻塞请求收尾。
		go p.concurrency.ReleaseClientCapacity(
			context.Background(), keyInfo.UserID, keyInfo.KeyID, keyInfo.GroupID, slotID,
			keyInfo.UserMaxConcurrency > 0, keyInfo.KeyMaxConcurrency > 0,
		)
	}, ""
}

// execute 单次上游调用：构建请求 → 直发 → 按流式/非流式分派响应处理。
func (p *Pipeline) execute(c *gin.Context, ad adaptor.Adaptor, info *adaptor.RelayInfo, req *dto.ChatRequest, start, attemptStart time.Time, auditRequest *requestaudit.Handle, auditTarget requestaudit.Target) attemptResult {
	clientCtx := c.Request.Context()
	ctx := clientCtx
	var cancel context.CancelFunc
	var markStreamStarted func()
	if !info.Stream {
		ctx, cancel = context.WithTimeout(ctx, nonStreamTimeout)
	} else {
		// 流开始前仍跟随客户端取消；收到上游成功响应后脱离客户端连接，
		// 即使下游中断也继续排空上游，以捕获最终 usage。硬上限防异常长流。
		ctx, markStreamStarted, cancel = streamlife.DetachAfterStart(clientCtx, streamlife.MaxDuration)
	}
	defer cancel()

	httpReq, err := ad.BuildRequest(ctx, info, req)
	if err != nil {
		// BuildRequest 发生在触网之前：坏请求体/不支持端点/翻译失败/坏 URL 都是
		// 客户端或配置问题，failover 重试无益，且不应污染渠道健康。归 buildErr。
		return attemptResult{buildErr: err}
	}
	var auditAttempt *requestaudit.AttemptHandle
	if auditRequest != nil {
		auditAttempt, err = auditRequest.BeginAttemptFast(ctx, auditTarget, httpReq)
		if err != nil {
			return attemptResult{auditErr: err}
		}
	}
	resp, err := info.Client.Do(httpReq)
	if err != nil {
		return attemptResult{netErr: err, auditAttempt: auditAttempt}
	}
	defer func() { _ = resp.Body.Close() }()

	result := attemptResult{
		statusCode:   resp.StatusCode,
		headers:      resp.Header,
		contentType:  resp.Header.Get("Content-Type"),
		auditAttempt: auditAttempt,
	}
	if info.Stream && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// http.Client.Do 已收到上游响应头，视为流已开始；此后客户端断开不再取消上游。
		markStreamStarted()
	}

	// 上游非 2xx：读错误体（≤64KB）供判定/透传；错误体带 usage 仍计费。
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		result.body = body
		result.dataReceived = len(body) > 0
		if u, found := dto.ExtractUsage(body); found {
			result.usage = &u
		}
		if readErr != nil {
			// Keep the HTTP status/body for outcome classification, but retain
			// the transport error so an indeterminate partial error response is
			// never retried on another account.
			result.netErr = readErr
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
			sr := relaySSE(c.Writer, resp, attemptStart, extractUsage, forwardUsageChunk, isFirstContentLine, maxLineBytes, observer)
			result.usage = sr.usage
			result.dataReceived = sr.dataReceived
			result.attemptFirstTokenMs = sr.firstTokenMs
			if sr.firstTokenMs > 0 {
				requestBeforeAttemptMs := max(attemptStart.Sub(start).Milliseconds(), 0)
				result.firstTokenMs = requestBeforeAttemptMs + sr.firstTokenMs
			}
			result.written = sr.written
			if sr.upstreamError != nil {
				// 上游可能以 HTTP 200 建立 SSE，随后在首个真实内容前发送协议级
				// error/response.failed。此时尚未向客户端提交任何内容，转换成
				// 普通未写出 attempt，复用现有 outcome/failover 状态机。
				result.statusCode = sr.upstreamError.StatusCode
				result.body = sr.upstreamError.Body
				result.written = false
				return result
			}
			if sr.err != nil && !sr.written {
				// 首内容前的扫描/网络错误尚可安全切换调度单元；不能放进
				// streamErr，否则 status=2xx 会被误判为成功。
				result.netErr = sr.err
			} else {
				result.streamErr = sr.err
			}
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
		return attemptResult{netErr: err, statusCode: resp.StatusCode, headers: resp.Header,
			dataReceived: len(body) > 0, body: body, auditAttempt: auditAttempt}
	}
	if len(body) > maxResponseBodyBytes {
		return attemptResult{netErr: fmt.Errorf("上游响应体超过 %d 字节上限", maxResponseBodyBytes), statusCode: resp.StatusCode,
			headers: resp.Header, dataReceived: len(body) > 0, body: body, auditAttempt: auditAttempt}
	}
	rewritten, usage := ad.ParseNonStreamResponse(info, body)
	result.body = rewritten
	result.usage = usage
	result.dataReceived = len(body) > 0
	return result
}

// isSSEContentType 判断响应 Content-Type 是否为 SSE（容忍 charset 等参数与大小写）。
func isSSEContentType(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream")
}

// recordUsage 计费收尾：ComputeCosts → Calculate 三管道 → UsageRecord 落账。
// price 为转发前缺价预检解析的快照（每请求解析一次，不二次 Get，
// 避免请求期间缓存失效把已定价请求静默记 0）；缺价请求在预检已被拒绝，进不到这里。
func (p *Pipeline) recordUsage(c *gin.Context, keyInfo *auth.APIKeyInfo, ch *registry.ChannelKeySnapshot, req *dto.ChatRequest, endpoint string, result attemptResult, start time.Time, price pricing.Price) {
	if p.sink == nil {
		return
	}

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
	billingSnapshot := resolveUsageBilling(endpoint, price, usage)
	usageStatus := usageStatusFor(result, usage, billingSnapshot.Calls)
	if !persistableUsageStatus(usageStatus) {
		return
	}

	// 用户 / 分组 RPM 观测计数：与 usage_log 同源，仅实际落账的请求计入，口径对齐仪表盘。
	// 用 Background ctx，避免请求收尾（尤其流式结束）ctx 已取消导致漏计；
	// 纯观测口径，异步执行不阻塞计费收尾。
	go p.rpm.IncrementUserGroupRPM(context.Background(), keyInfo.UserID, keyInfo.GroupID)

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
		Calls:                 billingSnapshot.Calls,
		BillingMode:           billingSnapshot.Mode,
		InputPrice:            billingSnapshot.InputPrice,
		OutputPrice:           price.Output,
		CachedInputPrice:      price.CachedInput,
		CacheCreationPrice:    price.CacheCreation5m,
		CacheCreation1hPrice:  price.CacheCreation1h,
		ServiceTier:           tier,
		ReasoningEffort:       reasoningEffort,
		ImageSize:             usage.ImageSize,
		ImageQuality:          usage.ImageQuality,
		UsageStatus:           usageStatus,
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

// usageStatusFor 区分正常完成、计量缺失与流中断。按次/按张请求即使没有
// token usage，只要已得到有效计次数也属于已计量，避免把图像/搜索记录误标缺失。
func usageStatusFor(result attemptResult, usage dto.Usage, billedCalls int) string {
	aborted := result.written && (result.streamErr != nil || !result.done)
	missing := result.usage == nil && billedCalls == 0 &&
		usage.PromptTokens == 0 && usage.CompletionTokens == 0 &&
		usage.CachedTokens == 0 && usage.CacheCreationTokens == 0 &&
		usage.CacheCreation5mTokens == 0 && usage.CacheCreation1hTokens == 0
	switch {
	case aborted && missing:
		return billing.UsageStatusStreamAbortedUsageMissing
	case aborted:
		return billing.UsageStatusStreamAborted
	case missing:
		return billing.UsageStatusMissing
	default:
		return billing.UsageStatusCompleted
	}
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
	copySafeUpstreamResponseHeaders(c.Writer.Header(), result.headers)
	contentType := result.contentType
	if contentType == "" {
		contentType = result.headers.Get("Content-Type")
	}
	if contentType == "" {
		contentType = "application/json"
	}
	// Preserve the attempt result's normalized content type. The header map is
	// copied for metadata, but this field is authoritative after adaptor body
	// rewriting and matches the pre-header-copy behavior.
	c.Header("Content-Type", contentType)
	c.Data(result.statusCode, contentType, result.body)
}

// writePassthroughUpstreamBody preserves a raw upstream response without
// inventing JSON metadata. In particular, native Realtime may return SDP or a
// body with no Content-Type at all, and control-plane calls may return 204.
func writePassthroughUpstreamBody(c *gin.Context, result attemptResult) {
	copySafeUpstreamResponseHeaders(c.Writer.Header(), result.headers)
	contentType := result.contentType
	if contentType == "" && result.headers != nil {
		contentType = result.headers.Get("Content-Type")
	}
	if contentType != "" {
		c.Writer.Header().Set("Content-Type", contentType)
	} else {
		// A present nil value suppresses net/http's response-body sniffing while
		// emitting no Content-Type field on the wire.
		c.Writer.Header()["Content-Type"] = nil
	}

	c.Status(result.statusCode)
	if !upstreamResponseBodyAllowed(c, result.statusCode) || len(result.body) == 0 {
		c.Writer.WriteHeaderNow()
		return
	}
	if _, err := c.Writer.Write(result.body); err != nil {
		_ = c.Error(err)
		c.Abort()
	}
}

func writeUpstreamBodyForMode(c *gin.Context, result attemptResult, passthrough bool) {
	if passthrough {
		writePassthroughUpstreamBody(c, result)
		return
	}
	writeUpstreamBody(c, result)
}

func upstreamResponseBodyAllowed(c *gin.Context, status int) bool {
	if c != nil && c.Request != nil && c.Request.Method == http.MethodHead {
		return false
	}
	return (status < 100 || status > 199) && status != http.StatusNoContent && status != http.StatusNotModified
}

// hopByHopResponseHeaders are connection-scoped fields that must never be
// forwarded by a proxy. Content-Length is deliberately handled separately:
// adaptors may rewrite the response body (for example, model mappings), so an
// upstream length could be stale and corrupt the downstream framing.
var hopByHopResponseHeaders = map[string]struct{}{
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
	"content-length":      {},
	"set-cookie":          {},
}

// copySafeUpstreamResponseHeaders copies end-to-end response metadata while
// stripping hop-by-hop headers. RFC 9110 also allows Connection to nominate
// extension fields; those tokens are removed as well.
func copySafeUpstreamResponseHeaders(dst, src http.Header) {
	if dst == nil || src == nil {
		return
	}

	connectionTokens := make(map[string]struct{})
	for name, values := range src {
		if !strings.EqualFold(name, "Connection") {
			continue
		}
		for _, value := range values {
			for _, token := range strings.Split(value, ",") {
				token = strings.ToLower(strings.TrimSpace(token))
				if token != "" {
					connectionTokens[token] = struct{}{}
				}
			}
		}
	}
	// Clear any connection-scoped fields that may have been installed by
	// middleware before this helper runs, including Connection-nominated
	// extension fields. This makes the helper safe when called with a reused
	// response header map.
	for name := range dst {
		lowerName := strings.ToLower(name)
		if _, blocked := hopByHopResponseHeaders[lowerName]; blocked {
			delete(dst, name)
			continue
		}
		if _, blocked := connectionTokens[lowerName]; blocked {
			delete(dst, name)
		}
	}

	for name, values := range src {
		canonical := http.CanonicalHeaderKey(name)
		lowerName := strings.ToLower(name)
		if _, blocked := hopByHopResponseHeaders[lowerName]; blocked {
			continue
		}
		if _, blocked := connectionTokens[lowerName]; blocked {
			continue
		}
		// Replace, rather than Add, so stale middleware values cannot leak into
		// an upstream response with the same field name.
		dst[canonical] = append([]string(nil), values...)
	}
}

// writeUpstreamError 不可重试 4xx：语义保留、载体重建——上游错误体解析出的语义字段
// （已由调用方脱敏）经 errfmt 按入口协议渲染，HTTP 状态码保留上游原值；
// 原始响应体不透传（只进失败留痕）。
func writeUpstreamError(c *gin.Context, status int, up errfmt.UpstreamError) {
	c.JSON(status, errfmt.RenderUpstream(entryProtocolOf(c), status, up, requestIDOf(c)))
}

// writeAllFailed 全部渠道耗尽后的响应选择。
// 上游 429 属资源池暂时不可用，对下游统一渲染为 503；本地用户/API Key
// 并发限制仍在预检阶段返回 429，避免客户端把上游限流误判为自身额度耗尽。
// 返回写下的 (状态码, error_type, error_code, message) 供失败留痕复用同一事实源。
func writeAllFailed(c *gin.Context, summary failureSummary) (int, string, string, string) {
	switch {
	case summary.rateLimited:
		retryAfter := summary.minRetryAfter
		if retryAfter <= 0 {
			retryAfter = time.Second
		}
		msg := "上游资源池暂时不可用，请稍后重试"
		writeTemporaryUnavailableError(c, "upstream_pool_exhausted", msg, retryAfter)
		return http.StatusServiceUnavailable, "server_error", "upstream_pool_exhausted", msg
	case summary.localCapacity:
		msg := "渠道容量已满，请稍后重试"
		writeError(c, http.StatusServiceUnavailable, "server_error", "all_channels_busy", msg)
		return http.StatusServiceUnavailable, "server_error", "all_channels_busy", msg
	case summary.authFailed:
		// 渠道存在但上游认证/配额失败（401/402/403）：与「无可用渠道」区分，指向密钥问题。
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

// waitForCapacity 优先等待本实例槽位释放，并以定时轮询作为跨实例兜底。
// 请求被取消时返回 false。
func (p *Pipeline) waitForCapacity(ctx context.Context, delay time.Duration, deadline time.Time) bool {
	if remaining := time.Until(deadline); delay > remaining {
		delay = remaining
	}
	if delay <= 0 {
		return true
	}
	if p.concurrency != nil {
		return p.concurrency.WaitForCapacity(ctx, delay)
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
