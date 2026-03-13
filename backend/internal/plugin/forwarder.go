package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/ratelimit"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
	sdk "github.com/DouDOU-start/airgate-sdk"
)

// Forwarder 请求转发器
// 完整流程：认证 → 限流 → 余额预检 → 调度 → 并发控制 → 转发 → 计费 → 记录
type Forwarder struct {
	manager     *Manager
	db          *ent.Client
	scheduler   *scheduler.Scheduler
	concurrency *scheduler.ConcurrencyManager
	limiter     *ratelimit.Limiter
	calculator  *billing.Calculator
	priceMgr    *billing.PriceManager
	recorder    *billing.Recorder
}

// NewForwarder 创建转发器
func NewForwarder(
	manager *Manager,
	db *ent.Client,
	sched *scheduler.Scheduler,
	concurrency *scheduler.ConcurrencyManager,
	limiter *ratelimit.Limiter,
	calculator *billing.Calculator,
	priceMgr *billing.PriceManager,
	recorder *billing.Recorder,
) *Forwarder {
	return &Forwarder{
		manager:     manager,
		db:          db,
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
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未认证"})
		return
	}
	keyInfo := keyInfoRaw.(*auth.APIKeyInfo)

	// 2. 读取请求体
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求体失败"})
		return
	}

	// 3. 提取 model 和 stream 字段
	model, stream := extractModelAndStream(body)

	// 4. 匹配插件
	requestPath := c.Param("path")
	if requestPath == "" {
		requestPath = c.Request.URL.Path
	}
	inst := f.manager.MatchPluginByPathPrefix(requestPath)
	if inst == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到匹配的插件"})
		return
	}

	// 5. 限流检查
	if err := f.limiter.Check(c.Request.Context(), keyInfo.UserID, inst.Platform); err != nil {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
		return
	}

	// 6. 余额预检（balance > 0）
	user, err := f.db.User.Get(c.Request.Context(), keyInfo.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用户失败"})
		return
	}
	if user.Balance <= 0 {
		c.JSON(http.StatusPaymentRequired, gin.H{"error": "余额不足"})
		return
	}

	// 7. 账户调度
	account, err := f.scheduler.SelectAccount(
		c.Request.Context(),
		inst.Platform,
		model,
		keyInfo.UserID,
		keyInfo.GroupID,
		"", // sessionID 暂不使用
	)
	if err != nil {
		slog.Warn("账户调度失败", "platform", inst.Platform, "model", model, "error", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "无可用账户"})
		return
	}

	// 8. 并发控制
	requestID := uuid.New().String()
	maxConc := account.MaxConcurrency
	if maxConc <= 0 {
		maxConc = 5
	}
	if err := f.concurrency.AcquireSlot(c.Request.Context(), account.ID, requestID, maxConc); err != nil {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "并发已满，请稍后重试"})
		return
	}
	defer f.concurrency.ReleaseSlot(c.Request.Context(), account.ID, requestID)

	// 9. 构造 ForwardRequest 并调用插件
	// 获取代理 URL（如果有关联代理）
	proxyURL := ""
	if proxy, err := account.QueryProxy().Only(c.Request.Context()); err == nil && proxy != nil {
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

	fwdReq := &sdk.ForwardRequest{
		Account: sdkAccount,
		Body:    body,
		Headers: c.Request.Header,
		Model:   model,
		Stream:  stream,
	}

	// 流式请求需要设置 Writer
	if stream {
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		fwdReq.Writer = c.Writer
	}

	result, err := inst.Gateway.Forward(c.Request.Context(), fwdReq)
	duration := time.Since(start)

	// 10. 上报调度结果
	f.scheduler.ReportResult(account.ID, err == nil && result != nil && result.StatusCode < 500, duration)

	if err != nil {
		slog.Error("插件转发失败", "plugin", inst.Name, "error", err)
		if !stream {
			c.JSON(http.StatusBadGateway, gin.H{"error": "插件转发失败"})
		}
		return
	}

	// 11. 计费
	actualModel := result.Model
	if actualModel == "" {
		actualModel = model
	}

	// 获取分组倍率
	group, _ := f.db.Group.Get(c.Request.Context(), keyInfo.GroupID)
	groupRate := 1.0
	if group != nil {
		groupRate = group.RateMultiplier
	}

	price, _ := f.priceMgr.GetPrice(inst.Platform, actualModel)
	calcResult := f.calculator.Calculate(billing.CalculateInput{
		InputTokens:           result.InputTokens,
		OutputTokens:          result.OutputTokens,
		CacheTokens:           result.CacheTokens,
		Model:                 actualModel,
		Platform:              inst.Platform,
		GroupRateMultiplier:   groupRate,
		AccountRateMultiplier: account.RateMultiplier,
		UserRateMultiplier:    1.0,
	}, price)

	// 12. 扣减余额
	if calcResult.ActualCost > 0 {
		_ = f.db.User.UpdateOneID(keyInfo.UserID).
			AddBalance(-calcResult.ActualCost).
			Exec(c.Request.Context())

		// 更新 API Key 使用量
		_ = f.db.APIKey.UpdateOneID(keyInfo.KeyID).
			AddUsedQuota(calcResult.ActualCost).
			Exec(c.Request.Context())
	}

	// 13. 异步记录使用量
	f.recorder.Record(billing.UsageRecord{
		UserID:                keyInfo.UserID,
		APIKeyID:              keyInfo.KeyID,
		AccountID:             account.ID,
		GroupID:               keyInfo.GroupID,
		Platform:              inst.Platform,
		Model:                 actualModel,
		InputTokens:           result.InputTokens,
		OutputTokens:          result.OutputTokens,
		CacheTokens:           result.CacheTokens,
		InputCost:             calcResult.InputCost,
		OutputCost:            calcResult.OutputCost,
		CacheCost:             calcResult.CacheCost,
		TotalCost:             calcResult.TotalCost,
		ActualCost:            calcResult.ActualCost,
		RateMultiplier:        calcResult.RateMultiplier,
		AccountRateMultiplier: calcResult.AccountRateMultiplier,
		Stream:                stream,
		DurationMs:            duration.Milliseconds(),
		UserAgent:             c.Request.UserAgent(),
		IPAddress:             c.ClientIP(),
	})

	// 14. 非流式响应已由插件 gRPC 返回，这里无需再写入
	// 流式响应已在 ForwardStream 中直接写入 Writer
	if !stream && result.StatusCode > 0 {
		// 非流式：gRPC Forward 已返回 body，但当前 SDK 设计中
		// 非流式响应通过 gRPC 一次性返回，响应体也已写入
		// 此处只需确保状态码正确
		c.Status(result.StatusCode)
	}
}

// extractModelAndStream 从 JSON body 中提取 model 和 stream 字段
func extractModelAndStream(body []byte) (string, bool) {
	var parsed struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(body, &parsed)
	return parsed.Model, parsed.Stream
}
