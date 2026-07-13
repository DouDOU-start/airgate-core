package user

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestAdjustBalanceRejectsInvalidAction(t *testing.T) {
	service := NewService(stubRepository{
		updateBalance: func(BalanceChange) (User, error) {
			t.Fatal("非法 action 不应触达仓储层")
			return User{}, nil
		},
	})

	_, err := service.AdjustBalance(t.Context(), 1, BalanceChange{Action: "noop", Amount: 1})
	if err != ErrInvalidBalanceAction {
		t.Fatalf("expected ErrInvalidBalanceAction, got %v", err)
	}
}

// TestAdjustBalanceSurfacesInsufficientBalance 余额不足由 store 在事务内判定，
// service 原样透出并不再自行预读余额。
func TestAdjustBalanceSurfacesInsufficientBalance(t *testing.T) {
	service := NewService(stubRepository{
		updateBalance: func(change BalanceChange) (User, error) {
			if change.Action != "subtract" || change.Amount != 10 {
				t.Fatalf("unexpected change: %+v", change)
			}
			return User{}, ErrInsufficientBalance
		},
	})

	_, err := service.AdjustBalance(t.Context(), 1, BalanceChange{Action: "subtract", Amount: 10})
	if err != ErrInsufficientBalance {
		t.Fatalf("expected ErrInsufficientBalance, got %v", err)
	}
}

// TestCheckBalanceAlertRollsBackNotifiedOnSendFailure 邮件发送失败时应回滚
// notified 标记，使下次消费能够重试；而非永久卡在「已通知」状态。
func TestCheckBalanceAlertRollsBackNotifiedOnSendFailure(t *testing.T) {
	notifiedCalls := make(chan bool, 2)
	service := NewService(stubRepository{
		findByID: func() (User, error) {
			return User{
				ID:                    1,
				Email:                 "user@example.com",
				Balance:               10,
				BalanceAlertThreshold: 20,
				BalanceAlertNotified:  false,
			}, nil
		},
		setBalanceAlertNotified: func(_ context.Context, userID int, notified bool) error {
			if userID != 1 {
				t.Fatalf("unexpected userID: %d", userID)
			}
			notifiedCalls <- notified
			return nil
		},
	})
	service.SetBalanceAlertCallback(func(string, float64, float64) error {
		return errBalanceAlertSendFailedForTest
	})

	service.CheckBalanceAlert(t.Context(), 1)

	first := waitForNotifiedCall(t, notifiedCalls)
	if !first {
		t.Fatalf("expected notified=true to be set first (claim before async send), got false")
	}
	second := waitForNotifiedCall(t, notifiedCalls)
	if second {
		t.Fatalf("expected notified to be rolled back to false after send failure, got true")
	}
}

var errBalanceAlertSendFailedForTest = errors.New("smtp not configured")

