package plugin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/routing"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// 访问日志富化键的本地别名，避免在大量 c.Set 处重复写出包名。
const (
	ginCtxKeyModel     = middleware.CtxKeyAccessModel
	ginCtxKeyPlatform  = middleware.CtxKeyAccessPlatform
	ginCtxKeyAccountID = middleware.CtxKeyAccessAccountID
	ginCtxKeyAttempts  = middleware.CtxKeyAccessAttempts
)

// Forwarder 请求转发器：认证 → 余额预检 → 调度 → 并发闸门 → 转发 → 判决 → 计费 → 记录。
type Forwarder struct {
	db          *ent.Client
	manager     *Manager
	scheduler   *scheduler.Scheduler
	concurrency *scheduler.ConcurrencyManager
	calculator  *billing.Calculator
	recorder    *billing.Recorder
}

// NewForwarder 创建转发器。
func NewForwarder(
	db *ent.Client,
	manager *Manager,
	sched *scheduler.Scheduler,
	concurrency *scheduler.ConcurrencyManager,
	calculator *billing.Calculator,
	recorder *billing.Recorder,
) *Forwarder {
	return &Forwarder{
		db:          db,
		manager:     manager,
		scheduler:   sched,
		concurrency: concurrency,
		calculator:  calculator,
		recorder:    recorder,
	}
}

// maxFailoverAttempts 最大 failover 次数（账号级失败后切换新账号上游调用的上限）。
const maxFailoverAttempts = 3

// queueWaitTimeout 所有账号 slot 都被占满时，请求最多排队等多久再放弃。
// 1 分钟对号池小 / 并发高的场景能把毛刺吸收掉；超过这个时长意味着号池真的不够用。
const queueWaitTimeout = 60 * time.Second

// queuePollInterval slot 未释放时的初始轮询间隔；连续排队会指数退避到上限。
const queuePollInterval = 200 * time.Millisecond

const queueMaxPollInterval = 2 * time.Second

// 499 是 nginx 风格的 Client Closed Request，仅用于本地日志和状态归类。
const statusClientClosedRequest = 499

// allRoutesFailedDefaultRetryAfter 客户端最终因真实上游限流被拒时，若没有任何上游 RetryAfter 可参考，
// 给客户端一个保守的退避建议。1s 既能避免雪崩，又比 60s 更贴合"瞬时限流"的真实恢复节奏。
const allRoutesFailedDefaultRetryAfter = time.Second

