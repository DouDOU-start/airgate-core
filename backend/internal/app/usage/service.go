package usage

import (
	"context"
	"strings"
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
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.ListUser(ctx, userID, filter)
	if err != nil {
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
	return s.repo.SummaryUser(ctx, userID, filter)
}

// ListAdmin 查询管理员使用记录列表。
func (s *Service) ListAdmin(ctx context.Context, filter ListFilter) (ListResult, error) {
	page, pageSize := NormalizePage(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.ListAdmin(ctx, filter)
	if err != nil {
		return ListResult{}, err
	}

	return ListResult{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// AdminStats 查询管理员聚合统计。
func (s *Service) AdminStats(ctx context.Context, filter StatsFilter, groupBy string) (StatsResult, error) {
	summary, err := s.repo.SummaryAdmin(ctx, filter)
	if err != nil {
		return StatsResult{}, err
	}

	result := StatsResult{Summary: summary}
	for _, item := range strings.Split(groupBy, ",") {
		switch strings.TrimSpace(item) {
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
		return nil, err
	}
	return BuildTrendBuckets(entries, filter.Granularity), nil
}