func waitForNotifiedCall(t *testing.T, ch chan bool) bool {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SetBalanceAlertNotified call")
		return false
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

// TestListByRuntimeStatSortsAndPaginates 并发/RPM 排序：先取全量候选 id，按
// 指标降序排序后再分页，且分页数据取的是排序后那一页对应的 id。
func TestListByRuntimeStatSortsAndPaginates(t *testing.T) {
	allIDs := []int{1, 2, 3, 4}
	repo := stubRepository{
		listIDs: func(_ context.Context, filter ListFilter) ([]int, error) {
			if filter.SortBy != SortByConcurrency {
				t.Fatalf("unexpected SortBy passed to ListIDs: %q", filter.SortBy)
			}
			return append([]int(nil), allIDs...), nil
		},
		listByIDs: func(_ context.Context, ids []int) ([]User, error) {
			// 期望第 1 页（page_size=2）取到并发数最高的两个用户：3(9)、1(5)。
			want := []int{1, 3}
			got := append([]int(nil), ids...)
			sort.Ints(got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ListByIDs got ids=%v, want=%v", got, want)
			}
			users := make([]User, 0, len(ids))
			for _, id := range ids {
				users = append(users, User{ID: id})
			}
			return users, nil
		},
	}
	service := NewService(repo)
	service.SetRuntimeStatsReaders(stubConcurrencyReader{1: 5, 2: 1, 3: 9, 4: 2}, nil)

	result, err := service.List(t.Context(), ListFilter{Page: 1, PageSize: 2, SortBy: SortByConcurrency})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if result.Total != 4 {
		t.Fatalf("Total = %d, want 4", result.Total)
	}
	if len(result.List) != 2 || result.List[0].ID != 3 || result.List[1].ID != 1 {
		t.Fatalf("unexpected sorted page: %+v", result.List)
	}
}

// TestListByRuntimeStatRejectsTooManyCandidates 候选集超过阈值时直接拒绝排序，
// 而不是悄悄退化成默认排序或对海量 id 发起超大 Redis pipeline。
func TestListByRuntimeStatRejectsTooManyCandidates(t *testing.T) {
	ids := make([]int, maxRuntimeStatSortCandidates+1)
	for i := range ids {
		ids[i] = i + 1
	}
	repo := stubRepository{
		listIDs: func(_ context.Context, _ ListFilter) ([]int, error) {
			return ids, nil
		},
		listByIDs: func(_ context.Context, _ []int) ([]User, error) {
			t.Fatal("候选集超限时不应继续查询用户详情")
			return nil, nil
		},
	}
	service := NewService(repo)
	service.SetRuntimeStatsReaders(stubConcurrencyReader{}, nil)

	_, err := service.List(t.Context(), ListFilter{Page: 1, PageSize: 20, SortBy: SortByRPM})
	if !errors.Is(err, ErrTooManySortCandidates) {
		t.Fatalf("expected ErrTooManySortCandidates, got %v", err)
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
	findByID                func() (User, error)
	list                    func(context.Context, ListFilter) ([]User, int64, error)
	listIDs                 func(context.Context, ListFilter) ([]int, error)
	listByIDs               func(context.Context, []int) ([]User, error)
	listAPIKeys             func(context.Context, int, int, int) ([]APIKey, int64, error)
	updateBalance           func(BalanceChange) (User, error)
	setBalanceAlertNotified func(context.Context, int, bool) error
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
func (s stubRepository) ListIDs(ctx context.Context, filter ListFilter) ([]int, error) {
	if s.listIDs == nil {
		return nil, nil
	}
	return s.listIDs(ctx, filter)
}
func (s stubRepository) ListByIDs(ctx context.Context, ids []int) ([]User, error) {
	if s.listByIDs == nil {
		return nil, nil
	}
	return s.listByIDs(ctx, ids)
}
func (s stubRepository) EmailExists(_ context.Context, _ string) (bool, error) { return false, nil }
func (s stubRepository) ListWithGroupRateOverride(_ context.Context, _ int64) ([]GroupRateOverride, error) {
	return nil, nil
}
func (s stubRepository) Create(_ context.Context, _ Mutation) (User, error) { return User{}, nil }
func (s stubRepository) Update(_ context.Context, _ int, _ Mutation) (User, error) {
	return User{}, nil
}
func (s stubRepository) UpdateBalance(_ context.Context, _ int, change BalanceChange) (User, error) {
	if s.updateBalance != nil {
		return s.updateBalance(change)
	}
	return User{}, nil
}
func (s stubRepository) Delete(_ context.Context, _ int) error { return nil }
func (s stubRepository) ListBalanceLogs(_ context.Context, _ int, _, _ int) ([]BalanceLog, int64, error) {
	return nil, 0, nil
}
func (s stubRepository) UpdateBalanceAlert(_ context.Context, _ int, _ float64) error { return nil }
func (s stubRepository) SetBalanceAlertNotified(ctx context.Context, userID int, notified bool) error {
	if s.setBalanceAlertNotified != nil {
		return s.setBalanceAlertNotified(ctx, userID, notified)
	}
	return nil
}
func (s stubRepository) ListAPIKeys(ctx context.Context, userID, page, pageSize int, _ time.Time) ([]APIKey, int64, error) {
	if s.listAPIKeys == nil {
		return nil, 0, nil
	}
	return s.listAPIKeys(ctx, userID, page, pageSize)
}
func (s stubRepository) GetAPIKeyInfo(_ context.Context, _ int) (APIKeyBrief, error) {
	return APIKeyBrief{}, nil
}
