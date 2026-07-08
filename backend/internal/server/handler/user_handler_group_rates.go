package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// ListGroupRateOverrides 列出某个分组下所有设置了专属倍率的用户。
// GET /api/v1/admin/groups/:groupId/rate-overrides
func (h *UserHandler) ListGroupRateOverrides(c *gin.Context) {
	groupID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "无效的分组 ID")
		return
	}

	items, err := h.service.ListGroupRateOverrides(c.Request.Context(), groupID)
	if err != nil {
		httpCode, message := h.handleError("查询分组专属倍率失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	resp := make([]dto.GroupRateOverrideResp, 0, len(items))
	for _, it := range items {
		resp = append(resp, dto.GroupRateOverrideResp{
			UserID:   int64(it.UserID),
			Email:    it.Email,
			Username: it.Username,
			Rate:     it.Rate,
		})
	}
	response.Success(c, resp)
}

// SetGroupRateOverride 设置或更新用户在指定分组下的专属倍率。
// PUT /api/v1/admin/groups/:groupId/rate-overrides/:userId
func (h *UserHandler) SetGroupRateOverride(c *gin.Context) {
	groupID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "无效的分组 ID")
		return
	}
	userID, err := strconv.Atoi(c.Param("userId"))
	if err != nil || userID <= 0 {
		response.BadRequest(c, "无效的用户 ID")
		return
	}

	var req dto.SetGroupRateOverrideReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.SetGroupRate(c.Request.Context(), userID, groupID, req.Rate)
	if err != nil {
		httpCode, message := h.handleError("设置分组专属倍率失败", "设置失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, dto.GroupRateOverrideResp{
		UserID:   int64(item.UserID),
		Email:    item.Email,
		Username: item.Username,
		Rate:     item.Rate,
	})
}

// DeleteGroupRateOverride 删除用户在指定分组下的专属倍率。
// DELETE /api/v1/admin/groups/:groupId/rate-overrides/:userId
func (h *UserHandler) DeleteGroupRateOverride(c *gin.Context) {
	groupID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "无效的分组 ID")
		return
	}
	userID, err := strconv.Atoi(c.Param("userId"))
	if err != nil || userID <= 0 {
		response.BadRequest(c, "无效的用户 ID")
		return
	}

	if err := h.service.DeleteGroupRate(c.Request.Context(), userID, groupID); err != nil {
		httpCode, message := h.handleError("删除分组专属倍率失败", "删除失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, gin.H{})
}
