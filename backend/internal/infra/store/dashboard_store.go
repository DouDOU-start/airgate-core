package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/DouDOU-start/airgate-core/ent"
	entaccount "github.com/DouDOU-start/airgate-core/ent/account"
	entapikey "github.com/DouDOU-start/airgate-core/ent/apikey"
	"github.com/DouDOU-start/airgate-core/ent/predicate"
	entusagelog "github.com/DouDOU-start/airgate-core/ent/usagelog"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
	appdashboard "github.com/DouDOU-start/airgate-core/internal/app/dashboard"
	"github.com/DouDOU-start/airgate-core/internal/pkg/usagemodel"
)

// DashboardStore 使用 Ent 实现仪表盘仓储。
type DashboardStore struct {
	db  *ent.Client
	rdb *redis.Client
}

// NewDashboardStore 创建仪表盘仓储。
func NewDashboardStore(db *ent.Client, rdb ...*redis.Client) *DashboardStore {
	var cache *redis.Client
	if len(rdb) > 0 {
		cache = rdb[0]
	}
	return &DashboardStore{db: db, rdb: cache}
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

// ListTrendLogs 读取趋势聚合所需日志。userID 为 0 表示查全部。
func (s *DashboardStore) ListTrendLogs(ctx context.Context, startTime, endTime time.Time, userID int) ([]appdashboard.TrendLog, error) {
	preds := []predicate.UsageLog{
		entusagelog.CreatedAtGTE(startTime),
		entusagelog.CreatedAtLT(endTime),
	}
	if userID > 0 {
		preds = append(preds, usageUserPredicate(int64(userID)))
	}

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
			entusagelog.FieldCreatedAt,
		).
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
			CreatedAt:           item.CreatedAt,
		}
		result = append(result, log)
	}

	return result, nil
}

type usageTotals struct {
	Requests     int64
	Tokens       int64
	Cost         float64
	StandardCost float64
}

type usageTodaySnapshot struct {
	Requests           int64
	ImageRequests      int64
	NonImageRequests   int64
	Tokens             int64
	Cost               float64
	StandardCost       float64
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
	}
	if err := query.Clone().Aggregate(
		ent.Count(),
		ent.As(ent.Sum(entusagelog.FieldInputTokens), "input_sum"),
		ent.As(ent.Sum(entusagelog.FieldOutputTokens), "output_sum"),
		ent.As(ent.Sum(entusagelog.FieldCachedInputTokens), "cache_sum"),
		ent.As(ent.Sum(entusagelog.FieldCacheCreationTokens), "cache_creation_sum"),
		ent.As(ent.Sum(entusagelog.FieldActualCost), "cost_sum"),
		ent.As(ent.Sum(entusagelog.FieldTotalCost), "standard_cost_sum"),
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

	totalAccounts, err := s.db.Account.Query().Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}
	// "enabled" = 任何非 disabled 状态（active / rate_limited / degraded 都能被调度）。
	enabledAccounts, err := s.db.Account.Query().
		Where(entaccount.StateNEQ(entaccount.StateDisabled)).
		Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}
	// "error" = disabled + 有错误信息（区分人工禁用和状态机自动禁用）。
	errorAccounts, err := s.db.Account.Query().
		Where(entaccount.StateEQ(entaccount.StateDisabled), entaccount.ErrorMsgNEQ("")).
		Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}

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
		TotalAccounts:           int64(totalAccounts),
		EnabledAccounts:         int64(enabledAccounts),
		ErrorAccounts:           int64(errorAccounts),
		TotalUsers:              int64(totalUsers),
		NewUsersToday:           int64(newUsersToday),
		TodayRequests:           todayUsage.Requests,
		TodayImageRequests:      todayUsage.ImageRequests,
		TodayNonImageRequests:   todayUsage.NonImageRequests,
		AllTimeRequests:         allTimeTotals.Requests,
		TodayTokens:             todayUsage.Tokens,
		TodayCost:               todayUsage.Cost,
		TodayStandardCost:       todayUsage.StandardCost,
		TodayNonImageDurationMs: todayUsage.NonImageDurationMs,
		TodayFirstTokenRequests: todayUsage.FirstTokenRequests,
		TodayFirstTokenMs:       todayUsage.FirstTokenMs,
		TodayImageDurationMs:    todayUsage.ImageDurationMs,
		ActiveUsers:             todayUsage.ActiveUsers,
		AllTimeTokens:           allTimeTotals.Tokens,
		AllTimeCost:             allTimeTotals.Cost,
		AllTimeStandardCost:     allTimeTotals.StandardCost,
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
