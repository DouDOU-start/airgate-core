package group

import (
	"context"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
	"github.com/DouDOU-start/airgate-core/internal/pkg/pagination"
	"github.com/DouDOU-start/airgate-core/internal/pkg/timezone"
)

// Service 提供分组域用例编排。
type Service struct {
	repo        Repository
	concurrency ConcurrencyReader
	rpm         RPMReader
	userRates   UserRatesReader
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

// SetUserRatesReader 注入用户倍率读取器（server 装配阶段调用；nil 安全，
// 未注入时用户视角列表的 EffectiveRate 保持 0 值）。
func (s *Service) SetUserRatesReader(reader UserRatesReader) {
	s.userRates = reader
}

// List 查询管理员分组列表。
func (s *Service) List(ctx context.Context, filter ListFilter) (ListResult, error) {
	page, pageSize := pagination.Normalize(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.List(ctx, filter)
	if err != nil {
		logx.LoggerFromContext(ctx).Error("group_lookup_failed",
			"op", "list",
			logx.LogFieldError, err)
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

// availableForUserMax AvailableForUser 的分组数上限（远超实际规模的保护值）。
const availableForUserMax = 500

// AvailableForUser 返回用户全部可用分组（不分页；OAuth userinfo 场景）。
func (s *Service) AvailableForUser(ctx context.Context, userID int) ([]Group, error) {
	list, _, err := s.repo.ListAvailable(ctx, AvailableFilter{UserID: userID, Page: 1, PageSize: availableForUserMax})
	if err != nil {
		return nil, err
	}
	s.attachEffectiveRates(ctx, userID, list)
	return list, nil
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
	s.attachEffectiveRates(ctx, filter.UserID, list)

	return ListResult{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// attachEffectiveRates 为用户视角的分组列表解析实际计费倍率
// （复用计费侧优先级链：用户专属 > 等级 > 分组档位，见 billing.ResolveBillingRateForGroup）。
// 读取器未注入或读取失败时保持 0 值，不影响列表主流程。
func (s *Service) attachEffectiveRates(ctx context.Context, userID int, list []Group) {
	if s.userRates == nil || userID <= 0 || len(list) == 0 {
		return
	}
	groupRates, tierRates, err := s.userRates.BillingRates(ctx, userID)
	if err != nil {
		logx.LoggerFromContext(ctx).Warn("group_effective_rate_skipped",
			logx.LogFieldUserID, userID,
			logx.LogFieldError, err)
		return
	}
	for i := range list {
		list[i].EffectiveRate = billing.ResolveBillingRateForGroup(groupRates, tierRates, list[i].ID, list[i].RateMultiplier)
	}
}

// PublicRateRange 计算全部非专属分组的倍率区间，供模型广场展示"大概打几折"。
// ok=false 表示没有可展示的区间（无非专属分组，或所有非专属分组倍率相同——单点区间
// 没有对比意义），此时调用方应只展示价格、不展示倍率。
func (s *Service) PublicRateRange(ctx context.Context) (min, max float64, ok bool) {
	rates, err := s.repo.PublicRateMultipliers(ctx)
	if err != nil || len(rates) == 0 {
		if err != nil {
			logx.LoggerFromContext(ctx).Warn("group_public_rate_range_failed", logx.LogFieldError, err)
		}
		return 0, 0, false
	}

	min, max = rates[0], rates[0]
	for _, r := range rates[1:] {
		if r < min {
			min = r
		}
		if r > max {
			max = r
		}
	}
	if min == max {
		return 0, 0, false
	}
	return min, max, true
}

// Get 获取分组详情。
func (s *Service) Get(ctx context.Context, id int) (Group, error) {
	g, err := s.repo.FindByID(ctx, id)
	if err != nil {
		logx.LoggerFromContext(ctx).Error("group_lookup_failed",
			logx.LogFieldGroupID, id,
			logx.LogFieldError, err)
	}
	return g, err
}

// Create 创建分组。
func (s *Service) Create(ctx context.Context, input CreateInput) (Group, error) {
	logger := logx.LoggerFromContext(ctx)
	g, err := s.repo.Create(ctx, input)
	if err != nil {
		logger.Error("group_persist_failed",
			"op", "create",
			"name", input.Name,
			logx.LogFieldPlatform, input.Platform,
			logx.LogFieldError, err)
		return g, err
	}
	logger.Info("group_create_succeeded",
		logx.LogFieldGroupID, g.ID,
		"name", g.Name,
		logx.LogFieldPlatform, g.Platform)
	return g, err
}

// Update 更新分组。
func (s *Service) Update(ctx context.Context, id int, input UpdateInput) (Group, error) {
	logger := logx.LoggerFromContext(ctx)
	g, err := s.repo.Update(ctx, id, input)
	if err != nil {
		logger.Error("group_persist_failed",
			"op", "update",
			logx.LogFieldGroupID, id,
			logx.LogFieldError, err)
		return g, err
	}
	logger.Info("group_update_succeeded", logx.LogFieldGroupID, id)
	return g, err
}

// Delete 删除分组。
func (s *Service) Delete(ctx context.Context, id int) error {
	logger := logx.LoggerFromContext(ctx)
	if err := s.repo.Delete(ctx, id); err != nil {
		logger.Error("group_persist_failed",
			"op", "delete",
			logx.LogFieldGroupID, id,
			logx.LogFieldError, err)
		return err
	}
	logger.Info("group_delete_succeeded", logx.LogFieldGroupID, id)
	return nil
}
