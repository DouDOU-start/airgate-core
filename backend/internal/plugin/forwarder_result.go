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
		if !state.stream && execution.result.Body != nil {
			writeForwardResponse(c, execution.result)
		}
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
	openAIError(c, http.StatusBadGateway, "server_error", "upstream_error", "插件转发失败")
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

	switch {
	case isSuccess:
		f.scheduler.ReportResult(state.account.ID, true, execution.duration)
		f.scheduler.RefreshSession(ctx, state.account.ID, state.sessionID, state.account.Extra)
	case isRateLimited:
		f.scheduler.DecrementRPM(ctx, state.account.ID)
		slog.Warn("上游限流", "account_id", state.account.ID, "retry_after", execution.result.RetryAfter)
	case isAccountError:
		f.scheduler.DecrementRPM(ctx, state.account.ID)
		f.scheduler.ReportAccountError(state.account.ID, resolveAccountErrorReason(execution))
	case execution.err != nil:
		f.scheduler.DecrementRPM(ctx, state.account.ID)
		if shouldPenalizeForwardError(execution.err) {
			f.scheduler.ReportResult(state.account.ID, false, execution.duration, execution.err.Error())
		} else {
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

	groupRate := state.keyInfo.GroupRateMultiplier
	if groupRate <= 0 {
		groupRate = 1.0
	}

	calcResult := f.calculator.Calculate(billing.CalculateInput{
		InputCost:             result.InputCost,
		OutputCost:            result.OutputCost,
		CachedInputCost:       result.CachedInputCost,
		GroupRateMultiplier:   groupRate,
		AccountRateMultiplier: state.account.RateMultiplier,
		UserRateMultiplier:    1.0,
	})

	f.scheduler.AddWindowCost(c.Request.Context(), state.account.ID, calcResult.ActualCost)

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
		ReasoningOutputTokens: result.ReasoningOutputTokens,
		InputPrice:            result.InputPrice,
		OutputPrice:           result.OutputPrice,
		CachedInputPrice:      result.CachedInputPrice,
		InputCost:             calcResult.InputCost,
		OutputCost:            calcResult.OutputCost,
		CachedInputCost:       calcResult.CachedInputCost,
		TotalCost:             calcResult.TotalCost,
		ActualCost:            calcResult.ActualCost,
		RateMultiplier:        calcResult.RateMultiplier,
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
