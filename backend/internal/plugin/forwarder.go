package plugin

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

	"github.com/DouDOU-start/airgate-core/ent"
	entaccount "github.com/DouDOU-start/airgate-core/ent/account"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/ratelimit"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
	sdk "github.com/DouDOU-start/airgate-sdk"
)

// openAIError 返回 OpenAI 兼容的错误格式，确保 Claude Code 等客户端能正确识别
func openAIError(c *gin.Context, status int, errType, code, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
			"code":    code,
		},
	})
}

// Forwarder 请求转发器
// 完整流程：认证 → 限流 → 余额预检 → 调度 → 并发控制 → 转发 → 计费 → 记录
type Forwarder struct {
	db          *ent.Client
	manager     *Manager
	scheduler   *scheduler.Scheduler
	concurrency *scheduler.ConcurrencyManager
	limiter     *ratelimit.Limiter
	calculator  *billing.Calculator
	priceMgr    *billing.PriceManager
	recorder    *billing.Recorder
}

// NewForwarder 创建转发器
func NewForwarder(
	db *ent.Client,
	manager *Manager,
	sched *scheduler.Scheduler,
	concurrency *scheduler.ConcurrencyManager,
	limiter *ratelimit.Limiter,
	calculator *billing.Calculator,
	priceMgr *billing.PriceManager,
	recorder *billing.Recorder,
) *Forwarder {
	return &Forwarder{
		db:          db,
		manager:     manager,
		scheduler:   sched,
		concurrency: concurrency,
		limiter:     limiter,
		calculator:  calculator,
		priceMgr:    priceMgr,
		recorder:    recorder,
	}
}