// Forward 入口。失败时自动 failover 到其它账号，最多 maxFailoverAttempts 次。
//
// Middleware：OnForwardBegin 只在首次 attempt 调用（避免 failover 污染审计计数），
// OnForwardEnd 在最终一次 attempt（成功或放弃）触发，LIFO 降序。Begin DENY 会拒绝请求。
func (f *Forwarder) Forward(c *gin.Context) {
	state, ok := f.parseRequest(c)
	if !ok {
		return
	}
	if !f.checkBalance(c, state) {
		return
	}

	// 请求级 logger：继承 middleware 注入的 request_id / user_id / group_id 等字段，
	// 再叠加 model / platform 让所有 forward 阶段日志自带上下文。
	logger := sdk.LoggerFromContext(c.Request.Context()).With(
		sdk.LogFieldModel, state.model,
		sdk.LogFieldPlatform, state.plugin.Name,
	)
	// http_request 中间件最终会输出一行总览，model/platform 写回 gin ctx 让那一行带上。
	c.Set(ginCtxKeyModel, state.model)
	c.Set(ginCtxKeyPlatform, state.plugin.Name)

	logger.Debug("forward_request_start",
		"stream", state.stream,
		"input_tokens_est", len(state.body),
		"scheduling_models", state.schedulingModelCandidates(),
	)

	// 只读元信息快车道：插件本地合成响应，跳过整条账号 / 闸门 / failover 链路。
	if isMetadataOnlyPath(state.requestPath) {
		f.forwardMetadataOnly(c, state)
		return
	}

	releaseClientQuota := f.acquireClientQuota(c, state)
	if releaseClientQuota == nil {
		return // 429 已写
	}
	defer releaseClientQuota()

	requirements := routing.Requirements{
		NeedsImage: requestNeedsImage(state.requestPath, state.model, state.body),
	}
	routes := routesForAPIKey(state, requirements)
	if len(routes) == 0 {
		logger.Warn("forward_no_eligible_route",
			sdk.LogFieldUserID, state.keyInfo.UserID,
		)
		if errResp, ok := apiKeyGroupRequirementError(state.keyInfo, requirements); ok {
			openAIError(c, errResp.status, errResp.errType, errResp.code, errResp.message)
			return
		}
		openAIError(c, http.StatusServiceUnavailable, "server_error", "no_available_route", "请求暂时无法完成，请稍后重试")
		return
	}

	hardExclude := make([]int, 0, maxFailoverAttempts*len(routes))
	var mwBag map[string]string
	beginCalled := false
	ctx := c.Request.Context()
	startedAt := state.startedAt
	totalAttempts := 0

	failureSummary := allRoutesFailureSummary{}

	for _, route := range routes {
		state.selectedRoute = route
		state.keyInfo = keyInfoForRoute(state.keyInfo, route)

		softExclude := make([]int, 0, maxFailoverAttempts)
		attempt := 0
		queueDeadline := time.Now().Add(queueWaitTimeout)
		queuePollDelay := queuePollInterval

		for attempt < maxFailoverAttempts {
			if status := canceledRequestStatus(ctx.Err()); status != 0 {
				markCanceledRequest(c, status)
				logger.Debug("forward_request_canceled",
					"status_code", status,
					"attempts", totalAttempts,
				)
				return
			}

			exclude := make([]int, 0, len(hardExclude)+len(softExclude))
			exclude = append(exclude, hardExclude...)
			exclude = append(exclude, softExclude...)

			if err := f.pickAccount(c, state, exclude...); err != nil {
				if status := canceledRequestStatus(ctx.Err()); status != 0 {
					markCanceledRequest(c, status)
					logger.Debug("forward_request_canceled",
						"status_code", status,
						"attempts", totalAttempts,
					)
					return
				}
				failureSummary.recordPickAccountError(err)
				if len(softExclude) > 0 && time.Now().Before(queueDeadline) {
					softExclude = softExclude[:0]
					wait := queuePollDelay
					if remaining := time.Until(queueDeadline); remaining < wait {
						wait = remaining
					}
					if wait > 0 {
						timer := time.NewTimer(wait)
						select {
						case <-ctx.Done():
							if !timer.Stop() {
								select {
								case <-timer.C:
								default:
								}
							}
							if status := canceledRequestStatus(ctx.Err()); status != 0 {
								markCanceledRequest(c, status)
								logger.Debug("forward_request_canceled",
									"status_code", status,
									"attempts", totalAttempts,
								)
								return
							}
							return
						case <-timer.C:
						}
					}
					if queuePollDelay < queueMaxPollInterval {
						queuePollDelay *= 2
						if queuePollDelay > queueMaxPollInterval {
							queuePollDelay = queueMaxPollInterval
						}
					}
					continue
				}
				attrs := []any{sdk.LogFieldError, err}
				if models := state.schedulingModelCandidates(); len(models) > 0 {
					attrs = append(attrs, "scheduling_models", models)
				}
				if len(hardExclude) > 0 {
					attrs = append(attrs, "hard_excluded", hardExclude)
				}
				if len(softExclude) > 0 {
					attrs = append(attrs, "soft_excluded", softExclude)
				}
				logger.Warn("forward_pick_account_failed", attrs...)
				break
			}
			queuePollDelay = queuePollInterval

			accountID := state.account.ID
			// logger 已经从 auth middleware 继承了 group_id，这里只补 account_id 避免重复字段。
			attemptLogger := logger.With(sdk.LogFieldAccountID, accountID)
			releaseAccountSlot, ok := f.acquireAccountSlot(c, state)
			if !ok {
				failureSummary.recordLocalCapacityFailure()
				softExclude = append(softExclude, accountID)
				continue
			}

			if !beginCalled {
				allowed, bag := f.runForwardBeginChain(c, state)
				beginCalled = true
				if !allowed {
					f.scheduler.DecrementRPM(ctx, accountID)
					releaseAccountSlot()
					return
				}
				mwBag = bag
			}

			execution := f.callPlugin(c, state)
			attempt++
			totalAttempts++

			requestCanceled := canceledRequestStatus(ctx.Err())
			if requestCanceled != 0 {
				if !hasForwardResult(execution) {
					releaseAccountSlot()
					f.scheduler.DecrementRPM(context.Background(), accountID)
					c.Set(ginCtxKeyAccountID, accountID)
					c.Set(ginCtxKeyAttempts, totalAttempts)
					markCanceledRequest(c, requestCanceled)
					logger.Debug("forward_request_canceled",
						"status_code", requestCanceled,
						"attempts", totalAttempts,
					)
					return
				}
				execution.err = nil
			}

			if requestCanceled == 0 && f.canFailover(c, state, execution) {
				failureSummary.recordExecution(execution)
				attrs := []any{
					"attempt", attempt,
					"kind", execution.outcome.Kind,
					sdk.LogFieldDurationMs, execution.duration.Milliseconds(),
					sdk.LogFieldReason, judgmentReason(execution),
				}
				if s := execution.outcome.Upstream.StatusCode; s > 0 {
					attrs = append(attrs, "upstream_status", s)
				}
				if execution.err != nil {
					attrs = append(attrs, sdk.LogFieldError, execution.err)
				}
				attemptLogger.Warn("forward_attempt_failed", attrs...)
				releaseAccountSlot()
				f.applyOutcome(ctx, state, execution)

				if execution.outcome.Kind.IsAccountFault() {
					hardExclude = append(hardExclude, accountID)
				} else {
					softExclude = append(softExclude, accountID)
				}
				continue
			}

			f.runForwardEndChain(c, state, execution, mwBag)
			f.writeResult(c, state, execution)
			releaseAccountSlot()
			// 总览写回 gin ctx，由 http_request 中间件统一输出，避免双行重复。
			c.Set(ginCtxKeyAccountID, accountID)
			c.Set(ginCtxKeyAttempts, totalAttempts)
			// 仅在发生过 failover 时单独打 Info；正常一次成功只留 Debug，避免噪声。
			if totalAttempts > 1 {
				attemptLogger.Info("forward_request_completed_after_retry",
					sdk.LogFieldStatus, execution.outcome.Upstream.StatusCode,
					sdk.LogFieldDurationMs, time.Since(startedAt).Milliseconds(),
					"attempts", totalAttempts,
				)
			} else {
				attemptLogger.Debug("forward_request_completed",
					sdk.LogFieldStatus, execution.outcome.Upstream.StatusCode,
					sdk.LogFieldDurationMs, time.Since(startedAt).Milliseconds(),
				)
			}
			return
		}

		logger.Debug("forward_route_failover_exhausted",
			"attempts", attempt,
			"scheduling_models", state.schedulingModelCandidates(),
		)
	}

	failAttrs := []any{
		sdk.LogFieldDurationMs, time.Since(startedAt).Milliseconds(),
		"attempts", totalAttempts,
		"scheduling_models", state.schedulingModelCandidates(),
	}
	if len(hardExclude) > 0 {
		failAttrs = append(failAttrs, "tried_accounts", hardExclude)
	}
	if failureSummary.rateLimitedSeen {
		failAttrs = append(failAttrs, "rate_limited_retry_after_ms", failureSummary.rateLimitedRetryAfter.Milliseconds())
	}
	logger.Error("forward_request_failed", failAttrs...)

	writeAllRoutesFailed(c, failureSummary)
}

