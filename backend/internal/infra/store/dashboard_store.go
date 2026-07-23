package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/DouDOU-start/airgate-core/ent"
	entapikey "github.com/DouDOU-start/airgate-core/ent/apikey"
	entchannelkey "github.com/DouDOU-start/airgate-core/ent/channelkey"
	"github.com/DouDOU-start/airgate-core/ent/predicate"
	entusagelog "github.com/DouDOU-start/airgate-core/ent/usagelog"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
	appdashboard "github.com/DouDOU-start/airgate-core/internal/app/dashboard"
	"github.com/DouDOU-start/airgate-core/internal/pkg/usagemodel"
)

// DashboardStore 使用 Ent 实现仪表盘仓储。
type DashboardStore struct {
	db         *ent.Client
	rdb        *redis.Client
	sqlDialect string
}

// NewDashboardStore 创建仪表盘仓储。sqlDialect 传 dialect.Postgres 以启用 SQL 侧分桶聚合。
func NewDashboardStore(db *ent.Client, sqlDialect string, rdb ...*redis.Client) *DashboardStore {
	var cache *redis.Client
	if len(rdb) > 0 {
		cache = rdb[0]
	}
	return &DashboardStore{db: db, rdb: cache, sqlDialect: sqlDialect}
}

const dashboardStatsCacheTTL = 10 * time.Second
const dashboardStatsLockTTL = 5 * time.Second
const dashboardStatsLockWait = 1 * time.Second

var dashboardStatsLockReleaseScript = redis.NewScript(`
	local key = KEYS[1]
	local token = ARGV[1]
	if redis.call('GET', key) == token then
		return redis.call('DEL', key)
	end
	return 0
`)

// LoadStatsSnapshot 读取统计快照。userID 为 0 表示查全部。
func (s *DashboardStore) LoadStatsSnapshot(ctx context.Context, todayStart, fiveMinAgo time.Time, userID int) (appdashboard.StatsSnapshot, error) {
	if snapshot, ok := s.loadStatsSnapshotCache(ctx, userID, todayStart); ok {
		return snapshot, nil
	}

	if token, ok, lockBusy := s.tryLockStatsSnapshot(ctx, userID, todayStart); ok {
		defer s.releaseStatsSnapshotLock(context.Background(), userID, todayStart, token)

		if snapshot, ok := s.loadStatsSnapshotCache(ctx, userID, todayStart); ok {
			return snapshot, nil
		}

		snapshot, err := s.loadStatsSnapshotFresh(ctx, todayStart, fiveMinAgo, userID)
		if err != nil {
			return appdashboard.StatsSnapshot{}, err
		}
		s.storeStatsSnapshotCache(ctx, userID, todayStart, snapshot)
		return snapshot, nil
	} else if lockBusy {
		if snapshot, ok := s.waitForStatsSnapshotCache(ctx, userID, todayStart, dashboardStatsLockWait); ok {
			return snapshot, nil
		}
	}

	snapshot, err := s.loadStatsSnapshotFresh(ctx, todayStart, fiveMinAgo, userID)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}
	s.storeStatsSnapshotCache(ctx, userID, todayStart, snapshot)
	return snapshot, nil
}

