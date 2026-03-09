package handler

import (
	"fmt"
	"log/slog"
	"strconv"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/account"
	"github.com/DouDOU-start/airgate-core/internal/plugin"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
	"github.com/gin-gonic/gin"
)

// AccountHandler 上游账号管理 Handler
type AccountHandler struct {
	db        *ent.Client
	pluginMgr *plugin.Manager
}

// NewAccountHandler 创建 AccountHandler
func NewAccountHandler(db *ent.Client, pluginMgr *plugin.Manager) *AccountHandler {
	return &AccountHandler{db: db, pluginMgr: pluginMgr}
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

	list := make([]dto.AccountResp, 0, len(accounts))
	for _, a := range accounts {
		list = append(list, toAccountResp(a))
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

// TestAccount 测试账号连通性
func (h *AccountHandler) TestAccount(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的账号 ID")
		return
	}

	// 查询账号
	a, err := h.db.Account.Get(c.Request.Context(), id)
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
		response.Success(c, map[string]interface{}{
			"success": false,
			"message": "未找到平台 " + a.Platform + " 对应的插件，请先安装并启用插件",
		})
		return
	}

	// 调用插件的 ValidateCredentials
	creds := make(map[string]string, len(a.Credentials))
	for k, v := range a.Credentials {
		creds[k] = fmt.Sprintf("%v", v)
	}

	if err := inst.Gateway.ValidateCredentials(c.Request.Context(), creds); err != nil {
		response.Success(c, map[string]interface{}{
			"success": false,
			"message": "凭证验证失败: " + err.Error(),
		})
		return
	}

	response.Success(c, map[string]interface{}{
		"success": true,
		"message": "凭证验证通过",
	})
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
