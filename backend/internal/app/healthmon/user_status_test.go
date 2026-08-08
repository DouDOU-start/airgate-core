package healthmon

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type userStatusRepository struct {
	visible       []GroupMeta
	success       []SuccessAgg
	failures      []FailureRaw
	visibleUserID int
	successIDs    []int
	failureIDs    []int
	successSince  time.Time
	failureSince  time.Time
	successCalled bool
	failureCalled bool
}

func (r *userStatusRepository) AggregateSuccess(context.Context, time.Time) ([]SuccessAgg, error) {
	return nil, nil
}

func (r *userStatusRepository) AggregateSuccessByGroup(context.Context, time.Time) ([]SuccessAgg, error) {
	return nil, nil
}

func (r *userStatusRepository) AggregateFailureRaws(context.Context, time.Time) ([]FailureRaw, error) {
	return nil, nil
}

func (r *userStatusRepository) AggregateFailureRawsByGroup(context.Context, time.Time) ([]FailureRaw, error) {
	return nil, nil
}

func (r *userStatusRepository) ListKeyMeta(context.Context, []int) ([]KeyMeta, error) {
	return nil, nil
}

func (r *userStatusRepository) ListGroupMeta(context.Context, []int) ([]GroupMeta, error) {
	return nil, nil
}

func (r *userStatusRepository) ListUserVisibleGroupMeta(_ context.Context, userID int) ([]GroupMeta, error) {
	r.visibleUserID = userID
	return r.visible, nil
}

func (r *userStatusRepository) AggregateSuccessByGroupIDs(_ context.Context, since time.Time, ids []int) ([]SuccessAgg, error) {
	r.successSince = since
	r.successCalled = true
	r.successIDs = make([]int, len(ids))
	copy(r.successIDs, ids)
	return r.success, nil
}

func (r *userStatusRepository) AggregateFailureRawsByGroupIDs(_ context.Context, since time.Time, ids []int) ([]FailureRaw, error) {
	r.failureSince = since
	r.failureCalled = true
	r.failureIDs = make([]int, len(ids))
	copy(r.failureIDs, ids)
	return r.failures, nil
}

func (r *userStatusRepository) CountKeyAvailability(context.Context) (int64, int64, error) {
	return 0, 0, nil
}

func (r *userStatusRepository) LatestErrors(context.Context, time.Time, []int) ([]LastErrorRow, error) {
	return nil, nil
}

func (r *userStatusRepository) LatestErrorsByGroup(context.Context, time.Time, []int) ([]LastErrorRow, error) {
	return nil, nil
}

