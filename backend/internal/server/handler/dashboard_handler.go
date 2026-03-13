package handler

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/usagelog"
	"github.com/DouDOU-start/airgate-core/internal/plugin"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// DashboardHandler 仪表盘 Handler
type DashboardHandler struct {
	db      *ent.Client
	plugins *plugin.Manager
}

// NewDashboardHandler 创建 DashboardHandler
func NewDashboardHandler(db *ent.Client, plugins *plugin.Manager) *DashboardHandler {
	return &DashboardHandler{db: db, plugins: plugins}
}

// Stats 返回仪表盘统计数据
func (h *DashboardHandler) Stats(c *gin.Context) {
	ctx := c.Request.Context()

	// 查询各表总数
	totalUsers, err := h.db.User.Query().Count(ctx)
	if err != nil {
		slog.Error("查询用户总数失败", "error", err)
		totalUsers = 0
	}

	totalAccounts, err := h.db.Account.Query().Count(ctx)
	if err != nil {
		slog.Error("查询账号总数失败", "error", err)
		totalAccounts = 0
	}

	totalGroups, err := h.db.Group.Query().Count(ctx)
	if err != nil {
		slog.Error("查询分组总数失败", "error", err)
		totalGroups = 0
	}

	totalAPIKeys, err := h.db.APIKey.Query().Count(ctx)
	if err != nil {
		slog.Error("查询密钥总数失败", "error", err)
		totalAPIKeys = 0
	}

	activePlugins := h.plugins.RunningCount()

	// 今日统计：按今天的日期筛选使用日志
	todayStart := time.Now().Truncate(24 * time.Hour)

	todayLogs, err := h.db.UsageLog.Query().
		Where(usagelog.CreatedAtGTE(todayStart)).
		All(ctx)
	if err != nil {
		slog.Error("查询今日使用记录失败", "error", err)
		todayLogs = nil
	}

	var totalRequests int64
	var totalTokens int64
	var totalRevenue float64

	for _, l := range todayLogs {
		totalRequests++
		totalTokens += int64(l.InputTokens + l.OutputTokens + l.CacheTokens)
		totalRevenue += l.ActualCost
	}

	response.Success(c, dto.DashboardStatsResp{
		TotalUsers:    int64(totalUsers),
		TotalAccounts: int64(totalAccounts),
		TotalGroups:   int64(totalGroups),
		TotalAPIKeys:  int64(totalAPIKeys),
		TotalRequests: totalRequests,
		TotalTokens:   totalTokens,
		TotalRevenue:  totalRevenue,
		ActivePlugins: int64(activePlugins),
	})
}
