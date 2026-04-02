package settings

import "context"

// Service 提供设置域用例编排。
type Service struct {
	repo Repository
}

// NewService 创建设置服务。
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List 查询设置列表。
func (s *Service) List(ctx context.Context, group string) ([]Setting, error) {
	return s.repo.List(ctx, group)
}

// Update 批量更新设置。
func (s *Service) Update(ctx context.Context, items []ItemInput) error {
	cloned := make([]ItemInput, 0, len(items))
	for _, item := range items {
		cloned = append(cloned, ItemInput{
			Key:   item.Key,
			Value: item.Value,
		})
	}
	return s.repo.UpsertMany(ctx, cloned)
}
