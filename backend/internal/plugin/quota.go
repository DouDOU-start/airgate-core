package plugin

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	sdk "github.com/DouDOU-start/airgate-sdk"
)

// checkBalance 执行请求进入业务逻辑前的最后一道准入：余额预检。
// 只读元信息路径（/v1/models 等）对所有认证通过的 key 放行，不卡余额。
//
// 限流 / 并发闸门不在此处——它们分别在 acquireClientQuota（用户/API Key 级）
// 和 acquireAccountSlot（账号级）中，各自带 release 回调。
func (f *Forwarder) checkBalance(c *gin.Context, state *forwardState) bool {
	if isMetadataOnlyPath(state.requestPath) {
		return true
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

// isMetadataOnlyPath 只读元信息（/v1/models 等）不打上游、不计费、不需要账号调度。
func isMetadataOnlyPath(path string) bool {
	switch path {
	case "/v1/models", "/models":
		return true
	}
	return false
}

// acquireClientQuota 获取用户级 + API Key 级两层并发槽。返回 release 回调；
// 任意一层超限都直接写 429 并返回 nil（调用方看到 nil 立即 return）。
//
// slot ID 独立于 state.requestID：后者在每次 failover 会被重新生成，而这两层槽位
// 跨整个 Forward 请求稳定，必须有稳定 ID 保证 SREM 能匹配上。
func (f *Forwarder) acquireClientQuota(c *gin.Context, state *forwardState) func() {
	ctx := c.Request.Context()
	slotID := uuid.New().String()
	userID, keyID := state.keyInfo.UserID, state.keyInfo.KeyID

	userHeld := false
	if max := state.keyInfo.UserMaxConcurrency; max > 0 {
		if err := f.concurrency.AcquireUserSlot(ctx, userID, slotID, max, 0); err != nil {
			openAIError(c, http.StatusTooManyRequests, "rate_limit_error", "user_concurrency_limit", "用户并发已达上限，请稍后重试")
			return nil
		}
		userHeld = true
	}

	keyHeld := false
	if max := state.keyInfo.KeyMaxConcurrency; max > 0 {
		if err := f.concurrency.AcquireAPIKeySlot(ctx, keyID, slotID, max, 0); err != nil {
			if userHeld {
				f.concurrency.ReleaseUserSlot(ctx, userID, slotID)
			}
			openAIError(c, http.StatusTooManyRequests, "rate_limit_error", "apikey_concurrency_limit", "API Key 并发已达上限，请稍后重试")
			return nil
		}
		keyHeld = true
	}

	// 反向释放：apikey 先，user 后。
	return func() {
		if keyHeld {
			f.concurrency.ReleaseAPIKeySlot(ctx, keyID, slotID)
		}
		if userHeld {
			f.concurrency.ReleaseUserSlot(ctx, userID, slotID)
		}
	}
}

// pickAccount 调度选号并写到 state.account。失败时返回 error，由调用方决定如何处理
// （例如主循环可以根据 softExclude 是否非空决定排队等待还是直接写 503）。
func (f *Forwarder) pickAccount(c *gin.Context, state *forwardState, excludeIDs ...int) error {
	account, err := f.scheduler.SelectAccount(
		c.Request.Context(),
		state.requestedPlatform,
		state.model,
		state.keyInfo.UserID,
		state.keyInfo.GroupID,
		state.sessionID,
		excludeIDs...,
	)
	if err != nil {
		return err
	}
	state.account = account
	return nil
}

// acquireAccountSlot 获取账号级闸门：RPM 配额 + 真实用户消息串行锁 + 账号并发槽。
// 返回 release func 与 ok 标记。ok=false 表示当前账号暂不可用（RPM 已满 / 并发已满），
// 调用方应把本账号加入 excludeIDs 并 failover 到下一个账号。失败时不写客户端响应——
// 由主循环在 failover 全部用尽时兜底写 503。
//
// 每次 failover attempt 都要重新 acquire。release 顺序和 acquire 顺序相反。
func (f *Forwarder) acquireAccountSlot(c *gin.Context, state *forwardState) (func(), bool) {
	ctx := c.Request.Context()
	state.requestID = uuid.New().String()

	// 1. RPM 原子检查并递增
	maxRPM := scheduler.ExtraInt(state.account.Extra, "max_rpm")
	if !f.scheduler.TryIncrementRPM(ctx, state.account.ID, maxRPM) {
		slog.Info("账号 RPM 已达上限，尝试 failover",
			"account_id", state.account.ID, "max_rpm", maxRPM)
		return nil, false
	}

	// 2. 消息锁 + 均摊延迟（仅真实用户消息）
	releaseMsgLock := func() {}
	if scheduler.IsRealUserMessage(state.body) {
		acquired, _ := f.scheduler.AcquireMessageLock(ctx, state.account.ID, state.requestID, state.account.Extra)
		if acquired {
			releaseMsgLock = func() {
				f.scheduler.ReleaseMessageLock(ctx, state.account.ID, state.requestID)
			}
			f.scheduler.EnforceMessageDelay(ctx, state.account.ID, state.account.Extra)
		}
	}

	// 3. 账号并发槽
	maxConc := state.account.MaxConcurrency
	if maxConc <= 0 {
		maxConc = 5
	}
	slotTTL := time.Duration(scheduler.ExtraInt(state.account.Extra, "slot_ttl_seconds")) * time.Second

	if err := f.concurrency.AcquireSlot(ctx, state.account.ID, state.requestID, maxConc, slotTTL); err != nil {
		releaseMsgLock()
		f.scheduler.DecrementRPM(ctx, state.account.ID)
		slog.Info("账号并发已满，尝试 failover",
			"account_id", state.account.ID, "max_concurrency", maxConc)
		return nil, false
	}

	// 反向释放：slot → msg lock。RPM 不在 release 里回退——正常完成流程会通过
	// scheduler.Apply 决定是否 DecrementRPM（非 Success 判决都会回退）。
	return func() {
		f.concurrency.ReleaseSlot(ctx, state.account.ID, state.requestID)
		releaseMsgLock()
	}, true
}

// forwardMetadataOnly 处理只读元信息请求（/v1/models 等）。
// 插件本地合成响应，不需要账号、不计费、不走 middleware / failover。
func (f *Forwarder) forwardMetadataOnly(c *gin.Context, state *forwardState) {
	req := &sdk.ForwardRequest{
		// Account 留空：插件对 metadata 路径的判断发生在访问 account 之前
		Account: &sdk.Account{Platform: state.requestedPlatform},
		Body:    state.body,
		Headers: buildHeaders(c.Request.Header, state.keyInfo),
		Model:   state.model,
		Stream:  false,
	}
	req.Headers.Set("X-Forwarded-Path", state.requestPath)
	req.Headers.Set("X-Forwarded-Method", c.Request.Method)

	outcome, err := state.plugin.Gateway.Forward(c.Request.Context(), req)
	if err != nil {
		slog.Error("metadata 请求插件失败", "plugin", state.plugin.Name, "path", state.requestPath, "error", err)
		openAIError(c, http.StatusBadGateway, "server_error", "upstream_error", "metadata 请求插件失败")
		return
	}
	if len(outcome.Upstream.Body) == 0 {
		openAIError(c, http.StatusBadGateway, "server_error", "upstream_error", "metadata 请求插件返回空响应")
		return
	}
	writeUpstream(c, outcome.Upstream)
}