func canceledRequestStatus(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, context.Canceled):
		return statusClientClosedRequest
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	default:
		return 0
	}
}

func markCanceledRequest(c *gin.Context, status int) {
	if c == nil || status == 0 || c.Writer.Written() {
		return
	}
	c.Status(status)
}

func finalizeRequestContext(ctx context.Context) context.Context {
	if canceledRequestStatus(ctx.Err()) != 0 {
		return context.Background()
	}
	return ctx
}

func hasForwardResult(execution forwardExecution) bool {
	if execution.outcome.Kind != sdk.OutcomeUnknown {
		return true
	}
	if execution.outcome.Upstream.StatusCode > 0 || len(execution.outcome.Upstream.Body) > 0 || len(execution.outcome.Upstream.Headers) > 0 {
		return true
	}
	return execution.outcome.Usage != nil || len(execution.outcome.UpdatedCredentials) > 0
}

type allRoutesFailureSummary struct {
	rateLimitedSeen       bool
	rateLimitedRetryAfter time.Duration
	localCapacitySeen     bool
	accountUnavailable    bool
	accountDeadSeen       bool
	upstreamTimeoutSeen   bool
	upstreamFailureSeen   bool
}

func (s *allRoutesFailureSummary) recordExecution(execution forwardExecution) {
	switch execution.outcome.Kind {
	case sdk.OutcomeAccountRateLimited:
		s.rateLimitedSeen = true
		s.recordRetryAfter(execution.outcome.RetryAfter)
	case sdk.OutcomeAccountDead:
		s.accountDeadSeen = true
	case sdk.OutcomeUpstreamTransient:
		if isTimeoutFailure(execution) {
			s.upstreamTimeoutSeen = true
			return
		}
		s.upstreamFailureSeen = true
	case sdk.OutcomeUnknown:
		if execution.err != nil {
			s.upstreamFailureSeen = true
		}
	}
}