// ListTrendLogs 读取趋势聚合所需日志。userID / channelID / channelKeyID 为 0 表示不过滤该维度。
func (s *DashboardStore) ListTrendLogs(ctx context.Context, startTime, endTime time.Time, userID, channelID, channelKeyID int) ([]appdashboard.TrendLog, error) {
	preds := []predicate.UsageLog{
		entusagelog.CreatedAtGTE(startTime),
		entusagelog.CreatedAtLT(endTime),
	}
	if userID > 0 {
		preds = append(preds, usageUserPredicate(int64(userID)))
	}
	if channelID > 0 {
		preds = append(preds, entusagelog.ChannelIDEQ(channelID))
	}
	if channelKeyID > 0 {
		preds = append(preds, entusagelog.ChannelKeyIDEQ(channelKeyID))
	}

	const trendLogLimit = 50000
	list, err := s.db.UsageLog.Query().
		Where(preds...).
		Select(
			entusagelog.FieldUserIDSnapshot,
			entusagelog.FieldUserEmailSnapshot,
			entusagelog.FieldModel,
			entusagelog.FieldInputTokens,
			entusagelog.FieldOutputTokens,
			entusagelog.FieldCachedInputTokens,
			entusagelog.FieldCacheCreationTokens,
			entusagelog.FieldActualCost,
			entusagelog.FieldTotalCost,
			entusagelog.FieldAccountRateMultiplier,
			entusagelog.FieldCreatedAt,
		).
		Order(ent.Desc(entusagelog.FieldCreatedAt)).
		Limit(trendLogLimit).
		All(ctx)
	if err != nil {
		return nil, err
	}

	emailMap := make(map[int]string)
	userIDs := make([]int, 0, len(list))
	seenUserIDs := make(map[int]struct{}, len(list))
	for _, item := range list {
		if item.UserIDSnapshot > 0 && item.UserEmailSnapshot == "" {
			if _, ok := seenUserIDs[item.UserIDSnapshot]; ok {
				continue
			}
			seenUserIDs[item.UserIDSnapshot] = struct{}{}
			userIDs = append(userIDs, item.UserIDSnapshot)
		}
	}
	if len(userIDs) > 0 {
		users, err := s.db.User.Query().Where(entuser.IDIn(userIDs...)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range users {
			emailMap[item.ID] = item.Email
		}
	}

	result := make([]appdashboard.TrendLog, 0, len(list))
	for _, item := range list {
		log := appdashboard.TrendLog{
			UserID:              item.UserIDSnapshot,
			UserEmail:           coalesceString(item.UserEmailSnapshot, emailMap[item.UserIDSnapshot]),
			Model:               item.Model,
			InputTokens:         int64(item.InputTokens),
			OutputTokens:        int64(item.OutputTokens),
			CachedInputTokens:   int64(item.CachedInputTokens),
			CacheCreationTokens: int64(item.CacheCreationTokens),
			ActualCost:          item.ActualCost,
			StandardCost:        item.TotalCost,
			ChannelCost:         item.TotalCost * item.AccountRateMultiplier,
			CreatedAt:           item.CreatedAt,
		}
		result = append(result, log)
	}

	return result, nil
}

// AggregatedTrend 在 Postgres 侧完成 4 种分桶聚合，避免把数万行原始记录拉进内存。
// 返回 (Trend{}, false, nil) 表示不支持（非 Postgres / 无效时区），调用方应回退 ListTrendLogs。
func (s *DashboardStore) AggregatedTrend(ctx context.Context, q appdashboard.AggregatedTrendQuery) (appdashboard.Trend, bool, error) {
	if s.sqlDialect != dialect.Postgres || q.TZName == "" {
		return appdashboard.Trend{}, false, nil
	}
	if _, err := time.LoadLocation(q.TZName); err != nil {
		return appdashboard.Trend{}, false, nil
	}

	baseQuery := func() *ent.UsageLogQuery {
		preds := []predicate.UsageLog{
			entusagelog.CreatedAtGTE(q.StartTime),
			entusagelog.CreatedAtLT(q.EndTime),
		}
		if q.UserID > 0 {
			preds = append(preds, usageUserPredicate(int64(q.UserID)))
		}
		if q.ChannelID > 0 {
			preds = append(preds, entusagelog.ChannelIDEQ(q.ChannelID))
		}
		if q.ChannelKeyID > 0 {
			preds = append(preds, entusagelog.ChannelKeyIDEQ(q.ChannelKeyID))
		}
		return s.db.UsageLog.Query().Where(preds...)
	}

	unit := "day"
	if q.Granularity == "hour" {
		unit = "hour"
	}
	tzLit := sqlStringLiteral(q.TZName)

	// 1) Token 趋势：按时间桶聚合
	tokenTrend, err := s.aggTokenTrendPG(ctx, baseQuery(), unit, tzLit, q.Loc, q.FillKeys)
	if err != nil {
		return appdashboard.Trend{}, false, err
	}

	// 2) 模型分布：按 model 聚合
	modelDist, err := s.aggModelDistPG(ctx, baseQuery(), tzLit)
	if err != nil {
		return appdashboard.Trend{}, false, err
	}

	// 3) 用户排行：按 user_id 聚合
	userRanking, err := s.aggUserRankingPG(ctx, baseQuery())
	if err != nil {
		return appdashboard.Trend{}, false, err
	}

	// 4) Top 用户趋势：先选 Top 12，再按 (user_id, 时间桶) 聚合
	topUsers, err := s.aggTopUsersPG(ctx, baseQuery(), unit, tzLit, q.Loc, q.FillKeys)
	if err != nil {
		return appdashboard.Trend{}, false, err
	}

	return appdashboard.Trend{
		ModelDistribution: modelDist,
		UserRanking:       userRanking,
		TokenTrend:        tokenTrend,
		TopUsers:          topUsers,
	}, true, nil
}

func (s *DashboardStore) aggTokenTrendPG(ctx context.Context, query *ent.UsageLogQuery, unit, tzLit string, loc *time.Location, fillKeys []string) ([]appdashboard.TimeBucket, error) {
	var rows []struct {
		Bucket              time.Time `json:"bucket"`
		Requests            int64     `json:"requests"`
		InputTokens         int64     `json:"input_tokens"`
		OutputTokens        int64     `json:"output_tokens"`
		CachedInputTokens   int64     `json:"cached_input_tokens"`
		CacheCreationTokens int64     `json:"cache_creation_tokens"`
		ActualCost          float64   `json:"actual_cost"`
		TotalCost           float64   `json:"total_cost"`
		ChannelCost         float64   `json:"channel_cost"`
	}
	err := query.Modify(func(sel *entsql.Selector) {
		bucket := fmt.Sprintf("date_trunc('%s', %s AT TIME ZONE %s)", unit, sel.C(entusagelog.FieldCreatedAt), tzLit)
		chCost := fmt.Sprintf("COALESCE(SUM(%s * %s), 0)", sel.C(entusagelog.FieldTotalCost), sel.C(entusagelog.FieldAccountRateMultiplier))
		sel.Select(
			entsql.As(bucket, "bucket"),
			entsql.As("COUNT(*)", "requests"),
			entsql.As("COALESCE(SUM("+sel.C(entusagelog.FieldInputTokens)+"), 0)", "input_tokens"),
			entsql.As("COALESCE(SUM("+sel.C(entusagelog.FieldOutputTokens)+"), 0)", "output_tokens"),
			entsql.As("COALESCE(SUM("+sel.C(entusagelog.FieldCachedInputTokens)+"), 0)", "cached_input_tokens"),
			entsql.As("COALESCE(SUM("+sel.C(entusagelog.FieldCacheCreationTokens)+"), 0)", "cache_creation_tokens"),
			entsql.As("COALESCE(SUM("+sel.C(entusagelog.FieldActualCost)+"), 0)", "actual_cost"),
			entsql.As("COALESCE(SUM("+sel.C(entusagelog.FieldTotalCost)+"), 0)", "total_cost"),
			entsql.As(chCost, "channel_cost"),
		).GroupBy("bucket")
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	layout := "2006-01-02"
	if unit == "hour" {
		layout = "2006-01-02 15:00"
	}

	bucketMap := make(map[string]*appdashboard.TimeBucket, len(rows))
	for _, row := range rows {
		b := row.Bucket
		key := time.Date(b.Year(), b.Month(), b.Day(), b.Hour(), b.Minute(), b.Second(), 0, loc).Format(layout)
		bucketMap[key] = &appdashboard.TimeBucket{
			Time:          key,
			Requests:      row.Requests,
			InputTokens:   row.InputTokens,
			OutputTokens:  row.OutputTokens,
			CachedInput:   row.CachedInputTokens,
			CacheCreation: row.CacheCreationTokens,
			ActualCost:    row.ActualCost,
			StandardCost:  row.TotalCost,
			ChannelCost:   row.ChannelCost,
		}
	}
	for _, key := range fillKeys {
		if bucketMap[key] == nil {
			bucketMap[key] = &appdashboard.TimeBucket{Time: key}
		}
	}

	result := make([]appdashboard.TimeBucket, 0, len(bucketMap))
	for _, item := range bucketMap {
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Time < result[j].Time })
	return result, nil
}

func (s *DashboardStore) aggModelDistPG(ctx context.Context, query *ent.UsageLogQuery, tzLit string) ([]appdashboard.ModelStats, error) {
	var rows []struct {
		Model       string  `json:"model"`
		Requests    int64   `json:"requests"`
		Tokens      int64   `json:"tokens"`
		ActualCost  float64 `json:"actual_cost"`
		TotalCost   float64 `json:"total_cost"`
		ChannelCost float64 `json:"channel_cost"`
	}
	err := query.Modify(func(sel *entsql.Selector) {
		tokens := fmt.Sprintf("COALESCE(SUM(%s), 0) + COALESCE(SUM(%s), 0) + COALESCE(SUM(%s), 0) + COALESCE(SUM(%s), 0)",
			sel.C(entusagelog.FieldInputTokens), sel.C(entusagelog.FieldOutputTokens),
			sel.C(entusagelog.FieldCachedInputTokens), sel.C(entusagelog.FieldCacheCreationTokens))
		chCost := fmt.Sprintf("COALESCE(SUM(%s * %s), 0)", sel.C(entusagelog.FieldTotalCost), sel.C(entusagelog.FieldAccountRateMultiplier))
		sel.Select(
			sel.C(entusagelog.FieldModel),
			entsql.As("COUNT(*)", "requests"),
			entsql.As(tokens, "tokens"),
			entsql.As("COALESCE(SUM("+sel.C(entusagelog.FieldActualCost)+"), 0)", "actual_cost"),
			entsql.As("COALESCE(SUM("+sel.C(entusagelog.FieldTotalCost)+"), 0)", "total_cost"),
			entsql.As(chCost, "channel_cost"),
		).GroupBy(sel.C(entusagelog.FieldModel))
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	result := make([]appdashboard.ModelStats, 0, len(rows))
	for _, row := range rows {
		result = append(result, appdashboard.ModelStats{
			Model:        row.Model,
			Requests:     row.Requests,
			Tokens:       row.Tokens,
			ActualCost:   row.ActualCost,
			StandardCost: row.TotalCost,
			ChannelCost:  row.ChannelCost,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ActualCost > result[j].ActualCost })
	return result, nil
}

func (s *DashboardStore) aggUserRankingPG(ctx context.Context, query *ent.UsageLogQuery) ([]appdashboard.UserRanking, error) {
	var rows []struct {
		UserID     int     `json:"user_id_snapshot"`
		Email      string  `json:"email"`
		Requests   int64   `json:"requests"`
		Tokens     int64   `json:"tokens"`
		ActualCost float64 `json:"actual_cost"`
		TotalCost  float64 `json:"total_cost"`
	}
	err := query.Modify(func(sel *entsql.Selector) {
		tokens := fmt.Sprintf("COALESCE(SUM(%s), 0) + COALESCE(SUM(%s), 0) + COALESCE(SUM(%s), 0) + COALESCE(SUM(%s), 0)",
			sel.C(entusagelog.FieldInputTokens), sel.C(entusagelog.FieldOutputTokens),
			sel.C(entusagelog.FieldCachedInputTokens), sel.C(entusagelog.FieldCacheCreationTokens))
		sel.Select(
			sel.C(entusagelog.FieldUserIDSnapshot),
			entsql.As("MAX("+sel.C(entusagelog.FieldUserEmailSnapshot)+")", "email"),
			entsql.As("COUNT(*)", "requests"),
			entsql.As(tokens, "tokens"),
			entsql.As("COALESCE(SUM("+sel.C(entusagelog.FieldActualCost)+"), 0)", "actual_cost"),
			entsql.As("COALESCE(SUM("+sel.C(entusagelog.FieldTotalCost)+"), 0)", "total_cost"),
		).GroupBy(sel.C(entusagelog.FieldUserIDSnapshot))
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	result := make([]appdashboard.UserRanking, 0, len(rows))
	for _, row := range rows {
		result = append(result, appdashboard.UserRanking{
			UserID:       int64(row.UserID),
			Email:        row.Email,
			Requests:     row.Requests,
			Tokens:       row.Tokens,
			ActualCost:   row.ActualCost,
			StandardCost: row.TotalCost,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ActualCost > result[j].ActualCost })
	return result, nil
}

func (s *DashboardStore) aggTopUsersPG(ctx context.Context, query *ent.UsageLogQuery, unit, tzLit string, loc *time.Location, fillKeys []string) ([]appdashboard.UserTrend, error) {
	// 第一步：找 Top 12 用户
	var topRows []struct {
		UserID int    `json:"user_id_snapshot"`
		Email  string `json:"email"`
		Tokens int64  `json:"tokens"`
	}
	err := query.Clone().Modify(func(sel *entsql.Selector) {
		tokens := fmt.Sprintf("COALESCE(SUM(%s), 0) + COALESCE(SUM(%s), 0) + COALESCE(SUM(%s), 0) + COALESCE(SUM(%s), 0)",
			sel.C(entusagelog.FieldInputTokens), sel.C(entusagelog.FieldOutputTokens),
			sel.C(entusagelog.FieldCachedInputTokens), sel.C(entusagelog.FieldCacheCreationTokens))
		sel.Select(
			sel.C(entusagelog.FieldUserIDSnapshot),
			entsql.As("MAX("+sel.C(entusagelog.FieldUserEmailSnapshot)+")", "email"),
			entsql.As(tokens, "tokens"),
		).GroupBy(sel.C(entusagelog.FieldUserIDSnapshot)).
			OrderBy(entsql.Desc("tokens")).
			Limit(12)
	}).Scan(ctx, &topRows)
	if err != nil {
		return nil, err
	}
	if len(topRows) == 0 {
		return nil, nil
	}

	topUserIDs := make([]int, 0, len(topRows))
	emailByID := make(map[int]string, len(topRows))
	for _, row := range topRows {
		topUserIDs = append(topUserIDs, row.UserID)
		emailByID[row.UserID] = row.Email
	}

	// 第二步：按 (user_id, 时间桶) 聚合
	var trendRows []struct {
		UserID int       `json:"user_id_snapshot"`
		Bucket time.Time `json:"bucket"`
		Tokens int64     `json:"tokens"`
	}
	err = query.Where(entusagelog.UserIDSnapshotIn(topUserIDs...)).
		Modify(func(sel *entsql.Selector) {
			bucket := fmt.Sprintf("date_trunc('%s', %s AT TIME ZONE %s)", unit, sel.C(entusagelog.FieldCreatedAt), tzLit)
			tokens := fmt.Sprintf("COALESCE(SUM(%s), 0) + COALESCE(SUM(%s), 0) + COALESCE(SUM(%s), 0) + COALESCE(SUM(%s), 0)",
				sel.C(entusagelog.FieldInputTokens), sel.C(entusagelog.FieldOutputTokens),
				sel.C(entusagelog.FieldCachedInputTokens), sel.C(entusagelog.FieldCacheCreationTokens))
			sel.Select(
				sel.C(entusagelog.FieldUserIDSnapshot),
				entsql.As(bucket, "bucket"),
				entsql.As(tokens, "tokens"),
			).GroupBy(sel.C(entusagelog.FieldUserIDSnapshot), "bucket")
		}).Scan(ctx, &trendRows)
	if err != nil {
		return nil, err
	}

	layout := "2006-01-02"
	if unit == "hour" {
		layout = "2006-01-02 15:00"
	}

	userBuckets := make(map[int]map[string]int64, len(topUserIDs))
	for _, row := range trendRows {
		b := row.Bucket
		key := time.Date(b.Year(), b.Month(), b.Day(), b.Hour(), b.Minute(), b.Second(), 0, loc).Format(layout)
		if userBuckets[row.UserID] == nil {
			userBuckets[row.UserID] = make(map[string]int64)
		}
		userBuckets[row.UserID][key] = row.Tokens
	}
	for _, buckets := range userBuckets {
		for _, key := range fillKeys {
			if _, ok := buckets[key]; !ok {
				buckets[key] = 0
			}
		}
	}

	result := make([]appdashboard.UserTrend, 0, len(topRows))
	for _, top := range topRows {
		points := make([]appdashboard.UserTrendPoint, 0, len(userBuckets[top.UserID]))
		for key, tokens := range userBuckets[top.UserID] {
			points = append(points, appdashboard.UserTrendPoint{Time: key, Tokens: tokens})
		}
		sort.Slice(points, func(i, j int) bool { return points[i].Time < points[j].Time })
		result = append(result, appdashboard.UserTrend{
			UserID: int64(top.UserID),
			Email:  emailByID[top.UserID],
			Trend:  points,
		})
	}
	return result, nil
}

type usageTotals struct {
	Requests     int64
	Tokens       int64
	Cost         float64
	StandardCost float64
	ChannelCost  float64
}

// usageLogChannelCostSum 渠道成本聚合：Σ(total_cost × account_rate_multiplier)（查询期现算，见 usagelog schema）。
func usageLogChannelCostSum() ent.AggregateFunc {
	return func(s *entsql.Selector) string {
		return "COALESCE(SUM(" + s.C(entusagelog.FieldTotalCost) + " * " + s.C(entusagelog.FieldAccountRateMultiplier) + "), 0)"
	}
}

type usageTodaySnapshot struct {
	Requests           int64
	ImageRequests      int64
	NonImageRequests   int64
	Tokens             int64
	Cost               float64
	StandardCost       float64
	ChannelCost        float64
	NonImageDurationMs int64
	FirstTokenRequests int64
	FirstTokenMs       int64
	ImageDurationMs    int64
	ActiveUsers        int64
}

func queryUsageTotals(ctx context.Context, query *ent.UsageLogQuery) (usageTotals, error) {
	var rows []struct {
		Count            int     `json:"count"`
		InputSum         int64   `json:"input_sum"`
		OutputSum        int64   `json:"output_sum"`
		CacheSum         int64   `json:"cache_sum"`
		CacheCreationSum int64   `json:"cache_creation_sum"`
		CostSum          float64 `json:"cost_sum"`
		StandardCostSum  float64 `json:"standard_cost_sum"`
		ChannelCostSum   float64 `json:"channel_cost_sum"`
	}
	if err := query.Clone().Aggregate(
		ent.Count(),
		ent.As(ent.Sum(entusagelog.FieldInputTokens), "input_sum"),
		ent.As(ent.Sum(entusagelog.FieldOutputTokens), "output_sum"),
		ent.As(ent.Sum(entusagelog.FieldCachedInputTokens), "cache_sum"),
		ent.As(ent.Sum(entusagelog.FieldCacheCreationTokens), "cache_creation_sum"),
		ent.As(ent.Sum(entusagelog.FieldActualCost), "cost_sum"),
		ent.As(ent.Sum(entusagelog.FieldTotalCost), "standard_cost_sum"),
		ent.As(usageLogChannelCostSum(), "channel_cost_sum"),
	).Scan(ctx, &rows); err != nil {
		return usageTotals{}, err
	}
	if len(rows) == 0 {
		return usageTotals{}, nil
	}
	return usageTotals{
		Requests:     int64(rows[0].Count),
		Tokens:       rows[0].InputSum + rows[0].OutputSum + rows[0].CacheSum + rows[0].CacheCreationSum,
		Cost:         rows[0].CostSum,
		StandardCost: rows[0].StandardCostSum,
		ChannelCost:  rows[0].ChannelCostSum,
	}, nil
}

func queryTodayUsageSnapshot(ctx context.Context, query *ent.UsageLogQuery, todayStart time.Time) (usageTodaySnapshot, error) {
	var rows []struct {
		Count              int     `json:"count"`
		InputSum           int64   `json:"input_sum"`
		OutputSum          int64   `json:"output_sum"`
		CacheSum           int64   `json:"cache_sum"`
		CacheCreationSum   int64   `json:"cache_creation_sum"`
		CostSum            float64 `json:"cost_sum"`
		StandardCostSum    float64 `json:"standard_cost_sum"`
		ChannelCostSum     float64 `json:"channel_cost_sum"`
		ImageRequests      int64   `json:"image_requests"`
		NonImageRequests   int64   `json:"non_image_requests"`
		NonImageDurationMs int64   `json:"non_image_duration_ms"`
		FirstTokenRequests int64   `json:"first_token_requests"`
		FirstTokenMs       int64   `json:"first_token_ms"`
		ImageDurationMs    int64   `json:"image_duration_ms"`
		ActiveUsers        int64   `json:"active_users"`
	}
	if err := query.Clone().
		Where(entusagelog.CreatedAtGTE(todayStart)).
		Aggregate(
			ent.Count(),
			ent.As(ent.Sum(entusagelog.FieldInputTokens), "input_sum"),
			ent.As(ent.Sum(entusagelog.FieldOutputTokens), "output_sum"),
			ent.As(ent.Sum(entusagelog.FieldCachedInputTokens), "cache_sum"),
			ent.As(ent.Sum(entusagelog.FieldCacheCreationTokens), "cache_creation_sum"),
			ent.As(ent.Sum(entusagelog.FieldActualCost), "cost_sum"),
			ent.As(ent.Sum(entusagelog.FieldTotalCost), "standard_cost_sum"),
			ent.As(usageLogChannelCostSum(), "channel_cost_sum"),
			ent.As(usageLogCountIf(usageLogImageCondition), "image_requests"),
			ent.As(usageLogCountIf(usageLogNonImageCondition), "non_image_requests"),
			ent.As(usageLogSumIf(usageLogNonImageCondition, entusagelog.FieldDurationMs), "non_image_duration_ms"),
			ent.As(usageLogCountIf(usageLogFirstTokenCondition), "first_token_requests"),
			ent.As(usageLogSumIf(usageLogFirstTokenCondition, entusagelog.FieldFirstTokenMs), "first_token_ms"),
			ent.As(usageLogSumIf(usageLogImageCondition, entusagelog.FieldDurationMs), "image_duration_ms"),
			ent.As(usageLogDistinctActiveUsers(), "active_users"),
		).
		Scan(ctx, &rows); err != nil {
		return usageTodaySnapshot{}, err
	}
	if len(rows) == 0 {
		return usageTodaySnapshot{}, nil
	}
	return usageTodaySnapshot{
		Requests:           int64(rows[0].Count),
		ImageRequests:      rows[0].ImageRequests,
		NonImageRequests:   rows[0].NonImageRequests,
		Tokens:             rows[0].InputSum + rows[0].OutputSum + rows[0].CacheSum + rows[0].CacheCreationSum,
		Cost:               rows[0].CostSum,
		StandardCost:       rows[0].StandardCostSum,
		ChannelCost:        rows[0].ChannelCostSum,
		NonImageDurationMs: rows[0].NonImageDurationMs,
		FirstTokenRequests: rows[0].FirstTokenRequests,
		FirstTokenMs:       rows[0].FirstTokenMs,
		ImageDurationMs:    rows[0].ImageDurationMs,
		ActiveUsers:        rows[0].ActiveUsers,
	}, nil
}

func usageLogDistinctActiveUsers() ent.AggregateFunc {
	return func(s *entsql.Selector) string {
		userID := "COALESCE(NULLIF(" + s.C(entusagelog.FieldUserIDSnapshot) + ", 0), " + s.C(entusagelog.UserColumn) + ")"
		return "COUNT(DISTINCT " + userID + ")"
	}
}

func usageLogImageCondition(s *entsql.Selector) string {
	return "LOWER(TRIM(" + s.C(entusagelog.FieldModel) + ")) LIKE " + sqlStringLiteral(usagemodel.ImagePrefix+"%")
}

func usageLogNonImageCondition(s *entsql.Selector) string {
	return "NOT (" + usageLogImageCondition(s) + ")"
}

func usageLogFirstTokenCondition(s *entsql.Selector) string {
	return usageLogNonImageCondition(s) + " AND " + s.C(entusagelog.FieldFirstTokenMs) + " > 0"
}

func usageLogCountIf(condition func(*entsql.Selector) string) ent.AggregateFunc {
	return func(s *entsql.Selector) string {
		return "COALESCE(SUM(CASE WHEN " + condition(s) + " THEN 1 ELSE 0 END), 0)"
	}
}

func usageLogSumIf(condition func(*entsql.Selector) string, field string) ent.AggregateFunc {
	return func(s *entsql.Selector) string {
		return "COALESCE(SUM(CASE WHEN " + condition(s) + " THEN " + s.C(field) + " ELSE 0 END), 0)"
	}
}

func sqlStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func dashboardStatsCacheKey(userID int, todayStart time.Time) string {
	return fmt.Sprintf("airgate:dashboard:v1:stats:%d:%d", userID, todayStart.UTC().Unix())
}

func (s *DashboardStore) loadStatsSnapshotCache(ctx context.Context, userID int, todayStart time.Time) (appdashboard.StatsSnapshot, bool) {
	if s.rdb == nil {
		return appdashboard.StatsSnapshot{}, false
	}
	raw, err := s.rdb.Get(ctx, dashboardStatsCacheKey(userID, todayStart)).Bytes()
	if err != nil {
		return appdashboard.StatsSnapshot{}, false
	}
	var snapshot appdashboard.StatsSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		_ = s.rdb.Del(ctx, dashboardStatsCacheKey(userID, todayStart)).Err()
		return appdashboard.StatsSnapshot{}, false
	}
	return snapshot, true
}

func (s *DashboardStore) storeStatsSnapshotCache(ctx context.Context, userID int, todayStart time.Time, snapshot appdashboard.StatsSnapshot) {
	if s.rdb == nil {
		return
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	_ = s.rdb.Set(ctx, dashboardStatsCacheKey(userID, todayStart), raw, dashboardStatsCacheTTL).Err()
}

func (s *DashboardStore) loadStatsSnapshotFresh(ctx context.Context, todayStart, fiveMinAgo time.Time, userID int) (appdashboard.StatsSnapshot, error) {
	// 用户过滤谓词
	var userPred []predicate.UsageLog
	if userID > 0 {
		userPred = append(userPred, usageUserPredicate(int64(userID)))
	}

	totalAPIKeys, err := s.db.APIKey.Query().Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}
	enabledAPIKeys, err := s.db.APIKey.Query().Where(entapikey.StatusEQ(entapikey.StatusActive)).Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}

	totalChannels, err := s.db.Channel.Query().Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}
	// 状态已下沉到 key 级：渠道仅作供应商容器，启用/停用按密钥端点（ChannelKey）统计。
	totalKeys, err := s.db.ChannelKey.Query().Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}
	// enabled = 可参与调度的密钥；disabled = 人工停用 + 自动熔断（disabled_manual / disabled_auto）。
	enabledKeys, err := s.db.ChannelKey.Query().
		Where(entchannelkey.StatusEQ(entchannelkey.StatusEnabled)).
		Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}
	disabledKeys := totalKeys - enabledKeys

	totalUsers, err := s.db.User.Query().Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}
	newUsersToday, err := s.db.User.Query().Where(entuser.CreatedAtGTE(todayStart)).Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}

	usageQuery := s.db.UsageLog.Query().Where(userPred...)
	allTimeTotals, err := queryUsageTotals(ctx, usageQuery)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}

	todayUsage, err := queryTodayUsageSnapshot(ctx, usageQuery, todayStart)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}

	recentTotals, err := queryUsageTotals(ctx, usageQuery.Clone().Where(entusagelog.CreatedAtGTE(fiveMinAgo)))
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}

	return appdashboard.StatsSnapshot{
		TotalAPIKeys:            int64(totalAPIKeys),
		EnabledAPIKeys:          int64(enabledAPIKeys),
		TotalChannels:           int64(totalChannels),
		EnabledKeys:             int64(enabledKeys),
		DisabledKeys:            int64(disabledKeys),
		TotalUsers:              int64(totalUsers),
		NewUsersToday:           int64(newUsersToday),
		TodayRequests:           todayUsage.Requests,
		TodayImageRequests:      todayUsage.ImageRequests,
		TodayNonImageRequests:   todayUsage.NonImageRequests,
		AllTimeRequests:         allTimeTotals.Requests,
		TodayTokens:             todayUsage.Tokens,
		TodayCost:               todayUsage.Cost,
		TodayStandardCost:       todayUsage.StandardCost,
		TodayChannelCost:        todayUsage.ChannelCost,
		TodayNonImageDurationMs: todayUsage.NonImageDurationMs,
		TodayFirstTokenRequests: todayUsage.FirstTokenRequests,
		TodayFirstTokenMs:       todayUsage.FirstTokenMs,
		TodayImageDurationMs:    todayUsage.ImageDurationMs,
		ActiveUsers:             todayUsage.ActiveUsers,
		AllTimeTokens:           allTimeTotals.Tokens,
		AllTimeCost:             allTimeTotals.Cost,
		AllTimeStandardCost:     allTimeTotals.StandardCost,
		AllTimeChannelCost:      allTimeTotals.ChannelCost,
		RecentRequests:          recentTotals.Requests,
		RecentTokens:            recentTotals.Tokens,
	}, nil
}

