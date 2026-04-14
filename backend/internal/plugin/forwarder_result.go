package plugin

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent/account"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	sdk "github.com/DouDOU-start/airgate-sdk"
)

func (f *Forwarder) finishForward(c *gin.Context, state *forwardState, execution forwardExecution) {
	ctx := c.Request.Context()

	f.reportForwardExecution(ctx, state, execution)
	f.persistUpdatedCredentials(state.account.ID, execution.result)

	if execution.err != nil {
		slog.Error("插件转发失败", "plugin", state.plugin.Name, "error", execution.err)
		maybeWriteForwardError(c, state, execution)
		return
	}

	if execution.result == nil {
		slog.Error("插件未返回结果", "plugin", state.plugin.Name)
		if !state.stream {
			openAIError(c, http.StatusBadGateway, "server_error", "upstream_error", "插件未返回结果")
		}
		return
	}

	if execution.result.StatusCode >= 400 {
		writeSanitizedForwardError(c, state, execution)
		return
	}

	f.recordForwardUsage(c, state, execution)

	if !state.stream && execution.result.Body != nil {
		writeForwardResponse(c, execution.result)
	}
}

func maybeWriteForwardError(c *gin.Context, state *forwardState, execution forwardExecution) {
	if execution.err == nil || c == nil || state == nil {
		return
	}
	if state.stream && c.Writer.Written() {
		return
	}
	// 客户端请求自身错误：把插件回填的 4xx 状态码与原始错误信息透传出去，
	// 让客户端能看到诸如"model is not supported"这类有用提示，而不是笼统的 502。
	if isClientSideForwardError(execution) {
		message := execution.result.ErrorMessage
		if message == "" {
			message = execution.err.Error()
		}
		openAIError(c, execution.result.StatusCode, "invalid_request_error", "invalid_request", message)
		return
	}
	openAIError(c, http.StatusBadGateway, "server_error", "upstream_error", "插件转发失败")
}

// writeSanitizedForwardError 处理上游返回 4xx/5xx 时的响应。
// 客户端侧错误（如 model 不支持）透传原始信息；其余一律脱敏为 502 upstream_error，
// 仅按账号状态给出大类说明，完整错误仅落服务端日志，避免暴露上游内部状态。
func writeSanitizedForwardError(c *gin.Context, state *forwardState, execution forwardExecution) {
	if state.stream && c.Writer.Written() {
		return
	}

	if isClientSideForwardError(execution) {
		if !state.stream && execution.result.Body != nil {
			writeForwardResponse(c, execution.result)
			return
		}
		message := execution.result.ErrorMessage
		if message == "" {
			message = "请求参数无效"
		}
		openAIError(c, execution.result.StatusCode, "invalid_request_error", "invalid_request", message)
		return
	}

	slog.Error("上游返回错误，已脱敏响应给客户端",
		"plugin", state.plugin.Name,
		"account_id", state.account.ID,
		"status", execution.result.StatusCode,
		"account_status", execution.result.AccountStatus,
		"error_message", execution.result.ErrorMessage)

	openAIError(c, http.StatusBadGateway, "server_error", "upstream_error", sanitizedUpstreamMessage(execution.result.AccountStatus))
}

func sanitizedUpstreamMessage(status sdk.AccountStatus) string {
	switch status {
	case sdk.AccountStatusRateLimited:
		return "上游账号当前被限流，请稍后重试"
	case sdk.AccountStatusDisabled:
		return "上游账号不可用，请联系管理员"
	case sdk.AccountStatusExpired:
		return "上游账号凭证已过期，请联系管理员"
	default:
		return "上游服务暂不可用，请稍后重试"
	}
}

func (f *Forwarder) reportForwardExecution(ctx context.Context, state *forwardState, execution forwardExecution) {
	accountStatus := sdk.AccountStatusOK
	if execution.result != nil {
		accountStatus = execution.result.AccountStatus
	}

	isSuccess := execution.err == nil && execution.result != nil &&
		execution.result.StatusCode >= 200 && execution.result.StatusCode < 400
	isRateLimited := accountStatus == sdk.AccountStatusRateLimited
	isAccountError := accountStatus == sdk.AccountStatusExpired || accountStatus == sdk.AccountStatusDisabled

	// 账号池场景：上游是账号池（如 sub2api）时，"No available accounts"
	// 之类的池子耗尽错误是**请求级**的瞬时事件，不是账号级故障：
	//   - 本地账号凭证完全没坏
	//   - 池子下一秒可能就恢复了
	// 所以：本次请求当作软失败返回给用户（由客户端/上层决定是否重试），
	// 账号**不暂停调度**、**不累计 fail count**、**不写 status=error**。
	// 下一个进来的请求依然可以选中这个账号，让上游自己决定是否已恢复。
	if isAccountError && state.account != nil && state.account.UpstreamIsPool {
		slog.Warn("上游账号池临时耗尽，软失败返回但不影响调度",
			"account_id", state.account.ID,
			"reason", resolveAccountErrorReason(execution))
		f.scheduler.DecrementRPM(ctx, state.account.ID)
		return
	}

	switch {
	case isSuccess:
		f.scheduler.ReportResult(state.account.ID, true, execution.duration)
		f.scheduler.RefreshSession(ctx, state.account.ID, state.sessionID, state.account.Extra)
	case isRateLimited:
		f.scheduler.DecrementRPM(ctx, state.account.ID)
		f.scheduler.MarkOverloaded(ctx, state.account.ID, execution.result.RetryAfter)
		slog.Warn("上游限流，临时暂停调度", "account_id", state.account.ID, "retry_after", execution.result.RetryAfter)
	case isAccountError:
		f.scheduler.DecrementRPM(ctx, state.account.ID)
		f.scheduler.ReportAccountError(state.account.ID, resolveAccountErrorReason(execution))
	case execution.err != nil:
		f.scheduler.DecrementRPM(ctx, state.account.ID)
		switch {
		case isClientSideForwardError(execution):
			// 客户端请求本身的问题（不被支持的 model 等），账号是无辜的，不计失败。
			slog.Warn("忽略客户端侧转发错误",
				"account_id", state.account.ID,
				"status", execution.result.StatusCode,
				"error", execution.err)
		case shouldPenalizeForwardError(execution.err):
			f.scheduler.ReportResult(state.account.ID, false, execution.duration, execution.err.Error())
		default:
			slog.Warn("忽略非惩罚性转发错误", "account_id", state.account.ID, "error", execution.err)
		}
	default:
		f.scheduler.DecrementRPM(ctx, state.account.ID)
	}
}

