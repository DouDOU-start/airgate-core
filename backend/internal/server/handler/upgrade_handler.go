package handler

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/server/response"
	"github.com/DouDOU-start/airgate-core/internal/upgrade"
)

// UpgradeHandler 系统更新 Handler。
type UpgradeHandler struct {
	service *upgrade.Service
}

// NewUpgradeHandler 创建 UpgradeHandler。
func NewUpgradeHandler(service *upgrade.Service) *UpgradeHandler {
	return &UpgradeHandler{service: service}
}

// GetInfo 返回升级总览（当前版本、最新版本、模式、是否有更新）。
func (h *UpgradeHandler) GetInfo(c *gin.Context) {
	info, err := h.service.Info(c.Request.Context())
	if err != nil {
		slog.Error("查询升级信息失败", "error", err)
		response.InternalError(c, "查询升级信息失败")
		return
	}
	response.Success(c, info)
}

// GetStatus 返回升级状态机当前快照。前端轮询用。
func (h *UpgradeHandler) GetStatus(c *gin.Context) {
	response.Success(c, h.service.Status())
}

// Run 触发升级。仅 systemd 模式有效。body 必须带 confirm_db_backup=true。
func (h *UpgradeHandler) Run(c *gin.Context) {
	var req upgrade.RunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	if err := h.service.Run(c.Request.Context(), req); err != nil {
		slog.Warn("触发升级失败", "error", err)
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, gin.H{"started": true})
}
