package handler

import (
	"log/slog"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/account"
	"github.com/DouDOU-start/airgate-core/ent/apikey"
	"github.com/DouDOU-start/airgate-core/ent/usagelog"
	"github.com/DouDOU-start/airgate-core/ent/user"
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
	role, _ := c.Get("role")
	if role != "admin" {
		response.Forbidden(c, "需要管理员权限")
		return
	}

	ctx := c.Request.Context()
	todayStart := time.Now().Truncate(24 * time.Hour)

	// 并行查询各项统计（简单依次查询，避免复杂的 goroutine）

	// API 密钥统计
	totalAPIKeys, _ := h.db.APIKey.Query().Count(ctx)
	enabledAPIKeys, _ := h.db.APIKey.Query().Where(apikey.StatusEQ(apikey.StatusActive)).Count(ctx)

	// 账号统计
	totalAccounts, _ := h.db.Account.Query().Count(ctx)
	enabledAccounts, _ := h.db.Account.Query().Where(account.StatusEQ(account.StatusActive)).Count(ctx)
	errorAccounts, _ := h.db.Account.Query().Where(account.StatusEQ(account.StatusError)).Count(ctx)

	// 用户统计
	totalUsers, _ := h.db.User.Query().Count(ctx)
	newUsersToday, _ := h.db.User.Query().Where(user.CreatedAtGTE(todayStart)).Count(ctx)

	// 请求统计
	allTimeRequests, _ := h.db.UsageLog.Query().Count(ctx)

	// 今日使用日志统计
	todayLogs, err := h.db.UsageLog.Query().
		Where(usagelog.CreatedAtGTE(todayStart)).
		All(ctx)
	if err != nil {
		slog.Error("查询今日使用记录失败", "error", err)
		todayLogs = nil
	}

	var todayRequests int64
	var todayTokens int64
	var todayCost float64
	var todayDurationSum int64
	activeUserSet := make(map[int]bool)

	for _, l := range todayLogs {
		todayRequests++
		todayTokens += int64(l.InputTokens + l.OutputTokens + l.CachedInputTokens)
		todayCost += l.ActualCost
		todayDurationSum += l.DurationMs
		if u := l.Edges.User; u != nil {
			activeUserSet[u.ID] = true
		}
	}

	// 如果 todayLogs 没有加载 user edge，尝试用查询获取活跃用户数
	activeUsers := int64(len(activeUserSet))
	if activeUsers == 0 && todayRequests > 0 {
		// 通过 distinct 查询今日活跃用户数
		todayLogsWithUser, err := h.db.UsageLog.Query().
			Where(usagelog.CreatedAtGTE(todayStart)).
			WithUser().
			All(ctx)
		if err == nil {
			for _, l := range todayLogsWithUser {
				if l.Edges.User != nil {
					activeUserSet[l.Edges.User.ID] = true
				}
			}
			activeUsers = int64(len(activeUserSet))
		}
	}

	// 全局 Token/Cost 统计
	var allTimeAgg []struct {
		TotalTokens int64   `json:"sum_input_tokens"`
		TotalCost   float64 `json:"sum_actual_cost"`
	}
	err = h.db.UsageLog.Query().
		Aggregate(
			ent.Sum(usagelog.FieldInputTokens),
			ent.Sum(usagelog.FieldOutputTokens),
			ent.Sum(usagelog.FieldCachedInputTokens),
			ent.Sum(usagelog.FieldActualCost),
		).
		Scan(ctx, &allTimeAgg)

	var allTimeTokens int64
	var allTimeCost float64
	if err == nil && len(allTimeAgg) > 0 {
		// 注意：Sum 只扫描了 input_tokens，需要单独查 output/cache
		// 这里用简单方式：直接用聚合查询
		allTimeCost = allTimeAgg[0].TotalCost
	}

	// 重新做精确的全局 token 聚合
	var tokenAgg []struct {
		InputSum  int64 `json:"sum_input_tokens"`
		OutputSum int64 `json:"sum_output_tokens"`
		CacheSum  int64 `json:"sum_cache_tokens"`
	}
	err = h.db.UsageLog.Query().
		Aggregate(
			ent.Sum(usagelog.FieldInputTokens),
			ent.Sum(usagelog.FieldOutputTokens),
			ent.Sum(usagelog.FieldCachedInputTokens),
		).
		Scan(ctx, &tokenAgg)
	if err == nil && len(tokenAgg) > 0 {
		allTimeTokens = tokenAgg[0].InputSum + tokenAgg[0].OutputSum + tokenAgg[0].CacheSum
	}

	var costAgg []struct {
		CostSum float64 `json:"sum_actual_cost"`
	}
	err = h.db.UsageLog.Query().
		Aggregate(ent.Sum(usagelog.FieldActualCost)).
		Scan(ctx, &costAgg)
	if err == nil && len(costAgg) > 0 {
		allTimeCost = costAgg[0].CostSum
	}

	// 性能指标：基于最近 5 分钟的数据
	fiveMinAgo := time.Now().Add(-5 * time.Minute)
	recentCount, _ := h.db.UsageLog.Query().
		Where(usagelog.CreatedAtGTE(fiveMinAgo)).
		Count(ctx)

	var recentTokenAgg []struct {
		InputSum    int64 `json:"sum_input_tokens"`
		OutputSum   int64 `json:"sum_output_tokens"`
		CacheSum    int64 `json:"sum_cache_tokens"`
		DurationSum int64 `json:"sum_duration_ms"`
	}
	_ = h.db.UsageLog.Query().
		Where(usagelog.CreatedAtGTE(fiveMinAgo)).
		Aggregate(
			ent.Sum(usagelog.FieldInputTokens),
			ent.Sum(usagelog.FieldOutputTokens),
			ent.Sum(usagelog.FieldCachedInputTokens),
			ent.Sum(usagelog.FieldDurationMs),
		).
		Scan(ctx, &recentTokenAgg)

	rpm := float64(recentCount) / 5.0
	var tpm float64
	if len(recentTokenAgg) > 0 {
		recentTokens := recentTokenAgg[0].InputSum + recentTokenAgg[0].OutputSum + recentTokenAgg[0].CacheSum
		tpm = float64(recentTokens) / 5.0
	}

	// 平均响应时间（今日）
	var avgDuration float64
	if todayRequests > 0 {
		avgDuration = float64(todayDurationSum) / float64(todayRequests)
	}

	response.Success(c, dto.DashboardStatsResp{
		TotalAPIKeys:    int64(totalAPIKeys),
		EnabledAPIKeys:  int64(enabledAPIKeys),
		TotalAccounts:   int64(totalAccounts),
		EnabledAccounts: int64(enabledAccounts),
		ErrorAccounts:   int64(errorAccounts),
		TodayRequests:   todayRequests,
		AllTimeRequests: int64(allTimeRequests),
		TotalUsers:      int64(totalUsers),
		NewUsersToday:   int64(newUsersToday),
		TodayTokens:     todayTokens,
		TodayCost:       todayCost,
		AllTimeTokens:   allTimeTokens,
		AllTimeCost:     allTimeCost,
		RPM:             rpm,
		TPM:             tpm,
		AvgDurationMs:   avgDuration,
		ActiveUsers:     activeUsers,
	})
}

