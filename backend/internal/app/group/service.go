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
	rpm         RPMReader
}

// NewService 创建分组服务。
func NewService(repo Repository, concurrency ConcurrencyReader) *Service {
	return &Service{repo: repo, concurrency: concurrency}
}

// SetRPMReader 注入分组 RPM 读取器（server 装配阶段调用；nil 安全，
// 未注入时列表 RPM 保持 0 值）。
func (s *Service) SetRPMReader(rpm RPMReader) {
	s.rpm = rpm
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
	s.attachRuntimeStats(ctx, list)

	return ListResult{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// attachRuntimeStats 为列表页分组批量填充运行时观测指标（在途并发 / 当前分钟 RPM）。
// 读取器未注入或 Redis 不可用时保持 0 值，不影响列表主流程。
func (s *Service) attachRuntimeStats(ctx context.Context, list []Group) {
	if len(list) == 0 {
		return
	}
	ids := make([]int, len(list))
	for i, g := range list {
		ids[i] = g.ID
	}
	var counts, rpms map[int]int
	if s.concurrency != nil {
		counts = s.concurrency.GetGroupCurrentCounts(ctx, ids)
	}
	if s.rpm != nil {
		rpms = s.rpm.GetGroupRPMs(ctx, ids)
	}
	for i := range list {
		list[i].CurrentConcurrency = counts[list[i].ID]
		list[i].CurrentRPM = rpms[list[i].ID]
	}
}

// StatsForGroups 批量查询分组统计信息（今日/累计用量）。
// tz 决定"今日"起点；为空时回退到服务器本地时区。
func (s *Service) StatsForGroups(ctx context.Context, groupIDs []int, tz string) (map[int]GroupStats, error) {
	loc := timezone.Resolve(tz)
	todayStart := timezone.StartOfDay(time.Now().In(loc))
	return s.repo.StatsForGroups(ctx, groupIDs, todayStart)
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
