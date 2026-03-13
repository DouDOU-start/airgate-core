package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	sdk "github.com/DouDOU-start/airgate-sdk"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/account"
	"github.com/DouDOU-start/airgate-core/internal/plugin"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// AccountHandler 上游账号管理 Handler
type AccountHandler struct {
	db          *ent.Client
	pluginMgr   *plugin.Manager
	concurrency *scheduler.ConcurrencyManager
}

// NewAccountHandler 创建 AccountHandler
func NewAccountHandler(db *ent.Client, pluginMgr *plugin.Manager, concurrency *scheduler.ConcurrencyManager) *AccountHandler {
	return &AccountHandler{db: db, pluginMgr: pluginMgr, concurrency: concurrency}
}

// ListAccounts 查询账号列表（支持分页、平台/状态筛选）
func (h *AccountHandler) ListAccounts(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	query := h.db.Account.Query()

	// 关键词搜索
	if page.Keyword != "" {
		query = query.Where(account.NameContains(page.Keyword))
	}

	// 平台筛选
	if platform := c.Query("platform"); platform != "" {
		query = query.Where(account.PlatformEQ(platform))
	}

	// 状态筛选
	if status := c.Query("status"); status != "" {
		query = query.Where(account.StatusEQ(account.Status(status)))
	}

	// 总数
	total, err := query.Count(c.Request.Context())
	if err != nil {
		slog.Error("查询账号总数失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	// 分页查询，加载关联的分组和代理
	accounts, err := query.
		WithGroups().
		WithProxy().
		Offset((page.Page - 1) * page.PageSize).
		Limit(page.PageSize).
		Order(ent.Desc(account.FieldCreatedAt)).
		All(c.Request.Context())
	if err != nil {
		slog.Error("查询账号列表失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	// 批量获取当前并发数
	accountIDs := make([]int, len(accounts))
	for i, a := range accounts {
		accountIDs[i] = a.ID
	}
	concurrencyCounts := h.concurrency.GetCurrentCounts(c.Request.Context(), accountIDs)

	list := make([]dto.AccountResp, 0, len(accounts))
	for _, a := range accounts {
		resp := toAccountResp(a)
		resp.CurrentConcurrency = concurrencyCounts[a.ID]
		list = append(list, resp)
	}

	response.Success(c, response.PagedData(list, int64(total), page.Page, page.PageSize))
}

// CreateAccount 创建账号
func (h *AccountHandler) CreateAccount(c *gin.Context) {
	var req dto.CreateAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	builder := h.db.Account.Create().
		SetName(req.Name).
		SetPlatform(req.Platform).
		SetType(req.Type).
		SetCredentials(req.Credentials).
		SetPriority(req.Priority).
		SetMaxConcurrency(req.MaxConcurrency).
		SetRateMultiplier(req.RateMultiplier)

	// 关联分组
	if len(req.GroupIDs) > 0 {
		ids := make([]int, len(req.GroupIDs))
		for i, id := range req.GroupIDs {
			ids[i] = int(id)
		}
		builder = builder.AddGroupIDs(ids...)
	}

	// 关联代理
	if req.ProxyID != nil {
		builder = builder.SetProxyID(int(*req.ProxyID))
	}

	a, err := builder.Save(c.Request.Context())
	if err != nil {
		slog.Error("创建账号失败", "error", err)
		response.InternalError(c, "创建失败")
		return
	}

	// 重新加载关联数据
	a, err = h.db.Account.Query().
		Where(account.IDEQ(a.ID)).
		WithGroups().
		WithProxy().
		Only(c.Request.Context())
	if err != nil {
		slog.Error("加载账号关联数据失败", "error", err)
		response.InternalError(c, "创建成功但加载关联数据失败")
		return
	}

	response.Success(c, toAccountResp(a))
}

// UpdateAccount 更新账号
func (h *AccountHandler) UpdateAccount(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}

	var req dto.UpdateAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	builder := h.db.Account.UpdateOneID(id)

	if req.Name != nil {
		builder = builder.SetName(*req.Name)
	}
	if req.Type != nil {
		builder = builder.SetType(*req.Type)
	}
	if req.Credentials != nil {
		builder = builder.SetCredentials(req.Credentials)
	}
	if req.Status != nil {
		builder = builder.SetStatus(account.Status(*req.Status))
	}
	if req.Priority != nil {
		builder = builder.SetPriority(*req.Priority)
	}
	if req.MaxConcurrency != nil {
		builder = builder.SetMaxConcurrency(*req.MaxConcurrency)
	}
	if req.RateMultiplier != nil {
		builder = builder.SetRateMultiplier(*req.RateMultiplier)
	}

	// 更新分组关联（先清除再添加）
	if req.GroupIDs != nil {
		builder = builder.ClearGroups()
		if len(req.GroupIDs) > 0 {
			ids := make([]int, len(req.GroupIDs))
			for i, gid := range req.GroupIDs {
				ids[i] = int(gid)
			}
			builder = builder.AddGroupIDs(ids...)
		}
	}

	// 更新代理关联
	if req.ProxyID != nil {
		builder = builder.ClearProxy().SetProxyID(int(*req.ProxyID))
	}

	a, err := builder.Save(c.Request.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			response.NotFound(c, "账号不存在")
			return
		}
		slog.Error("更新账号失败", "error", err)
		response.InternalError(c, "更新失败")
		return
	}

	// 重新加载关联数据
	a, err = h.db.Account.Query().
		Where(account.IDEQ(a.ID)).
		WithGroups().
		WithProxy().
		Only(c.Request.Context())
	if err != nil {
		slog.Error("加载账号关联数据失败", "error", err)
		response.InternalError(c, "更新成功但加载关联数据失败")
		return
	}

	response.Success(c, toAccountResp(a))
}

// DeleteAccount 删除账号
func (h *AccountHandler) DeleteAccount(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}

	if err := h.db.Account.DeleteOneID(id).Exec(c.Request.Context()); err != nil {
		if ent.IsNotFound(err) {
			response.NotFound(c, "账号不存在")
			return
		}
		slog.Error("删除账号失败", "error", err)
		response.InternalError(c, "删除失败")
		return
	}

	response.Success(c, nil)
}

// ToggleScheduling 快速切换账号的调度状态（active ↔ disabled）
// PATCH /api/v1/admin/accounts/:id/toggle
func (h *AccountHandler) ToggleScheduling(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}

	a, err := h.db.Account.Get(c.Request.Context(), id)
	if err != nil {
		if ent.IsNotFound(err) {
			response.NotFound(c, "账号不存在")
			return
		}
		slog.Error("查询账号失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	// active → disabled，其他（disabled/error）→ active
	newStatus := account.StatusDisabled
	if a.Status != account.StatusActive {
		newStatus = account.StatusActive
	}

	if err := h.db.Account.UpdateOneID(id).
		SetStatus(newStatus).
		Exec(c.Request.Context()); err != nil {
		slog.Error("切换调度状态失败", "error", err)
		response.InternalError(c, "切换失败")
		return
	}

	response.Success(c, map[string]any{
		"id":     id,
		"status": string(newStatus),
	})
}

// TestAccount 测试账号连通性（SSE 流式）
// POST /api/v1/admin/accounts/:id/test
// 请求体: { "model_id": "gpt-4o" }
// 响应: SSE 流，包含 test_start / 插件原始 SSE / test_complete 事件
func (h *AccountHandler) TestAccount(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}

	var req struct {
		ModelID string `json:"model_id"`
	}
	_ = c.ShouldBindJSON(&req)

	// 查询账号（加载代理关联）
	a, err := h.db.Account.Query().
		Where(account.IDEQ(id)).
		WithProxy().
		Only(c.Request.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			response.NotFound(c, "账号不存在")
			return
		}
		slog.Error("查询账号失败", "error", err)
		response.InternalError(c, "测试失败")
		return
	}

	// 查找对应平台的插件
	inst := h.pluginMgr.GetPluginByPlatform(a.Platform)
	if inst == nil {
		response.InternalError(c, "未找到平台 "+a.Platform+" 对应的插件")
		return
	}

	// 模型 ID 默认取平台第一个模型
	modelID := req.ModelID
	if modelID == "" {
		if models := h.pluginMgr.GetModels(a.Platform); len(models) > 0 {
			modelID = models[0].ID
		}
	}
	if modelID == "" {
		response.BadRequest(c, "请指定测试模型")
		return
	}

	// 设置 SSE 响应头
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()

	// 发送 test_start 事件
	sendSSEEvent(c.Writer, map[string]any{
		"type":         "test_start",
		"account":      a.Name,
		"model":        modelID,
		"account_type": a.Type,
	})

	// 构造凭证和代理
	creds := make(map[string]string, len(a.Credentials))
	for k, v := range a.Credentials {
		creds[k] = fmt.Sprintf("%v", v)
	}

	proxyURL := ""
	if proxy := a.Edges.Proxy; proxy != nil {
		if proxy.Username != "" {
			proxyURL = fmt.Sprintf("%s://%s:%s@%s:%d", proxy.Protocol, proxy.Username, proxy.Password, proxy.Address, proxy.Port)
		} else {
			proxyURL = fmt.Sprintf("%s://%s:%d", proxy.Protocol, proxy.Address, proxy.Port)
		}
	}

	sdkAccount := &sdk.Account{
		ID:          int64(a.ID),
		Name:        a.Name,
		Platform:    a.Platform,
		Type:        a.Type,
		Credentials: creds,
		ProxyURL:    proxyURL,
	}

	// 构造最小测试请求体（OpenAI 兼容格式）
	testBody, _ := json.Marshal(map[string]any{
		"model":    modelID,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
		"stream":   true,
	})

	fwdReq := &sdk.ForwardRequest{
		Account: sdkAccount,
		Body:    testBody,
		Headers: http.Header{"Content-Type": {"application/json"}},
		Model:   modelID,
		Stream:  true,
		Writer:  c.Writer,
	}

	// 调用 Forward，流式写入上游 SSE 数据
	_, err = inst.Gateway.Forward(c.Request.Context(), fwdReq)

	// 发送 test_complete 事件
	if err != nil {
		sendSSEEvent(c.Writer, map[string]any{
			"type":    "test_complete",
			"success": false,
			"error":   err.Error(),
		})
	} else {
		sendSSEEvent(c.Writer, map[string]any{
			"type":    "test_complete",
			"success": true,
		})
	}
}

