package group

import (
	"context"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/pkg/pagination"
	"github.com/DouDOU-start/airgate-core/internal/pkg/timezone"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// Service 提供分组域用例编排。
type Service struct {
	repo        Repository
	concurrency ConcurrencyReader
}

// NewService 创建分组服务。
func NewService(repo Repository, concurrency ConcurrencyReader) *Service {
	return &Service{repo: repo, concurrency: concurrency}
}

// List 查询管理员分组列表。
func (s *Service) List(ctx context.Context, filter ListFilter) (ListResult, error) {
	page, pageSize := pagination.Normalize(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.List(ctx, filter)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("group_lookup_failed",
			"op", "list",
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

// StatsForGroups 批量查询分组统计信息（含实时容量）。
// tz 决定"今日"起点；为空时回退到服务器本地时区。
func (s *Service) StatsForGroups(ctx context.Context, groupIDs []int, tz string) (map[int]GroupStats, error) {
	loc := timezone.Resolve(tz)
	todayStart := timezone.StartOfDay(time.Now().In(loc))
	stats, activeAccounts, err := s.repo.StatsForGroups(ctx, groupIDs, todayStart)
	if err != nil {
		return nil, err
	}

	// 收集所有活跃账号 ID，批量查询当前并发数
	var allAccountIDs []int
	for _, accs := range activeAccounts {
		for _, a := range accs {
			allAccountIDs = append(allAccountIDs, a.AccountID)
		}
	}
	counts := s.concurrency.GetCurrentCounts(ctx, allAccountIDs)

	// 按分组聚合已用容量
	for groupID, accs := range activeAccounts {
		st := stats[groupID]
		for _, a := range accs {
			st.CapacityUsed += counts[a.AccountID]
		}
		stats[groupID] = st
	}

	return stats, nil
}

// ListAvailable 查询用户可用分组列表。
func (s *Service) ListAvailable(ctx context.Context, filter AvailableFilter) (ListResult, error) {
	page, pageSize := pagination.Normalize(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.ListAvailable(ctx, filter)
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

// Get 获取分组详情。
func (s *Service) Get(ctx context.Context, id int) (Group, error) {
	g, err := s.repo.FindByID(ctx, id)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("group_lookup_failed",
			sdk.LogFieldGroupID, id,
			sdk.LogFieldError, err)
	}
	return g, err
}

// Create 创建分组。
func (s *Service) Create(ctx context.Context, input CreateInput) (Group, error) {
	logger := sdk.LoggerFromContext(ctx)
	input.Quotas = cloneQuotas(input.Quotas)
	input.ModelRouting = cloneModelRouting(input.ModelRouting)
	input.PluginSettings = clonePluginSettings(input.PluginSettings)
	g, err := s.repo.Create(ctx, input)
	if err != nil {
		logger.Error("group_persist_failed",
			"op", "create",
			"name", input.Name,
			sdk.LogFieldPlatform, input.Platform,
			sdk.LogFieldError, err)
		return g, err
	}
	logger.Info("group_create_succeeded",
		sdk.LogFieldGroupID, g.ID,
		"name", g.Name,
		sdk.LogFieldPlatform, g.Platform)
	return g, err
}

// Update 更新分组。
func (s *Service) Update(ctx context.Context, id int, input UpdateInput) (Group, error) {
	logger := sdk.LoggerFromContext(ctx)
	input.Quotas = cloneQuotas(input.Quotas)
	input.ModelRouting = cloneModelRouting(input.ModelRouting)
	input.PluginSettings = clonePluginSettings(input.PluginSettings)
	g, err := s.repo.Update(ctx, id, input)
	if err != nil {
		logger.Error("group_persist_failed",
			"op", "update",
			sdk.LogFieldGroupID, id,
			sdk.LogFieldError, err)
		return g, err
	}
	logger.Info("group_update_succeeded", sdk.LogFieldGroupID, id)
	if input.ModelRouting != nil {
		logger.Info("group_routing_updated", sdk.LogFieldGroupID, id)
	}
	return g, err
}

// Delete 删除分组。
func (s *Service) Delete(ctx context.Context, id int) error {
	logger := sdk.LoggerFromContext(ctx)
	if err := s.repo.Delete(ctx, id); err != nil {
		logger.Error("group_persist_failed",
			"op", "delete",
			sdk.LogFieldGroupID, id,
			sdk.LogFieldError, err)
		return err
	}
	logger.Info("group_delete_succeeded", sdk.LogFieldGroupID, id)
	return nil
}
