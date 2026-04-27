package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

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
		Page:        page.Page,
		PageSize:    page.PageSize,
		Keyword:     page.Keyword,
		Platform:    c.Query("platform"),
		State:       c.Query("state"),
		AccountType: c.Query("account_type"),
		GroupID:     parseOptionalInt(c.Query("group_id")),
		ProxyID:     parseOptionalInt(c.Query("proxy_id")),
	})
	if err != nil {
		httpCode, message := h.handleError("查询账号列表失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	list := make([]dto.AccountResp, 0, len(result.List))
	for _, item := range result.List {
		resp := toAccountResp(item)
		// 家族冷却落 Redis、不在 DB，handler 层叠加：N 次 Redis SCAN（每账号 0~3 个 key），
		// page_size=20 默认下额外 RTT 可忽略；scheduler 不可用时直接为空，不影响主响应。
		resp.FamilyCooldowns = h.familyCooldownsFor(c.Request.Context(), item.ID)
		list = append(list, resp)
	}

	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// ExportAccounts 按当前筛选条件导出账号（返回 JSON 数据，前端落盘为文件）。
// 当前端传入 ids=1,2,3 时只导出这些账号；否则按查询条件导出全部匹配的账号。
func (h *AccountHandler) ExportAccounts(c *gin.Context) {
	accounts, err := h.service.ExportAll(c.Request.Context(), appaccount.ListFilter{
		Keyword:     c.Query("keyword"),
		Platform:    c.Query("platform"),
		State:       c.Query("state"),
		AccountType: c.Query("account_type"),
		GroupID:     parseOptionalInt(c.Query("group_id")),
		ProxyID:     parseOptionalInt(c.Query("proxy_id")),
		IDs:         parseIDList(c.Query("ids")),
	})
	if err != nil {
		httpCode, message := h.handleError("导出账号失败", "导出失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	items := make([]dto.AccountExportItem, 0, len(accounts))
	for _, account := range accounts {
		items = append(items, toAccountExportItem(account))
	}

	response.Success(c, dto.AccountExportFile{
		Version:    1,
		ExportedAt: time.Now().In(beijingTZ).Format(time.RFC3339),
		Count:      len(items),
		Accounts:   items,
	})
}

// beijingTZ 北京时区（UTC+8），用于导出文件时间戳。
// LoadLocation 失败时回退为固定偏移，避免依赖系统 tzdata。
var beijingTZ = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return loc
	}
	return time.FixedZone("CST", 8*3600)
}()

// ImportAccounts 批量导入账号。
func (h *AccountHandler) ImportAccounts(c *gin.Context) {
	var req dto.ImportAccountsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	if len(req.Accounts) == 0 {
		response.BadRequest(c, "导入文件中没有账号数据")
		return
	}

	inputs := make([]appaccount.CreateInput, 0, len(req.Accounts))
	for _, item := range req.Accounts {
		inputs = append(inputs, appaccount.CreateInput{
			Name:           item.Name,
			Platform:       item.Platform,
			Type:           item.Type,
			Credentials:    item.Credentials,
			Priority:       item.Priority,
			MaxConcurrency: item.MaxConcurrency,
			RateMultiplier: item.RateMultiplier,
		})
	}

	summary := h.service.Import(c.Request.Context(), inputs)

	resp := dto.ImportAccountsResp{
		Imported: summary.Imported,
		Failed:   summary.Failed,
	}
	for _, e := range summary.Errors {
		resp.Errors = append(resp.Errors, dto.ImportItemErrorResp{
			Index:   e.Index,
			Name:    e.Name,
			Message: e.Message,
		})
	}
	response.Success(c, resp)
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
		UpstreamIsPool: req.UpstreamIsPool,
		GroupIDs:       req.GroupIDs,
	})
	if err != nil {
		httpCode, message := h.handleError("创建账号失败", "创建失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	resp := toAccountResp(item)
	resp.FamilyCooldowns = h.familyCooldownsFor(c.Request.Context(), item.ID)
	response.Success(c, resp)
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
		State:          req.State,
		Priority:       req.Priority,
		MaxConcurrency: req.MaxConcurrency,
		RateMultiplier: req.RateMultiplier,
		UpstreamIsPool: req.UpstreamIsPool,
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

	resp := toAccountResp(item)
	resp.FamilyCooldowns = h.familyCooldownsFor(c.Request.Context(), item.ID)
	response.Success(c, resp)
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

// BulkUpdateAccounts 批量更新账号字段（group_ids 为追加模式）。
func (h *AccountHandler) BulkUpdateAccounts(c *gin.Context) {
	var req dto.BulkUpdateAccountsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	result := h.service.BulkUpdate(c.Request.Context(), appaccount.BulkUpdateInput{
		IDs:            req.AccountIDs,
		State:          req.State,
		Priority:       req.Priority,
		MaxConcurrency: req.MaxConcurrency,
		RateMultiplier: req.RateMultiplier,
		GroupIDs:       req.GroupIDs,
		HasGroupIDs:    req.GroupIDs != nil,
		ProxyID:        req.ProxyID,
		HasProxyID:     req.ProxyID != nil,
	})
	response.Success(c, toBulkOpResp(result))
}

// BulkDeleteAccounts 批量删除账号。
func (h *AccountHandler) BulkDeleteAccounts(c *gin.Context) {
	var req dto.BulkAccountIDsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	result := h.service.BulkDelete(c.Request.Context(), req.AccountIDs)
	response.Success(c, toBulkOpResp(result))
}

// BulkRefreshQuota 批量刷新账号额度/令牌，使用 SSE 流式返回进度。
// 事件类型：
//   - {type:"start", total}
//   - {type:"progress", id, done, total, success, error?, plan_type?}
//   - {type:"complete", success, failed}
func (h *AccountHandler) BulkRefreshQuota(c *gin.Context) {
	var req dto.BulkAccountIDsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()

	total := len(req.AccountIDs)
	sendSSEEvent(c.Writer, map[string]any{"type": "start", "total": total})

	success, failed := 0, 0
	ctx := c.Request.Context()
	for i, id := range req.AccountIDs {
		if ctx.Err() != nil {
			// 客户端主动关闭，终止循环
			return
		}
		evt := map[string]any{
			"type":  "progress",
			"id":    id,
			"done":  i + 1,
			"total": total,
		}
		result, err := h.service.RefreshQuota(ctx, id)
		if err != nil {
			failed++
			evt["success"] = false
			evt["error"] = err.Error()
			if errors.Is(err, appaccount.ErrReauthRequired) {
				evt["reauth_required"] = true
			}
		} else {
			success++
			evt["success"] = true
			if result.PlanType != "" {
				evt["plan_type"] = result.PlanType
			}
			if result.ReauthWarning != "" {
				evt["reauth_warning"] = result.ReauthWarning
			}
		}
		sendSSEEvent(c.Writer, evt)
	}

	sendSSEEvent(c.Writer, map[string]any{
		"type":    "complete",
		"success": success,
		"failed":  failed,
	})
}

func toBulkOpResp(r appaccount.BulkResult) dto.BulkOpResp {
	items := make([]dto.BulkOpItemResp, 0, len(r.Results))
	for _, item := range r.Results {
		items = append(items, dto.BulkOpItemResp{
			ID:      item.ID,
			Success: item.Success,
			Error:   item.Error,
		})
	}
	successIDs := r.SuccessIDs
	if successIDs == nil {
		successIDs = []int{}
	}
	failedIDs := r.FailedIDs
	if failedIDs == nil {
		failedIDs = []int{}
	}
	return dto.BulkOpResp{
		Success:    r.Success,
		Failed:     r.Failed,
		SuccessIDs: successIDs,
		FailedIDs:  failedIDs,
		Results:    items,
	}
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
		"id":    result.ID,
		"state": result.State,
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

	resp := gin.H{
		"plan_type":                 result.PlanType,
		"email":                     result.Email,
		"subscription_active_until": result.SubscriptionActiveUntil,
	}
	if result.ReauthWarning != "" {
		resp["reauth_warning"] = result.ReauthWarning
	}
	response.Success(c, resp)
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
		TZ:        c.Query("tz"),
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
		"state":            result.State,
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