// GetAccountModels 获取账号所属平台的模型列表
// GET /api/v1/admin/accounts/:id/models
func (h *AccountHandler) GetAccountModels(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}

	a, err := h.db.Account.Get(c.Request.Context(), id)
	if err != nil {
		if ent.IsNotFound(err) {
			response.NotFound(c, "账号不存在")
			return
		}
		slog.Error("查询账号失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	models := h.pluginMgr.GetModels(a.Platform)
	type modelResp struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	list := make([]modelResp, len(models))
	for i, m := range models {
		list[i] = modelResp{ID: m.ID, Name: m.Name}
	}

	response.Success(c, list)
}

// GetAccountUsage 获取账号的用量窗口（通过插件 HandleRequest 透传）
// GET /api/v1/admin/accounts/usage[?platform=xxx]
// platform 可选；不传时查询所有平台
func (h *AccountHandler) GetAccountUsage(c *gin.Context) {
	platform := c.Query("platform")

	// 收集需要查询的平台 → 插件实例
	type platformQuery struct {
		platform string
		inst     *plugin.PluginInstance
	}
	var queries []platformQuery

	if platform != "" {
		inst := h.pluginMgr.GetPluginByPlatform(platform)
		if inst != nil {
			queries = append(queries, platformQuery{platform, inst})
		}
	} else {
		// 从已加载插件中获取所有 gateway 平台
		for _, meta := range h.pluginMgr.GetAllPluginMeta() {
			if meta.Platform == "" {
				continue
			}
			inst := h.pluginMgr.GetPluginByPlatform(meta.Platform)
			if inst != nil {
				queries = append(queries, platformQuery{meta.Platform, inst})
			}
		}
	}

	merged := make(map[string]any)

	type acctReq struct {
		ID          int               `json:"id"`
		Credentials map[string]string `json:"credentials"`
	}

	for _, q := range queries {
		accounts, err := h.db.Account.Query().
			Where(account.PlatformEQ(q.platform)).
			All(c.Request.Context())
		if err != nil || len(accounts) == 0 {
			continue
		}

		reqList := make([]acctReq, len(accounts))
		for i, a := range accounts {
			reqList[i] = acctReq{ID: a.ID, Credentials: a.Credentials}
		}
		body, _ := json.Marshal(reqList)

		status, _, respBody, err := q.inst.Gateway.HandleHTTPRequest(
			c.Request.Context(), "POST", "usage/accounts", "", nil, body,
		)
		if err != nil || status != http.StatusOK {
			continue
		}

		var result struct {
			Accounts map[string]any `json:"accounts"`
		}
		if json.Unmarshal(respBody, &result) == nil {
			for k, v := range result.Accounts {
				merged[k] = v
			}
		}
	}

	response.Success(c, map[string]any{"accounts": merged})
}

// sendSSEEvent 发送一个 SSE 事件
func sendSSEEvent(w http.ResponseWriter, data any) {
	b, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// GetCredentialsSchema 获取指定平台的凭证字段 schema
func (h *AccountHandler) GetCredentialsSchema(c *gin.Context) {
	platform := c.Param("platform")

	// 优先使用新模型：AccountTypes
	if accountTypes := h.pluginMgr.GetAccountTypes(platform); len(accountTypes) > 0 {
		resp := dto.CredentialSchemaResp{}
		for _, at := range accountTypes {
			atResp := dto.AccountTypeResp{
				Key:         at.Key,
				Label:       at.Label,
				Description: at.Description,
			}
			for _, f := range at.Fields {
				fieldResp := dto.CredentialFieldResp{
					Key:         f.Key,
					Label:       f.Label,
					Type:        f.Type,
					Required:    f.Required,
					Placeholder: f.Placeholder,
				}
				atResp.Fields = append(atResp.Fields, fieldResp)
			}
			resp.AccountTypes = append(resp.AccountTypes, atResp)
		}

		// 向后兼容：fields 继续返回默认账号类型的字段
		if len(resp.AccountTypes) > 0 {
			resp.Fields = resp.AccountTypes[0].Fields
		}

		response.Success(c, resp)
		return
	}

	// 旧模型兼容：CredentialFields
	if fields := h.pluginMgr.GetCredentialFields(platform); len(fields) > 0 {
		respFields := make([]dto.CredentialFieldResp, len(fields))
		for i, f := range fields {
			respFields[i] = dto.CredentialFieldResp{
				Key:         f.Key,
				Label:       f.Label,
				Type:        f.Type,
				Required:    f.Required,
				Placeholder: f.Placeholder,
			}
		}

		response.Success(c, dto.CredentialSchemaResp{Fields: respFields})
		return
	}

	// fallback: 静态定义
	schemas := map[string]dto.CredentialSchemaResp{
		"openai": {
			Fields: []dto.CredentialFieldResp{
				{Key: "api_key", Label: "API Key", Type: "password", Required: true, Placeholder: "sk-..."},
				{Key: "base_url", Label: "Base URL", Type: "text", Required: false, Placeholder: "https://api.openai.com/v1"},
			},
		},
		"claude": {
			Fields: []dto.CredentialFieldResp{
				{Key: "api_key", Label: "API Key", Type: "password", Required: true, Placeholder: "sk-ant-..."},
				{Key: "base_url", Label: "Base URL", Type: "text", Required: false, Placeholder: "https://api.anthropic.com"},
			},
		},
		"gemini": {
			Fields: []dto.CredentialFieldResp{
				{Key: "api_key", Label: "API Key", Type: "password", Required: true, Placeholder: "AIza..."},
			},
		},
	}

	schema, ok := schemas[platform]
	if !ok {
		schema = dto.CredentialSchemaResp{
			Fields: []dto.CredentialFieldResp{
				{Key: "api_key", Label: "API Key", Type: "password", Required: true, Placeholder: ""},
				{Key: "base_url", Label: "Base URL", Type: "text", Required: false, Placeholder: ""},
			},
		}
	}

	response.Success(c, schema)
}

// toAccountResp 将 ent.Account 转换为 dto.AccountResp
func toAccountResp(a *ent.Account) dto.AccountResp {
	resp := dto.AccountResp{
		ID:             int64(a.ID),
		Name:           a.Name,
		Platform:       a.Platform,
		Type:           a.Type,
		Credentials:    a.Credentials,
		Status:         string(a.Status),
		Priority:       a.Priority,
		MaxConcurrency: a.MaxConcurrency,
		RateMultiplier: a.RateMultiplier,
		ErrorMsg:       a.ErrorMsg,
		TimeMixin: dto.TimeMixin{
			CreatedAt: a.CreatedAt,
			UpdatedAt: a.UpdatedAt,
		},
	}

	if a.LastUsedAt != nil {
		t := a.LastUsedAt.Format("2006-01-02T15:04:05Z")
		resp.LastUsedAt = &t
	}

	// 代理 ID
	if a.Edges.Proxy != nil {
		pid := int64(a.Edges.Proxy.ID)
		resp.ProxyID = &pid
	}

	// 分组 ID 列表
	groupIDs := make([]int64, 0, len(a.Edges.Groups))
	for _, g := range a.Edges.Groups {
		groupIDs = append(groupIDs, int64(g.ID))
	}
	resp.GroupIDs = groupIDs

	return resp
}
