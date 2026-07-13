package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// ListAllowedUsers 列出获准访问该专属分组的用户。
// GET /api/v1/admin/groups/:id/allowed-users
func (h *GroupHandler) ListAllowedUsers(c *gin.Context) {
	groupID, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的分组 ID")
		return
	}

	items, err := h.service.AllowedUsers(c.Request.Context(), groupID)
	if err != nil {
		httpCode, message := h.handleError("查询专属分组用户失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	resp := make([]dto.GroupAllowedUserResp, 0, len(items))
	for _, item := range items {
		resp = append(resp, dto.GroupAllowedUserResp{
			UserID:   int64(item.UserID),
			Email:    item.Email,
			Username: item.Username,
		})
	}
	response.Success(c, resp)
}

// GrantAllowedUser 授予用户访问该专属分组的权限。
// POST /api/v1/admin/groups/:id/allowed-users/:userId
func (h *GroupHandler) GrantAllowedUser(c *gin.Context) {
	groupID, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的分组 ID")
		return
	}
	userID, err := ParseID(c.Param("userId"))
	if err != nil {
		response.BadRequest(c, "无效的用户 ID")
		return
	}

	if err := h.service.GrantAllowedUser(c.Request.Context(), groupID, userID); err != nil {
		httpCode, message := h.handleError("授予专属分组权限失败", "授予失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, gin.H{})
}

// RevokeAllowedUser 撤销用户访问该专属分组的权限。
// DELETE /api/v1/admin/groups/:id/allowed-users/:userId
func (h *GroupHandler) RevokeAllowedUser(c *gin.Context) {
	groupID, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的分组 ID")
		return
	}
	userID, err := ParseID(c.Param("userId"))
	if err != nil {
		response.BadRequest(c, "无效的用户 ID")
		return
	}

	if err := h.service.RevokeAllowedUser(c.Request.Context(), groupID, userID); err != nil {
		httpCode, message := h.handleError("撤销专属分组权限失败", "撤销失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, gin.H{})
}
