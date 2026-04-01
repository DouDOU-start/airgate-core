package handler

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/account"
	"github.com/DouDOU-start/airgate-core/ent/group"
	"github.com/DouDOU-start/airgate-core/ent/usagelog"
	"github.com/DouDOU-start/airgate-core/ent/user"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// UsageHandler 使用记录 Handler
type UsageHandler struct {
	db *ent.Client
}

// NewUsageHandler 创建 UsageHandler
func NewUsageHandler(db *ent.Client) *UsageHandler {
	return &UsageHandler{db: db}
}

// UserUsage 用户查看自己的使用记录（筛选、分页）
func (h *UsageHandler) UserUsage(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	var q dto.UsageQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		response.BindError(c, err)
		return
	}

	query := h.db.UsageLog.Query().
		Where(usagelog.HasUserWith(user.IDEQ(uid)))

	// 应用筛选条件
	query = applyUsageFilters(query, &q)

	total, err := query.Count(c.Request.Context())
	if err != nil {
		slog.Error("查询使用记录总数失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	logs, err := query.
		WithAPIKey().
		WithAccount().
		Offset((q.Page - 1) * q.PageSize).
		Limit(q.PageSize).
		Order(ent.Desc(usagelog.FieldCreatedAt)).
		All(c.Request.Context())
	if err != nil {
		slog.Error("查询使用记录失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	list := make([]dto.UsageLogResp, 0, len(logs))
	for _, l := range logs {
		list = append(list, toUsageLogResp(l, int64(uid), ""))
	}

	response.Success(c, response.PagedData(list, int64(total), q.Page, q.PageSize))
}

// UserUsageStats 用户聚合统计（支持筛选条件）
func (h *UsageHandler) UserUsageStats(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	var q dto.UsageFilterQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		response.BindError(c, err)
		return
	}

	ctx := c.Request.Context()

	query := h.db.UsageLog.Query().
		Where(usagelog.HasUserWith(user.IDEQ(uid)))
	query = applyFilterQuery(query, &q)

	totalRequests, err := query.Count(ctx)
	if err != nil {
		slog.Error("统计用户请求数失败", "error", err)
		response.InternalError(c, "统计失败")
		return
	}

	// 重新构建 query 进行聚合（Count 会消费 query）
	aggQuery := h.db.UsageLog.Query().
		Where(usagelog.HasUserWith(user.IDEQ(uid)))
	aggQuery = applyFilterQuery(aggQuery, &q)

	var results []struct {
		InputTokens       int64   `json:"input_tokens"`
		OutputTokens      int64   `json:"output_tokens"`
		CachedInputTokens int64   `json:"cached_input_tokens"`
		TotalCost         float64 `json:"total_cost"`
		ActualCost        float64 `json:"actual_cost"`
	}
	err = aggQuery.
		Aggregate(
			ent.As(ent.Sum(usagelog.FieldInputTokens), "input_tokens"),
			ent.As(ent.Sum(usagelog.FieldOutputTokens), "output_tokens"),
			ent.As(ent.Sum(usagelog.FieldCachedInputTokens), "cached_input_tokens"),
			ent.As(ent.Sum(usagelog.FieldTotalCost), "total_cost"),
			ent.As(ent.Sum(usagelog.FieldActualCost), "actual_cost"),
		).
		Scan(ctx, &results)

	var totalTokens int64
	var totalCost, totalActualCost float64
	if err == nil && len(results) > 0 {
		totalTokens = results[0].InputTokens + results[0].OutputTokens + results[0].CachedInputTokens
		totalCost = results[0].TotalCost
		totalActualCost = results[0].ActualCost
	}

	response.Success(c, dto.UsageStatsResp{
		TotalRequests:   int64(totalRequests),
		TotalTokens:     totalTokens,
		TotalCost:       totalCost,
		TotalActualCost: totalActualCost,
	})
}

// AdminUsage 管理员查看全局使用记录
func (h *UsageHandler) AdminUsage(c *gin.Context) {
	var q dto.UsageQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		response.BindError(c, err)
		return
	}

	query := h.db.UsageLog.Query()

	// 用户 ID 筛选
	if q.UserID != nil {
		query = query.Where(usagelog.HasUserWith(user.IDEQ(int(*q.UserID))))
	}

	// 应用其他筛选条件
	query = applyUsageFilters(query, &q)

	total, err := query.Count(c.Request.Context())
	if err != nil {
		slog.Error("查询使用记录总数失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	logs, err := query.
		WithUser().
		WithAPIKey().
		WithAccount().
		Offset((q.Page - 1) * q.PageSize).
		Limit(q.PageSize).
		Order(ent.Desc(usagelog.FieldCreatedAt)).
		All(c.Request.Context())
	if err != nil {
		slog.Error("查询使用记录失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	list := make([]dto.UsageLogResp, 0, len(logs))
	for _, l := range logs {
		var uid int64
		var email string
		if l.Edges.User != nil {
			uid = int64(l.Edges.User.ID)
			email = l.Edges.User.Email
		}
		list = append(list, toUsageLogResp(l, uid, email))
	}

	response.Success(c, response.PagedData(list, int64(total), q.Page, q.PageSize))
}

// AdminUsageStats 管理员聚合统计（支持 group_by 分组）
func (h *UsageHandler) AdminUsageStats(c *gin.Context) {
	var q dto.UsageStatsQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		response.BindError(c, err)
		return
	}

	ctx := c.Request.Context()

	// 构建基础查询（应用所有筛选条件）
	baseQuery := h.db.UsageLog.Query()
	if q.UserID != nil {
		baseQuery = baseQuery.Where(usagelog.HasUserWith(user.IDEQ(int(*q.UserID))))
	}
	baseQuery = applyFilterQuery(baseQuery, &dto.UsageFilterQuery{
		Platform:  q.Platform,
		Model:     q.Model,
		StartDate: q.StartDate,
		EndDate:   q.EndDate,
	})

	// 总请求数
	totalRequests, err := baseQuery.Clone().Count(ctx)
	if err != nil {
		slog.Error("统计总请求数失败", "error", err)
		response.InternalError(c, "统计失败")
		return
	}

	// 使用 Ent 聚合查询获取总计
	var results []struct {
		InputTokens       int64   `json:"sum_input_tokens"`
		OutputTokens      int64   `json:"sum_output_tokens"`
		CachedInputTokens int64   `json:"sum_cached_input_tokens"`
		TotalCost         float64 `json:"sum_total_cost"`
		TotalActualCost   float64 `json:"sum_actual_cost"`
	}
	err = baseQuery.Clone().
		Aggregate(
			ent.Sum(usagelog.FieldInputTokens),
			ent.Sum(usagelog.FieldOutputTokens),
			ent.Sum(usagelog.FieldCachedInputTokens),
			ent.Sum(usagelog.FieldTotalCost),
			ent.Sum(usagelog.FieldActualCost),
		).
		Scan(ctx, &results)

	var totalTokens int64
	var totalCost, totalActualCost float64
	if err == nil && len(results) > 0 {
		totalTokens = results[0].InputTokens + results[0].OutputTokens + results[0].CachedInputTokens
		totalCost = results[0].TotalCost
		totalActualCost = results[0].TotalActualCost
	}

	resp := dto.UsageStatsResp{
		TotalRequests:   int64(totalRequests),
		TotalTokens:     totalTokens,
		TotalCost:       totalCost,
		TotalActualCost: totalActualCost,
	}

	// 分组聚合（支持逗号分隔多维度）
	for _, gb := range strings.Split(q.GroupBy, ",") {
		gb = strings.TrimSpace(gb)
		switch gb {
		case "model":
			resp.ByModel, err = h.statsByModel(ctx, baseQuery.Clone())
		case "user":
			resp.ByUser, err = h.statsByUser(ctx, baseQuery.Clone())
		case "account":
			resp.ByAccount, err = h.statsByAccount(ctx, baseQuery.Clone())
		case "group":
			resp.ByGroup, err = h.statsByGroup(ctx, baseQuery.Clone())
		default:
			continue
		}
		if err != nil {
			slog.Error("分组统计失败", "group_by", gb, "error", err)
			response.InternalError(c, "统计失败")
			return
		}
	}

	response.Success(c, resp)
}

// statsByModel 按模型分组统计
func (h *UsageHandler) statsByModel(ctx context.Context, query *ent.UsageLogQuery) ([]dto.ModelStats, error) {
	var rows []struct {
		Model        string  `json:"model"`
		Count        int     `json:"count"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		TotalCost    float64 `json:"total_cost"`
		ActualCost   float64 `json:"actual_cost"`
	}
	err := query.GroupBy(usagelog.FieldModel).
		Aggregate(
			ent.Count(),
			ent.As(ent.Sum(usagelog.FieldInputTokens), "input_tokens"),
			ent.As(ent.Sum(usagelog.FieldOutputTokens), "output_tokens"),
			ent.As(ent.Sum(usagelog.FieldTotalCost), "total_cost"),
			ent.As(ent.Sum(usagelog.FieldActualCost), "actual_cost"),
		).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	result := make([]dto.ModelStats, 0, len(rows))
	for _, r := range rows {
		result = append(result, dto.ModelStats{
			Model:      r.Model,
			Requests:   int64(r.Count),
			Tokens:     r.InputTokens + r.OutputTokens,
			TotalCost:  r.TotalCost,
			ActualCost: r.ActualCost,
		})
	}
	return result, nil
}

// statsByUser 按用户分组统计
func (h *UsageHandler) statsByUser(ctx context.Context, query *ent.UsageLogQuery) ([]dto.UserStats, error) {
	var rows []struct {
		UserID       int     `json:"user_usage_logs"`
		Count        int     `json:"count"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		TotalCost    float64 `json:"total_cost"`
		ActualCost   float64 `json:"actual_cost"`
	}
	err := query.GroupBy("user_usage_logs").
		Aggregate(
			ent.Count(),
			ent.As(ent.Sum(usagelog.FieldInputTokens), "input_tokens"),
			ent.As(ent.Sum(usagelog.FieldOutputTokens), "output_tokens"),
			ent.As(ent.Sum(usagelog.FieldTotalCost), "total_cost"),
			ent.As(ent.Sum(usagelog.FieldActualCost), "actual_cost"),
		).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	// 批量查询用户 email
	userIDs := make([]int, 0, len(rows))
	for _, r := range rows {
		if r.UserID > 0 {
			userIDs = append(userIDs, r.UserID)
		}
	}
	emailMap := make(map[int]string)
	if len(userIDs) > 0 {
		users, _ := h.db.User.Query().Where(user.IDIn(userIDs...)).All(ctx)
		for _, u := range users {
			emailMap[u.ID] = u.Email
		}
	}
	result := make([]dto.UserStats, 0, len(rows))
	for _, r := range rows {
		result = append(result, dto.UserStats{
			UserID:     int64(r.UserID),
			Email:      emailMap[r.UserID],
			Requests:   int64(r.Count),
			Tokens:     r.InputTokens + r.OutputTokens,
			TotalCost:  r.TotalCost,
			ActualCost: r.ActualCost,
		})
	}
	return result, nil
}

// statsByAccount 按账号分组统计
func (h *UsageHandler) statsByAccount(ctx context.Context, query *ent.UsageLogQuery) ([]dto.AccountStats, error) {
	var rows []struct {
		AccountID    int     `json:"account_usage_logs"`
		Count        int     `json:"count"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		TotalCost    float64 `json:"total_cost"`
		ActualCost   float64 `json:"actual_cost"`
	}
	err := query.GroupBy("account_usage_logs").
		Aggregate(
			ent.Count(),
			ent.As(ent.Sum(usagelog.FieldInputTokens), "input_tokens"),
			ent.As(ent.Sum(usagelog.FieldOutputTokens), "output_tokens"),
			ent.As(ent.Sum(usagelog.FieldTotalCost), "total_cost"),
			ent.As(ent.Sum(usagelog.FieldActualCost), "actual_cost"),
		).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	// 批量查询账号名称
	accountIDs := make([]int, 0, len(rows))
	for _, r := range rows {
		if r.AccountID > 0 {
			accountIDs = append(accountIDs, r.AccountID)
		}
	}
	nameMap := make(map[int]string)
	if len(accountIDs) > 0 {
		accounts, _ := h.db.Account.Query().Where(account.IDIn(accountIDs...)).All(ctx)
		for _, a := range accounts {
			nameMap[a.ID] = a.Name
		}
	}
	result := make([]dto.AccountStats, 0, len(rows))
	for _, r := range rows {
		result = append(result, dto.AccountStats{
			AccountID:  int64(r.AccountID),
			Name:       nameMap[r.AccountID],
			Requests:   int64(r.Count),
			Tokens:     r.InputTokens + r.OutputTokens,
			TotalCost:  r.TotalCost,
			ActualCost: r.ActualCost,
		})
	}
	return result, nil
}

// statsByGroup 按分组统计
func (h *UsageHandler) statsByGroup(ctx context.Context, query *ent.UsageLogQuery) ([]dto.GroupStats, error) {
	var rows []struct {
		GroupID      int     `json:"group_usage_logs"`
		Count        int     `json:"count"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		TotalCost    float64 `json:"total_cost"`
		ActualCost   float64 `json:"actual_cost"`
	}
	err := query.GroupBy("group_usage_logs").
		Aggregate(
			ent.Count(),
			ent.As(ent.Sum(usagelog.FieldInputTokens), "input_tokens"),
			ent.As(ent.Sum(usagelog.FieldOutputTokens), "output_tokens"),
			ent.As(ent.Sum(usagelog.FieldTotalCost), "total_cost"),
			ent.As(ent.Sum(usagelog.FieldActualCost), "actual_cost"),
		).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	// 批量查询分组名称
	groupIDs := make([]int, 0, len(rows))
	for _, r := range rows {
		if r.GroupID > 0 {
			groupIDs = append(groupIDs, r.GroupID)
		}
	}
	nameMap := make(map[int]string)
	if len(groupIDs) > 0 {
		groups, _ := h.db.Group.Query().Where(group.IDIn(groupIDs...)).All(ctx)
		for _, g := range groups {
			nameMap[g.ID] = g.Name
		}
	}
	result := make([]dto.GroupStats, 0, len(rows))
	for _, r := range rows {
		result = append(result, dto.GroupStats{
			GroupID:    int64(r.GroupID),
			Name:       nameMap[r.GroupID],
			Requests:   int64(r.Count),
			Tokens:     r.InputTokens + r.OutputTokens,
			TotalCost:  r.TotalCost,
			ActualCost: r.ActualCost,
		})
	}
	return result, nil
}

// applyFilterQuery 应用筛选条件（不含分页）
func applyFilterQuery(query *ent.UsageLogQuery, q *dto.UsageFilterQuery) *ent.UsageLogQuery {
	if q.Platform != "" {
		query = query.Where(usagelog.PlatformEQ(q.Platform))
	}
	if q.Model != "" {
		query = query.Where(usagelog.ModelContains(q.Model))
	}
	if q.StartDate != "" {
		t, err := time.Parse("2006-01-02", q.StartDate)
		if err == nil {
			query = query.Where(usagelog.CreatedAtGTE(t))
		}
	}
	if q.EndDate != "" {
		t, err := time.Parse("2006-01-02", q.EndDate)
		if err == nil {
			query = query.Where(usagelog.CreatedAtLT(t.AddDate(0, 0, 1)))
		}
	}
	return query
}

// applyUsageFilters 应用使用记录的通用筛选条件
func applyUsageFilters(query *ent.UsageLogQuery, q *dto.UsageQuery) *ent.UsageLogQuery {
	return applyFilterQuery(query, &dto.UsageFilterQuery{
		Platform:  q.Platform,
		Model:     q.Model,
		StartDate: q.StartDate,
		EndDate:   q.EndDate,
	})
}

// AdminUsageTrend 管理员 Token 使用趋势
func (h *UsageHandler) AdminUsageTrend(c *gin.Context) {
	var q dto.UsageTrendQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		response.BindError(c, err)
		return
	}

	ctx := c.Request.Context()

	// 构建查询
	query := h.db.UsageLog.Query()
	if q.UserID != nil {
		query = query.Where(usagelog.HasUserWith(user.IDEQ(int(*q.UserID))))
	}
	query = applyFilterQuery(query, &dto.UsageFilterQuery{
		Platform:  q.Platform,
		Model:     q.Model,
		StartDate: q.StartDate,
		EndDate:   q.EndDate,
	})

	// 如果没有指定时间范围，默认最近 24 小时
	if q.StartDate == "" && q.EndDate == "" {
		query = query.Where(usagelog.CreatedAtGTE(time.Now().Add(-24 * time.Hour)))
	}

	logs, err := query.All(ctx)
	if err != nil {
		slog.Error("查询趋势数据失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	// 按时间桶聚合
	timeFmt := "2006-01-02"
	if q.Granularity == "hour" {
		timeFmt = "2006-01-02 15:00"
	}
	bucketMap := make(map[string]*dto.UsageTrendBucket)
	for _, l := range logs {
		key := l.CreatedAt.Format(timeFmt)
		tb, ok := bucketMap[key]
		if !ok {
			tb = &dto.UsageTrendBucket{Time: key}
			bucketMap[key] = tb
		}
		tb.InputTokens += int64(l.InputTokens)
		tb.OutputTokens += int64(l.OutputTokens)
		tb.CacheRead += int64(l.CachedInputTokens)
		tb.ActualCost += l.ActualCost
		tb.StandardCost += l.TotalCost
	}

	trend := make([]dto.UsageTrendBucket, 0, len(bucketMap))
	for _, tb := range bucketMap {
		trend = append(trend, *tb)
	}
	sort.Slice(trend, func(i, j int) bool {
		return trend[i].Time < trend[j].Time
	})

	response.Success(c, trend)
}

// toUsageLogResp 将 ent.UsageLog 转换为 dto.UsageLogResp
func toUsageLogResp(l *ent.UsageLog, userID int64, userEmail string) dto.UsageLogResp {
	var apiKeyName, apiKeyHint string
	apiKeyDeleted := l.Edges.APIKey == nil
	if !apiKeyDeleted {
		apiKeyName = l.Edges.APIKey.Name
		apiKeyHint = l.Edges.APIKey.KeyHint
	}
	var accountID int64
	var accountName string
	if l.Edges.Account != nil {
		accountID = int64(l.Edges.Account.ID)
		if email, ok := l.Edges.Account.Credentials["email"]; ok && email != "" {
			accountName = email
		} else {
			accountName = l.Edges.Account.Name
		}
	}
	return dto.UsageLogResp{
		ID:                    int64(l.ID),
		UserID:                userID,
		UserEmail:             userEmail,
		APIKeyName:            apiKeyName,
		APIKeyHint:            apiKeyHint,
		APIKeyDeleted:         apiKeyDeleted,
		AccountID:             accountID,
		AccountName:           accountName,
		Platform:              l.Platform,
		Model:                 l.Model,
		InputTokens:           l.InputTokens,
		OutputTokens:          l.OutputTokens,
		CachedInputTokens:     l.CachedInputTokens,
		ReasoningOutputTokens: l.ReasoningOutputTokens,
		InputCost:             l.InputCost,
		OutputCost:            l.OutputCost,
		CachedInputCost:       l.CachedInputCost,
		TotalCost:             l.TotalCost,
		ActualCost:            l.ActualCost,
		RateMultiplier:        l.RateMultiplier,
		AccountRateMultiplier: l.AccountRateMultiplier,
		ServiceTier:           l.ServiceTier,
		Stream:                l.Stream,
		DurationMs:            l.DurationMs,
		FirstTokenMs:          l.FirstTokenMs,
		UserAgent:             l.UserAgent,
		IPAddress:             l.IPAddress,
		CreatedAt:             l.CreatedAt.Format(time.RFC3339),
	}
}
