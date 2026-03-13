package handler

import (
	"log/slog"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/user"
	"github.com/DouDOU-start/airgate-core/ent/usersubscription"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// SubscriptionHandler 订阅管理 Handler
type SubscriptionHandler struct {
	db *ent.Client
}

// NewSubscriptionHandler 创建 SubscriptionHandler
func NewSubscriptionHandler(db *ent.Client) *SubscriptionHandler {
	return &SubscriptionHandler{db: db}
}

// UserSubscriptions 用户查看自己的订阅列表
func (h *SubscriptionHandler) UserSubscriptions(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	query := h.db.UserSubscription.Query().
		Where(usersubscription.HasUserWith(user.IDEQ(uid))).
		WithGroup()

	total, err := query.Count(c.Request.Context())
	if err != nil {
		slog.Error("查询订阅总数失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	subs, err := query.
		Offset((page.Page - 1) * page.PageSize).
		Limit(page.PageSize).
		Order(ent.Desc(usersubscription.FieldCreatedAt)).
		All(c.Request.Context())
	if err != nil {
		slog.Error("查询订阅列表失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	list := make([]dto.SubscriptionResp, 0, len(subs))
	for _, s := range subs {
		list = append(list, toSubscriptionResp(s, int64(uid)))
	}

	response.Success(c, response.PagedData(list, int64(total), page.Page, page.PageSize))
}

// ActiveSubscriptions 用户查看活跃订阅
func (h *SubscriptionHandler) ActiveSubscriptions(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	subs, err := h.db.UserSubscription.Query().
		Where(
			usersubscription.HasUserWith(user.IDEQ(uid)),
			usersubscription.StatusEQ(usersubscription.StatusActive),
		).
		WithGroup().
		Order(ent.Desc(usersubscription.FieldCreatedAt)).
		All(c.Request.Context())
	if err != nil {
		slog.Error("查询活跃订阅失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	list := make([]dto.SubscriptionResp, 0, len(subs))
	for _, s := range subs {
		list = append(list, toSubscriptionResp(s, int64(uid)))
	}

	response.Success(c, list)
}

// SubscriptionProgress 用户查看订阅使用进度（占位实现）
func (h *SubscriptionHandler) SubscriptionProgress(c *gin.Context) {
	// 占位返回空进度数据
	response.Success(c, []dto.SubscriptionProgressResp{})
}

// AdminListSubscriptions 管理员列表所有订阅
func (h *SubscriptionHandler) AdminListSubscriptions(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	query := h.db.UserSubscription.Query().WithUser().WithGroup()

	// 状态筛选
	if status := c.Query("status"); status != "" {
		query = query.Where(usersubscription.StatusEQ(usersubscription.Status(status)))
	}

	// 用户 ID 筛选
	if userIDStr := c.Query("user_id"); userIDStr != "" {
		uid, err := strconv.Atoi(userIDStr)
		if err == nil {
			query = query.Where(usersubscription.HasUserWith(user.IDEQ(uid)))
		}
	}

	total, err := query.Count(c.Request.Context())
	if err != nil {
		slog.Error("查询订阅总数失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	subs, err := query.
		Offset((page.Page - 1) * page.PageSize).
		Limit(page.PageSize).
		Order(ent.Desc(usersubscription.FieldCreatedAt)).
		All(c.Request.Context())
	if err != nil {
		slog.Error("查询订阅列表失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	list := make([]dto.SubscriptionResp, 0, len(subs))
	for _, s := range subs {
		var uid int64
		if s.Edges.User != nil {
			uid = int64(s.Edges.User.ID)
		}
		list = append(list, toSubscriptionResp(s, uid))
	}

	response.Success(c, response.PagedData(list, int64(total), page.Page, page.PageSize))
}

// AdminAssign 管理员分配订阅
func (h *SubscriptionHandler) AdminAssign(c *gin.Context) {
	var req dto.AssignSubscriptionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	expiresAt, err := time.Parse(time.RFC3339, req.ExpiresAt)
	if err != nil {
		response.BadRequest(c, "过期时间格式错误，请使用 RFC3339 格式")
		return
	}

	sub, err := h.db.UserSubscription.Create().
		SetUserID(int(req.UserID)).
		SetGroupID(int(req.GroupID)).
		SetEffectiveAt(time.Now()).
		SetExpiresAt(expiresAt).
		SetStatus(usersubscription.StatusActive).
		Save(c.Request.Context())
	if err != nil {
		slog.Error("分配订阅失败", "error", err)
		response.InternalError(c, "分配失败")
		return
	}

	// 重新查询以加载 edges
	sub, err = h.db.UserSubscription.Query().
		Where(usersubscription.IDEQ(sub.ID)).
		WithUser().
		WithGroup().
		Only(c.Request.Context())
	if err != nil {
		slog.Error("加载订阅关联数据失败", "error", err)
		response.InternalError(c, "分配成功但加载关联数据失败")
		return
	}

	var uid int64
	if sub.Edges.User != nil {
		uid = int64(sub.Edges.User.ID)
	}

	response.Success(c, toSubscriptionResp(sub, uid))
}

// AdminBulkAssign 管理员批量分配订阅
func (h *SubscriptionHandler) AdminBulkAssign(c *gin.Context) {
	var req dto.BulkAssignReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	expiresAt, err := time.Parse(time.RFC3339, req.ExpiresAt)
	if err != nil {
		response.BadRequest(c, "过期时间格式错误，请使用 RFC3339 格式")
		return
	}

	// 批量创建
	builders := make([]*ent.UserSubscriptionCreate, 0, len(req.UserIDs))
	for _, uid := range req.UserIDs {
		builder := h.db.UserSubscription.Create().
			SetUserID(int(uid)).
			SetGroupID(int(req.GroupID)).
			SetEffectiveAt(time.Now()).
			SetExpiresAt(expiresAt).
			SetStatus(usersubscription.StatusActive)
		builders = append(builders, builder)
	}

	subs, err := h.db.UserSubscription.CreateBulk(builders...).Save(c.Request.Context())
	if err != nil {
		slog.Error("批量分配订阅失败", "error", err)
		response.InternalError(c, "批量分配失败")
		return
	}

	response.Success(c, map[string]interface{}{
		"created": len(subs),
	})
}

// AdminAdjust 管理员调整订阅
func (h *SubscriptionHandler) AdminAdjust(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的订阅 ID")
		return
	}

	var req dto.AdjustSubscriptionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	builder := h.db.UserSubscription.UpdateOneID(id)

	if req.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			response.BadRequest(c, "过期时间格式错误")
			return
		}
		builder = builder.SetExpiresAt(t)
	}
	if req.Status != nil {
		builder = builder.SetStatus(usersubscription.Status(*req.Status))
	}

	_, err = builder.Save(c.Request.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			response.NotFound(c, "订阅不存在")
			return
		}
		slog.Error("调整订阅失败", "error", err)
		response.InternalError(c, "调整失败")
		return
	}

	// 重新查询以加载 edges
	sub, err := h.db.UserSubscription.Query().
		Where(usersubscription.IDEQ(id)).
		WithUser().
		WithGroup().
		Only(c.Request.Context())
	if err != nil {
		slog.Error("加载订阅关联数据失败", "error", err)
		response.InternalError(c, "调整成功但加载关联数据失败")
		return
	}

	var uid int64
	if sub.Edges.User != nil {
		uid = int64(sub.Edges.User.ID)
	}

	response.Success(c, toSubscriptionResp(sub, uid))
}

// toSubscriptionResp 将 ent.UserSubscription 转换为 dto.SubscriptionResp
func toSubscriptionResp(s *ent.UserSubscription, userID int64) dto.SubscriptionResp {
	resp := dto.SubscriptionResp{
		ID:          int64(s.ID),
		UserID:      userID,
		EffectiveAt: s.EffectiveAt.Format(time.RFC3339),
		ExpiresAt:   s.ExpiresAt.Format(time.RFC3339),
		Usage:       s.Usage,
		Status:      string(s.Status),
		TimeMixin: dto.TimeMixin{
			CreatedAt: s.CreatedAt,
			UpdatedAt: s.UpdatedAt,
		},
	}

	// 设置关联的分组信息
	if g := s.Edges.Group; g != nil {
		resp.GroupID = int64(g.ID)
		resp.GroupName = g.Name
	}

	return resp
}
