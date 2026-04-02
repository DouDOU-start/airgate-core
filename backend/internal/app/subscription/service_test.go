package subscription

import (
	"context"
	"testing"
	"time"
)

func TestUserSubscriptionsNormalizesPagination(t *testing.T) {
	var captured UserListFilter
	service := NewService(subscriptionStubRepository{
		listByUser: func(_ context.Context, filter UserListFilter) ([]Subscription, int64, error) {
			captured = filter
			return nil, 0, nil
		},
	})

	result, err := service.UserSubscriptions(t.Context(), UserListFilter{UserID: 1})
	if err != nil {
		t.Fatalf("UserSubscriptions() returned error: %v", err)
	}
	if captured.Page != 1 || captured.PageSize != 20 {
		t.Fatalf("normalized filter = %+v, want page=1 pageSize=20", captured)
	}
	if result.Page != 1 || result.PageSize != 20 {
		t.Fatalf("result pagination = %+v, want page=1 pageSize=20", result)
	}
}

func TestAdminAssignValidatesRFC3339(t *testing.T) {
	service := NewService(subscriptionStubRepository{})
	_, err := service.AdminAssign(t.Context(), AssignInput{
		UserID:    1,
		GroupID:   2,
		ExpiresAt: "2026-01-01",
	})
	if err != ErrInvalidExpiresAt {
		t.Fatalf("expected ErrInvalidExpiresAt, got %v", err)
	}
}

func TestAdminAdjustValidatesRFC3339(t *testing.T) {
	service := NewService(subscriptionStubRepository{})
	value := "2026-01-01"
	_, err := service.AdminAdjust(t.Context(), 1, AdjustInput{ExpiresAt: &value})
	if err != ErrInvalidAdjustExpiresAt {
		t.Fatalf("expected ErrInvalidAdjustExpiresAt, got %v", err)
	}
}

type subscriptionStubRepository struct {
	listByUser       func(context.Context, UserListFilter) ([]Subscription, int64, error)
	listActiveByUser func(context.Context, int) ([]Subscription, error)
	listAdmin        func(context.Context, AdminListFilter) ([]Subscription, int64, error)
	create           func(context.Context, CreateInput) (Subscription, error)
	bulkCreate       func(context.Context, BulkCreateInput) (int, error)
	update           func(context.Context, int, UpdateInput) (Subscription, error)
}

func (s subscriptionStubRepository) ListByUser(ctx context.Context, filter UserListFilter) ([]Subscription, int64, error) {
	if s.listByUser == nil {
		return nil, 0, nil
	}
	return s.listByUser(ctx, filter)
}

func (s subscriptionStubRepository) ListActiveByUser(ctx context.Context, userID int) ([]Subscription, error) {
	if s.listActiveByUser == nil {
		return nil, nil
	}
	return s.listActiveByUser(ctx, userID)
}

func (s subscriptionStubRepository) ListAdmin(ctx context.Context, filter AdminListFilter) ([]Subscription, int64, error) {
	if s.listAdmin == nil {
		return nil, 0, nil
	}
	return s.listAdmin(ctx, filter)
}

func (s subscriptionStubRepository) Create(ctx context.Context, input CreateInput) (Subscription, error) {
	if s.create == nil {
		return Subscription{
			ID:          1,
			UserID:      input.UserID,
			GroupID:     input.GroupID,
			EffectiveAt: input.EffectiveAt,
			ExpiresAt:   input.ExpiresAt,
			Status:      input.Status,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}, nil
	}
	return s.create(ctx, input)
}

func (s subscriptionStubRepository) BulkCreate(ctx context.Context, input BulkCreateInput) (int, error) {
	if s.bulkCreate == nil {
		return len(input.UserIDs), nil
	}
	return s.bulkCreate(ctx, input)
}

func (s subscriptionStubRepository) Update(ctx context.Context, id int, input UpdateInput) (Subscription, error) {
	if s.update == nil {
		return Subscription{ID: id}, nil
	}
	return s.update(ctx, id, input)
}
