package group

import (
	"context"
	"time"
)

const (
	defaultPage     = 1
	defaultPageSize = 20
)

// Repository 定义分组域持久化接口。
type Repository interface {
	List(context.Context, ListFilter) ([]Group, int64, error)
	ListAvailable(context.Context, AvailableFilter) ([]Group, int64, error)
	FindByID(context.Context, int) (Group, error)
	Create(context.Context, CreateInput) (Group, error)
	Update(context.Context, int, UpdateInput) (Group, error)
	Delete(context.Context, int) error
	StatsForGroups(ctx context.Context, groupIDs []int, todayStart time.Time) (stats map[int]GroupStats, activeAccounts map[int][]AccountCapacity, err error)
}

// ConcurrencyReader 并发读接口。
type ConcurrencyReader interface {
	GetCurrentCounts(context.Context, []int) map[int]int
}

// GroupStats 描述分组统计信息。
type GroupStats struct {
	AccountActive   int
	AccountError    int
	AccountDisabled int
	AccountTotal    int
	CapacityUsed    int
	CapacityTotal   int
	TodayCost       float64
	TotalCost       float64
}

// AccountCapacity 描述每个分组中活跃账号的容量信息。
type AccountCapacity struct {
	AccountID      int
	MaxConcurrency int
}

// Group 描述分组领域对象。
type Group struct {
	ID                int
	Name              string
	Platform          string
	RateMultiplier    float64
	IsExclusive       bool
	StatusVisible     bool
	SubscriptionType  string
	Quotas            map[string]any
	ModelRouting      map[string][]int64
	ServiceTier       string
	ForceInstructions string
	Note              string
	SortWeight        int
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// ListFilter 描述管理员分组列表查询条件。
type ListFilter struct {
	Page        int
	PageSize    int
	Keyword     string
	Platform    string
	ServiceTier string
}

// AvailableFilter 描述用户可用分组查询条件。
type AvailableFilter struct {
	UserID   int
	Page     int
	PageSize int
	Keyword  string
	Platform string
}

// ListResult 描述分页结果。
type ListResult struct {
	List     []Group
	Total    int64
	Page     int
	PageSize int
}

// CreateInput 描述创建分组输入。
type CreateInput struct {
	Name              string
	Platform          string
	RateMultiplier    float64
	IsExclusive       bool
	StatusVisible     bool
	SubscriptionType  string
	Quotas            map[string]any
	ModelRouting      map[string][]int64
	ServiceTier       string
	ForceInstructions string
	Note              string
	SortWeight        int
	// CopyAccountsFromGroupIDs 指定在新分组创建后从这些分组复制账号绑定（同平台，自动去重）。
	CopyAccountsFromGroupIDs []int
}

// UpdateInput 描述更新分组输入。
type UpdateInput struct {
	Name              *string
	RateMultiplier    *float64
	IsExclusive       *bool
	StatusVisible     *bool
	SubscriptionType  *string
	Quotas            map[string]any
	ModelRouting      map[string][]int64
	ServiceTier       *string
	ForceInstructions *string
	Note              *string
	SortWeight        *int
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

func cloneQuotas(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func cloneModelRouting(input map[string][]int64) map[string][]int64 {
	if input == nil {
		return nil
	}
	cloned := make(map[string][]int64, len(input))
	for key, value := range input {
		cloned[key] = append([]int64(nil), value...)
	}
	return cloned
}
