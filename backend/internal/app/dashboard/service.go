package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/DouDOU-start/airgate-core/internal/pkg/timezone"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// Service 提供仪表盘用例编排。
type Service struct {
	repo Repository
	rdb  *redis.Client
	now  func() time.Time
}

// NewService 创建仪表盘服务。
func NewService(repo Repository, rdb ...*redis.Client) *Service {
	var cache *redis.Client
	if len(rdb) > 0 {
		cache = rdb[0]
	}
	return &Service{
		repo: repo,
		rdb:  cache,
		now:  time.Now,
	}
}

const (
	trendCacheTTL   = 15 * time.Second
	trendLockTTL    = 5 * time.Second
	trendLockWait   = 1 * time.Second
	trendCacheV1Key = "airgate:dashboard:v1:trend"
)

var trendLockReleaseScript = redis.NewScript(`
	local key = KEYS[1]
	local token = ARGV[1]
	if redis.call('GET', key) == token then
		return redis.call('DEL', key)
	end
	return 0
`)

// Stats 查询仪表盘统计。userID 为 0 表示查全部。
// tz 为调用方的 IANA 时区名（如 "Asia/Shanghai"、"America/New_York"），决定"今天"的起点；
// 为空或无法解析时回退到服务器本地时区。
func (s *Service) Stats(ctx context.Context, userID int, tz string) (Stats, error) {
	loc := timezone.Resolve(tz)
	now := s.now().In(loc)
	todayStart := timezone.StartOfDay(now)
	fiveMinAgo := now.Add(-5 * time.Minute)

	snapshot, err := s.repo.LoadStatsSnapshot(ctx, todayStart, fiveMinAgo, userID)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("dashboard_query_failed",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldReason, "stats_snapshot",
			sdk.LogFieldError, err,
		)
		return Stats{}, err
	}

	result := Stats{
		TotalAPIKeys:        snapshot.TotalAPIKeys,
		EnabledAPIKeys:      snapshot.EnabledAPIKeys,
		TotalAccounts:       snapshot.TotalAccounts,
		EnabledAccounts:     snapshot.EnabledAccounts,
		ErrorAccounts:       snapshot.ErrorAccounts,
		TodayRequests:       snapshot.TodayRequests,
		TodayImageRequests:  snapshot.TodayImageRequests,
		AllTimeRequests:     snapshot.AllTimeRequests,
		TotalUsers:          snapshot.TotalUsers,
		NewUsersToday:       snapshot.NewUsersToday,
		TodayTokens:         snapshot.TodayTokens,
		TodayCost:           snapshot.TodayCost,
		TodayStandardCost:   snapshot.TodayStandardCost,
		AllTimeTokens:       snapshot.AllTimeTokens,
		AllTimeCost:         snapshot.AllTimeCost,
		AllTimeStandardCost: snapshot.AllTimeStandardCost,
		ActiveUsers:         snapshot.ActiveUsers,
		RPM:                 float64(snapshot.RecentRequests) / 5.0,
		TPM:                 float64(snapshot.RecentTokens) / 5.0,
	}
	if snapshot.TodayNonImageRequests > 0 {
		result.AvgDurationMs = float64(snapshot.TodayNonImageDurationMs) / float64(snapshot.TodayNonImageRequests)
	}
	if snapshot.TodayFirstTokenRequests > 0 {
		result.AvgFirstTokenMs = float64(snapshot.TodayFirstTokenMs) / float64(snapshot.TodayFirstTokenRequests)
	}
	if snapshot.TodayImageRequests > 0 {
		result.AvgImageDurationMs = float64(snapshot.TodayImageDurationMs) / float64(snapshot.TodayImageRequests)
	}

	return result, nil
}

// Trend 查询仪表盘趋势。
// query.TZ 决定时间范围边界以及输出时间桶的格式化时区；为空时回退到服务器本地时区。
func (s *Service) Trend(ctx context.Context, query TrendQuery) (Trend, error) {
	loc := timezone.Resolve(query.TZ)
	now := s.now().In(loc)
	startTime, endTime := resolveTrendTimeRange(query, now)
	cacheKey := trendCacheKey(query, loc, startTime, endTime)
	if trend, ok := s.loadTrendCache(ctx, cacheKey); ok {
		return trend, nil
	}

	if token, ok, lockBusy := s.tryLockTrendCache(ctx, cacheKey); ok {
		defer s.releaseTrendCacheLock(context.Background(), cacheKey, token)

		if trend, ok := s.loadTrendCache(ctx, cacheKey); ok {
			return trend, nil
		}

		trend, err := s.loadTrendFresh(ctx, query, loc, startTime, endTime)
		if err != nil {
			sdk.LoggerFromContext(ctx).Error("dashboard_query_failed",
				sdk.LogFieldUserID, query.UserID,
				sdk.LogFieldReason, "trend_logs",
				sdk.LogFieldError, err,
			)
			return Trend{}, err
		}
		s.storeTrendCache(ctx, cacheKey, trend)
		return trend, nil
	} else if lockBusy {
		if trend, ok := s.waitForTrendCache(ctx, cacheKey, trendLockWait); ok {
			return trend, nil
		}
	}

	trend, err := s.loadTrendFresh(ctx, query, loc, startTime, endTime)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("dashboard_query_failed",
			sdk.LogFieldUserID, query.UserID,
			sdk.LogFieldReason, "trend_logs",
			sdk.LogFieldError, err,
		)
		return Trend{}, err
	}
	s.storeTrendCache(ctx, cacheKey, trend)
	return trend, nil
}

