package subscription

import (
	"context"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/pkg/pagination"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
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
	page, pageSize := pagination.Normalize(filter.Page, filter.PageSize)
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
	page, pageSize := pagination.Normalize(filter.Page, filter.PageSize)
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
	logger := sdk.LoggerFromContext(ctx)
	expiresAt, err := time.Parse(time.RFC3339, input.ExpiresAt)
	if err != nil {
		logger.Warn("subscription_rejected",
			sdk.LogFieldReason, "invalid_expires_at",
			sdk.LogFieldUserID, input.UserID,
			sdk.LogFieldGroupID, input.GroupID)
		return Subscription{}, ErrInvalidExpiresAt
	}

	sub, err := s.repo.Create(ctx, CreateInput{
		UserID:      input.UserID,
		GroupID:     input.GroupID,
		EffectiveAt: s.now(),
		ExpiresAt:   expiresAt,
		Status:      "active",
	})
	if err != nil {
		logger.Error("subscription_persist_failed",
			"op", "create",
			sdk.LogFieldUserID, input.UserID,
			sdk.LogFieldGroupID, input.GroupID,
			sdk.LogFieldError, err)
		return sub, err
	}
	logger.Info("subscription_created",
		"subscription_id", sub.ID,
		sdk.LogFieldUserID, sub.UserID,
		sdk.LogFieldGroupID, sub.GroupID)
	return sub, nil
}

// AdminBulkAssign 管理员批量分配订阅。
func (s *Service) AdminBulkAssign(ctx context.Context, input BulkAssignInput) (int, error) {
	logger := sdk.LoggerFromContext(ctx)
	expiresAt, err := time.Parse(time.RFC3339, input.ExpiresAt)
	if err != nil {
		logger.Warn("subscription_rejected",
			sdk.LogFieldReason, "invalid_expires_at",
			"op", "bulk_assign",
			sdk.LogFieldGroupID, input.GroupID)
		return 0, ErrInvalidExpiresAt
	}

	count, err := s.repo.BulkCreate(ctx, BulkCreateInput{
		UserIDs:     append([]int(nil), input.UserIDs...),
		GroupID:     input.GroupID,
		EffectiveAt: s.now(),
		ExpiresAt:   expiresAt,
		Status:      "active",
	})
	if err != nil {
		logger.Error("subscription_persist_failed",
			"op", "bulk_create",
			sdk.LogFieldGroupID, input.GroupID,
			"user_count", len(input.UserIDs),
			sdk.LogFieldError, err)
		return count, err
	}
	logger.Info("subscription_created",
		"op", "bulk",
		sdk.LogFieldGroupID, input.GroupID,
		"created", count)
	return count, nil
}

// AdminAdjust 管理员调整订阅。
func (s *Service) AdminAdjust(ctx context.Context, id int, input AdjustInput) (Subscription, error) {
	logger := sdk.LoggerFromContext(ctx)
	update := UpdateInput{
		Status: input.Status,
	}
	if input.ExpiresAt != nil {
		parsed, err := time.Parse(time.RFC3339, *input.ExpiresAt)
		if err != nil {
			logger.Warn("subscription_rejected",
				sdk.LogFieldReason, "invalid_adjust_expires_at",
				"subscription_id", id)
			return Subscription{}, ErrInvalidAdjustExpiresAt
		}
		update.ExpiresAt = &parsed
	}

	sub, err := s.repo.Update(ctx, id, update)
	if err != nil {
		logger.Error("subscription_persist_failed",
			"op", "update",
			"subscription_id", id,
			sdk.LogFieldError, err)
		return sub, err
	}
	if input.Status != nil && *input.Status == "cancelled" {
		logger.Info("subscription_cancelled", "subscription_id", id)
	} else {
		logger.Info("subscription_plan_changed", "subscription_id", id)
	}
	return sub, nil
}
