package user

import (
	"context"
	"testing"
	"time"
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

	result, err := service.ListAPIKeys(t.Context(), 7, 0, 0, "")
	if err != nil {
		t.Fatalf("ListAPIKeys returned error: %v", err)
	}
	if result.Page != 1 || result.PageSize != 20 || result.Total != 1 || len(result.List) != 1 {
		t.Fatalf("unexpected ListAPIKeys result: %+v", result)
	}
}

// stubConcurrencyReader / stubRPMReader 固定映射的运行时指标读取器。
type stubConcurrencyReader map[int]int

func (s stubConcurrencyReader) GetUserCurrentCounts(_ context.Context, _ []int) map[int]int {
	return s
}

type stubRPMReader map[int]int

func (s stubRPMReader) GetUserRPMs(_ context.Context, _ []int) map[int]int { return s }

// TestListAttachesRuntimeStats 列表按用户回填并发/RPM；读取器缺项与未注入均保持 0。
func TestListAttachesRuntimeStats(t *testing.T) {
	users := []User{{ID: 1}, {ID: 2}, {ID: 3}}
	repo := stubRepository{
		list: func(_ context.Context, _ ListFilter) ([]User, int64, error) {
			return append([]User(nil), users...), int64(len(users)), nil
		},
	}

	cases := []struct {
		name        string
		concurrency ConcurrencyReader
		rpm         RPMReader
		wantConc    map[int]int
		wantRPM     map[int]int
	}{
		{
			name:        "读取器命中与缺项",
			concurrency: stubConcurrencyReader{1: 4, 2: 0},
			rpm:         stubRPMReader{1: 120, 3: 7},
			wantConc:    map[int]int{1: 4, 2: 0, 3: 0},
			wantRPM:     map[int]int{1: 120, 2: 0, 3: 7},
		},
		{
			name:     "未注入读取器保持零值",
			wantConc: map[int]int{1: 0, 2: 0, 3: 0},
			wantRPM:  map[int]int{1: 0, 2: 0, 3: 0},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := NewService(repo)
			service.SetRuntimeStatsReaders(tc.concurrency, tc.rpm)

			result, err := service.List(t.Context(), ListFilter{})
			if err != nil {
				t.Fatalf("List returned error: %v", err)
			}
			for _, u := range result.List {
				if u.CurrentConcurrency != tc.wantConc[u.ID] {
					t.Errorf("user %d CurrentConcurrency = %d, want %d", u.ID, u.CurrentConcurrency, tc.wantConc[u.ID])
				}
				if u.CurrentRPM != tc.wantRPM[u.ID] {
					t.Errorf("user %d CurrentRPM = %d, want %d", u.ID, u.CurrentRPM, tc.wantRPM[u.ID])
				}
			}
		})
	}
}

type stubRepository struct {
	findByID    func() (User, error)
	list        func(context.Context, ListFilter) ([]User, int64, error)
	listAPIKeys func(context.Context, int, int, int) ([]APIKey, int64, error)
}

func (s stubRepository) FindByID(_ context.Context, _ int, _ bool) (User, error) {
	return s.findByID()
}

func (s stubRepository) List(ctx context.Context, filter ListFilter) ([]User, int64, error) {
	if s.list == nil {
		return nil, 0, nil
	}
	return s.list(ctx, filter)
}
func (s stubRepository) EmailExists(_ context.Context, _ string) (bool, error) { return false, nil }
func (s stubRepository) ListWithGroupRateOverride(_ context.Context, _ int64) ([]GroupRateOverride, error) {
	return nil, nil
}
func (s stubRepository) Create(_ context.Context, _ Mutation) (User, error) { return User{}, nil }
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
func (s stubRepository) UpdateBalanceAlert(_ context.Context, _ int, _ float64) error { return nil }
func (s stubRepository) SetBalanceAlertNotified(_ context.Context, _ int, _ bool) error {
	return nil
}
func (s stubRepository) ListAPIKeys(ctx context.Context, userID, page, pageSize int, _ time.Time) ([]APIKey, int64, error) {
	if s.listAPIKeys == nil {
		return nil, 0, nil
	}
	return s.listAPIKeys(ctx, userID, page, pageSize)
}
func (s stubRepository) GetAPIKeyName(_ context.Context, _ int) (string, error) {
	return "", nil
}
func (s stubRepository) GetAPIKeyInfo(_ context.Context, _ int) (APIKeyBrief, error) {
	return APIKeyBrief{}, nil
}
