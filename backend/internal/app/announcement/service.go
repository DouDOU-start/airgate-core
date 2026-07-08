package announcement

import (
	"context"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/pkg/pagination"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// activeListLimit 用户端单次拉取生效公告的上限，防止历史公告无限膨胀拖垮接口。
const activeListLimit = 200

// Service 提供公告域用例编排。
type Service struct {
	repo Repository
}

// NewService 创建公告服务。
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List 查询管理员公告列表。
func (s *Service) List(ctx context.Context, filter ListFilter) (ListResult, error) {
	page, pageSize := pagination.Normalize(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	if filter.Status != "" && !validStatus(filter.Status) {
		return ListResult{}, ErrInvalidStatus
	}

	list, total, err := s.repo.List(ctx, filter)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("announcement_lookup_failed",
			"op", "list",
			sdk.LogFieldError, err)
		return ListResult{}, err
	}

	return ListResult{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// Get 获取公告详情。
func (s *Service) Get(ctx context.Context, id int) (Announcement, error) {
	item, err := s.repo.FindByID(ctx, id)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("announcement_lookup_failed",
			"announcement_id", id,
			sdk.LogFieldError, err)
	}
	return item, err
}

// Create 创建公告。
func (s *Service) Create(ctx context.Context, input CreateInput) (Announcement, error) {
	logger := sdk.LoggerFromContext(ctx)

	record := CreateRecord{
		Title:      input.Title,
		Content:    input.Content,
		Status:     defaultString(input.Status, StatusDraft),
		NotifyMode: defaultString(input.NotifyMode, NotifyModeSilent),
	}
	if !validStatus(record.Status) {
		return Announcement{}, ErrInvalidStatus
	}
	if !validNotifyMode(record.NotifyMode) {
		return Announcement{}, ErrInvalidNotifyMode
	}

	startsAt, _, err := parseTimeInput(input.StartsAt)
	if err != nil {
		return Announcement{}, err
	}
	endsAt, _, err := parseTimeInput(input.EndsAt)
	if err != nil {
		return Announcement{}, err
	}
	if err := validateTimeRange(startsAt, endsAt); err != nil {
		return Announcement{}, err
	}
	record.StartsAt = startsAt
	record.EndsAt = endsAt

	item, err := s.repo.Create(ctx, record)
	if err != nil {
		logger.Error("announcement_persist_failed",
			"op", "create",
			"title", input.Title,
			sdk.LogFieldError, err)
		return item, err
	}
	logger.Info("announcement_create_succeeded",
		"announcement_id", item.ID,
		"status", item.Status)
	return item, nil
}

// Update 更新公告。
func (s *Service) Update(ctx context.Context, id int, input UpdateInput) (Announcement, error) {
	logger := sdk.LoggerFromContext(ctx)

	if input.Status != nil && !validStatus(*input.Status) {
		return Announcement{}, ErrInvalidStatus
	}
	if input.NotifyMode != nil && !validNotifyMode(*input.NotifyMode) {
		return Announcement{}, ErrInvalidNotifyMode
	}

	record := UpdateRecord{
		Title:      input.Title,
		Content:    input.Content,
		Status:     input.Status,
		NotifyMode: input.NotifyMode,
	}

	startsAt, hasStartsAt, err := parseTimeInput(input.StartsAt)
	if err != nil {
		return Announcement{}, err
	}
	endsAt, hasEndsAt, err := parseTimeInput(input.EndsAt)
	if err != nil {
		return Announcement{}, err
	}
	record.StartsAt, record.HasStartsAt = startsAt, hasStartsAt
	record.EndsAt, record.HasEndsAt = endsAt, hasEndsAt

	// 起止窗口校验须基于更新后的有效值：只改一侧时与库中另一侧合并判断。
	if hasStartsAt || hasEndsAt {
		existing, err := s.repo.FindByID(ctx, id)
		if err != nil {
			return Announcement{}, err
		}
		effStarts, effEnds := existing.StartsAt, existing.EndsAt
		if hasStartsAt {
			effStarts = startsAt
		}
		if hasEndsAt {
			effEnds = endsAt
		}
		if err := validateTimeRange(effStarts, effEnds); err != nil {
			return Announcement{}, err
		}
	}

	item, err := s.repo.Update(ctx, id, record)
	if err != nil {
		logger.Error("announcement_persist_failed",
			"op", "update",
			"announcement_id", id,
			sdk.LogFieldError, err)
		return item, err
	}
	logger.Info("announcement_update_succeeded", "announcement_id", id)
	return item, nil
}

// Delete 删除公告（连带清理已读记录）。
func (s *Service) Delete(ctx context.Context, id int) error {
	logger := sdk.LoggerFromContext(ctx)
	if err := s.repo.Delete(ctx, id); err != nil {
		logger.Error("announcement_persist_failed",
			"op", "delete",
			"announcement_id", id,
			sdk.LogFieldError, err)
		return err
	}
	logger.Info("announcement_delete_succeeded", "announcement_id", id)
	return nil
}

// ListForUser 查询用户可见公告：当前生效的公告，未读优先，同组按 ID 倒序。
func (s *Service) ListForUser(ctx context.Context, userID int, unreadOnly bool) ([]UserAnnouncement, error) {
	active, err := s.repo.ListActive(ctx, time.Now(), activeListLimit)
	if err != nil {
		return nil, err
	}
	if len(active) == 0 {
		return []UserAnnouncement{}, nil
	}

	ids := make([]int, 0, len(active))
	for _, item := range active {
		ids = append(ids, item.ID)
	}
	readTimes, err := s.repo.ReadTimes(ctx, userID, ids)
	if err != nil {
		return nil, err
	}

	// ListActive 已按 ID 倒序返回，这里稳定分区：未读在前、已读在后。
	unread := make([]UserAnnouncement, 0, len(active))
	read := make([]UserAnnouncement, 0, len(active))
	for _, item := range active {
		entry := UserAnnouncement{Announcement: item}
		if readAt, ok := readTimes[item.ID]; ok {
			t := readAt
			entry.ReadAt = &t
			read = append(read, entry)
		} else {
			unread = append(unread, entry)
		}
	}
	if unreadOnly {
		return unread, nil
	}
	return append(unread, read...), nil
}

// MarkRead 标记公告已读。
// 公告须对当前用户可见（生效中），否则按不存在处理，防止探测草稿/过期公告。
func (s *Service) MarkRead(ctx context.Context, userID, id int) error {
	item, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if !item.IsActiveAt(time.Now()) {
		return ErrAnnouncementNotFound
	}
	return s.repo.MarkRead(ctx, id, userID, time.Now())
}

// MarkAllRead 将当前生效的全部公告标记为已读。
func (s *Service) MarkAllRead(ctx context.Context, userID int) error {
	active, err := s.repo.ListActive(ctx, time.Now(), activeListLimit)
	if err != nil {
		return err
	}
	if len(active) == 0 {
		return nil
	}
	ids := make([]int, 0, len(active))
	for _, item := range active {
		ids = append(ids, item.ID)
	}
	return s.repo.MarkReadBulk(ctx, userID, ids, time.Now())
}

func validStatus(status string) bool {
	switch status {
	case StatusDraft, StatusActive, StatusArchived:
		return true
	}
	return false
}

func validNotifyMode(mode string) bool {
	switch mode {
	case NotifyModeSilent, NotifyModePopup:
		return true
	}
	return false
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// parseTimeInput 解析 RFC3339 时间入参：nil = 未传（不修改），空串 = 清空。
func parseTimeInput(raw *string) (*time.Time, bool, error) {
	if raw == nil {
		return nil, false, nil
	}
	if *raw == "" {
		return nil, true, nil
	}
	parsed, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		return nil, false, ErrInvalidTime
	}
	return &parsed, true, nil
}

func validateTimeRange(startsAt, endsAt *time.Time) error {
	if startsAt != nil && endsAt != nil && !startsAt.Before(*endsAt) {
		return ErrInvalidTimeRange
	}
	return nil
}
