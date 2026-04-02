package group

import "context"

// Service 提供分组域用例编排。
type Service struct {
	repo Repository
}

// NewService 创建分组服务。
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
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
