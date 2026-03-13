package handler

import (
	"log/slog"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/group"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// GroupHandler 分组管理 Handler
type GroupHandler struct {
	db *ent.Client
}

// NewGroupHandler 创建 GroupHandler
func NewGroupHandler(db *ent.Client) *GroupHandler {
	return &GroupHandler{db: db}
}

// ListGroups 查询分组列表
func (h *GroupHandler) ListGroups(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	query := h.db.Group.Query()

	// 关键词搜索
	if page.Keyword != "" {
		query = query.Where(group.NameContains(page.Keyword))
	}

	// 平台筛选
	if platform := c.Query("platform"); platform != "" {
		query = query.Where(group.PlatformEQ(platform))
	}

	// 总数
	total, err := query.Count(c.Request.Context())
	if err != nil {
		slog.Error("查询分组总数失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	// 分页查询
	groups, err := query.
		Offset((page.Page-1)*page.PageSize).
		Limit(page.PageSize).
		Order(ent.Desc(group.FieldSortWeight), ent.Desc(group.FieldCreatedAt)).
		All(c.Request.Context())
	if err != nil {
		slog.Error("查询分组列表失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	list := make([]dto.GroupResp, 0, len(groups))
	for _, g := range groups {
		list = append(list, toGroupResp(g))
	}

	response.Success(c, response.PagedData(list, int64(total), page.Page, page.PageSize))
}

// GetGroup 获取分组详情
func (h *GroupHandler) GetGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的分组 ID")
		return
	}

	g, err := h.db.Group.Get(c.Request.Context(), id)
	if err != nil {
		if ent.IsNotFound(err) {
			response.NotFound(c, "分组不存在")
			return
		}
		slog.Error("查询分组失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	response.Success(c, toGroupResp(g))
}

// CreateGroup 创建分组
func (h *GroupHandler) CreateGroup(c *gin.Context) {
	var req dto.CreateGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	builder := h.db.Group.Create().
		SetName(req.Name).
		SetPlatform(req.Platform).
		SetRateMultiplier(req.RateMultiplier).
		SetIsExclusive(req.IsExclusive).
		SetSubscriptionType(group.SubscriptionType(req.SubscriptionType)).
		SetSortWeight(req.SortWeight)

	if req.Quotas != nil {
		builder = builder.SetQuotas(req.Quotas)
	}
	if req.ModelRouting != nil {
		builder = builder.SetModelRouting(req.ModelRouting)
	}

	g, err := builder.Save(c.Request.Context())
	if err != nil {
		slog.Error("创建分组失败", "error", err)
		response.InternalError(c, "创建失败")
		return
	}

	response.Success(c, toGroupResp(g))
}

// UpdateGroup 更新分组
func (h *GroupHandler) UpdateGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的分组 ID")
		return
	}

	var req dto.UpdateGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	builder := h.db.Group.UpdateOneID(id)

	if req.Name != nil {
		builder = builder.SetName(*req.Name)
	}
	if req.RateMultiplier != nil {
		builder = builder.SetRateMultiplier(*req.RateMultiplier)
	}
	if req.IsExclusive != nil {
		builder = builder.SetIsExclusive(*req.IsExclusive)
	}
	if req.SubscriptionType != nil {
		builder = builder.SetSubscriptionType(group.SubscriptionType(*req.SubscriptionType))
	}
	if req.Quotas != nil {
		builder = builder.SetQuotas(req.Quotas)
	}
	if req.ModelRouting != nil {
		builder = builder.SetModelRouting(req.ModelRouting)
	}
	if req.SortWeight != nil {
		builder = builder.SetSortWeight(*req.SortWeight)
	}

	g, err := builder.Save(c.Request.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			response.NotFound(c, "分组不存在")
			return
		}
		slog.Error("更新分组失败", "error", err)
		response.InternalError(c, "更新失败")
		return
	}

	response.Success(c, toGroupResp(g))
}

// toGroupResp 将 ent.Group 转换为 dto.GroupResp
func toGroupResp(g *ent.Group) dto.GroupResp {
	return dto.GroupResp{
		ID:               int64(g.ID),
		Name:             g.Name,
		Platform:         g.Platform,
		RateMultiplier:   g.RateMultiplier,
		IsExclusive:      g.IsExclusive,
		SubscriptionType: string(g.SubscriptionType),
		Quotas:           g.Quotas,
		ModelRouting:     g.ModelRouting,
		SortWeight:       g.SortWeight,
		TimeMixin: dto.TimeMixin{
			CreatedAt: g.CreatedAt,
			UpdatedAt: g.UpdatedAt,
		},
	}
}