// Trend 返回仪表盘趋势数据（模型分布、Token 趋势、Top 用户）
func (h *DashboardHandler) Trend(c *gin.Context) {
	role, _ := c.Get("role")
	if role != "admin" {
		response.Forbidden(c, "需要管理员权限")
		return
	}

	ctx := c.Request.Context()

	var req dto.DashboardTrendReq
	if err := c.ShouldBindQuery(&req); err != nil {
		response.BindError(c, err)
		return
	}

	// 计算时间范围
	startTime, endTime := parseTrendTimeRange(req)

	// 查询时间范围内的所有日志
	logs, err := h.db.UsageLog.Query().
		Where(
			usagelog.CreatedAtGTE(startTime),
			usagelog.CreatedAtLT(endTime),
		).
		WithUser().
		All(ctx)
	if err != nil {
		slog.Error("查询趋势数据失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	// 1. 模型分布
	modelMap := make(map[string]*dto.DashboardModelStats)
	for _, l := range logs {
		ms, ok := modelMap[l.Model]
		if !ok {
			ms = &dto.DashboardModelStats{Model: l.Model}
			modelMap[l.Model] = ms
		}
		ms.Requests++
		ms.Tokens += int64(l.InputTokens + l.OutputTokens + l.CachedInputTokens)
		ms.ActualCost += l.ActualCost
		ms.StandardCost += l.TotalCost
	}
	modelDist := make([]dto.DashboardModelStats, 0, len(modelMap))
	for _, ms := range modelMap {
		modelDist = append(modelDist, *ms)
	}
	sort.Slice(modelDist, func(i, j int) bool {
		return modelDist[i].Requests > modelDist[j].Requests
	})

	// 2. 用户消费排行
	userMap := make(map[int]*dto.DashboardUserRanking)
	for _, l := range logs {
		var uid int
		var email string
		if l.Edges.User != nil {
			uid = l.Edges.User.ID
			email = l.Edges.User.Email
		}
		ur, ok := userMap[uid]
		if !ok {
			ur = &dto.DashboardUserRanking{UserID: int64(uid), Email: email}
			userMap[uid] = ur
		}
		ur.Requests++
		ur.Tokens += int64(l.InputTokens + l.OutputTokens + l.CachedInputTokens)
		ur.ActualCost += l.ActualCost
		ur.StandardCost += l.TotalCost
	}
	userRanking := make([]dto.DashboardUserRanking, 0, len(userMap))
	for _, ur := range userMap {
		userRanking = append(userRanking, *ur)
	}
	sort.Slice(userRanking, func(i, j int) bool {
		return userRanking[i].ActualCost > userRanking[j].ActualCost
	})

	// 3. Token 使用趋势（按时间桶分组）
	timeFmt := "2006-01-02"
	if req.Granularity == "hour" {
		timeFmt = "2006-01-02 15:00"
	}
	tokenBucketMap := make(map[string]*dto.DashboardTimeBucket)
	for _, l := range logs {
		key := l.CreatedAt.Format(timeFmt)
		tb, ok := tokenBucketMap[key]
		if !ok {
			tb = &dto.DashboardTimeBucket{Time: key}
			tokenBucketMap[key] = tb
		}
		tb.InputTokens += int64(l.InputTokens)
		tb.OutputTokens += int64(l.OutputTokens)
		tb.CachedInput += int64(l.CachedInputTokens)
	}
	tokenTrend := make([]dto.DashboardTimeBucket, 0, len(tokenBucketMap))
	for _, tb := range tokenBucketMap {
		tokenTrend = append(tokenTrend, *tb)
	}
	sort.Slice(tokenTrend, func(i, j int) bool {
		return tokenTrend[i].Time < tokenTrend[j].Time
	})

	// 4. Top 12 用户使用趋势
	// 先找出 top 12 用户（按总 token 排序）
	type userTotal struct {
		uid   int
		email string
		total int64
	}
	userTotalMap := make(map[int]*userTotal)
	for _, l := range logs {
		var uid int
		var email string
		if l.Edges.User != nil {
			uid = l.Edges.User.ID
			email = l.Edges.User.Email
		}
		ut, ok := userTotalMap[uid]
		if !ok {
			ut = &userTotal{uid: uid, email: email}
			userTotalMap[uid] = ut
		}
		ut.total += int64(l.InputTokens + l.OutputTokens + l.CachedInputTokens)
	}
	userTotals := make([]userTotal, 0, len(userTotalMap))
	for _, ut := range userTotalMap {
		userTotals = append(userTotals, *ut)
	}
	sort.Slice(userTotals, func(i, j int) bool {
		return userTotals[i].total > userTotals[j].total
	})
	if len(userTotals) > 12 {
		userTotals = userTotals[:12]
	}

	// 为 top 用户构建时间趋势
	topUserIDs := make(map[int]bool)
	for _, ut := range userTotals {
		topUserIDs[ut.uid] = true
	}
	// userTimeBuckets: uid -> time -> tokens
	userTimeBuckets := make(map[int]map[string]int64)
	for _, l := range logs {
		var uid int
		if l.Edges.User != nil {
			uid = l.Edges.User.ID
		}
		if !topUserIDs[uid] {
			continue
		}
		key := l.CreatedAt.Format(timeFmt)
		if userTimeBuckets[uid] == nil {
			userTimeBuckets[uid] = make(map[string]int64)
		}
		userTimeBuckets[uid][key] += int64(l.InputTokens + l.OutputTokens + l.CachedInputTokens)
	}

	topUsers := make([]dto.DashboardUserTrend, 0, len(userTotals))
	for _, ut := range userTotals {
		buckets := userTimeBuckets[ut.uid]
		trend := make([]dto.DashboardUserTrendPoint, 0, len(buckets))
		for t, tokens := range buckets {
			trend = append(trend, dto.DashboardUserTrendPoint{Time: t, Tokens: tokens})
		}
		sort.Slice(trend, func(i, j int) bool {
			return trend[i].Time < trend[j].Time
		})
		topUsers = append(topUsers, dto.DashboardUserTrend{
			UserID: int64(ut.uid),
			Email:  ut.email,
			Trend:  trend,
		})
	}

	response.Success(c, dto.DashboardTrendResp{
		ModelDistribution: modelDist,
		UserRanking:       userRanking,
		TokenTrend:        tokenTrend,
		TopUsers:          topUsers,
	})
}

// parseTrendTimeRange 解析趋势查询的时间范围
func parseTrendTimeRange(req dto.DashboardTrendReq) (time.Time, time.Time) {
	now := time.Now()
	endTime := now

	switch req.Range {
	case "today":
		return now.Truncate(24 * time.Hour), endTime
	case "7d":
		return now.AddDate(0, 0, -7).Truncate(24 * time.Hour), endTime
	case "30d":
		return now.AddDate(0, 0, -30).Truncate(24 * time.Hour), endTime
	case "90d":
		return now.AddDate(0, 0, -90).Truncate(24 * time.Hour), endTime
	case "custom":
		startTime := now.AddDate(0, 0, -30).Truncate(24 * time.Hour)
		if req.StartDate != "" {
			if t, err := time.Parse("2006-01-02", req.StartDate); err == nil {
				startTime = t
			}
		}
		if req.EndDate != "" {
			if t, err := time.Parse("2006-01-02", req.EndDate); err == nil {
				endTime = t.AddDate(0, 0, 1) // 包含当天
			}
		}
		return startTime, endTime
	default:
		return now.Truncate(24 * time.Hour), endTime
	}
}
