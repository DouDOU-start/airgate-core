package subscription

import (
	"context"
	"time"
)

// Service 提供订阅域用例编排。
type Service struct {
	repo Repository
	now  func() time.Time
}

// NewService 创建订阅服务。
func NewService(repo Repository) *Service {
	return &Service{
		repo: repo,
		now:  time.Now,
	}
}

// UserSubscriptions 用户查看自己的订阅列表。
func (s *Service) UserSubscriptions(ctx context.Context, filter UserListFilter) (ListResult, error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.ListByUser(ctx, filter)
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

// ActiveSubscriptions 用户查看活跃订阅。
func (s *Service) ActiveSubscriptions(ctx context.Context, userID int) ([]Subscription, error) {
	return s.repo.ListActiveByUser(ctx, userID)
}

// SubscriptionProgress 用户查看订阅使用进度。
// 当前保持与历史行为一致，返回空列表占位。
func (s *Service) SubscriptionProgress(_ context.Context, _ int) ([]SubscriptionProgress, error) {
	return []SubscriptionProgress{}, nil
}

// AdminListSubscriptions 管理员查看订阅列表。
func (s *Service) AdminListSubscriptions(ctx context.Context, filter AdminListFilter) (ListResult, error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
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

// AdminAssign 管理员分配订阅。
func (s *Service) AdminAssign(ctx context.Context, input AssignInput) (Subscription, error) {
	expiresAt, err := time.Parse(time.RFC3339, input.ExpiresAt)
	if err != nil {
		return Subscription{}, ErrInvalidExpiresAt
	}

	return s.repo.Create(ctx, CreateInput{
		UserID:      input.UserID,
		GroupID:     input.GroupID,
		EffectiveAt: s.now(),
		ExpiresAt:   expiresAt,
		Status:      "active",
	})
}

// AdminBulkAssign 管理员批量分配订阅。
func (s *Service) AdminBulkAssign(ctx context.Context, input BulkAssignInput) (int, error) {
	expiresAt, err := time.Parse(time.RFC3339, input.ExpiresAt)
	if err != nil {
		return 0, ErrInvalidExpiresAt
	}

	return s.repo.BulkCreate(ctx, BulkCreateInput{
		UserIDs:     append([]int(nil), input.UserIDs...),
		GroupID:     input.GroupID,
		EffectiveAt: s.now(),
		ExpiresAt:   expiresAt,
		Status:      "active",
	})
}

// AdminAdjust 管理员调整订阅。
func (s *Service) AdminAdjust(ctx context.Context, id int, input AdjustInput) (Subscription, error) {
	update := UpdateInput{
		Status: input.Status,
	}
	if input.ExpiresAt != nil {
		parsed, err := time.Parse(time.RFC3339, *input.ExpiresAt)
		if err != nil {
			return Subscription{}, ErrInvalidAdjustExpiresAt
		}
		update.ExpiresAt = &parsed
	}

	return s.repo.Update(ctx, id, update)
}
