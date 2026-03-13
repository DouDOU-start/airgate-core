package handler

import (
	"log/slog"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/proxy"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// ProxyHandler 代理管理 Handler
type ProxyHandler struct {
	db *ent.Client
}

// NewProxyHandler 创建 ProxyHandler
func NewProxyHandler(db *ent.Client) *ProxyHandler {
	return &ProxyHandler{db: db}
}

// ListProxies 分页列表代理
func (h *ProxyHandler) ListProxies(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	query := h.db.Proxy.Query()

	// 关键词搜索
	if page.Keyword != "" {
		query = query.Where(proxy.NameContains(page.Keyword))
	}

	// 状态筛选
	if status := c.Query("status"); status != "" {
		query = query.Where(proxy.StatusEQ(proxy.Status(status)))
	}

	total, err := query.Count(c.Request.Context())
	if err != nil {
		slog.Error("查询代理总数失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	proxies, err := query.
		Offset((page.Page - 1) * page.PageSize).
		Limit(page.PageSize).
		Order(ent.Desc(proxy.FieldCreatedAt)).
		All(c.Request.Context())
	if err != nil {
		slog.Error("查询代理列表失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	list := make([]dto.ProxyResp, 0, len(proxies))
	for _, p := range proxies {
		list = append(list, toProxyResp(p))
	}

	response.Success(c, response.PagedData(list, int64(total), page.Page, page.PageSize))
}

// CreateProxy 创建代理
func (h *ProxyHandler) CreateProxy(c *gin.Context) {
	var req dto.CreateProxyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	builder := h.db.Proxy.Create().
		SetName(req.Name).
		SetProtocol(proxy.Protocol(req.Protocol)).
		SetAddress(req.Address).
		SetPort(req.Port)

	if req.Username != "" {
		builder = builder.SetUsername(req.Username)
	}
	if req.Password != "" {
		builder = builder.SetPassword(req.Password)
	}

	p, err := builder.Save(c.Request.Context())
	if err != nil {
		slog.Error("创建代理失败", "error", err)
		response.InternalError(c, "创建失败")
		return
	}

	response.Success(c, toProxyResp(p))
}

// UpdateProxy 更新代理
func (h *ProxyHandler) UpdateProxy(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的代理 ID")
		return
	}

	var req dto.UpdateProxyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	builder := h.db.Proxy.UpdateOneID(id)

	if req.Name != nil {
		builder = builder.SetName(*req.Name)
	}
	if req.Protocol != nil {
		builder = builder.SetProtocol(proxy.Protocol(*req.Protocol))
	}
	if req.Address != nil {
		builder = builder.SetAddress(*req.Address)
	}
	if req.Port != nil {
		builder = builder.SetPort(*req.Port)
	}
	if req.Username != nil {
		builder = builder.SetUsername(*req.Username)
	}
	if req.Password != nil {
		builder = builder.SetPassword(*req.Password)
	}
	if req.Status != nil {
		builder = builder.SetStatus(proxy.Status(*req.Status))
	}

	p, err := builder.Save(c.Request.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			response.NotFound(c, "代理不存在")
			return
		}
		slog.Error("更新代理失败", "error", err)
		response.InternalError(c, "更新失败")
		return
	}

	response.Success(c, toProxyResp(p))
}

// DeleteProxy 删除代理
func (h *ProxyHandler) DeleteProxy(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的代理 ID")
		return
	}

	if err := h.db.Proxy.DeleteOneID(id).Exec(c.Request.Context()); err != nil {
		if ent.IsNotFound(err) {
			response.NotFound(c, "代理不存在")
			return
		}
		slog.Error("删除代理失败", "error", err)
		response.InternalError(c, "删除失败")
		return
	}

	response.Success(c, nil)
}

// TestProxy 测试代理连通性（占位实现）
func (h *ProxyHandler) TestProxy(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的代理 ID")
		return
	}

	// 检查代理是否存在
	exists, err := h.db.Proxy.Query().Where(proxy.IDEQ(id)).Exist(c.Request.Context())
	if err != nil {
		slog.Error("查询代理失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}
	if !exists {
		response.NotFound(c, "代理不存在")
		return
	}

	// 占位返回成功（实际连通性测试待完善）
	response.Success(c, dto.TestProxyResp{
		Success: true,
		Latency: 0,
	})
}

// toProxyResp 将 ent.Proxy 转换为 dto.ProxyResp
func toProxyResp(p *ent.Proxy) dto.ProxyResp {
	return dto.ProxyResp{
		ID:       int64(p.ID),
		Name:     p.Name,
		Protocol: string(p.Protocol),
		Address:  p.Address,
		Port:     p.Port,
		Username: p.Username,
		Status:   string(p.Status),
		TimeMixin: dto.TimeMixin{
			CreatedAt: p.CreatedAt,
			UpdatedAt: p.UpdatedAt,
		},
	}
}
