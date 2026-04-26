package plugin

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent/account"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	sdk "github.com/DouDOU-start/airgate-sdk"
)

// openAIError 以 OpenAI 兼容格式返回错误，保证 Claude Code 等客户端能识别。
func openAIError(c *gin.Context, status int, errType, code, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
			"code":    code,
		},
	})
}

// writeResult 是一次 forward 的终点。按 outcome.Kind 分派响应写入。
//
//	系统错误（err != nil）    → 记录判决 + 脱敏错误响应
//	Success                   → 计费 + 透传上游响应
//	ClientError               → 透传上游响应（客户端能看到原始错误），可选计费
//	账号级 / 上游抖动 / 流中断 → 脱敏错误响应
func (f *Forwarder) writeResult(c *gin.Context, state *forwardState, execution forwardExecution) {
	ctx := c.Request.Context()

	f.applyOutcome(ctx, state, execution)
	f.persistUpdatedCredentials(state.account.ID, execution.outcome.UpdatedCredentials)

	if execution.err != nil {
		slog.Error("插件转发失败",
			"plugin", state.plugin.Name,
			"kind", execution.outcome.Kind,
			"error", execution.err)
		writeFailureResponse(c, state, execution)
		return
	}

	switch execution.outcome.Kind {
	case sdk.OutcomeSuccess:
		f.recordUsage(c, state, execution)
		if !state.stream {
			writeUpstream(c, execution.outcome.Upstream)
		}
	case sdk.OutcomeClientError:
		slog.Warn("上游返回客户端错误，脱敏响应给客户端",
			"plugin", state.plugin.Name,
			"account_id", state.account.ID,
			"group_id", state.keyInfo.GroupID,
			"status_code", execution.outcome.Upstream.StatusCode,
			"reason", execution.outcome.Reason)
		if !state.stream {
			openAIError(c, http.StatusBadRequest, "invalid_request_error", "invalid_request", "请求无法完成，请检查输入后重试")
		}
		if execution.outcome.Usage != nil {
			f.recordUsage(c, state, execution)
		}
	default:
		writeFailureResponse(c, state, execution)
	}
}

// writeFailureResponse 非 Success / 非 ClientError 的响应：脱敏为 502（或 429），
// 按 Kind 给出大类说明。流式已写入时 no-op。
func writeFailureResponse(c *gin.Context, state *forwardState, execution forwardExecution) {
	if state.stream && c.Writer.Written() {
		return
	}
	pluginName := ""
	if state.plugin != nil {
		pluginName = state.plugin.Name
	}
	accountID := 0
	if state.account != nil {
		accountID = state.account.ID
	}
	groupID := 0
	if state.keyInfo != nil {
		groupID = state.keyInfo.GroupID
	}
	slog.Warn("判决为失败，脱敏响应给客户端",
		"plugin", pluginName,
		"account_id", accountID,
		"group_id", groupID,
		"kind", execution.outcome.Kind,
		"status_code", execution.outcome.Upstream.StatusCode,
		"reason", execution.outcome.Reason)

	status := http.StatusBadGateway
	if execution.outcome.Kind == sdk.OutcomeAccountRateLimited {
		status = http.StatusTooManyRequests
	}
	openAIError(c, status, "server_error", "upstream_error", sanitizedMessage(execution.outcome.Kind))
}

func sanitizedMessage(kind sdk.OutcomeKind) string {
	switch kind {
	case sdk.OutcomeAccountRateLimited:
		return "上游账号当前被限流，请稍后重试"
	case sdk.OutcomeAccountDead:
		return "上游账号不可用，请联系管理员"
	case sdk.OutcomeStreamAborted:
		return "响应流中断"
	case sdk.OutcomeUpstreamTransient:
		return "上游服务暂不可用，请稍后重试"
	default:
		return "上游服务暂不可用，请稍后重试"
	}
}

// applyOutcome 把本次判决交给 scheduler.Apply，由状态机统一处理。
// forwarder 不再关心 MarkOverloaded / MarkDegraded / ReportAccountError 等内部方法。
func (f *Forwarder) applyOutcome(ctx context.Context, state *forwardState, execution forwardExecution) {
	j := scheduler.Judgment{
		Kind:       execution.outcome.Kind,
		RetryAfter: execution.outcome.RetryAfter,
		Reason:     judgmentReason(execution),
		Duration:   execution.duration,
		IsPool:     state.account != nil && state.account.UpstreamIsPool,
	}
	f.scheduler.Apply(ctx, state.account.ID, j)

	// Success 额外刷新会话（状态机内部已更新 last_used_at）
	if execution.outcome.Kind == sdk.OutcomeSuccess {
		f.scheduler.RefreshSession(ctx, state.account.ID, state.sessionID, state.account.Extra)
	}
	// Unknown 留日志提示契约不完整
	if execution.outcome.Kind == sdk.OutcomeUnknown && execution.err != nil {
		slog.Warn("插件未声明 Outcome.Kind 且返回 error，按 Unknown 保守处理",
			"account_id", state.account.ID,
			"error", execution.err)
	}
}

