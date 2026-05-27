package usage

import (
	"context"
	"strings"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// Service 使用记录用例服务。
type Service struct {
	repo Repository
}

// NewService 创建使用记录服务。
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// ListUser 查询当前用户的使用记录。
func (s *Service) ListUser(ctx context.Context, userID int64, filter ListFilter) (ListResult, error) {
	page, pageSize := NormalizePage(filter.Page, filter.PageSize)
	if filter.BeforeID <= 0 {
		page = 1
	}
	filter.Page = page
	filter.PageSize = pageSize

	list, hasMore, nextCursor, err := s.repo.ListUser(ctx, userID, filter)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("usage_query_failed",
			"scope", "user_list",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldError, err)
		return ListResult{}, err
	}

	return ListResult{
		List:       list,
		Total:      usageListTotal(page, pageSize, len(list), hasMore),
		Page:       page,
		PageSize:   pageSize,
		HasMore:    hasMore,
		NextCursor: nextCursor,
		TotalExact: !hasMore,
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

// ListAdmin 查询管理员使用记录列表。
func (s *Service) ListAdmin(ctx context.Context, filter ListFilter) (ListResult, error) {
	page, pageSize := NormalizePage(filter.Page, filter.PageSize)
	if filter.BeforeID <= 0 {
		page = 1
	}
	filter.Page = page
	filter.PageSize = pageSize

	list, hasMore, nextCursor, err := s.repo.ListAdmin(ctx, filter)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("usage_query_failed",
			"scope", "admin_list",
			sdk.LogFieldError, err)
		return ListResult{}, err
	}

	return ListResult{
		List:       list,
		Total:      usageListTotal(page, pageSize, len(list), hasMore),
		Page:       page,
		PageSize:   pageSize,
		HasMore:    hasMore,
		NextCursor: nextCursor,
		TotalExact: !hasMore,
	}, nil
}

func usageListTotal(page, pageSize, listLen int, hasMore bool) int64 {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	total := int64((page-1)*pageSize + listLen)
	if hasMore {
		total++
	}
	return total
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
	summary, err := s.repo.SummaryAdmin(ctx, filter)
	if err != nil {
		logger.Error("usage_query_failed",
			"scope", "admin_summary",
			sdk.LogFieldError, err)
		return StatsResult{}, err
	}

	result := StatsResult{Summary: summary}
	for _, item := range strings.Split(groupBy, ",") {
		dimension := strings.TrimSpace(item)
		switch dimension {
		case "model":
			result.ByModel, err = s.repo.StatsByModel(ctx, filter)
		case "user":
			result.ByUser, err = s.repo.StatsByUser(ctx, filter)
		case "account":
			result.ByAccount, err = s.repo.StatsByAccount(ctx, filter)
		case "group":
			result.ByGroup, err = s.repo.StatsByGroup(ctx, filter)
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
}

// AdminTrend 查询管理员趋势统计。
func (s *Service) AdminTrend(ctx context.Context, filter TrendFilter) ([]TrendBucket, error) {
	if filter.StartDate == "" && filter.EndDate == "" && filter.DefaultRecentHours <= 0 {
		filter.DefaultRecentHours = 24
	}

	entries, err := s.repo.TrendEntries(ctx, filter)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("usage_query_failed",
			"scope", "admin_trend",
			sdk.LogFieldError, err)
		return nil, err
	}
	return BuildTrendBuckets(entries, filter.Granularity, filter.TZ), nil
}
