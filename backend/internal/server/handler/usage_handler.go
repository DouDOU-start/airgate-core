package handler

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
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
		list = append(list, toUsageLogResp(l, int64(uid)))
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
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		TotalCost    float64 `json:"total_cost"`
		ActualCost   float64 `json:"actual_cost"`
	}
	err = aggQuery.
		Aggregate(
			ent.As(ent.Sum(usagelog.FieldInputTokens), "input_tokens"),
			ent.As(ent.Sum(usagelog.FieldOutputTokens), "output_tokens"),
			ent.As(ent.Sum(usagelog.FieldTotalCost), "total_cost"),
			ent.As(ent.Sum(usagelog.FieldActualCost), "actual_cost"),
		).
		Scan(ctx, &results)

	var totalTokens int64
	var totalCost, totalActualCost float64
	if err == nil && len(results) > 0 {
		totalTokens = results[0].InputTokens + results[0].OutputTokens
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
		if l.Edges.User != nil {
			uid = int64(l.Edges.User.ID)
		}
		list = append(list, toUsageLogResp(l, uid))
	}

	response.Success(c, response.PagedData(list, int64(total), q.Page, q.PageSize))
}

// AdminUsageStats 管理员聚合统计
func (h *UsageHandler) AdminUsageStats(c *gin.Context) {
	ctx := c.Request.Context()

	// 总请求数
	totalRequests, err := h.db.UsageLog.Query().Count(ctx)
	if err != nil {
		slog.Error("统计总请求数失败", "error", err)
		response.InternalError(c, "统计失败")
		return
	}

	// 使用 Ent 聚合查询获取总计
	var results []struct {
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		TotalCost    float64 `json:"total_cost"`
		ActualCost   float64 `json:"actual_cost"`
	}
	err = h.db.UsageLog.Query().
		Aggregate(
			ent.As(ent.Sum(usagelog.FieldInputTokens), "input_tokens"),
			ent.As(ent.Sum(usagelog.FieldOutputTokens), "output_tokens"),
			ent.As(ent.Sum(usagelog.FieldTotalCost), "total_cost"),
			ent.As(ent.Sum(usagelog.FieldActualCost), "actual_cost"),
		).
		Scan(ctx, &results)

	var totalTokens int64
	var totalCost, totalActualCost float64
	if err == nil && len(results) > 0 {
		totalTokens = results[0].InputTokens + results[0].OutputTokens
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

// applyFilterQuery 应用筛选条件（不含分页）
func applyFilterQuery(query *ent.UsageLogQuery, q *dto.UsageFilterQuery) *ent.UsageLogQuery {
	if q.Platform != "" {
		query = query.Where(usagelog.PlatformEQ(q.Platform))
	}
	if q.Model != "" {
		query = query.Where(usagelog.ModelEQ(q.Model))
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

// toUsageLogResp 将 ent.UsageLog 转换为 dto.UsageLogResp
func toUsageLogResp(l *ent.UsageLog, userID int64) dto.UsageLogResp {
	return dto.UsageLogResp{
		ID:                    int64(l.ID),
		UserID:                userID,
		Platform:              l.Platform,
		Model:                 l.Model,
		InputTokens:           l.InputTokens,
		OutputTokens:          l.OutputTokens,
		CacheTokens:           l.CacheTokens,
		InputCost:             l.InputCost,
		OutputCost:            l.OutputCost,
		CacheCost:             l.CacheCost,
		TotalCost:             l.TotalCost,
		ActualCost:            l.ActualCost,
		RateMultiplier:        l.RateMultiplier,
		AccountRateMultiplier: l.AccountRateMultiplier,
		Stream:                l.Stream,
		DurationMs:            l.DurationMs,
		FirstTokenMs:          l.FirstTokenMs,
		UserAgent:             l.UserAgent,
		IPAddress:             l.IPAddress,
		CreatedAt:             l.CreatedAt.Format(time.RFC3339),
	}
}
