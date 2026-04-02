package handler

import (
	"github.com/gin-gonic/gin"

	appdashboard "github.com/DouDOU-start/airgate-core/internal/app/dashboard"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// Stats 返回仪表盘统计数据。
func (h *DashboardHandler) Stats(c *gin.Context) {
	if !ensureAdminRole(c) {
		response.Forbidden(c, "需要管理员权限")
		return
	}

	stats, err := h.service.Stats(c.Request.Context())
	if err != nil {
		h.handleError("查询仪表盘统计失败", err)
		response.InternalError(c, "查询失败")
		return
	}

	response.Success(c, toDashboardStatsResp(stats))
}

// Trend 返回仪表盘趋势数据。
func (h *DashboardHandler) Trend(c *gin.Context) {
	if !ensureAdminRole(c) {
		response.Forbidden(c, "需要管理员权限")
		return
	}

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
	})
	if err != nil {
		h.handleError("查询仪表盘趋势失败", err)
		response.InternalError(c, "查询失败")
		return
	}

	response.Success(c, toDashboardTrendResp(trend))
}
