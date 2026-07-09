package handler

import (
	"github.com/gin-gonic/gin"

	appannouncement "github.com/DouDOU-start/airgate-core/internal/app/announcement"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// ListAnnouncements 查询公告列表（管理员）。
func (h *AnnouncementHandler) ListAnnouncements(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.List(c.Request.Context(), appannouncement.ListFilter{
		Page:     page.Page,
		PageSize: page.PageSize,
		Keyword:  page.Keyword,
		Status:   c.Query("status"),
	})
	if err != nil {
		httpCode, message := h.handleError("查询公告列表失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	list := make([]dto.AnnouncementResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, toAnnouncementRespFromDomain(item))
	}

	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// GetAnnouncement 获取公告详情（管理员）。
func (h *AnnouncementHandler) GetAnnouncement(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的公告 ID")
		return
	}

	item, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		httpCode, message := h.handleError("查询公告失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toAnnouncementRespFromDomain(item))
}

// CreateAnnouncement 创建公告（管理员）。
func (h *AnnouncementHandler) CreateAnnouncement(c *gin.Context) {
	var req dto.CreateAnnouncementReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.Create(c.Request.Context(), appannouncement.CreateInput{
		Title:      req.Title,
		Content:    req.Content,
		Status:     req.Status,
		NotifyMode: req.NotifyMode,
		StartsAt:   req.StartsAt,
		EndsAt:     req.EndsAt,
	})
	if err != nil {
		httpCode, message := h.handleError("创建公告失败", "创建失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toAnnouncementRespFromDomain(item))
}

// UpdateAnnouncement 更新公告（管理员）。
func (h *AnnouncementHandler) UpdateAnnouncement(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的公告 ID")
		return
	}

	var req dto.UpdateAnnouncementReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.Update(c.Request.Context(), id, appannouncement.UpdateInput{
		Title:      req.Title,
		Content:    req.Content,
		Status:     req.Status,
		NotifyMode: req.NotifyMode,
		StartsAt:   req.StartsAt,
		EndsAt:     req.EndsAt,
	})
	if err != nil {
		httpCode, message := h.handleError("更新公告失败", "更新失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toAnnouncementRespFromDomain(item))
}

// DeleteAnnouncement 删除公告（管理员）。
func (h *AnnouncementHandler) DeleteAnnouncement(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的公告 ID")
		return
	}

	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		httpCode, message := h.handleError("删除公告失败", "删除失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, nil)
}

// ListMyAnnouncements 查询当前用户可见公告（未读优先）。
// query unread_only=true 时仅返回未读。
func (h *AnnouncementHandler) ListMyAnnouncements(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	unreadOnly := c.Query("unread_only") == "true"
	list, err := h.service.ListForUser(c.Request.Context(), userID, unreadOnly)
	if err != nil {
		httpCode, message := h.handleError("查询用户公告失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	resp := make([]dto.UserAnnouncementResp, 0, len(list))
	for _, item := range list {
		resp = append(resp, toUserAnnouncementResp(item))
	}

	response.Success(c, resp)
}

// MarkAnnouncementRead 标记公告已读。
func (h *AnnouncementHandler) MarkAnnouncementRead(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的公告 ID")
		return
	}

	if err := h.service.MarkRead(c.Request.Context(), userID, id); err != nil {
		httpCode, message := h.handleError("标记公告已读失败", "操作失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, nil)
}

// MarkAllAnnouncementsRead 将当前生效的全部公告标记为已读。
func (h *AnnouncementHandler) MarkAllAnnouncementsRead(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	if err := h.service.MarkAllRead(c.Request.Context(), userID); err != nil {
		httpCode, message := h.handleError("标记全部公告已读失败", "操作失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, nil)
}
