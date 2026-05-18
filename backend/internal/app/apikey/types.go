package apikey

import (
	"context"
	"time"
)

const SearchScopeAPIKey = "api_key"

// Key API Key 领域对象。
type Key struct {
	ID              int
	Name            string
	KeyHint         string
	KeyHash         string
	KeyEncrypted    string
	PlainKey        string
	UserID          int
	GroupID         *int
	IPWhitelist     []string
	IPBlacklist     []string
	QuotaUSD        float64
	UsedQuota       float64 // 账面已用（含 sell_rate markup）
	UsedQuotaActual float64 // 真实成本已用（聚合 sum(usage_log.actual_cost)，仅在 fetch 时填充）
	SellRate        float64 // 销售倍率，0 表示未启用
	MaxConcurrency  int     // API Key 级并发上限，0 表示不限制
	TodayCost       float64
	ThirtyDayCost   float64
	Status          string
	ExpiresAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ListFilter API Key 列表查询参数。
type ListFilter struct {
	Page        int
	PageSize    int
	Keyword     string
	SearchScope string
}

// ListResult API Key 列表结果。
type ListResult struct {
	List     []Key
	Total    int64
	Page     int
	PageSize int
}

// CreateInput 创建 API Key 输入。
type CreateInput struct {
	Name           string
	GroupID        int64
	IPWhitelist    []string
	IPBlacklist    []string
	QuotaUSD       float64
	SellRate       float64
	MaxConcurrency int // 0 表示不限制
	ExpiresAt      *string
}

// UpdateInput 更新 API Key 输入。
type UpdateInput struct {
	Name           *string
	GroupID        *int64
	IPWhitelist    []string
	HasIPWhitelist bool
	IPBlacklist    []string
	HasIPBlacklist bool
	QuotaUSD       *float64
	SellRate       *float64
	MaxConcurrency *int // nil 表示不改动；指向 0 表示关闭并发限制
	ExpiresAt      *string
	Status         *string
}

// GroupAccess 分组可用性检查结果。
type GroupAccess struct {
	Exists  bool
	Allowed bool
}

// Mutation 创建/更新持久化输入。
type Mutation struct {
	Name           *string
	KeyHint        *string
	KeyHash        *string
	KeyEncrypted   *string
	UserID         *int
	GroupID        *int
	IPWhitelist    []string
	HasIPWhitelist bool
	IPBlacklist    []string
	HasIPBlacklist bool
	QuotaUSD       *float64
	SellRate       *float64
	MaxConcurrency *int
	ExpiresAt      *time.Time
	HasExpiresAt   bool
	Status         *string
}

// Repository API Key 持久化接口。
type Repository interface {
	ListByUser(context.Context, int, ListFilter) ([]Key, int64, error)
	ListAdmin(context.Context, ListFilter) ([]Key, int64, error)
	// KeyUsage 返回每个 key 的"今日"和"近 30 天"实际成本。
	// todayStart 必须由调用方按用户时区计算好。
	KeyUsage(ctx context.Context, keyIDs []int, todayStart time.Time) (map[int]float64, map[int]float64, error)
	GetGroupAccess(context.Context, int, int) (GroupAccess, error)
	Create(context.Context, Mutation) (Key, error)
	UpdateOwned(context.Context, int, int, Mutation) (Key, error)
	UpdateAdmin(context.Context, int, Mutation) (Key, error)
	DeleteOwned(context.Context, int, int) error
	FindOwned(context.Context, int, int) (Key, error)
}
