package user

import (
	"context"
	"testing"
)

func TestAdjustBalanceRejectsInvalidAction(t *testing.T) {
	service := NewService(stubRepository{
		findByID: func() (User, error) {
			return User{ID: 1, Balance: 10}, nil
		},
	})

	_, err := service.AdjustBalance(t.Context(), 1, BalanceChange{Action: "noop", Amount: 1})
	if err != ErrInvalidBalanceAction {
		t.Fatalf("expected ErrInvalidBalanceAction, got %v", err)
	}
}

func TestAdjustBalanceRejectsInsufficientBalance(t *testing.T) {
	service := NewService(stubRepository{
		findByID: func() (User, error) {
			return User{ID: 1, Balance: 5}, nil
		},
	})

	_, err := service.AdjustBalance(t.Context(), 1, BalanceChange{Action: "subtract", Amount: 10})
	if err != ErrInsufficientBalance {
		t.Fatalf("expected ErrInsufficientBalance, got %v", err)
	}
}

func TestListAPIKeysNormalizesPagination(t *testing.T) {
	service := NewService(stubRepository{
		listAPIKeys: func(_ context.Context, _ int, page, pageSize int) ([]APIKey, int64, error) {
			if page != 1 || pageSize != 20 {
				t.Fatalf("ListAPIKeys received page=%d pageSize=%d, want 1 and 20", page, pageSize)
			}
			return []APIKey{{ID: 1}}, 1, nil
		},
	})

	result, err := service.ListAPIKeys(t.Context(), 7, 0, 0)
	if err != nil {
		t.Fatalf("ListAPIKeys returned error: %v", err)
	}
	if result.Page != 1 || result.PageSize != 20 || result.Total != 1 || len(result.List) != 1 {
		t.Fatalf("unexpected ListAPIKeys result: %+v", result)
	}
}

type stubRepository struct {
	findByID    func() (User, error)
	listAPIKeys func(context.Context, int, int, int) ([]APIKey, int64, error)
}

func (s stubRepository) FindByID(_ context.Context, _ int, _ bool) (User, error) {
	return s.findByID()
}

func (s stubRepository) List(_ context.Context, _ ListFilter) ([]User, int64, error) {
	return nil, 0, nil
}
func (s stubRepository) EmailExists(_ context.Context, _ string) (bool, error) { return false, nil }
func (s stubRepository) Create(_ context.Context, _ Mutation) (User, error)    { return User{}, nil }
func (s stubRepository) Update(_ context.Context, _ int, _ Mutation) (User, error) {
	return User{}, nil
}
func (s stubRepository) UpdateBalance(_ context.Context, _ int, _ BalanceUpdate) (User, error) {
	return User{}, nil
}
func (s stubRepository) Delete(_ context.Context, _ int) error { return nil }
func (s stubRepository) ListBalanceLogs(_ context.Context, _ int, _, _ int) ([]BalanceLog, int64, error) {
	return nil, 0, nil
}
func (s stubRepository) ListAPIKeys(ctx context.Context, userID, page, pageSize int) ([]APIKey, int64, error) {
	if s.listAPIKeys == nil {
		return nil, 0, nil
	}
	return s.listAPIKeys(ctx, userID, page, pageSize)
}
