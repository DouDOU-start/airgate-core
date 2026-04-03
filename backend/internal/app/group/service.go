package group

import "context"

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
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.List(ctx, filter)
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

// StatsForGroups 批量查询分组统计信息（含实时容量）。
func (s *Service) StatsForGroups(ctx context.Context, groupIDs []int) (map[int]GroupStats, error) {
	stats, activeAccounts, err := s.repo.StatsForGroups(ctx, groupIDs)
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
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
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
	return s.repo.FindByID(ctx, id)
}

// Create 创建分组。
func (s *Service) Create(ctx context.Context, input CreateInput) (Group, error) {
	input.Quotas = cloneQuotas(input.Quotas)
	input.ModelRouting = cloneModelRouting(input.ModelRouting)
	return s.repo.Create(ctx, input)
}

// Update 更新分组。
func (s *Service) Update(ctx context.Context, id int, input UpdateInput) (Group, error) {
	input.Quotas = cloneQuotas(input.Quotas)
	input.ModelRouting = cloneModelRouting(input.ModelRouting)
	return s.repo.Update(ctx, id, input)
}

// Delete 删除分组。
func (s *Service) Delete(ctx context.Context, id int) error {
	return s.repo.Delete(ctx, id)
}
