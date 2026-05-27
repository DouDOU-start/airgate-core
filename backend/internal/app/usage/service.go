package usage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// Service 使用记录用例服务。
type Service struct {
	repo Repository
	rdb  *redis.Client
}

// NewService 创建使用记录服务。
func NewService(repo Repository, rdb ...*redis.Client) *Service {
	var cache *redis.Client
	if len(rdb) > 0 {
		cache = rdb[0]
	}
	return &Service{repo: repo, rdb: cache}
}

const (
	usageStatsCacheTTL = 10 * time.Second
	usageTrendCacheTTL = 15 * time.Second
	usageCacheLockTTL  = 5 * time.Second
	usageCacheLockWait = 1 * time.Second
	usageCacheV1Key    = "airgate:usage:v1"
)

var usageCacheLockReleaseScript = redis.NewScript(`
	local key = KEYS[1]
	local token = ARGV[1]
	if redis.call('GET', key) == token then
		return redis.call('DEL', key)
	end
	return 0
`)

// ListUser 查询当前用户的使用记录。
func (s *Service) ListUser(ctx context.Context, userID int64, filter ListFilter) (ListResult, error) {
	page, pageSize := NormalizePage(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.ListUser(ctx, userID, filter)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("usage_query_failed",
			"scope", "user_list",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldError, err)
		return ListResult{}, err
	}

	return ListResult{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// UserStats 查询当前用户汇总统计。
func (s *Service) UserStats(ctx context.Context, userID int64, filter StatsFilter) (Summary, error) {
	summary, err := s.repo.SummaryUser(ctx, userID, filter)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("usage_query_failed",
			"scope", "user_summary",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldError, err)
	}
	return summary, err
}

// UserStatsWithModels 查询当前用户统计页的完整数据，并用 Redis 短 TTL 缓存热点筛选结果。
func (s *Service) UserStatsWithModels(ctx context.Context, userID int64, filter StatsFilter) (UserStatsResult, error) {
	key := usageCacheKey("user-stats", struct {
		UserID int64
		Filter StatsFilter
	}{UserID: userID, Filter: filter})

	return usageCachedResult(ctx, s.rdb, key, usageStatsCacheTTL, func(loadCtx context.Context) (UserStatsResult, error) {
		summary, err := s.repo.SummaryUser(loadCtx, userID, filter)
		if err != nil {
			sdk.LoggerFromContext(loadCtx).Error("usage_query_failed",
				"scope", "user_summary",
				sdk.LogFieldUserID, userID,
				sdk.LogFieldError, err)
			return UserStatsResult{}, err
		}

		modelFilter := filter
		modelFilter.UserID = &userID
		modelStats, err := s.repo.StatsByModel(loadCtx, modelFilter)
		if err != nil {
			sdk.LoggerFromContext(loadCtx).Error("usage_query_failed",
				"scope", "user_stats_by_model",
				sdk.LogFieldUserID, userID,
				sdk.LogFieldError, err)
			return UserStatsResult{}, err
		}
		return UserStatsResult{Summary: summary, ByModel: modelStats}, nil
	})
}

// ListAdmin 查询管理员使用记录列表。
func (s *Service) ListAdmin(ctx context.Context, filter ListFilter) (ListResult, error) {
	page, pageSize := NormalizePage(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.ListAdmin(ctx, filter)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("usage_query_failed",
			"scope", "admin_list",
			sdk.LogFieldError, err)
		return ListResult{}, err
	}

	return ListResult{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// StatsByModel 按模型分组统计。
func (s *Service) StatsByModel(ctx context.Context, filter StatsFilter) ([]ModelStats, error) {
	stats, err := s.repo.StatsByModel(ctx, filter)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("usage_query_failed",
			"scope", "stats_by_model",
			sdk.LogFieldError, err)
	}
	return stats, err
}

// AdminStats 查询管理员聚合统计。
func (s *Service) AdminStats(ctx context.Context, filter StatsFilter, groupBy string) (StatsResult, error) {
	logger := sdk.LoggerFromContext(ctx)
	groupBy = normalizeStatsGroupBy(groupBy)
	key := usageCacheKey("admin-stats", struct {
		Filter  StatsFilter
		GroupBy string
	}{Filter: filter, GroupBy: groupBy})

	return usageCachedResult(ctx, s.rdb, key, usageStatsCacheTTL, func(loadCtx context.Context) (StatsResult, error) {
		summary, err := s.repo.SummaryAdmin(loadCtx, filter)
		if err != nil {
			logger.Error("usage_query_failed",
				"scope", "admin_summary",
				sdk.LogFieldError, err)
			return StatsResult{}, err
		}

		result := StatsResult{Summary: summary}
		for _, dimension := range strings.Split(groupBy, ",") {
			switch dimension {
			case "model":
				result.ByModel, err = s.repo.StatsByModel(loadCtx, filter)
			case "user":
				result.ByUser, err = s.repo.StatsByUser(loadCtx, filter)
			case "account":
				result.ByAccount, err = s.repo.StatsByAccount(loadCtx, filter)
			case "group":
				result.ByGroup, err = s.repo.StatsByGroup(loadCtx, filter)
			default:
				continue
			}
			if err != nil {
				logger.Error("usage_query_failed",
					"scope", "admin_stats",
					"group_by", dimension,
					sdk.LogFieldError, err)
				return StatsResult{}, err
			}
		}

		return result, nil
	})
}

// AdminTrend 查询管理员趋势统计。
func (s *Service) AdminTrend(ctx context.Context, filter TrendFilter) ([]TrendBucket, error) {
	filter = normalizeTrendFilter(filter)
	key := usageCacheKey("trend", filter)

	return usageCachedResult(ctx, s.rdb, key, usageTrendCacheTTL, func(loadCtx context.Context) ([]TrendBucket, error) {
		entries, err := s.repo.TrendEntries(loadCtx, filter)
		if err != nil {
			sdk.LoggerFromContext(loadCtx).Error("usage_query_failed",
				"scope", "admin_trend",
				sdk.LogFieldError, err)
			return nil, err
		}
		return BuildTrendBuckets(entries, filter.Granularity, filter.TZ), nil
	})
}

func normalizeTrendFilter(filter TrendFilter) TrendFilter {
	if filter.StartDate == "" && filter.EndDate == "" && filter.DefaultRecentHours <= 0 {
		filter.DefaultRecentHours = 24
	}
	return filter
}

func normalizeStatsGroupBy(groupBy string) string {
	if groupBy == "" {
		return ""
	}
	allowed := map[string]struct{}{
		"model":   {},
		"user":    {},
		"account": {},
		"group":   {},
	}
	seen := make(map[string]struct{})
	dimensions := make([]string, 0, 4)
	for _, item := range strings.Split(groupBy, ",") {
		dimension := strings.TrimSpace(item)
		if _, ok := allowed[dimension]; !ok {
			continue
		}
		if _, ok := seen[dimension]; ok {
			continue
		}
		seen[dimension] = struct{}{}
		dimensions = append(dimensions, dimension)
	}
	sort.Strings(dimensions)
	return strings.Join(dimensions, ",")
}

func usageCacheKey(kind string, payload any) string {
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte(fmt.Sprintf("%#v", payload))
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%s:%s:%s", usageCacheV1Key, kind, hex.EncodeToString(sum[:]))
}

func usageCachedResult[T any](ctx context.Context, rdb *redis.Client, key string, ttl time.Duration, loader func(context.Context) (T, error)) (T, error) {
	if rdb == nil {
		return loader(ctx)
	}
	if value, ok := usageLoadCache[T](ctx, rdb, key); ok {
		return value, nil
	}

	if token, ok, busy := usageTryCacheLock(ctx, rdb, key); ok {
		defer usageReleaseCacheLock(key, token, rdb)
		if value, ok := usageLoadCache[T](ctx, rdb, key); ok {
			return value, nil
		}
		value, err := loader(ctx)
		if err != nil {
			var zero T
			return zero, err
		}
		usageStoreCache(rdb, key, value, ttl)
		return value, nil
	} else if busy {
		if value, ok := usageWaitForCache[T](ctx, rdb, key, usageCacheLockWait); ok {
			return value, nil
		}
	}

	value, err := loader(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	usageStoreCache(rdb, key, value, ttl)
	return value, nil
}

func usageLoadCache[T any](ctx context.Context, rdb *redis.Client, key string) (T, bool) {
	var zero T
	raw, err := rdb.Get(ctx, key).Bytes()
	if err != nil {
		return zero, false
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		_ = rdb.Del(ctx, key).Err()
		return zero, false
	}
	return value, true
}

func usageStoreCache[T any](rdb *redis.Client, key string, value T, ttl time.Duration) {
	raw, err := json.Marshal(value)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = rdb.Set(ctx, key, raw, ttl).Err()
}

func usageTryCacheLock(ctx context.Context, rdb *redis.Client, key string) (string, bool, bool) {
	token := uuid.NewString()
	ok, err := rdb.SetNX(ctx, key+":lock", token, usageCacheLockTTL).Result()
	if err != nil {
		return "", false, false
	}
	if !ok {
		return "", false, true
	}
	return token, true, false
}

func usageReleaseCacheLock(key, token string, rdb *redis.Client) {
	if token == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _ = usageCacheLockReleaseScript.Run(ctx, rdb, []string{key + ":lock"}, token).Result()
}

func usageWaitForCache[T any](ctx context.Context, rdb *redis.Client, key string, timeout time.Duration) (T, bool) {
	var zero T
	deadline := time.Now().Add(timeout)
	delay := 50 * time.Millisecond
	for {
		if value, ok := usageLoadCache[T](ctx, rdb, key); ok {
			return value, true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return zero, false
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
			return zero, false
		case <-timer.C:
		}
		if delay < 250*time.Millisecond {
			delay *= 2
		}
	}
}
