package group

import (
	"context"
	"testing"
	"time"
)

func TestListNormalizesPagination(t *testing.T) {
	var captured ListFilter

	service := NewService(groupStubRepository{
		list: func(_ context.Context, filter ListFilter) ([]Group, int64, error) {
			captured = filter
			return nil, 0, nil
		},
	}, stubConcurrencyReader{})

	result, err := service.List(t.Context(), ListFilter{})
	if err != nil {
		t.Fatalf("List() returned error: %v", err)
	}
	if captured.Page != 1 || captured.PageSize != 20 {
		t.Fatalf("List() normalized filter = %+v, want page=1 pageSize=20", captured)
	}
	if result.Page != 1 || result.PageSize != 20 {
		t.Fatalf("List() result pagination = %+v, want page=1 pageSize=20", result)
	}
}

type stubConcurrencyReader struct{}

func (stubConcurrencyReader) GetGroupCurrentCounts(_ context.Context, _ []int) map[int]int {
	return nil
}

type groupStubRepository struct {
	list             func(context.Context, ListFilter) ([]Group, int64, error)
	listAvailable    func(context.Context, AvailableFilter) ([]Group, int64, error)
	findByID         func(context.Context, int) (Group, error)
	create           func(context.Context, CreateInput) (Group, error)
	update           func(context.Context, int, UpdateInput) (Group, error)
	delete           func(context.Context, int) error
	statsForGroups   func(context.Context, []int) (map[int]GroupStats, error)
	publicRates      func(context.Context) ([]float64, error)
	allowedUsers     func(context.Context, int) ([]AllowedUser, error)
	grantUser        func(context.Context, int, int) error
	revokeUser       func(context.Context, int, int) error
	bindChannelKey   func(context.Context, int, int) error
	unbindChannelKey func(context.Context, int, int) error
}

func (s groupStubRepository) List(ctx context.Context, filter ListFilter) ([]Group, int64, error) {
	if s.list == nil {
		return nil, 0, nil
	}
	return s.list(ctx, filter)
}

func (s groupStubRepository) ListAvailable(ctx context.Context, filter AvailableFilter) ([]Group, int64, error) {
	if s.listAvailable == nil {
		return nil, 0, nil
	}
	return s.listAvailable(ctx, filter)
}

func (s groupStubRepository) FindByID(ctx context.Context, id int) (Group, error) {
	if s.findByID == nil {
		return Group{}, nil
	}
	return s.findByID(ctx, id)
}

func (s groupStubRepository) Create(ctx context.Context, input CreateInput) (Group, error) {
	if s.create == nil {
		return Group{}, nil
	}
	return s.create(ctx, input)
}

func (s groupStubRepository) Update(ctx context.Context, id int, input UpdateInput) (Group, error) {
	if s.update == nil {
		return Group{}, nil
	}
	return s.update(ctx, id, input)
}

func (s groupStubRepository) Delete(ctx context.Context, id int) error {
	if s.delete == nil {
		return nil
	}
	return s.delete(ctx, id)
}

func (s groupStubRepository) StatsForGroups(ctx context.Context, groupIDs []int, _ time.Time) (map[int]GroupStats, error) {
	if s.statsForGroups == nil {
		return nil, nil
	}
	return s.statsForGroups(ctx, groupIDs)
}

func (s groupStubRepository) PublicRateMultipliers(ctx context.Context) ([]float64, error) {
	if s.publicRates == nil {
		return nil, nil
	}
	return s.publicRates(ctx)
}

func (s groupStubRepository) AllowedUsers(ctx context.Context, groupID int) ([]AllowedUser, error) {
	if s.allowedUsers == nil {
		return nil, nil
	}
	return s.allowedUsers(ctx, groupID)
}

func (s groupStubRepository) GrantAllowedUser(ctx context.Context, groupID, userID int) error {
	if s.grantUser == nil {
		return nil
	}
	return s.grantUser(ctx, groupID, userID)
}

func (s groupStubRepository) RevokeAllowedUser(ctx context.Context, groupID, userID int) error {
	if s.revokeUser == nil {
		return nil
	}
	return s.revokeUser(ctx, groupID, userID)
}

func (s groupStubRepository) BindChannelKey(ctx context.Context, groupID, channelKeyID int) error {
	if s.bindChannelKey == nil {
		return nil
	}
	return s.bindChannelKey(ctx, groupID, channelKeyID)
}

func (s groupStubRepository) UnbindChannelKey(ctx context.Context, groupID, channelKeyID int) error {
	if s.unbindChannelKey == nil {
		return nil
	}
	return s.unbindChannelKey(ctx, groupID, channelKeyID)
}