func (s *Service) loadTrendFresh(ctx context.Context, query TrendQuery, loc *time.Location, startTime, endTime time.Time) (Trend, error) {
	logs, err := s.repo.ListTrendLogs(ctx, startTime, endTime, query.UserID)
	if err != nil {
		return Trend{}, err
	}

	return Trend{
		ModelDistribution: aggregateModelDistribution(logs),
		UserRanking:       aggregateUserRanking(logs),
		TokenTrend:        aggregateTokenTrend(logs, query.Granularity, loc),
		TopUsers:          aggregateTopUsers(logs, query.Granularity, loc),
	}, nil
}

func trendCacheKey(query TrendQuery, loc *time.Location, startTime, endTime time.Time) string {
	const trendBucketSeconds = 15
	return fmt.Sprintf("%s:%s:%s:%d:%d:%d:%d:%s:%s:%s",
		trendCacheV1Key,
		loc.String(),
		query.Range,
		query.UserID,
		startTime.UTC().Unix(),
		endTime.UTC().Unix()/trendBucketSeconds,
		trendBucketSeconds,
		query.Granularity,
		query.StartDate,
		query.EndDate,
	)
}

func (s *Service) loadTrendCache(ctx context.Context, key string) (Trend, bool) {
	if s.rdb == nil {
		return Trend{}, false
	}
	raw, err := s.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return Trend{}, false
	}
	var trend Trend
	if err := json.Unmarshal(raw, &trend); err != nil {
		_ = s.rdb.Del(ctx, key).Err()
		return Trend{}, false
	}
	return trend, true
}

func (s *Service) storeTrendCache(ctx context.Context, key string, trend Trend) {
	if s.rdb == nil {
		return
	}
	raw, err := json.Marshal(trend)
	if err != nil {
		return
	}
	_ = s.rdb.Set(ctx, key, raw, trendCacheTTL).Err()
}

func (s *Service) tryLockTrendCache(ctx context.Context, key string) (string, bool, bool) {
	if s.rdb == nil {
		return "", false, false
	}
	token := uuid.NewString()
	ok, err := s.rdb.SetNX(ctx, key+":lock", token, trendLockTTL).Result()
	if err != nil {
		return "", false, false
	}
	if !ok {
		return "", false, true
	}
	return token, true, false
}

func (s *Service) releaseTrendCacheLock(ctx context.Context, key, token string) {
	if s.rdb == nil || token == "" {
		return
	}
	_, _ = trendLockReleaseScript.Run(ctx, s.rdb, []string{key + ":lock"}, token).Result()
}

func (s *Service) waitForTrendCache(ctx context.Context, key string, timeout time.Duration) (Trend, bool) {
	if s.rdb == nil {
		return Trend{}, false
	}
	deadline := time.Now().Add(timeout)
	delay := 50 * time.Millisecond
	for {
		if trend, ok := s.loadTrendCache(ctx, key); ok {
			return trend, true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return Trend{}, false
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
			return Trend{}, false
		case <-timer.C:
		}
		if delay < 250*time.Millisecond {
			delay *= 2
		}
	}
}

// resolveTrendTimeRange 根据查询计算 [startTime, endTime) 区间。
// now 必须已处于目标时区，这样 startOfDay 和 ParseInLocation 才会使用调用方时区。
func resolveTrendTimeRange(query TrendQuery, now time.Time) (time.Time, time.Time) {
	endTime := now
	loc := now.Location()

	switch query.Range {
	case "today":
		return timezone.StartOfDay(now), endTime
	case "7d":
		return timezone.StartOfDay(now.AddDate(0, 0, -7)), endTime
	case "30d":
		return timezone.StartOfDay(now.AddDate(0, 0, -30)), endTime
	case "90d":
		return timezone.StartOfDay(now.AddDate(0, 0, -90)), endTime
	case "custom":
		startTime := timezone.StartOfDay(now.AddDate(0, 0, -30))
		if query.StartDate != "" {
			if parsed, err := timezone.ParseDate(query.StartDate, loc); err == nil {
				startTime = parsed
			}
		}
		if query.EndDate != "" {
			if parsed, err := timezone.ParseDate(query.EndDate, loc); err == nil {
				endTime = parsed.AddDate(0, 0, 1)
			}
		}
		return startTime, endTime
	default:
		return timezone.StartOfDay(now), endTime
	}
}

