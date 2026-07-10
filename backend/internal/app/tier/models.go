package tier

import (
	"context"
	"time"
)

// Tier 用户等级领域对象。Rates 是等级在各分组下的计费倍率（按 group_id 键），
// 参与计费优先级链（user.group_rates > tier.rates > group.rate_multiplier）。
type Tier struct {
	ID         int
	Name       string
	Rates      map[int64]float64
	Note       string
	SortWeight int
	CreatedAt  time.Time
	UpdatedAt  time.Time

	// UserCount 归属此等级的用户数（仅列表查询时填充）。
	UserCount int
}

// ListFilter 等级列表查询条件。
type ListFilter struct {
	Page     int
	PageSize int
	Keyword  string
}

// ListResult 分页结果。
type ListResult struct {
	List     []Tier
	Total    int64
	Page     int
	PageSize int
}

// CreateInput 创建等级输入。
type CreateInput struct {
	Name       string
	Rates      map[int64]float64
	Note       string
	SortWeight int
}

// UpdateInput 更新等级输入。Rates 整体替换（HasRates 区分"未提交"与"清空"）。
type UpdateInput struct {
	Name       *string
	Rates      map[int64]float64
	HasRates   bool
	Note       *string
	SortWeight *int
}

// Repository 等级持久化接口。
type Repository interface {
	List(context.Context, ListFilter) ([]Tier, int64, error)
	FindByID(context.Context, int) (Tier, error)
	Create(context.Context, CreateInput) (Tier, error)
	Update(context.Context, int, UpdateInput) (Tier, error)
	// Delete 删除等级；仍有用户归属时返回 TierHasUsersError。
	Delete(context.Context, int) error
}
