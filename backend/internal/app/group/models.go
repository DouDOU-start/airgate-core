package group

import (
	"context"
	"time"
)

// Repository 定义分组域持久化接口。
type Repository interface {
	List(context.Context, ListFilter) ([]Group, int64, error)
	ListAvailable(context.Context, AvailableFilter) ([]Group, int64, error)
	FindByID(context.Context, int) (Group, error)
	Create(context.Context, CreateInput) (Group, error)
	Update(context.Context, int, UpdateInput) (Group, error)
	Delete(context.Context, int) error
	StatsForGroups(ctx context.Context, groupIDs []int, todayStart time.Time) (map[int]GroupStats, error)
}

// ConcurrencyReader 分组在途并发数批量读取（由 scheduler.ConcurrencyManager 实现）。
type ConcurrencyReader interface {
	GetGroupCurrentCounts(context.Context, []int) map[int]int
}

// RPMReader 分组当前分钟 RPM 批量读取（由 scheduler.RPMCounter 实现）。
type RPMReader interface {
	GetGroupRPMs(context.Context, []int) map[int]int
}

// GroupStats 描述分组统计信息。金额为实扣口径（usage_log.actual_cost 汇总），非价目表原价。
type GroupStats struct {
	TodayCost float64
	TotalCost float64
}

// Group 描述分组领域对象。
type Group struct {
	ID                int
	Name              string
	Platform          string
	RateMultiplier    float64
	IsExclusive       bool
	StatusVisible     bool
	Quotas            map[string]any
	ModelRouting      map[string][]int64
	ServiceTier       string
	ForceInstructions string
	Note              string
	SortWeight        int
	CreatedAt         time.Time
	UpdatedAt         time.Time

	// CurrentConcurrency / CurrentRPM 运行时观测指标（在途请求数 / 当前分钟请求数），
	// 仅管理员列表查询时由读取器填充，不落库。
	CurrentConcurrency int
	CurrentRPM         int
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
	Quotas            map[string]any
	ModelRouting      map[string][]int64
	ServiceTier       string
	ForceInstructions string
	Note              string
	SortWeight        int
}

// UpdateInput 描述更新分组输入。
type UpdateInput struct {
	Name              *string
	RateMultiplier    *float64
	IsExclusive       *bool
	StatusVisible     *bool
	Quotas            map[string]any
	ModelRouting      map[string][]int64
	ServiceTier       *string
	ForceInstructions *string
	Note              *string
	SortWeight        *int
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