func (s *allRoutesFailureSummary) recordRetryAfter(retryAfter time.Duration) {
	if retryAfter <= 0 {
		return
	}
	if s.rateLimitedRetryAfter == 0 || retryAfter < s.rateLimitedRetryAfter {
		s.rateLimitedRetryAfter = retryAfter
	}
}

func (s *allRoutesFailureSummary) recordPickAccountError(error) {
	s.accountUnavailable = true
}

func (s *allRoutesFailureSummary) recordLocalCapacityFailure() {
	s.localCapacitySeen = true
}

type allRoutesFailureResponse struct {
	status     int
	errType    string
	code       string
	message    string
	retryAfter time.Duration
}

func writeAllRoutesFailed(c *gin.Context, summary allRoutesFailureSummary) {
	response := selectAllRoutesFailureResponse(summary)
	if response.status == http.StatusTooManyRequests {
		openAIRateLimitError(c, response.status, response.code, response.message, response.retryAfter)
		return
	}
	openAIError(c, response.status, response.errType, response.code, response.message)
}

func selectAllRoutesFailureResponse(summary allRoutesFailureSummary) allRoutesFailureResponse {
	if summary.rateLimitedSeen {
		retryAfter := summary.rateLimitedRetryAfter
		if retryAfter <= 0 {
			retryAfter = allRoutesFailedDefaultRetryAfter
		}
		return allRoutesFailureResponse{
			status:     http.StatusTooManyRequests,
			errType:    "rate_limit_error",
			code:       "all_routes_rate_limited",
			message:    "上游账号当前被限流，请稍后重试",
			retryAfter: retryAfter,
		}
	}
	if summary.localCapacitySeen {
		return allRoutesFailureResponse{
			status:  http.StatusServiceUnavailable,
			errType: "server_error",
			code:    "all_routes_failed",
			message: "请求暂时无法完成，请稍后重试",
		}
	}
	if summary.upstreamTimeoutSeen {
		return allRoutesFailureResponse{
			status:  http.StatusGatewayTimeout,
			errType: "server_error",
			code:    "upstream_timeout",
			message: "上游请求超时，请稍后重试",
		}
	}
	if summary.upstreamFailureSeen {
		return allRoutesFailureResponse{
			status:  http.StatusBadGateway,
			errType: "server_error",
			code:    "upstream_error",
			message: "上游服务暂不可用，请稍后重试",
		}
	}
	if summary.accountDeadSeen || summary.accountUnavailable {
		return allRoutesFailureResponse{
			status:  http.StatusServiceUnavailable,
			errType: "server_error",
			code:    "no_available_account",
			message: "暂无可用上游账号，请稍后重试",
		}
	}
	return allRoutesFailureResponse{
		status:  http.StatusServiceUnavailable,
		errType: "server_error",
		code:    "all_routes_failed",
		message: "请求暂时无法完成，请稍后重试",
	}
}

func returnableUpstream(up sdk.UpstreamResponse) bool {
	return up.StatusCode > 0 && len(up.Body) > 0
}

func isTimeoutFailure(execution forwardExecution) bool {
	if execution.outcome.Upstream.StatusCode == http.StatusGatewayTimeout {
		return true
	}
	if errors.Is(execution.err, context.DeadlineExceeded) {
		return true
	}
	var timeoutErr interface{ Timeout() bool }
	if errors.As(execution.err, &timeoutErr) && timeoutErr.Timeout() {
		return true
	}
	reason := strings.ToLower(judgmentReason(execution))
	return strings.Contains(reason, "timeout") || strings.Contains(reason, "timed out") || strings.Contains(reason, "deadline exceeded")
}

func routesForAPIKey(state *forwardState, requirements routing.Requirements) []routing.Candidate {
	if state == nil || state.keyInfo == nil {
		return nil
	}
	if !apiKeyGroupMatchesRequirements(state.keyInfo, requirements) {
		return nil
	}
	return []routing.Candidate{keyInfoRoute(state.keyInfo)}
}

