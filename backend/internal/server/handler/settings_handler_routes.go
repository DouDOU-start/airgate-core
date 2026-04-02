package handler

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// GetSettings 获取所有设置。
func (h *SettingsHandler) GetSettings(c *gin.Context) {
	list, err := h.service.List(c.Request.Context(), c.Query("group"))
	if err != nil {
		slog.Error("查询设置失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	resp := make([]dto.SettingResp, 0, len(list))
	for _, item := range list {
		resp = append(resp, toSettingResp(item))
	}
	response.Success(c, resp)
}

// UpdateSettings 批量更新设置。
func (h *SettingsHandler) UpdateSettings(c *gin.Context) {
	var req dto.UpdateSettingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	items := make([]appsettings.ItemInput, 0, len(req.Settings))
	for _, item := range req.Settings {
		items = append(items, appsettings.ItemInput{
			Key:   item.Key,
			Value: item.Value,
		})
	}

	if err := h.service.Update(c.Request.Context(), items); err != nil {
		slog.Error("更新设置失败", "error", err)
		response.InternalError(c, "更新设置失败")
		return
	}

	response.Success(c, nil)
}