func (s *DashboardStore) tryLockStatsSnapshot(ctx context.Context, userID int, todayStart time.Time) (string, bool, bool) {
	if s.rdb == nil {
		return "", false, false
	}
	token := uuid.NewString()
	ok, err := s.rdb.SetNX(ctx, dashboardStatsLockKey(userID, todayStart), token, dashboardStatsLockTTL).Result()
	if err != nil {
		return "", false, false
	}
	if !ok {
		return "", false, true
	}
	return token, true, false
}

func (s *DashboardStore) releaseStatsSnapshotLock(ctx context.Context, userID int, todayStart time.Time, token string) {
	if s.rdb == nil || token == "" {
		return
	}
	_, _ = dashboardStatsLockReleaseScript.Run(ctx, s.rdb, []string{dashboardStatsLockKey(userID, todayStart)}, token).Result()
}

func (s *DashboardStore) waitForStatsSnapshotCache(ctx context.Context, userID int, todayStart time.Time, timeout time.Duration) (appdashboard.StatsSnapshot, bool) {
	if s.rdb == nil {
		return appdashboard.StatsSnapshot{}, false
	}
	deadline := time.Now().Add(timeout)
	delay := 50 * time.Millisecond
	for {
		if snapshot, ok := s.loadStatsSnapshotCache(ctx, userID, todayStart); ok {
			return snapshot, true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return appdashboard.StatsSnapshot{}, false
		}
		wait := delay
		if remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return appdashboard.StatsSnapshot{}, false
		case <-timer.C:
		}
		if delay < 250*time.Millisecond {
			delay *= 2
		}
	}
}

func dashboardStatsLockKey(userID int, todayStart time.Time) string {
	return fmt.Sprintf("airgate:dashboard:v1:stats:lock:%d:%d", userID, todayStart.UTC().Unix())
}