// Forward 转发请求到对应插件
func (f *Forwarder) Forward(c *gin.Context) {
	start := time.Now()

	// 1. 获取 API Key 认证信息
	keyInfoRaw, exists := c.Get(middleware.CtxKeyKeyInfo)
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"message": "未认证",
				"type":    "authentication_error",
				"code":    "missing_api_key",
			},
		})
		return
	}
	keyInfo := keyInfoRaw.(*auth.APIKeyInfo)

	// 2. 读取请求体
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		openAIError(c, http.StatusBadRequest, "invalid_request_error", "invalid_request", "读取请求体失败")
		return
	}

	// 3. 提取 model、stream 和 sessionID 字段
	model, stream := extractModelAndStream(body)
	sessionID := extractSessionID(body)

	// 4. 匹配插件
	requestPath := c.Param("path")
	if requestPath == "" {
		requestPath = c.Request.URL.Path
	}

	// Responses API 本身就是流式协议，强制设为流式
	if strings.HasSuffix(requestPath, "/responses") {
		stream = true
	}
	var inst *PluginInstance
	if keyInfo.GroupPlatform != "" {
		inst = f.manager.MatchPluginByPlatformAndPath(keyInfo.GroupPlatform, requestPath)
		if inst == nil {
			slog.Warn("分组平台未找到可处理请求的插件",
				"group_id", keyInfo.GroupID,
				"platform", keyInfo.GroupPlatform,
				"path", requestPath,
			)
			openAIError(c, http.StatusNotFound, "invalid_request_error", "route_not_found", "当前 API Key 绑定的平台不支持该 API 路径")
			return
		}
	} else {
		inst = f.manager.MatchPluginByPathPrefix(requestPath)
		if inst == nil {
			openAIError(c, http.StatusNotFound, "invalid_request_error", "route_not_found", "未找到匹配的插件")
			return
		}
	}

	// 5. 限流检查
	if err := f.limiter.Check(c.Request.Context(), keyInfo.UserID, inst.Platform); err != nil {
		openAIError(c, http.StatusTooManyRequests, "rate_limit_error", "rate_limit_exceeded", err.Error())
		return
	}

	// 6. 余额预检（使用认证时预加载的余额，无需额外 DB 查询）
	if keyInfo.UserBalance <= 0 {
		c.JSON(http.StatusPaymentRequired, gin.H{
			"error": gin.H{
				"message": "余额不足",
				"type":    "insufficient_quota",
				"code":    "insufficient_quota",
			},
		})
		return
	}

	// 7. 账户调度
	account, err := f.scheduler.SelectAccount(
		c.Request.Context(),
		inst.Platform,
		model,
		keyInfo.UserID,
		keyInfo.GroupID,
		sessionID,
	)
	if err != nil {
		slog.Warn("账户调度失败", "platform", inst.Platform, "model", model, "error", err)
		openAIError(c, http.StatusServiceUnavailable, "server_error", "no_available_account", "无可用账户")
		return
	}

	// 7.5 预递增 RPM（调度选中后立即计数，避免并发请求绕过限制）
	f.scheduler.IncrementRPM(c.Request.Context(), account.ID)

	// 8. 用户消息串行化 + 自适应延迟（在并发槽之前，避免等待期间占用并发槽）
	requestID := uuid.New().String()
	isRealMsg := scheduler.IsRealUserMessage(body)
	if isRealMsg {
		acquired, _ := f.scheduler.AcquireMessageLock(c.Request.Context(), account.ID, requestID, account.Extra)
		if acquired {
			defer f.scheduler.ReleaseMessageLock(c.Request.Context(), account.ID, requestID)
			f.scheduler.EnforceMessageDelay(c.Request.Context(), account.ID, account.Extra)
		}
	}

	// 8.5 并发控制（延迟完成后再占用并发槽）
	maxConc := account.MaxConcurrency
	if maxConc <= 0 {
		maxConc = 5
	}
	if err := f.concurrency.AcquireSlot(c.Request.Context(), account.ID, requestID, maxConc); err != nil {
		f.scheduler.DecrementRPM(c.Request.Context(), account.ID)
		openAIError(c, http.StatusTooManyRequests, "rate_limit_error", "concurrency_limit", "并发已满，请稍后重试")
		return
	}
	defer f.concurrency.ReleaseSlot(c.Request.Context(), account.ID, requestID)

	// 9. 构造 ForwardRequest 并调用插件
	// 获取代理 URL（使用调度时预加载的边关系，无需额外 DB 查询）
	proxyURL := ""
	if proxy, err := account.Edges.ProxyOrErr(); err == nil && proxy != nil {
		if proxy.Username != "" {
			proxyURL = fmt.Sprintf("%s://%s:%s@%s:%d", proxy.Protocol, proxy.Username, proxy.Password, proxy.Address, proxy.Port)
		} else {
			proxyURL = fmt.Sprintf("%s://%s:%d", proxy.Protocol, proxy.Address, proxy.Port)
		}
	}

	sdkAccount := &sdk.Account{
		ID:          int64(account.ID),
		Name:        account.Name,
		Platform:    account.Platform,
		Type:        account.Type,
		Credentials: account.Credentials,
		ProxyURL:    proxyURL,
	}

	forwardHeaders := c.Request.Header.Clone()
	if keyInfo.GroupServiceTier != "" {
		forwardHeaders.Set("X-Airgate-Service-Tier", keyInfo.GroupServiceTier)
	}

	fwdReq := &sdk.ForwardRequest{
		Account: sdkAccount,
		Body:    body,
		Headers: forwardHeaders,
		Model:   model,
		Stream:  stream,
	}

	// 流式请求：传入 Writer，由插件根据上游响应设置 SSE 响应头和状态码
	if stream {
		fwdReq.Writer = c.Writer
	}

	result, err := inst.Gateway.Forward(c.Request.Context(), fwdReq)
	duration := time.Since(start)

	// 10. 上报调度结果 + RPM 计数 + 会话刷新
	// 根据 AccountStatus 精确判定：
	//   - "rate_limited" (429)：不计入失败，不递增 RPM（上游未实际处理）
	//   - "expired"/"disabled" (401/403)：计入失败（账号凭证问题）
	//   - 5xx / 网络错误：计入失败
	//   - 正常 2xx：成功
	accountStatus := ""
	if result != nil {
		accountStatus = result.AccountStatus
	}
	isSuccess := err == nil && result != nil && result.StatusCode >= 200 && result.StatusCode < 400
	isRateLimited := accountStatus == "rate_limited"
	isAccountError := accountStatus == "expired" || accountStatus == "disabled"

	switch {
	case isSuccess:
		// RPM 已在调度后预递增，无需重复
		f.scheduler.ReportResult(account.ID, true, duration)
		f.scheduler.RefreshSession(c.Request.Context(), account.ID, sessionID, account.Extra)
	case isRateLimited:
		// 429 限流：上游未实际处理，回退 RPM 预递增
		f.scheduler.DecrementRPM(c.Request.Context(), account.ID)
		slog.Warn("上游限流", "account_id", account.ID, "retry_after", result.RetryAfter)
	case isAccountError:
		// 401/403 账号凭证问题：回退 RPM，立即标记为 error
		f.scheduler.DecrementRPM(c.Request.Context(), account.ID)
		reason := "账号凭证错误"
		if result != nil && result.ErrorMessage != "" {
			reason = result.ErrorMessage
		} else if err != nil {
			reason = err.Error()
		}
		f.scheduler.ReportAccountError(account.ID, reason)
	case err != nil:
		// 5xx / 网络错误：回退 RPM，计入失败
		f.scheduler.DecrementRPM(c.Request.Context(), account.ID)
		f.scheduler.ReportResult(account.ID, false, duration, err.Error())
	default:
		// 其他 4xx（400/404/422 等客户端错误）：回退 RPM，不计入账户失败
		f.scheduler.DecrementRPM(c.Request.Context(), account.ID)
	}

	// 10.5 插件回传了更新后的凭证（如 token 刷新），异步持久化到数据库
	if result != nil && len(result.UpdatedCredentials) > 0 {
		go f.updateAccountCredentials(account.ID, result.UpdatedCredentials)
	}

	if err != nil {
		slog.Error("插件转发失败", "plugin", inst.Name, "error", err)
		if !stream {
			openAIError(c, http.StatusBadGateway, "server_error", "upstream_error", "插件转发失败")
		}
		return
	}

	// 10.7 上游返回 4xx 错误：透传给客户端（plugin 已将 4xx 作为 result 返回而非 error）
	if result != nil && result.StatusCode >= 400 {
		if !stream && result.Body != nil {
			for k, vals := range result.Headers {
				for _, v := range vals {
					c.Writer.Header().Set(k, v)
				}
			}
			c.Writer.WriteHeader(result.StatusCode)
			_, _ = c.Writer.Write(result.Body)
		}
		return
	}

	// 11. 计费
	actualModel := result.Model
	if actualModel == "" {
		actualModel = model
	}

	// 分组倍率（使用认证时预加载的数据，无需额外 DB 查询）
	groupRate := keyInfo.GroupRateMultiplier
	if groupRate <= 0 {
		groupRate = 1.0
	}

	price, _ := f.priceMgr.GetPrice(inst.Platform, actualModel)
	calcResult := f.calculator.Calculate(billing.CalculateInput{
		InputTokens:           result.InputTokens,
		OutputTokens:          result.OutputTokens,
		CachedInputTokens:     result.CachedInputTokens,
		ServiceTier:           result.ServiceTier,
		Model:                 actualModel,
		Platform:              inst.Platform,
		GroupRateMultiplier:   groupRate,
		AccountRateMultiplier: account.RateMultiplier,
		UserRateMultiplier:    1.0,
	}, price)

	// 11.5 增量更新窗口费用缓存（避免调度器使用过期数据）
	f.scheduler.AddWindowCost(c.Request.Context(), account.ID, calcResult.ActualCost)

	// 12. 异步记录使用量并扣费（由 Recorder 统一处理）
	f.recorder.Record(billing.UsageRecord{
		UserID:                keyInfo.UserID,
		APIKeyID:              keyInfo.KeyID,
		AccountID:             account.ID,
		GroupID:               keyInfo.GroupID,
		Platform:              inst.Platform,
		Model:                 actualModel,
		InputTokens:           result.InputTokens,
		OutputTokens:          result.OutputTokens,
		CachedInputTokens:     result.CachedInputTokens,
		CacheTokens:           result.CachedInputTokens,
		ReasoningOutputTokens: result.ReasoningOutputTokens,
		InputCost:             calcResult.InputCost,
		OutputCost:            calcResult.OutputCost,
		CachedInputCost:       calcResult.CachedInputCost,
		CacheCost:             calcResult.CacheCost,
		TotalCost:             calcResult.TotalCost,
		ActualCost:            calcResult.ActualCost,
		RateMultiplier:        calcResult.RateMultiplier,
		AccountRateMultiplier: calcResult.AccountRateMultiplier,
		ServiceTier:           result.ServiceTier,
		Stream:                stream,
		DurationMs:            duration.Milliseconds(),
		UserAgent:             c.Request.UserAgent(),
		IPAddress:             c.ClientIP(),
	})

	// 14. 写入响应
	// 流式响应已通过 Writer 直接写入客户端
	// 非流式响应通过 ForwardResult.Body 返回，需要由 Core 写入
	if !stream && result.Body != nil {
		for k, vals := range result.Headers {
			for _, v := range vals {
				c.Writer.Header().Set(k, v)
			}
		}
		c.Writer.WriteHeader(result.StatusCode)
		_, _ = c.Writer.Write(result.Body)
	}
}

// requestFields 从 JSON body 中一次性解析需要的字段
type requestFields struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Metadata struct {
		UserID string `json:"user_id"`
	} `json:"metadata"`
}

// extractModelAndStream 从 JSON body 中提取 model 和 stream 字段
func extractModelAndStream(body []byte) (string, bool) {
	var parsed requestFields
	_ = json.Unmarshal(body, &parsed)
	return parsed.Model, parsed.Stream
}

// extractSessionID 从 JSON body 的 metadata.user_id 中提取会话 ID
func extractSessionID(body []byte) string {
	var parsed requestFields
	_ = json.Unmarshal(body, &parsed)
	return parsed.Metadata.UserID
}

// updateAccountCredentials 异步更新账号凭证（合并写入，保留未变更的字段）
func (f *Forwarder) updateAccountCredentials(accountID int, updated map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 读取当前凭证
	acc, err := f.db.Account.Query().Where(entaccount.ID(accountID)).Only(ctx)
	if err != nil {
		slog.Error("更新凭证失败：查询账号", "account_id", accountID, "error", err)
		return
	}

	// 合并：保留原有字段，覆盖更新字段
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
