package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"

	appaccount "github.com/DouDOU-start/airgate-core/internal/app/account"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// ListAccounts 查询账号列表。
func (h *AccountHandler) ListAccounts(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.List(c.Request.Context(), appaccount.ListFilter{
		Page:     page.Page,
		PageSize: page.PageSize,
		Keyword:  page.Keyword,
		Platform: c.Query("platform"),
		Status:   c.Query("status"),
		GroupID:  parseOptionalInt(c.Query("group_id")),
		ProxyID:  parseOptionalInt(c.Query("proxy_id")),
	})
	if err != nil {
		httpCode, message := h.handleError("查询账号列表失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	list := make([]dto.AccountResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, toAccountResp(item))
	}

	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// CreateAccount 创建账号。
func (h *AccountHandler) CreateAccount(c *gin.Context) {
	var req dto.CreateAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.Create(c.Request.Context(), appaccount.CreateInput{
		Name:           req.Name,
		Platform:       req.Platform,
		Type:           req.Type,
		Credentials:    req.Credentials,
		Priority:       req.Priority,
		MaxConcurrency: req.MaxConcurrency,
		ProxyID:        req.ProxyID,
		RateMultiplier: req.RateMultiplier,
		GroupIDs:       req.GroupIDs,
	})
	if err != nil {
		httpCode, message := h.handleError("创建账号失败", "创建失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toAccountResp(item))
}

// UpdateAccount 更新账号。
func (h *AccountHandler) UpdateAccount(c *gin.Context) {
	id, err := parseAccountID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}

	var req dto.UpdateAccountReq
	if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
		response.BindError(c, err)
		return
	}

	rawPayload, err := decodeRawJSONBody(c)
	if err != nil {
		response.BadRequest(c, "请求体格式错误")
		return
	}

	input := appaccount.UpdateInput{
		Name:           req.Name,
		Type:           req.Type,
		Credentials:    req.Credentials,
		Status:         req.Status,
		Priority:       req.Priority,
		MaxConcurrency: req.MaxConcurrency,
		RateMultiplier: req.RateMultiplier,
		GroupIDs:       req.GroupIDs,
		HasGroupIDs:    req.GroupIDs != nil,
	}
	if rawProxyID, ok := rawPayload["proxy_id"]; ok {
		input.HasProxyID = true
		if strings.TrimSpace(string(rawProxyID)) != "null" {
			input.ProxyID = req.ProxyID
		}
	}

	item, err := h.service.Update(c.Request.Context(), id, input)
	if err != nil {
		httpCode, message := h.handleError("更新账号失败", "更新失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toAccountResp(item))
}

// DeleteAccount 删除账号。
func (h *AccountHandler) DeleteAccount(c *gin.Context) {
	id, err := parseAccountID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}

	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		httpCode, message := h.handleError("删除账号失败", "删除失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, nil)
}

// ToggleScheduling 快速切换账号调度状态。
func (h *AccountHandler) ToggleScheduling(c *gin.Context) {
	id, err := parseAccountID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}

	result, err := h.service.ToggleScheduling(c.Request.Context(), id)
	if err != nil {
		httpCode, message := h.handleError("切换调度状态失败", "切换失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, map[string]any{
		"id":     result.ID,
		"status": result.Status,
	})
}

// TestAccount 测试账号连通性。
func (h *AccountHandler) TestAccount(c *gin.Context) {
	id, err := parseAccountID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}

	var req struct {
		ModelID string `json:"model_id"`
	}
	_ = c.ShouldBindJSON(&req)

	testPlan, err := h.service.PrepareConnectivityTest(c.Request.Context(), id, req.ModelID)
	if err != nil {
		httpCode, message := h.handleError("测试账号失败", "测试失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()

	sendSSEEvent(c.Writer, map[string]any{
		"type":         "test_start",
		"account":      testPlan.AccountName,
		"model":        testPlan.ModelID,
		"account_type": testPlan.AccountType,
	})

	if err := testPlan.Run(c.Request.Context(), c.Writer); err != nil {
		sendSSEEvent(c.Writer, map[string]any{
			"type":    "test_complete",
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	sendSSEEvent(c.Writer, map[string]any{
		"type":    "test_complete",
		"success": true,
	})
}

// GetAccountModels 获取账号所属平台模型列表。
func (h *AccountHandler) GetAccountModels(c *gin.Context) {
	id, err := parseAccountID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}

	models, err := h.service.GetModels(c.Request.Context(), id)
	if err != nil {
		httpCode, message := h.handleError("获取账号模型列表失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, models)
}

// GetAccountUsage 获取账号额度信息。
func (h *AccountHandler) GetAccountUsage(c *gin.Context) {
	usage, err := h.service.GetAccountUsage(c.Request.Context(), c.Query("platform"))
	if err != nil {
		httpCode, message := h.handleError("查询账号额度失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, map[string]any{"accounts": usage})
}

// GetCredentialsSchema 获取指定平台的凭证 schema。
func (h *AccountHandler) GetCredentialsSchema(c *gin.Context) {
	schema := h.service.GetCredentialsSchema(c.Param("platform"))
	response.Success(c, toCredentialSchemaResp(schema))
}

// RefreshQuota 手动刷新账号额度。
func (h *AccountHandler) RefreshQuota(c *gin.Context) {
	id, err := parseAccountID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}

	result, err := h.service.RefreshQuota(c.Request.Context(), id)
	if err != nil {
		httpCode, message := h.handleError("刷新账号额度失败", "刷新额度失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, gin.H{
		"plan_type":                 result.PlanType,
		"email":                     result.Email,
		"subscription_active_until": result.SubscriptionActiveUntil,
	})
}

// GetAccountStats 获取单个账号使用统计。
func (h *AccountHandler) GetAccountStats(c *gin.Context) {
	id, err := parseAccountID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}

	result, err := h.service.GetStats(c.Request.Context(), id, appaccount.StatsQuery{
		StartDate: c.Query("start_date"),
		EndDate:   c.Query("end_date"),
	})
	if err != nil {
		httpCode, message := h.handleError("查询账号统计失败", "查询统计失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, gin.H{
		"account_id":       result.AccountID,
		"name":             result.Name,
		"platform":         result.Platform,
		"status":           result.Status,
		"start_date":       result.StartDate,
		"end_date":         result.EndDate,
		"total_days":       result.TotalDays,
		"today":            result.Today,
		"range":            result.Range,
		"daily_trend":      result.DailyTrend,
		"models":           result.Models,
		"active_days":      result.ActiveDays,
		"avg_duration_ms":  result.AvgDurationMs,
		"peak_cost_day":    result.PeakCostDay,
		"peak_request_day": result.PeakRequestDay,
	})
}

func decodeRawJSONBody(c *gin.Context) (map[string]json.RawMessage, error) {
	var rawPayload map[string]json.RawMessage
	rawBody, ok := c.Get(gin.BodyBytesKey)
	if !ok {
		return rawPayload, nil
	}
	bodyBytes, ok := rawBody.([]byte)
	if !ok || len(bodyBytes) == 0 {
		return rawPayload, nil
	}
	if err := json.Unmarshal(bodyBytes, &rawPayload); err != nil {
		return nil, err
	}
	return rawPayload, nil
}

func sendSSEEvent(w http.ResponseWriter, data any) {
	body, _ := json.Marshal(data)
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(body)
	_, _ = w.Write([]byte("\n\n"))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}
