package handler

import (
	"github.com/gin-gonic/gin"

	appsubscription "github.com/DouDOU-start/airgate-core/internal/app/subscription"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// UserSubscriptions 用户查看自己的订阅列表。
func (h *SubscriptionHandler) UserSubscriptions(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.UserSubscriptions(c.Request.Context(), appsubscription.UserListFilter{
		UserID:   userID,
		Page:     page.Page,
		PageSize: page.PageSize,
	})
	if err != nil {
		httpCode, message := h.handleError("查询订阅列表失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	list := make([]dto.SubscriptionResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, toSubscriptionRespFromDomain(item))
	}

	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// ActiveSubscriptions 用户查看活跃订阅。
func (h *SubscriptionHandler) ActiveSubscriptions(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	subs, err := h.service.ActiveSubscriptions(c.Request.Context(), userID)
	if err != nil {
		httpCode, message := h.handleError("查询活跃订阅失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	list := make([]dto.SubscriptionResp, 0, len(subs))
	for _, item := range subs {
		list = append(list, toSubscriptionRespFromDomain(item))
	}

	response.Success(c, list)
}

// SubscriptionProgress 用户查看订阅使用进度（占位实现）。
func (h *SubscriptionHandler) SubscriptionProgress(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	progresses, err := h.service.SubscriptionProgress(c.Request.Context(), userID)
	if err != nil {
		httpCode, message := h.handleError("查询订阅进度失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	resp := make([]dto.SubscriptionProgressResp, 0, len(progresses))
	for _, item := range progresses {
		resp = append(resp, toSubscriptionProgressRespFromDomain(item))
	}
	response.Success(c, resp)
}

// AdminListSubscriptions 管理员列表所有订阅。
func (h *SubscriptionHandler) AdminListSubscriptions(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.AdminListSubscriptions(c.Request.Context(), appsubscription.AdminListFilter{
		Page:     page.Page,
		PageSize: page.PageSize,
		Status:   c.Query("status"),
		UserID:   parseOptionalInt(c.Query("user_id")),
	})
	if err != nil {
		httpCode, message := h.handleError("查询订阅列表失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	list := make([]dto.SubscriptionResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, toSubscriptionRespFromDomain(item))
	}
	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// AdminAssign 管理员分配订阅。
func (h *SubscriptionHandler) AdminAssign(c *gin.Context) {
	var req dto.AssignSubscriptionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	sub, err := h.service.AdminAssign(c.Request.Context(), appsubscription.AssignInput{
		UserID:    int(req.UserID),
		GroupID:   int(req.GroupID),
		ExpiresAt: req.ExpiresAt,
	})
	if err != nil {
		httpCode, message := h.handleError("分配订阅失败", "分配失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toSubscriptionRespFromDomain(sub))
}

// AdminBulkAssign 管理员批量分配订阅。
func (h *SubscriptionHandler) AdminBulkAssign(c *gin.Context) {
	var req dto.BulkAssignReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	userIDs := make([]int, 0, len(req.UserIDs))
	for _, id := range req.UserIDs {
		userIDs = append(userIDs, int(id))
	}

	created, err := h.service.AdminBulkAssign(c.Request.Context(), appsubscription.BulkAssignInput{
		UserIDs:   userIDs,
		GroupID:   int(req.GroupID),
		ExpiresAt: req.ExpiresAt,
	})
	if err != nil {
		httpCode, message := h.handleError("批量分配订阅失败", "批量分配失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, map[string]any{"created": created})
}

// AdminAdjust 管理员调整订阅。
func (h *SubscriptionHandler) AdminAdjust(c *gin.Context) {
	id, err := parseSubscriptionID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的订阅 ID")
		return
	}

	var req dto.AdjustSubscriptionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	sub, err := h.service.AdminAdjust(c.Request.Context(), id, appsubscription.AdjustInput{
		ExpiresAt: req.ExpiresAt,
		Status:    req.Status,
	})
	if err != nil {
		httpCode, message := h.handleError("调整订阅失败", "调整失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toSubscriptionRespFromDomain(sub))
}
