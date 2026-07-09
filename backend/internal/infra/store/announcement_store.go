package store

import (
	"context"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entannouncement "github.com/DouDOU-start/airgate-core/ent/announcement"
	entannouncementread "github.com/DouDOU-start/airgate-core/ent/announcementread"
	appannouncement "github.com/DouDOU-start/airgate-core/internal/app/announcement"
)

// AnnouncementStore 使用 Ent 实现公告仓储。
type AnnouncementStore struct {
	db *ent.Client
}

// NewAnnouncementStore 创建公告仓储。
func NewAnnouncementStore(db *ent.Client) *AnnouncementStore {
	return &AnnouncementStore{db: db}
}

// List 查询管理员公告列表。
func (s *AnnouncementStore) List(ctx context.Context, filter appannouncement.ListFilter) ([]appannouncement.Announcement, int64, error) {
	query := s.db.Announcement.Query()
	if filter.Keyword != "" {
		query = query.Where(entannouncement.TitleContains(filter.Keyword))
	}
	if filter.Status != "" {
		query = query.Where(entannouncement.StatusEQ(filter.Status))
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	list, err := query.
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Order(ent.Desc(entannouncement.FieldID)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	return mapAnnouncements(list), int64(total), nil
}

// FindByID 按 ID 查询公告。
func (s *AnnouncementStore) FindByID(ctx context.Context, id int) (appannouncement.Announcement, error) {
	item, err := s.db.Announcement.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return appannouncement.Announcement{}, appannouncement.ErrAnnouncementNotFound
		}
		return appannouncement.Announcement{}, err
	}
	return mapAnnouncement(item), nil
}

// Create 创建公告。
func (s *AnnouncementStore) Create(ctx context.Context, record appannouncement.CreateRecord) (appannouncement.Announcement, error) {
	builder := s.db.Announcement.Create().
		SetTitle(record.Title).
		SetContent(record.Content).
		SetStatus(record.Status).
		SetNotifyMode(record.NotifyMode).
		SetNillableStartsAt(record.StartsAt).
		SetNillableEndsAt(record.EndsAt)

	item, err := builder.Save(ctx)
	if err != nil {
		return appannouncement.Announcement{}, err
	}
	return mapAnnouncement(item), nil
}

// Update 更新公告。
func (s *AnnouncementStore) Update(ctx context.Context, id int, record appannouncement.UpdateRecord) (appannouncement.Announcement, error) {
	builder := s.db.Announcement.UpdateOneID(id)

	if record.Title != nil {
		builder = builder.SetTitle(*record.Title)
	}
	if record.Content != nil {
		builder = builder.SetContent(*record.Content)
	}
	if record.Status != nil {
		builder = builder.SetStatus(*record.Status)
	}
	if record.NotifyMode != nil {
		builder = builder.SetNotifyMode(*record.NotifyMode)
	}
	if record.HasStartsAt {
		if record.StartsAt == nil {
			builder = builder.ClearStartsAt()
		} else {
			builder = builder.SetStartsAt(*record.StartsAt)
		}
	}
	if record.HasEndsAt {
		if record.EndsAt == nil {
			builder = builder.ClearEndsAt()
		} else {
			builder = builder.SetEndsAt(*record.EndsAt)
		}
	}

	item, err := builder.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appannouncement.Announcement{}, appannouncement.ErrAnnouncementNotFound
		}
		return appannouncement.Announcement{}, err
	}
	return mapAnnouncement(item), nil
}

// Delete 删除公告并清理其已读记录。
// announcement_reads 经普通 int 列关联（无外键级联），须在同一事务内手动清理。
func (s *AnnouncementStore) Delete(ctx context.Context, id int) error {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err = tx.Announcement.Get(ctx, id); err != nil {
		if ent.IsNotFound(err) {
			return appannouncement.ErrAnnouncementNotFound
		}
		return err
	}

	if _, err = tx.AnnouncementRead.Delete().
		Where(entannouncementread.AnnouncementIDEQ(id)).
		Exec(ctx); err != nil {
		return err
	}

	if err = tx.Announcement.DeleteOneID(id).Exec(ctx); err != nil {
		return err
	}

	return tx.Commit()
}

// ListActive 返回 now 时刻生效的公告，按 ID 倒序。
func (s *AnnouncementStore) ListActive(ctx context.Context, now time.Time, limit int) ([]appannouncement.Announcement, error) {
	list, err := s.db.Announcement.Query().
		Where(
			entannouncement.StatusEQ(appannouncement.StatusActive),
			entannouncement.Or(
				entannouncement.StartsAtIsNil(),
				entannouncement.StartsAtLTE(now),
			),
			entannouncement.Or(
				entannouncement.EndsAtIsNil(),
				entannouncement.EndsAtGT(now),
			),
		).
		Order(ent.Desc(entannouncement.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return mapAnnouncements(list), nil
}

// ReadTimes 返回用户对指定公告的首次已读时间。
func (s *AnnouncementStore) ReadTimes(ctx context.Context, userID int, announcementIDs []int) (map[int]time.Time, error) {
	if len(announcementIDs) == 0 {
		return map[int]time.Time{}, nil
	}
	rows, err := s.db.AnnouncementRead.Query().
		Where(
			entannouncementread.UserIDEQ(userID),
			entannouncementread.AnnouncementIDIn(announcementIDs...),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[int]time.Time, len(rows))
	for _, row := range rows {
		result[row.AnnouncementID] = row.ReadAt
	}
	return result, nil
}

// MarkRead 幂等写入已读记录：联合唯一索引冲突视为已读过，直接吞掉。
func (s *AnnouncementStore) MarkRead(ctx context.Context, announcementID, userID int, readAt time.Time) error {
	err := s.db.AnnouncementRead.Create().
		SetAnnouncementID(announcementID).
		SetUserID(userID).
		SetReadAt(readAt).
		Exec(ctx)
	if err != nil && !ent.IsConstraintError(err) {
		return err
	}
	return nil
}

// MarkReadBulk 批量幂等写入已读记录：单条 INSERT ... ON CONFLICT DO NOTHING，
// 已读过的行（联合唯一索引冲突）直接跳过，不覆盖首次已读时间。
func (s *AnnouncementStore) MarkReadBulk(ctx context.Context, userID int, announcementIDs []int, readAt time.Time) error {
	if len(announcementIDs) == 0 {
		return nil
	}
	builders := make([]*ent.AnnouncementReadCreate, 0, len(announcementIDs))
	for _, id := range announcementIDs {
		builders = append(builders, s.db.AnnouncementRead.Create().
			SetAnnouncementID(id).
			SetUserID(userID).
			SetReadAt(readAt))
	}
	return s.db.AnnouncementRead.CreateBulk(builders...).
		OnConflictColumns(
			entannouncementread.FieldAnnouncementID,
			entannouncementread.FieldUserID,
		).
		DoNothing().
		Exec(ctx)
}

func mapAnnouncements(items []*ent.Announcement) []appannouncement.Announcement {
	result := make([]appannouncement.Announcement, 0, len(items))
	for _, item := range items {
		result = append(result, mapAnnouncement(item))
	}
	return result
}

func mapAnnouncement(item *ent.Announcement) appannouncement.Announcement {
	return appannouncement.Announcement{
		ID:         item.ID,
		Title:      item.Title,
		Content:    item.Content,
		Status:     item.Status,
		NotifyMode: item.NotifyMode,
		StartsAt:   item.StartsAt,
		EndsAt:     item.EndsAt,
		CreatedAt:  item.CreatedAt,
		UpdatedAt:  item.UpdatedAt,
	}
}