func resolveAccountErrorReason(execution forwardExecution) string {
	if execution.result != nil && execution.result.ErrorMessage != "" {
		return execution.result.ErrorMessage
	}
	if execution.err != nil {
		return execution.err.Error()
	}
	return "账号凭证错误"
}

func (f *Forwarder) persistUpdatedCredentials(accountID int, result *sdk.ForwardResult) {
	if result == nil || len(result.UpdatedCredentials) == 0 {
		return
	}
	go f.updateAccountCredentials(accountID, result.UpdatedCredentials)
}

func (f *Forwarder) recordForwardUsage(c *gin.Context, state *forwardState, execution forwardExecution) {
	result := execution.result
	actualModel := result.Model
	if actualModel == "" {
		actualModel = state.model
	}

	// 三条独立倍率管道：
	//   - billingRate: 平台对 reseller 的计费倍率（group/user 优先级链）
	//   - sellRate:    reseller 对客户的销售倍率（独立 markup 管道）
	//   - accountRate: 账号自身的真实成本系数（独立"账号计费"统计管道）
	billingRate := billing.ResolveBillingRate(state.keyInfo)
	sellRate := state.keyInfo.SellRate
	accountRate := state.account.RateMultiplier

	calcResult := f.calculator.Calculate(billing.CalculateInput{
		InputCost:         result.InputCost,
		OutputCost:        result.OutputCost,
		CachedInputCost:   result.CachedInputCost,
		CacheCreationCost: result.CacheCreationCost,
		BillingRate:       billingRate,
		SellRate:          sellRate,
		AccountRate:       accountRate,
	})

	// scheduler 的 window cost 沿用 account_cost（= total × account_rate），
	// 用于追踪上游账号自身的窗口消耗，做 RPM/容量限流。与用户账单完全解耦。
	f.scheduler.AddWindowCost(c.Request.Context(), state.account.ID, calcResult.AccountCost)

	f.recorder.Record(billing.UsageRecord{
		UserID:                state.keyInfo.UserID,
		APIKeyID:              state.keyInfo.KeyID,
		AccountID:             state.account.ID,
		GroupID:               state.keyInfo.GroupID,
		Platform:              state.plugin.Platform,
		Model:                 actualModel,
		InputTokens:           result.InputTokens,
		OutputTokens:          result.OutputTokens,
		CachedInputTokens:     result.CachedInputTokens,
		CacheCreationTokens:   result.CacheCreationTokens,
		CacheCreation5mTokens: result.CacheCreation5mTokens,
		CacheCreation1hTokens: result.CacheCreation1hTokens,
		ReasoningOutputTokens: result.ReasoningOutputTokens,
		InputPrice:            result.InputPrice,
		OutputPrice:           result.OutputPrice,
		CachedInputPrice:      result.CachedInputPrice,
		CacheCreationPrice:    result.CacheCreationPrice,
		CacheCreation1hPrice:  result.CacheCreation1hPrice,
		InputCost:             calcResult.InputCost,
		OutputCost:            calcResult.OutputCost,
		CachedInputCost:       calcResult.CachedInputCost,
		CacheCreationCost:     calcResult.CacheCreationCost,
		TotalCost:             calcResult.TotalCost,
		ActualCost:            calcResult.ActualCost,
		BilledCost:            calcResult.BilledCost,
		AccountCost:           calcResult.AccountCost,
		RateMultiplier:        calcResult.RateMultiplier,
		SellRate:              calcResult.SellRate,
		AccountRateMultiplier: calcResult.AccountRateMultiplier,
		ServiceTier:           result.ServiceTier,
		Stream:                state.stream,
		DurationMs:            execution.duration.Milliseconds(),
		FirstTokenMs:          result.FirstTokenMs,
		UserAgent:             c.Request.UserAgent(),
		IPAddress:             c.ClientIP(),
	})
}

func writeForwardResponse(c *gin.Context, result *sdk.ForwardResult) {
	for k, vals := range result.Headers {
		for _, v := range vals {
			c.Writer.Header().Set(k, v)
		}
	}
	c.Writer.WriteHeader(result.StatusCode)
	_, _ = c.Writer.Write(result.Body)
}

// updateAccountCredentials 异步更新账号凭证（合并写入，保留未变更的字段）。
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