func aggregateModelDistribution(logs []TrendLog) []ModelStats {
	modelMap := make(map[string]*ModelStats)
	for _, item := range logs {
		stat := modelMap[item.Model]
		if stat == nil {
			stat = &ModelStats{Model: item.Model}
			modelMap[item.Model] = stat
		}
		stat.Requests++
		stat.Tokens += item.InputTokens + item.OutputTokens + item.CachedInputTokens + item.CacheCreationTokens
		stat.ActualCost += item.ActualCost
		stat.StandardCost += item.StandardCost
	}

	result := make([]ModelStats, 0, len(modelMap))
	for _, item := range modelMap {
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Requests > result[j].Requests
	})
	return result
}

func aggregateUserRanking(logs []TrendLog) []UserRanking {
	userMap := make(map[int]*UserRanking)
	for _, item := range logs {
		ranking := userMap[item.UserID]
		if ranking == nil {
			ranking = &UserRanking{
				UserID: int64(item.UserID),
				Email:  item.UserEmail,
			}
			userMap[item.UserID] = ranking
		}
		ranking.Requests++
		ranking.Tokens += item.InputTokens + item.OutputTokens + item.CachedInputTokens + item.CacheCreationTokens
		ranking.ActualCost += item.ActualCost
		ranking.StandardCost += item.StandardCost
	}

	result := make([]UserRanking, 0, len(userMap))
	for _, item := range userMap {
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ActualCost > result[j].ActualCost
	})
	return result
}

func aggregateTokenTrend(logs []TrendLog, granularity string, loc *time.Location) []TimeBucket {
	layout := trendTimeLayout(granularity)
	bucketMap := make(map[string]*TimeBucket)
	for _, item := range logs {
		key := item.CreatedAt.In(loc).Format(layout)
		bucket := bucketMap[key]
		if bucket == nil {
			bucket = &TimeBucket{Time: key}
			bucketMap[key] = bucket
		}
		bucket.InputTokens += item.InputTokens
		bucket.OutputTokens += item.OutputTokens
		bucket.CachedInput += item.CachedInputTokens
		bucket.CacheCreation += item.CacheCreationTokens
		bucket.ActualCost += item.ActualCost
		bucket.StandardCost += item.StandardCost
	}

	result := make([]TimeBucket, 0, len(bucketMap))
	for _, item := range bucketMap {
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Time < result[j].Time
	})
	return result
}

func aggregateTopUsers(logs []TrendLog, granularity string, loc *time.Location) []UserTrend {
	type userTotal struct {
		UserID int
		Email  string
		Tokens int64
	}

	totalMap := make(map[int]*userTotal)
	for _, item := range logs {
		total := totalMap[item.UserID]
		if total == nil {
			total = &userTotal{UserID: item.UserID, Email: item.UserEmail}
			totalMap[item.UserID] = total
		}
		total.Tokens += item.InputTokens + item.OutputTokens + item.CachedInputTokens + item.CacheCreationTokens
	}

	totals := make([]userTotal, 0, len(totalMap))
	for _, item := range totalMap {
		totals = append(totals, *item)
	}
	sort.Slice(totals, func(i, j int) bool {
		return totals[i].Tokens > totals[j].Tokens
	})
	if len(totals) > 12 {
		totals = totals[:12]
	}

	layout := trendTimeLayout(granularity)
	topUserSet := make(map[int]bool, len(totals))
	for _, item := range totals {
		topUserSet[item.UserID] = true
	}

	userBuckets := make(map[int]map[string]int64)
	for _, item := range logs {
		if !topUserSet[item.UserID] {
			continue
		}
		key := item.CreatedAt.In(loc).Format(layout)
		if userBuckets[item.UserID] == nil {
			userBuckets[item.UserID] = make(map[string]int64)
		}
		userBuckets[item.UserID][key] += item.InputTokens + item.OutputTokens + item.CachedInputTokens + item.CacheCreationTokens
	}

	result := make([]UserTrend, 0, len(totals))
	for _, item := range totals {
		points := make([]UserTrendPoint, 0, len(userBuckets[item.UserID]))
		for key, tokens := range userBuckets[item.UserID] {
			points = append(points, UserTrendPoint{
				Time:   key,
				Tokens: tokens,
			})
		}
		sort.Slice(points, func(i, j int) bool {
			return points[i].Time < points[j].Time
		})
		result = append(result, UserTrend{
			UserID: int64(item.UserID),
			Email:  item.Email,
			Trend:  points,
		})
	}

	return result
}

func trendTimeLayout(granularity string) string {
	if granularity == "hour" {
		return "2006-01-02 15:00"
	}
	return "2006-01-02"
}
