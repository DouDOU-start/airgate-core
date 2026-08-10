package handler

import (
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"

	appaccount "github.com/DouDOU-start/airgate-core/internal/app/account"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// ListAccounts 查询账号列表（凭证已脱敏）。
func (h *AccountHandler) ListAccounts(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}

	groupID := parseOptionalInt(c.Query("group_id"))
	ungrouped := groupID == nil && parseOptionalBool(c.Query("ungrouped"))

	result, err := h.service.List(c.Request.Context(), appaccount.ListFilter{
		Page:        page.Page,
		PageSize:    page.PageSize,
		Keyword:     page.Keyword,
		Platform:    c.Query("platform"),
		State:       c.Query("state"),
		AccountType: c.Query("account_type"),
		GroupID:     groupID,
		Ungrouped:   ungrouped,
		ProxyID:     parseOptionalInt(c.Query("proxy_id")),
		SortBy:      c.Query("sort_by"),
		SortOrder:   c.Query("sort_order"),
		TZ:          c.Query("tz"),
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
		Weight:         req.Weight,
		MaxConcurrency: req.MaxConcurrency,
		ProxyID:        req.ProxyID,
		RateMultiplier: req.RateMultiplier,
		Extra:          req.Extra,
		GroupIDs:       intSliceToInt64(req.GroupIDs),
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
	id, err := ParseID(c.Param("id"))
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
		Weight:         req.Weight,
		MaxConcurrency: req.MaxConcurrency,
		RateMultiplier: req.RateMultiplier,
		GroupIDs:       intSliceToInt64(req.GroupIDs),
		HasGroupIDs:    req.GroupIDs != nil,
		Models:         req.Models,
		ModelMapping:   req.ModelMapping,
	}
	if _, ok := rawPayload["extra"]; ok {
		input.HasExtra = true
		input.Extra = req.Extra
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
	id, err := ParseID(c.Param("id"))
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

// ExportAccounts 导出账号（明文 credentials；仅记 count 审计日志）。
func (h *AccountHandler) ExportAccounts(c *gin.Context) {
	groupID := parseOptionalInt(c.Query("group_id"))
	ungrouped := groupID == nil && parseOptionalBool(c.Query("ungrouped"))

	accounts, err := h.service.ExportAll(c.Request.Context(), appaccount.ListFilter{
		Keyword:     c.Query("keyword"),
		Platform:    c.Query("platform"),
		State:       c.Query("state"),
		AccountType: c.Query("account_type"),
		GroupID:     groupID,
		Ungrouped:   ungrouped,
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

	// 审计：只记 count，不落 token
	slog.Info("account_export", "count", len(items))

	response.Success(c, dto.AccountExportFile{
		Version:    1,
		ExportedAt: time.Now().In(exportTZ()).Format(time.RFC3339),
		Count:      len(items),
		Accounts:   items,
	})
}

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
		inputs = append(inputs, toAccountImportInput(item))
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

// ToggleScheduling 快速切换账号调度状态。
func (h *AccountHandler) ToggleScheduling(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
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

// BulkUpdateAccounts 批量更新账号字段。
func (h *AccountHandler) BulkUpdateAccounts(c *gin.Context) {
	var req dto.BulkUpdateAccountsReq
	if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
		response.BindError(c, err)
		return
	}

	rawPayload, err := decodeRawJSONBody(c)
	if err != nil {
		response.BadRequest(c, "请求体格式错误")
		return
	}

	input := appaccount.BulkUpdateInput{
		IDs:            req.AccountIDs,
		State:          req.State,
		Priority:       req.Priority,
		Weight:         req.Weight,
		MaxConcurrency: req.MaxConcurrency,
		RateMultiplier: req.RateMultiplier,
		GroupIDs:       intSliceToInt64(req.GroupIDs),
		HasGroupIDs:    req.GroupIDs != nil,
		Models:         req.Models,
		ModelMapping:   req.ModelMapping,
	}
	if rawProxyID, ok := rawPayload["proxy_id"]; ok {
		input.HasProxyID = true
		if strings.TrimSpace(string(rawProxyID)) != "null" {
			input.ProxyID = req.ProxyID
		}
	}

	result := h.service.BulkUpdate(c.Request.Context(), input)
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

// GetCredentialsSchema 返回平台凭证字段 schema（service 内置 codex/xai 等）。
func (h *AccountHandler) GetCredentialsSchema(c *gin.Context) {
	schema := h.service.GetCredentialsSchema(c.Param("platform"))
	response.Success(c, toCredentialSchemaResp(schema))
}

// ListAccountPlatforms 返回内置支持的账号平台列表。
func (h *AccountHandler) ListAccountPlatforms(c *gin.Context) {
	response.Success(c, h.service.ListPlatforms())
}

// GetOAuthHints 返回平台 OAuth 登录说明（回调端口/流程类型）。
func (h *AccountHandler) GetOAuthHints(c *gin.Context) {
	response.Success(c, appaccount.OAuthLoginHints(c.Param("platform")))
}

// StartOAuth 交互式发起 OAuth：立即返回授权链接/设备码，前端展示后由用户完成。
func (h *AccountHandler) StartOAuth(c *gin.Context) {
	platform := c.Param("platform")
	var req dto.StartOAuthReq
	if err := c.ShouldBindJSON(&req); err != nil {
		// body 可为空
		req = dto.StartOAuthReq{}
	}
	session, err := h.service.StartOAuth(c.Request.Context(), appaccount.OAuthStartInput{
		Platform:       platform,
		Name:           req.Name,
		ProxyURL:       req.ProxyURL,
		ProxyID:        req.ProxyID,
		GroupIDs:       intSliceToInt64(req.GroupIDs),
		Priority:       req.Priority,
		Weight:         req.Weight,
		MaxConcurrency: req.MaxConcurrency,
		RateMultiplier: req.RateMultiplier,
		ProjectID:      req.ProjectID,
		Mode:           req.Mode,
		AccountID:      req.AccountID,
	})
	if err != nil {
		httpCode, message := h.handleError("发起 OAuth 失败", "发起失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toOAuthSessionResp(session))
}

// RefreshAccountUsage 主动查询上游用量窗口（Codex /wham/usage、Claude /api/oauth/usage）。
func (h *AccountHandler) RefreshAccountUsage(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}
	_, item, err := h.service.RefreshUsage(c.Request.Context(), id)
	if err != nil {
		httpCode, message := h.handleError("刷新账号用量失败", "刷新用量失败", err)
		// 业务不可用类错误直接回上游信息
		if httpCode == 400 {
			response.Error(c, httpCode, httpCode, err.Error())
			return
		}
		if httpCode == 404 {
			response.Error(c, httpCode, httpCode, message)
			return
		}
		// 上游 502 场景：保留错误文案
		response.Error(c, 502, 502, err.Error())
		return
	}
	response.Success(c, toAccountResp(item))
}

// ConsumeAccountUsageReset 消费 Codex 限额重置积分（对齐 CPA / airgate-openai）。
func (h *AccountHandler) ConsumeAccountUsageReset(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}
	var req dto.ConsumeUsageResetReq
	_ = c.ShouldBindJSON(&req) // body 可空

	result, err := h.service.ConsumeUsageReset(c.Request.Context(), id, req.CreditID)
	if err != nil {
		httpCode, _ := h.handleError("重置账号额度失败", "重置失败", err)
		if httpCode == 400 {
			response.Error(c, httpCode, httpCode, err.Error())
			return
		}
		if httpCode == 404 {
			response.Error(c, 404, 404, err.Error())
			return
		}
		response.Error(c, 502, 502, err.Error())
		return
	}
	acc := toAccountResp(result.Account)
	resp := dto.ConsumeUsageResetResp{
		Code:         result.Code,
		WindowsReset: result.WindowsReset,
		Account:      acc,
		Usage:        acc.Usage,
	}
	response.Success(c, resp)
}

// GetAccountUsageStats 账号近 N 天使用统计（对齐 sub2api GET /accounts/:id/stats）。
func (h *AccountHandler) GetAccountUsageStats(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}
	days := 30
	if raw := strings.TrimSpace(c.Query("days")); raw != "" {
		if n, e := strconv.Atoi(raw); e == nil {
			days = n
		}
	}
	stats, err := h.service.GetUsageStats(c.Request.Context(), id, days)
	if err != nil {
		httpCode, message := h.handleError("查询账号统计失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toAccountUsageStatsResp(stats))
}

// ListAccountTestModels 连通性测试可选模型。
func (h *AccountHandler) ListAccountTestModels(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}
	models, err := h.service.AvailableTestModels(c.Request.Context(), id)
	if err != nil {
		httpCode, message := h.handleError("查询测试模型失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	out := make([]dto.AccountTestModelResp, 0, len(models))
	for _, m := range models {
		out = append(out, dto.AccountTestModelResp{
			ID:            m.ID,
			DisplayName:   m.DisplayName,
			Kind:          m.Kind,
			DefaultPrompt: m.DefaultPrompt,
		})
	}
	response.Success(c, out)
}

// TestAccountConnection 账号连通性测试（SSE，对齐 sub2api POST /accounts/:id/test）。
func (h *AccountHandler) TestAccountConnection(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}
	var req dto.AccountTestReq
	_ = c.ShouldBindJSON(&req)

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()

	emit := func(ev appaccount.TestEvent) {
		raw, _ := json.Marshal(ev)
		_, _ = c.Writer.Write([]byte("data: " + string(raw) + "\n\n"))
		if f, ok := c.Writer.(interface{ Flush() }); ok {
			f.Flush()
		}
	}
	_ = h.service.TestConnection(c.Request.Context(), id, req.ModelID, req.Prompt, appaccount.TestOptions{
		Mode: appaccount.TestMode(req.TestMode),
		Media: appaccount.TestMediaOptions{
			Duration:    req.Duration,
			AspectRatio: req.AspectRatio,
			Resolution:  req.Resolution,
		},
	}, emit)
}

// ImportCodexRefresh Codex Refresh Token 导入（对齐 airgate-openai import-refresh）。
func (h *AccountHandler) ImportCodexRefresh(c *gin.Context) {
	var req dto.CodexImportRefreshReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	item, err := h.service.ImportCodexRefresh(c.Request.Context(), appaccount.OAuthStartInput{
		Name:           req.Name,
		ProxyURL:       req.ProxyURL,
		ProxyID:        req.ProxyID,
		GroupIDs:       intSliceToInt64(req.GroupIDs),
		Priority:       req.Priority,
		Weight:         req.Weight,
		MaxConcurrency: req.MaxConcurrency,
		RateMultiplier: req.RateMultiplier,
		AccountID:      req.AccountID,
	}, req.RefreshToken, req.ClientID)
	if err != nil {
		// 上游 token 端点原文（含 JSON body）必须回给管理员，不能收成泛化「导入失败」
		httpCode, message := h.handleImportError("Codex RT 导入失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toAccountResp(item))
}

// ImportAntigravityRefresh Antigravity Refresh Token 导入。
func (h *AccountHandler) ImportAntigravityRefresh(c *gin.Context) {
	var req dto.AntigravityImportRefreshReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	item, err := h.service.ImportAntigravityRefresh(c.Request.Context(), appaccount.OAuthStartInput{
		Name:           req.Name,
		ProxyURL:       req.ProxyURL,
		ProxyID:        req.ProxyID,
		GroupIDs:       intSliceToInt64(req.GroupIDs),
		Priority:       req.Priority,
		Weight:         req.Weight,
		MaxConcurrency: req.MaxConcurrency,
		RateMultiplier: req.RateMultiplier,
		AccountID:      req.AccountID,
	}, req.RefreshToken)
	if err != nil {
		httpCode, message := h.handleImportError("Antigravity RT 导入失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toAccountResp(item))
}

// ImportCodexSession Codex Session 导入（对齐 airgate-openai import-session）。
func (h *AccountHandler) ImportCodexSession(c *gin.Context) {
	var req dto.CodexImportSessionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	item, err := h.service.ImportCodexSession(c.Request.Context(), appaccount.OAuthStartInput{
		Name:           req.Name,
		ProxyURL:       req.ProxyURL,
		ProxyID:        req.ProxyID,
		GroupIDs:       intSliceToInt64(req.GroupIDs),
		Priority:       req.Priority,
		Weight:         req.Weight,
		MaxConcurrency: req.MaxConcurrency,
		RateMultiplier: req.RateMultiplier,
		AccountID:      req.AccountID,
	}, req.Session)
	if err != nil {
		httpCode, message := h.handleImportError("Codex Session 导入失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toAccountResp(item))
}

// GetOAuthSession 查询 OAuth 会话状态。
func (h *AccountHandler) GetOAuthSession(c *gin.Context) {
	session, err := h.service.GetOAuthSession(c.Param("sessionId"))
	if err != nil {
		response.Error(c, 404, 404, err.Error())
		return
	}
	response.Success(c, toOAuthSessionResp(session))
}

// CompleteOAuth 粘贴 authorization code 完成 Claude 等 paste_code 流程。
func (h *AccountHandler) CompleteOAuth(c *gin.Context) {
	var req dto.CompleteOAuthReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	session, err := h.service.CompleteOAuth(c.Request.Context(), c.Param("sessionId"), appaccount.OAuthCompleteInput{
		Code: req.Code,
	})
	if err != nil {
		// 会话可能已标记 failed，仍返回会话体
		if session.ID != "" {
			response.Error(c, 400, 400, err.Error())
			return
		}
		httpCode, message := h.handleError("完成 OAuth 失败", "完成失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toOAuthSessionResp(session))
}

// decodeRawJSONBody 从已缓存的请求体解析原始 JSON map（配合 ShouldBindBodyWith）。
func decodeRawJSONBody(c *gin.Context) (map[string]json.RawMessage, error) {
	var raw map[string]json.RawMessage
	if err := c.ShouldBindBodyWith(&raw, binding.JSON); err != nil {
		return nil, err
	}
	return raw, nil
}

// exportTZ 导出时间戳时区（北京时间）。
func exportTZ() *time.Location {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return loc
	}
	return time.FixedZone("CST", 8*3600)
}