func apiKeyGroupMatchesRequirements(keyInfo *auth.APIKeyInfo, requirements routing.Requirements) bool {
	if keyInfo == nil {
		return false
	}
	if strings.EqualFold(keyInfo.GroupPlatform, "openai") {
		return !requirements.NeedsImage ||
			pluginSettingEnabledForKey(keyInfo.GroupPluginSettings, "openai", "image_enabled")
	}
	return true
}

type groupRequirementError struct {
	status  int
	errType string
	code    string
	message string
}

func apiKeyGroupRequirementError(keyInfo *auth.APIKeyInfo, requirements routing.Requirements) (groupRequirementError, bool) {
	if keyInfo == nil || !strings.EqualFold(keyInfo.GroupPlatform, "openai") {
		return groupRequirementError{}, false
	}
	imageEnabled := pluginSettingEnabledForKey(keyInfo.GroupPluginSettings, "openai", "image_enabled")
	if requirements.NeedsImage && !imageEnabled {
		return groupRequirementError{
			status:  http.StatusForbidden,
			errType: "invalid_request_error",
			code:    "image_generation_disabled",
			message: "当前分组未开启图片生成功能",
		}, true
	}
	return groupRequirementError{}, false
}

func pluginSettingEnabledForKey(settings map[string]map[string]string, plugin, key string) bool {
	for pluginName, kv := range settings {
		if !strings.EqualFold(pluginName, plugin) {
			continue
		}
		for k, v := range kv {
			if strings.EqualFold(k, key) {
				return strings.EqualFold(strings.TrimSpace(v), "true")
			}
		}
	}
	return false
}

func keyInfoRoute(keyInfo *auth.APIKeyInfo) routing.Candidate {
	return routing.Candidate{
		GroupID:                keyInfo.GroupID,
		Platform:               keyInfo.GroupPlatform,
		EffectiveRate:          billing.ResolveBillingRateForGroup(keyInfo.UserGroupRates, keyInfo.GroupID, keyInfo.GroupRateMultiplier),
		GroupRateMultiplier:    keyInfo.GroupRateMultiplier,
		GroupServiceTier:       keyInfo.GroupServiceTier,
		GroupForceInstructions: keyInfo.GroupForceInstructions,
		GroupPluginSettings:    clonePluginSettingsForKey(keyInfo.GroupPluginSettings),
		UserPluginSettings:     clonePluginSettingsForKey(keyInfo.UserGroupPluginSettings[int64(keyInfo.GroupID)]),
	}
}

func clonePluginSettingsForKey(in map[string]map[string]string) map[string]map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]map[string]string, len(in))
	for plugin, settings := range in {
		if len(settings) == 0 {
			continue
		}
		out[plugin] = make(map[string]string, len(settings))
		for k, v := range settings {
			out[plugin][k] = v
		}
	}
	return out
}

func keyInfoForRoute(base *auth.APIKeyInfo, route routing.Candidate) *auth.APIKeyInfo {
	info := *base
	info.GroupID = route.GroupID
	info.GroupPlatform = route.Platform
	info.GroupRateMultiplier = route.GroupRateMultiplier
	info.GroupServiceTier = route.GroupServiceTier
	info.GroupForceInstructions = route.GroupForceInstructions
	info.GroupPluginSettings = route.GroupPluginSettings
	if route.UserPluginSettings != nil {
		info.UserGroupPluginSettings = map[int64]map[string]map[string]string{
			int64(route.GroupID): clonePluginSettingsForKey(route.UserPluginSettings),
		}
	}
	return &info
}

// canFailover 是否允许换账号重试。
// 流式已写入 → 不可；err 非 nil（插件自身崩）→ 可；其余由 Kind.ShouldFailover() 决定。
func (f *Forwarder) canFailover(c *gin.Context, state *forwardState, execution forwardExecution) bool {
	if state.stream && c.Writer.Written() {
		return false
	}
	if execution.err != nil {
		return true
	}
	return execution.outcome.Kind.ShouldFailover()
}

// callPlugin 把请求发给插件。
func (f *Forwarder) callPlugin(c *gin.Context, state *forwardState) forwardExecution {
	outcome, err := state.plugin.Gateway.Forward(c.Request.Context(), buildPluginRequest(c, state))
	return forwardExecution{
		outcome:  outcome,
		err:      err,
		duration: time.Since(state.startedAt),
	}
}