func TestListUserGroupsScopesTrafficToVisibleGroups(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	repo := &userStatusRepository{
		visible: []GroupMeta{
			{ID: 10, Name: "public", Platform: "openai"},
			{ID: 30, Name: "exclusive-allowed", Platform: "claude"},
		},
		success: []SuccessAgg{
			{DimID: 10, Count: 10, AvgDuration: 1200, MaxDuration: 1500, AvgTTFT: 600, MaxTTFT: 800},
			{DimID: 30, Count: 8, AvgDuration: 2000, MaxDuration: 2500},
			// Repository output is treated defensively: metadata remains the output allow-list.
			{DimID: 99, Count: 1000, AvgDuration: 1, MaxDuration: 1},
		},
		failures: []FailureRaw{
			{GroupID: 30, StatusCode: 429, Count: 2},
			{GroupID: 99, StatusCode: 500, Count: 1000},
		},
	}
	service := NewService(repo)
	service.now = func() time.Time { return now }

	rows, err := service.ListUserGroups(context.Background(), 42, "1h")
	if err != nil {
		t.Fatalf("ListUserGroups returned error: %v", err)
	}
	if repo.visibleUserID != 42 {
		t.Fatalf("visible group lookup user = %d, want 42", repo.visibleUserID)
	}
	wantIDs := []int{10, 30}
	if !reflect.DeepEqual(repo.successIDs, wantIDs) || !reflect.DeepEqual(repo.failureIDs, wantIDs) {
		t.Fatalf("aggregate ids = success %v failure %v, want %v", repo.successIDs, repo.failureIDs, wantIDs)
	}
	wantSince := now.Add(-time.Hour)
	if !repo.successSince.Equal(wantSince) || !repo.failureSince.Equal(wantSince) {
		t.Fatalf("aggregate since = success %s failure %s, want %s", repo.successSince, repo.failureSince, wantSince)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2; rows=%+v", len(rows), rows)
	}

	byID := make(map[int]UserGroupStatus, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	if _, leaked := byID[99]; leaked {
		t.Fatal("aggregate-only group 99 leaked into user response")
	}
	if got := byID[10]; got.Status != UserHealthHealthy || got.Sample.N != 10 || got.HealthScore == nil || *got.HealthScore != 100 {
		t.Fatalf("healthy row = %+v, want healthy N=10 score=100", got)
	}
	if got := byID[30]; got.Status != UserHealthUnhealthy || got.Sample.N != 10 || got.Sample.E != 2 {
		t.Fatalf("unhealthy row = %+v, want unhealthy N=10 E=2", got)
	}
}

func TestListUserGroupsWithNoVisibleGroupsStaysEmpty(t *testing.T) {
	repo := &userStatusRepository{
		// Even a broken repository result must not create a row without visible metadata.
		success:  []SuccessAgg{{DimID: 99, Count: 100}},
		failures: []FailureRaw{{GroupID: 99, StatusCode: 500, Count: 100}},
	}
	service := NewService(repo)

	rows, err := service.ListUserGroups(context.Background(), 7, "5m")
	if err != nil {
		t.Fatalf("ListUserGroups returned error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %+v, want empty", rows)
	}
	if !repo.successCalled || !repo.failureCalled || repo.successIDs == nil || repo.failureIDs == nil {
		t.Fatalf("empty visible set must be passed as an explicit empty allow-list: success=%v failure=%v", repo.successIDs, repo.failureIDs)
	}
}

type staticSettingsLister struct {
	items []SettingItem
	err   error
}

func (l staticSettingsLister) List(context.Context, string) ([]SettingItem, error) {
	return l.items, l.err
}

func TestChannelStatusEnabledDefaultsTrue(t *testing.T) {
	service := NewService(&userStatusRepository{})
	if !service.ChannelStatusEnabled(context.Background()) {
		t.Fatal("nil settings lister should default to enabled")
	}

	service.SetSettingsLister(staticSettingsLister{})
	if !service.ChannelStatusEnabled(context.Background()) {
		t.Fatal("missing key should default to enabled")
	}

	service.SetSettingsLister(staticSettingsLister{
		items: []SettingItem{{Key: settingKeyChannelStatusEnabled, Value: "false"}},
	})
	if service.ChannelStatusEnabled(context.Background()) {
		t.Fatal("explicit false should disable channel status page")
	}
}

func TestUserChannelStatusRejectsWhenDisabled(t *testing.T) {
	repo := &userStatusRepository{
		visible: []GroupMeta{{ID: 10, Name: "public", Platform: "openai"}},
		success: []SuccessAgg{{DimID: 10, Count: 5}},
	}
	service := NewService(repo)
	service.SetSettingsLister(staticSettingsLister{
		items: []SettingItem{{Key: settingKeyChannelStatusEnabled, Value: "false"}},
	})

	if _, err := service.UserOverview(context.Background(), 1, "1h"); !errors.Is(err, ErrChannelStatusDisabled) {
		t.Fatalf("UserOverview err = %v, want ErrChannelStatusDisabled", err)
	}
	if _, err := service.ListUserGroups(context.Background(), 1, "1h"); !errors.Is(err, ErrChannelStatusDisabled) {
		t.Fatalf("ListUserGroups err = %v, want ErrChannelStatusDisabled", err)
	}
	if repo.successCalled || repo.failureCalled {
		t.Fatal("disabled gate must not query traffic aggregates")
	}
}

func TestUserHealthStatusBoundaries(t *testing.T) {
	score69, score70, score89, score90 := 69, 70, 89, 90
	tests := []struct {
		name   string
		sample Sample
		score  *int
		want   UserHealthStatus
	}{
		{name: "idle", sample: Sample{Idle: true}, score: &score90, want: UserHealthIdle},
		{name: "low sample takes precedence", sample: Sample{N: 1, LowSample: true}, score: &score90, want: UserHealthLowSample},
		{name: "missing score", sample: Sample{N: 10}, want: UserHealthUnhealthy},
		{name: "below 70", sample: Sample{N: 10}, score: &score69, want: UserHealthUnhealthy},
		{name: "70 is degraded", sample: Sample{N: 10}, score: &score70, want: UserHealthDegraded},
		{name: "89 is degraded", sample: Sample{N: 10}, score: &score89, want: UserHealthDegraded},
		{name: "90 is healthy", sample: Sample{N: 10}, score: &score90, want: UserHealthHealthy},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := userHealthStatus(test.sample, test.score); got != test.want {
				t.Fatalf("userHealthStatus() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestListUserGroupsRemovesBilledFailuresFromSuccessCount(t *testing.T) {
	tests := []struct {
		name     string
		success  int64
		failures []FailureRaw
		wantS    int64
		wantE    int64
		wantN    int64
	}{
		{
			name:    "billed SLA failure moves from success to error",
			success: 10,
			failures: []FailureRaw{
				{GroupID: 1, StatusCode: 429, Billed: true, Count: 2},
			},
			wantS: 8,
			wantE: 2,
			wantN: 10,
		},
		{
			name:    "billed client failure is removed but excluded from SLA",
			success: 10,
			failures: []FailureRaw{
				{GroupID: 1, StatusCode: 400, Billed: true, Count: 2},
			},
			wantS: 8,
			wantE: 0,
			wantN: 8,
		},
		{
			name:    "unbilled SLA failure adds to denominator",
			success: 10,
			failures: []FailureRaw{
				{GroupID: 1, StatusCode: 400, Billed: true, Count: 2},
				{GroupID: 1, StatusCode: 500, Billed: false, Count: 3},
			},
			wantS: 8,
			wantE: 3,
			wantN: 11,
		},
		{
			name:    "billed failure subtraction clamps at zero",
			success: 2,
			failures: []FailureRaw{
				{GroupID: 1, StatusCode: 400, Billed: true, Count: 5},
			},
			wantS: 0,
			wantE: 0,
			wantN: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &userStatusRepository{
				visible:  []GroupMeta{{ID: 1, Name: "visible"}},
				success:  []SuccessAgg{{DimID: 1, Count: test.success}},
				failures: test.failures,
			}
			service := NewService(repo)
			rows, err := service.ListUserGroups(context.Background(), 42, "1h")
			if err != nil {
				t.Fatalf("ListUserGroups returned error: %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("len(rows) = %d, want 1; rows=%+v", len(rows), rows)
			}
			got := rows[0].Sample
			if got.S != test.wantS || got.E != test.wantE || got.N != test.wantN {
				t.Fatalf("sample = %+v, want S=%d E=%d N=%d", got, test.wantS, test.wantE, test.wantN)
			}

			overview, err := service.UserOverview(context.Background(), 42, "1h")
			if err != nil {
				t.Fatalf("UserOverview returned error: %v", err)
			}
			got = overview.Sample
			if got.S != test.wantS || got.E != test.wantE || got.N != test.wantN {
				t.Fatalf("overview sample = %+v, want S=%d E=%d N=%d", got, test.wantS, test.wantE, test.wantN)
			}
		})
	}
}
