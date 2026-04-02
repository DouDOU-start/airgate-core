package plugin

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	sdk "github.com/DouDOU-start/airgate-sdk"
)

func (f *Forwarder) ensureForwardAllowed(c *gin.Context, state *forwardState) bool {
	if err := f.limiter.Check(c.Request.Context(), state.keyInfo.UserID, state.plugin.Platform); err != nil {
		openAIError(c, http.StatusTooManyRequests, "rate_limit_error", "rate_limit_exceeded", err.Error())
		return false
	}

	if state.keyInfo.UserBalance <= 0 {
		c.JSON(http.StatusPaymentRequired, gin.H{
			"error": gin.H{
				"message": "余额不足",
				"type":    "insufficient_quota",
				"code":    "insufficient_quota",
			},
		})
		return false
	}

	return true
}

func (f *Forwarder) selectForwardAccount(c *gin.Context, state *forwardState) bool {
	account, err := f.scheduler.SelectAccount(
		c.Request.Context(),
		state.plugin.Platform,
		state.model,
		state.keyInfo.UserID,
		state.keyInfo.GroupID,
		state.sessionID,
	)
	if err != nil {
		slog.Warn("账户调度失败", "platform", state.plugin.Platform, "model", state.model, "error", err)
		openAIError(c, http.StatusServiceUnavailable, "server_error", "no_available_account", "无可用账户")
		return false
	}

	state.account = account
	return true
}

func (f *Forwarder) prepareForwardExecution(c *gin.Context, state *forwardState) (func(), bool) {
	ctx := c.Request.Context()
	state.requestID = uuid.New().String()

	f.scheduler.IncrementRPM(ctx, state.account.ID)

	releaseMessageLock := func() {}
	if scheduler.IsRealUserMessage(state.body) {
		acquired, _ := f.scheduler.AcquireMessageLock(ctx, state.account.ID, state.requestID, state.account.Extra)
		if acquired {
			releaseMessageLock = func() {
				f.scheduler.ReleaseMessageLock(ctx, state.account.ID, state.requestID)
			}
			f.scheduler.EnforceMessageDelay(ctx, state.account.ID, state.account.Extra)
		}
	}

	maxConc := state.account.MaxConcurrency
	if maxConc <= 0 {
		maxConc = 5
	}

	if err := f.concurrency.AcquireSlot(ctx, state.account.ID, state.requestID, maxConc); err != nil {
		releaseMessageLock()
		f.scheduler.DecrementRPM(ctx, state.account.ID)
		openAIError(c, http.StatusTooManyRequests, "rate_limit_error", "concurrency_limit", "并发已满，请稍后重试")
		return nil, false
	}

	return func() {
		f.concurrency.ReleaseSlot(ctx, state.account.ID, state.requestID)
		releaseMessageLock()
	}, true
}

func (f *Forwarder) executeForward(c *gin.Context, state *forwardState) forwardExecution {
	result, err := state.plugin.Gateway.Forward(c.Request.Context(), buildForwardRequest(c, state))
	return forwardExecution{
		result:   result,
		err:      err,
		duration: time.Since(state.startedAt),
	}
}

func buildForwardRequest(c *gin.Context, state *forwardState) *sdk.ForwardRequest {
	fwdReq := &sdk.ForwardRequest{
		Account: buildSDKAccount(state.account),
		Body:    state.body,
		Headers: buildForwardHeaders(c.Request.Header, state.keyInfo),
		Model:   state.model,
		Stream:  state.stream,
	}
	if state.stream {
		fwdReq.Writer = c.Writer
	}
	return fwdReq
}

func buildForwardHeaders(source http.Header, keyInfo *auth.APIKeyInfo) http.Header {
	headers := source.Clone()
	if keyInfo.GroupServiceTier != "" {
		headers.Set("X-Airgate-Service-Tier", keyInfo.GroupServiceTier)
	}
	return headers
}

func buildSDKAccount(account *ent.Account) *sdk.Account {
	return &sdk.Account{
		ID:          int64(account.ID),
		Name:        account.Name,
		Platform:    account.Platform,
		Type:        account.Type,
		Credentials: account.Credentials,
		ProxyURL:    buildProxyURL(account),
	}
}

func buildProxyURL(account *ent.Account) string {
	proxy, err := account.Edges.ProxyOrErr()
	if err != nil || proxy == nil {
		return ""
	}

	if proxy.Username != "" {
		return fmt.Sprintf("%s://%s:%s@%s:%d", proxy.Protocol, proxy.Username, proxy.Password, proxy.Address, proxy.Port)
	}
	return fmt.Sprintf("%s://%s:%d", proxy.Protocol, proxy.Address, proxy.Port)
}
