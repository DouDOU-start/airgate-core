package handler

import (
	"github.com/gin-gonic/gin"

	appusage "github.com/DouDOU-start/airgate-core/internal/app/usage"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// UserUsage 用户查看自己的使用记录。
func (h *UsageHandler) UserUsage(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	var query dto.UsageQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.ListUser(c.Request.Context(), int64(userID), appusage.ListFilter{
		Page:      query.Page,
		PageSize:  query.PageSize,
		APIKeyID:  query.APIKeyID,
		AccountID: query.AccountID,
		GroupID:   query.GroupID,
		Platform:  query.Platform,
		Model:     query.Model,
		StartDate: query.StartDate,
		EndDate:   query.EndDate,
	})
	if err != nil {
		handleUsageError("查询用户使用记录失败", err)
		response.InternalError(c, "查询失败")
		return
	}

	list := make([]dto.UsageLogResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, toUsageLogResp(item))
	}
	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// UserUsageStats 用户聚合统计。
func (h *UsageHandler) UserUsageStats(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	var query dto.UsageFilterQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BindError(c, err)
		return
	}

	summary, err := h.service.UserStats(c.Request.Context(), int64(userID), appusage.StatsFilter{
		Platform:  query.Platform,
		Model:     query.Model,
		StartDate: query.StartDate,
		EndDate:   query.EndDate,
	})
	if err != nil {
		handleUsageError("统计用户使用记录失败", err)
		response.InternalError(c, "统计失败")
		return
	}

	response.Success(c, dto.UsageStatsResp{
		TotalRequests:   summary.TotalRequests,
		TotalTokens:     summary.TotalTokens,
		TotalCost:       summary.TotalCost,
		TotalActualCost: summary.TotalActualCost,
	})
}

// AdminUsage 管理员查看全局使用记录。
func (h *UsageHandler) AdminUsage(c *gin.Context) {
	var query dto.UsageQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.ListAdmin(c.Request.Context(), appusage.ListFilter{
		Page:      query.Page,
		PageSize:  query.PageSize,
		UserID:    query.UserID,
		APIKeyID:  query.APIKeyID,
		AccountID: query.AccountID,
		GroupID:   query.GroupID,
		Platform:  query.Platform,
		Model:     query.Model,
		StartDate: query.StartDate,
		EndDate:   query.EndDate,
	})
	if err != nil {
		handleUsageError("查询管理员使用记录失败", err)
		response.InternalError(c, "查询失败")
		return
	}

	list := make([]dto.UsageLogResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, toUsageLogResp(item))
	}
	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// AdminUsageStats 管理员聚合统计。
func (h *UsageHandler) AdminUsageStats(c *gin.Context) {
	var query dto.UsageStatsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.AdminStats(c.Request.Context(), appusage.StatsFilter{
		UserID:    query.UserID,
		Platform:  query.Platform,
		Model:     query.Model,
		StartDate: query.StartDate,
		EndDate:   query.EndDate,
	}, query.GroupBy)
	if err != nil {
		handleUsageError("查询管理员聚合统计失败", err)
		response.InternalError(c, "统计失败")
		return
	}

	response.Success(c, toUsageStatsResp(result))
}

// AdminUsageTrend 管理员 Token 使用趋势。
func (h *UsageHandler) AdminUsageTrend(c *gin.Context) {
	var query dto.UsageTrendQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.AdminTrend(c.Request.Context(), appusage.TrendFilter{
		StatsFilter: appusage.StatsFilter{
			UserID:    query.UserID,
			Platform:  query.Platform,
			Model:     query.Model,
			StartDate: query.StartDate,
			EndDate:   query.EndDate,
		},
		Granularity: query.Granularity,
	})
	if err != nil {
		handleUsageError("查询管理员趋势统计失败", err)
		response.InternalError(c, "查询失败")
		return
	}

	response.Success(c, toUsageTrendBuckets(result))
}

func currentUserID(c *gin.Context) (int, bool) {
	userID, exists := c.Get("user_id")
	if !exists {
		return 0, false
	}
	id, ok := userID.(int)
	return id, ok
}
