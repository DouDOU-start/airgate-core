package subscription

import (
	"context"
	"time"
)

const (
	defaultPage     = 1
	defaultPageSize = 20
)

// Repository 定义订阅域持久化接口。
type Repository interface {
	ListByUser(context.Context, UserListFilter) ([]Subscription, int64, error)
	ListActiveByUser(context.Context, int) ([]Subscription, error)
	ListAdmin(context.Context, AdminListFilter) ([]Subscription, int64, error)
	Create(context.Context, CreateInput) (Subscription, error)
	BulkCreate(context.Context, BulkCreateInput) (int, error)
	Update(context.Context, int, UpdateInput) (Subscription, error)
}

// Subscription 订阅领域对象。
type Subscription struct {
	ID          int
	UserID      int
	GroupID     int
	GroupName   string
	EffectiveAt time.Time
	ExpiresAt   time.Time
	Usage       map[string]any
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// UsageWindow 表示一个统计窗口。
type UsageWindow struct {
	Used  float64
	Limit float64
	Reset string
}

// SubscriptionProgress 表示订阅进度。
type SubscriptionProgress struct {
	GroupID   int
	GroupName string
	Daily     *UsageWindow
	Weekly    *UsageWindow
	Monthly   *UsageWindow
}

// ListResult 分页查询结果。
type ListResult struct {
	List     []Subscription
	Total    int64
	Page     int
	PageSize int
}

// UserListFilter 用户订阅列表筛选条件。
type UserListFilter struct {
	UserID   int
	Page     int
	PageSize int
}

// AdminListFilter 管理员列表筛选条件。
type AdminListFilter struct {
	Page     int
	PageSize int
	Status   string
	UserID   *int
}

// AssignInput 管理员单个分配输入。
type AssignInput struct {
	UserID    int
	GroupID   int
	ExpiresAt string
}

// BulkAssignInput 管理员批量分配输入。
type BulkAssignInput struct {
	UserIDs   []int
	GroupID   int
	ExpiresAt string
}

// AdjustInput 管理员调整输入。
type AdjustInput struct {
	ExpiresAt *string
	Status    *string
}

// CreateInput 仓储创建输入。
type CreateInput struct {
	UserID      int
	GroupID     int
	EffectiveAt time.Time
	ExpiresAt   time.Time
	Status      string
}

// BulkCreateInput 仓储批量创建输入。
type BulkCreateInput struct {
	UserIDs     []int
	GroupID     int
	EffectiveAt time.Time
	ExpiresAt   time.Time
	Status      string
}

// UpdateInput 仓储更新输入。
type UpdateInput struct {
	ExpiresAt *time.Time
	Status    *string
}

func normalizePage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = defaultPage
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	return page, pageSize
}
