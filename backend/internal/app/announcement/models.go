package announcement

import (
	"context"
	"time"
)

// 状态与通知模式合法值。
const (
	StatusDraft    = "draft"
	StatusActive   = "active"
	StatusArchived = "archived"

	NotifyModeSilent = "silent"
	NotifyModePopup  = "popup"
)

// Repository 定义公告域持久化接口。
type Repository interface {
	List(context.Context, ListFilter) ([]Announcement, int64, error)
	FindByID(context.Context, int) (Announcement, error)
	Create(context.Context, CreateRecord) (Announcement, error)
	Update(context.Context, int, UpdateRecord) (Announcement, error)
	Delete(context.Context, int) error
	// ListActive 返回 now 时刻生效的公告（active 且落在起止窗口内），按 ID 倒序，至多 limit 条。
	ListActive(ctx context.Context, now time.Time, limit int) ([]Announcement, error)
	// ReadTimes 返回用户对指定公告的首次已读时间（未读的公告不出现在结果中）。
	ReadTimes(ctx context.Context, userID int, announcementIDs []int) (map[int]time.Time, error)
	// MarkRead 幂等写入已读记录：重复标记不报错、不覆盖首次已读时间。
	MarkRead(ctx context.Context, announcementID, userID int, readAt time.Time) error
	// MarkReadBulk 批量幂等写入已读记录。
	MarkReadBulk(ctx context.Context, userID int, announcementIDs []int, readAt time.Time) error
}

// Announcement 描述公告领域对象。
type Announcement struct {
	ID         int
	Title      string
	Content    string
	Status     string
	NotifyMode string
	StartsAt   *time.Time
	EndsAt     *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// IsActiveAt 判断公告在 now 时刻是否生效（active 且落在起止窗口内）。
func (a Announcement) IsActiveAt(now time.Time) bool {
	if a.Status != StatusActive {
		return false
	}
	if a.StartsAt != nil && now.Before(*a.StartsAt) {
		return false
	}
	if a.EndsAt != nil && !now.Before(*a.EndsAt) {
		return false
	}
	return true
}

// UserAnnouncement 用户视角公告：附带当前用户的已读时间（nil = 未读）。
type UserAnnouncement struct {
	Announcement
	ReadAt *time.Time
}

// ListFilter 描述管理员公告列表查询条件。
type ListFilter struct {
	Page     int
	PageSize int
	Keyword  string
	Status   string
}

// ListResult 描述分页结果。
type ListResult struct {
	List     []Announcement
	Total    int64
	Page     int
	PageSize int
}

// CreateInput 描述创建公告输入。
// StartsAt/EndsAt 为 RFC3339 字符串，nil 或空串 = 立即生效 / 永久展示。
type CreateInput struct {
	Title      string
	Content    string
	Status     string
	NotifyMode string
	StartsAt   *string
	EndsAt     *string
}

// UpdateInput 描述更新公告输入。
// 时间字段沿用 apikey 域惯例：nil = 不修改，空串 = 清空。
type UpdateInput struct {
	Title      *string
	Content    *string
	Status     *string
	NotifyMode *string
	StartsAt   *string
	EndsAt     *string
}

// CreateRecord 描述仓储层创建落库输入（service 解析校验后产出）。
type CreateRecord struct {
	Title      string
	Content    string
	Status     string
	NotifyMode string
	StartsAt   *time.Time
	EndsAt     *time.Time
}

// UpdateRecord 描述仓储层更新落库输入。
// HasStartsAt/HasEndsAt 为 true 且对应指针为 nil 时表示清空该时间。
type UpdateRecord struct {
	Title       *string
	Content     *string
	Status      *string
	NotifyMode  *string
	StartsAt    *time.Time
	HasStartsAt bool
	EndsAt      *time.Time
	HasEndsAt   bool
}