// judgmentReason 优先 outcome.Reason，其次 err.Error()。
func judgmentReason(execution forwardExecution) string {
	if execution.outcome.Reason != "" {
		return execution.outcome.Reason
	}
	if execution.err != nil {
		return execution.err.Error()
	}
	return ""
}

// persistUpdatedCredentials 插件在 Forward 中刷新了凭证（OAuth 轮转）时异步落库。
func (f *Forwarder) persistUpdatedCredentials(accountID int, updated map[string]string) {
	if len(updated) == 0 {
		return
	}
	go f.updateAccountCredentials(accountID, updated)
}

// recordUsage 写 usage_log 并更新 scheduler 的窗口费用。调用前 outcome.Usage 必须非 nil。
func (f *Forwarder) recordUsage(c *gin.Context, state *forwardState, execution forwardExecution) {
	usage := execution.outcome.Usage
	if usage == nil {
		return
	}

	actualModel := usage.Model
	if actualModel == "" {
		actualModel = state.model
	}

	// 三条独立倍率管道：
	//   billingRate: 平台对 reseller 的计费倍率（group/user 优先级链）
	//   sellRate:    reseller 对客户的销售倍率（独立 markup 管道）
	//   accountRate: 账号自身的真实成本系数（"账号计费"统计管道）
	calc := f.calculator.Calculate(billing.CalculateInput{
		InputCost:         usage.InputCost,
		OutputCost:        usage.OutputCost,
		CachedInputCost:   usage.CachedInputCost,
		CacheCreationCost: usage.CacheCreationCost,
		BillingRate:       billing.ResolveBillingRate(state.keyInfo),
		SellRate:          state.keyInfo.SellRate,
		AccountRate:       state.account.RateMultiplier,
	})

	// 窗口费用沿用 account_cost（= total × account_rate），与用户账单解耦。
	f.scheduler.AddWindowCost(c.Request.Context(), state.account.ID, calc.AccountCost)

	f.recorder.Record(billing.UsageRecord{
		UserID:                state.keyInfo.UserID,
		APIKeyID:              state.keyInfo.KeyID,
		AccountID:             state.account.ID,
		GroupID:               state.keyInfo.GroupID,
		Platform:              state.plugin.Platform,
		Model:                 actualModel,
		InputTokens:           usage.InputTokens,
		OutputTokens:          usage.OutputTokens,
		CachedInputTokens:     usage.CachedInputTokens,
		CacheCreationTokens:   usage.CacheCreationTokens,
		CacheCreation5mTokens: usage.CacheCreation5mTokens,
		CacheCreation1hTokens: usage.CacheCreation1hTokens,
		ReasoningOutputTokens: usage.ReasoningOutputTokens,
		InputPrice:            usage.InputPrice,
		OutputPrice:           usage.OutputPrice,
		CachedInputPrice:      usage.CachedInputPrice,
		CacheCreationPrice:    usage.CacheCreationPrice,
		CacheCreation1hPrice:  usage.CacheCreation1hPrice,
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
		ServiceTier:           usage.ServiceTier,
		Stream:                state.stream,
		DurationMs:            execution.duration.Milliseconds(),
		FirstTokenMs:          usage.FirstTokenMs,
		UserAgent:             c.Request.UserAgent(),
		IPAddress:             c.ClientIP(),
	})
}

// writeUpstream 把上游原始响应透传给客户端。
func writeUpstream(c *gin.Context, up sdk.UpstreamResponse) {
	for k, vals := range up.Headers {
		for _, v := range vals {
			c.Writer.Header().Set(k, v)
		}
	}
	status := up.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	c.Writer.WriteHeader(status)
	_, _ = c.Writer.Write(up.Body)
}

// updateAccountCredentials 异步 merge 写入账号凭证，保留未变更字段。
func (f *Forwarder) updateAccountCredentials(accountID int, updated map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	acc, err := f.db.Account.Query().Where(account.ID(accountID)).Only(ctx)
	if err != nil {
		slog.Error("更新凭证失败：查询账号", "account_id", accountID, "error", err)
		return
	}

	merged := make(map[string]string, len(acc.Credentials)+len(updated))
	for k, v := range acc.Credentials {
		merged[k] = v
	}
	for k, v := range updated {
		merged[k] = v
	}

	if err := f.db.Account.UpdateOneID(accountID).SetCredentials(merged).Exec(ctx); err != nil {
		slog.Error("更新凭证失败：写入数据库", "account_id", accountID, "error", err)
		return
	}
	slog.Info("插件回传凭证已持久化", "account_id", accountID)
}
