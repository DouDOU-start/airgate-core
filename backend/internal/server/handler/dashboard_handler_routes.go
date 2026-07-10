package handler

import (
	"github.com/gin-gonic/gin"

	appdashboard "github.com/DouDOU-start/airgate-core/internal/app/dashboard"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// Stats 返回仪表盘统计数据（管理员权限由路由层 AdminOnly 中间件保证）。
func (h *DashboardHandler) Stats(c *gin.Context) {
	var req dto.DashboardStatsReq
	if err := c.ShouldBindQuery(&req); err != nil {
		response.BindError(c, err)
		return
	}

	stats, err := h.service.Stats(c.Request.Context(), req.UserID, req.TZ)
	if err != nil {
		h.handleError("查询仪表盘统计失败", err)
		response.InternalError(c, "查询失败")
		return
	}

	response.Success(c, toDashboardStatsResp(stats))
}

// Trend 返回仪表盘趋势数据（管理员权限由路由层 AdminOnly 中间件保证）。
func (h *DashboardHandler) Trend(c *gin.Context) {
	var req dto.DashboardTrendReq
	if err := c.ShouldBindQuery(&req); err != nil {
		response.BindError(c, err)
		return
	}

	trend, err := h.service.Trend(c.Request.Context(), appdashboard.TrendQuery{
		Range:       req.Range,
		Granularity: req.Granularity,
		StartDate:   req.StartDate,
		EndDate:     req.EndDate,
		UserID:      req.UserID,
		ChannelID:   req.ChannelID,
		TZ:          req.TZ,
	})
	if err != nil {
		h.handleError("查询仪表盘趋势失败", err)
		response.InternalError(c, "查询失败")
		return
	}

	response.Success(c, toDashboardTrendResp(trend))
}
