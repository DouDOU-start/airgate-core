package announcement

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIsActiveAt(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	before := now.Add(-time.Hour)
	after := now.Add(time.Hour)

	tests := []struct {
		name string
		item Announcement
		want bool
	}{
		{"active 无窗口", Announcement{Status: StatusActive}, true},
		{"draft 不生效", Announcement{Status: StatusDraft}, false},
		{"archived 不生效", Announcement{Status: StatusArchived}, false},
		{"窗口内", Announcement{Status: StatusActive, StartsAt: &before, EndsAt: &after}, true},
		{"未到开始时间", Announcement{Status: StatusActive, StartsAt: &after}, false},
		{"已到结束时间", Announcement{Status: StatusActive, EndsAt: &before}, false},
		{"结束时间等于 now 视为下线", Announcement{Status: StatusActive, EndsAt: &now}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.item.IsActiveAt(now); got != tt.want {
				t.Fatalf("IsActiveAt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateValidation(t *testing.T) {
	badTime := "not-a-time"
	starts := "2026-07-08T12:00:00Z"
	endsBefore := "2026-07-08T11:00:00Z"

	tests := []struct {
		name    string
		input   CreateInput
		wantErr error
	}{
		{"非法状态", CreateInput{Title: "t", Content: "c", Status: "bogus"}, ErrInvalidStatus},
		{"非法通知模式", CreateInput{Title: "t", Content: "c", NotifyMode: "bogus"}, ErrInvalidNotifyMode},
		{"非法时间格式", CreateInput{Title: "t", Content: "c", StartsAt: &badTime}, ErrInvalidTime},
		{"开始晚于结束", CreateInput{Title: "t", Content: "c", StartsAt: &starts, EndsAt: &endsBefore}, ErrInvalidTimeRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewService(announcementStubRepository{})
			_, err := service.Create(t.Context(), tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Create() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCreateDefaults(t *testing.T) {
	var captured CreateRecord
	service := NewService(announcementStubRepository{
		create: func(_ context.Context, record CreateRecord) (Announcement, error) {
			captured = record
			return Announcement{ID: 1}, nil
		},
	})

	if _, err := service.Create(t.Context(), CreateInput{Title: "t", Content: "c"}); err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}
	if captured.Status != StatusDraft {
		t.Fatalf("默认状态 = %q, want %q", captured.Status, StatusDraft)
	}
	if captured.NotifyMode != NotifyModeSilent {
		t.Fatalf("默认通知模式 = %q, want %q", captured.NotifyMode, NotifyModeSilent)
	}
}

func TestUpdateTimeRangeMergesExisting(t *testing.T) {
	existingEnds := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	service := NewService(announcementStubRepository{
		findByID: func(_ context.Context, _ int) (Announcement, error) {
			return Announcement{ID: 1, Status: StatusActive, EndsAt: &existingEnds}, nil
		},
	})

	// 仅更新开始时间，但晚于库中已有的结束时间：应报窗口非法。
	lateStarts := "2026-07-08T11:00:00Z"
	_, err := service.Update(t.Context(), 1, UpdateInput{StartsAt: &lateStarts})
	if !errors.Is(err, ErrInvalidTimeRange) {
		t.Fatalf("Update() error = %v, want %v", err, ErrInvalidTimeRange)
	}
}

func TestListForUserOrdering(t *testing.T) {
	readAt := time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC)
	service := NewService(announcementStubRepository{
		listActive: func(_ context.Context, _ time.Time, _ int) ([]Announcement, error) {
			// 仓储按 ID 倒序返回
			return []Announcement{{ID: 3}, {ID: 2}, {ID: 1}}, nil
		},
		readTimes: func(_ context.Context, _ int, _ []int) (map[int]time.Time, error) {
			return map[int]time.Time{3: readAt}, nil
		},
	})

	list, err := service.ListForUser(t.Context(), 7, false)
	if err != nil {
		t.Fatalf("ListForUser() returned error: %v", err)
	}
	gotIDs := make([]int, 0, len(list))
	for _, item := range list {
		gotIDs = append(gotIDs, item.ID)
	}
	// 未读（2、1）在前，已读（3）在后
	wantIDs := []int{2, 1, 3}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Fatalf("ListForUser() 顺序 = %v, want %v", gotIDs, wantIDs)
		}
	}
	if list[2].ReadAt == nil || !list[2].ReadAt.Equal(readAt) {
		t.Fatalf("已读时间 = %v, want %v", list[2].ReadAt, readAt)
	}

	unread, err := service.ListForUser(t.Context(), 7, true)
	if err != nil {
		t.Fatalf("ListForUser(unreadOnly) returned error: %v", err)
	}
	if len(unread) != 2 {
		t.Fatalf("ListForUser(unreadOnly) 条数 = %d, want 2", len(unread))
	}
}

func TestMarkReadRejectsInvisible(t *testing.T) {
	service := NewService(announcementStubRepository{
		findByID: func(_ context.Context, _ int) (Announcement, error) {
			return Announcement{ID: 1, Status: StatusDraft}, nil
		},
		markRead: func(_ context.Context, _, _ int, _ time.Time) error {
			t.Fatal("不可见公告不应写入已读记录")
			return nil
		},
	})

	err := service.MarkRead(t.Context(), 7, 1)
	if !errors.Is(err, ErrAnnouncementNotFound) {
		t.Fatalf("MarkRead() error = %v, want %v", err, ErrAnnouncementNotFound)
	}
}

type announcementStubRepository struct {
	list         func(context.Context, ListFilter) ([]Announcement, int64, error)
	findByID     func(context.Context, int) (Announcement, error)
	create       func(context.Context, CreateRecord) (Announcement, error)
	update       func(context.Context, int, UpdateRecord) (Announcement, error)
	delete       func(context.Context, int) error
	listActive   func(context.Context, time.Time, int) ([]Announcement, error)
	readTimes    func(context.Context, int, []int) (map[int]time.Time, error)
	markRead     func(context.Context, int, int, time.Time) error
	markReadBulk func(context.Context, int, []int, time.Time) error
}

func (s announcementStubRepository) List(ctx context.Context, filter ListFilter) ([]Announcement, int64, error) {
	if s.list == nil {
		return nil, 0, nil
	}
	return s.list(ctx, filter)
}

func (s announcementStubRepository) FindByID(ctx context.Context, id int) (Announcement, error) {
	if s.findByID == nil {
		return Announcement{}, nil
	}
	return s.findByID(ctx, id)
}

func (s announcementStubRepository) Create(ctx context.Context, record CreateRecord) (Announcement, error) {
	if s.create == nil {
		return Announcement{}, nil
	}
	return s.create(ctx, record)
}

func (s announcementStubRepository) Update(ctx context.Context, id int, record UpdateRecord) (Announcement, error) {
	if s.update == nil {
		return Announcement{}, nil
	}
	return s.update(ctx, id, record)
}

func (s announcementStubRepository) Delete(ctx context.Context, id int) error {
	if s.delete == nil {
		return nil
	}
	return s.delete(ctx, id)
}

func (s announcementStubRepository) ListActive(ctx context.Context, now time.Time, limit int) ([]Announcement, error) {
	if s.listActive == nil {
		return nil, nil
	}
	return s.listActive(ctx, now, limit)
}

func (s announcementStubRepository) ReadTimes(ctx context.Context, userID int, ids []int) (map[int]time.Time, error) {
	if s.readTimes == nil {
		return map[int]time.Time{}, nil
	}
	return s.readTimes(ctx, userID, ids)
}

func (s announcementStubRepository) MarkRead(ctx context.Context, announcementID, userID int, readAt time.Time) error {
	if s.markRead == nil {
		return nil
	}
	return s.markRead(ctx, announcementID, userID, readAt)
}

func (s announcementStubRepository) MarkReadBulk(ctx context.Context, userID int, ids []int, readAt time.Time) error {
	if s.markReadBulk == nil {
		return nil
	}
	return s.markReadBulk(ctx, userID, ids, readAt)
}
