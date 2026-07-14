package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// BindChannelKey 把渠道 key 绑定到该分组。
// POST /api/v1/admin/groups/:id/channel-keys/:keyId
func (h *GroupHandler) BindChannelKey(c *gin.Context) {
	groupID, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的分组 ID")
		return
	}
	keyID, err := ParseID(c.Param("keyId"))
	if err != nil {
		response.BadRequest(c, "无效的渠道 key ID")
		return
	}

	if err := h.service.BindChannelKey(c.Request.Context(), groupID, keyID); err != nil {
		httpCode, message := h.handleError("绑定渠道 key 失败", "绑定失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, gin.H{})
}

// UnbindChannelKey 把渠道 key 从该分组解绑。
// DELETE /api/v1/admin/groups/:id/channel-keys/:keyId
func (h *GroupHandler) UnbindChannelKey(c *gin.Context) {
	groupID, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的分组 ID")
		return
	}
	keyID, err := ParseID(c.Param("keyId"))
	if err != nil {
		response.BadRequest(c, "无效的渠道 key ID")
		return
	}

	if err := h.service.UnbindChannelKey(c.Request.Context(), groupID, keyID); err != nil {
		httpCode, message := h.handleError("解绑渠道 key 失败", "解绑失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, gin.H{})
}
