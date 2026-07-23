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
	// PublicRateMultipliers 返回全部非专属分组（is_exclusive=false）的倍率，供模型广场
	// 展示粗粒度折扣区间；私下谈价的专属分组不参与，避免泄露定价策略。
	PublicRateMultipliers(ctx context.Context) ([]float64, error)

	// AllowedUsers 列出获准访问该专属分组的用户（按邮箱排序）。
	AllowedUsers(ctx context.Context, groupID int) ([]AllowedUser, error)
	// GrantAllowedUser 授予用户访问该专属分组的权限；已授予时幂等成功。
	GrantAllowedUser(ctx context.Context, groupID, userID int) error
	// RevokeAllowedUser 撤销用户访问该专属分组的权限；未授予时幂等成功。
	RevokeAllowedUser(ctx context.Context, groupID, userID int) error

	// BindChannelKey 把渠道 key 绑定到该分组；已绑定时幂等成功。
	BindChannelKey(ctx context.Context, groupID, channelKeyID int) error
	// UnbindChannelKey 把渠道 key 从该分组解绑；未绑定时幂等成功。
	UnbindChannelKey(ctx context.Context, groupID, channelKeyID int) error
}

// AllowedUser 描述获准访问专属分组的用户（列表展示用最小字段集）。
type AllowedUser struct {
	UserID   int
	Email    string
	Username string
}

// ConcurrencyReader 分组在途并发数批量读取（由 scheduler.ConcurrencyManager 实现）。
type ConcurrencyReader interface {
	GetGroupCurrentCounts(context.Context, []int) map[int]int
}

// RPMReader 分组当前分钟 RPM 批量读取（由 scheduler.RPMCounter 实现）。
type RPMReader interface {
	GetGroupRPMs(context.Context, []int) map[int]int
}

// UserRatesReader 用户计费倍率读取（由 user 服务适配实现）：
// 返回用户专属倍率与等级倍率（均按 group_id 键），用于解析可用分组的实际倍率。
type UserRatesReader interface {
	BillingRates(ctx context.Context, userID int) (groupRates, tierRates map[int64]float64, err error)
}

// GroupStats 描述分组统计信息。金额为实扣口径（usage_log.actual_cost 汇总），非价目表原价。
type GroupStats struct {
	TodayCost float64
	TotalCost float64
}

// Group 描述分组领域对象。
type Group struct {
	ID             int
	Name           string
	Platform       string
	RateMultiplier float64
	// AlphaSearchPrice codex 联网搜索按次覆盖价（USD/次）；nil=沿用全局 gateway 设置。
	AlphaSearchPrice *float64
	IsExclusive      bool
	StatusVisible    bool
	// AllowedClients 客户端白名单：非空时仅允许指定类型的客户端访问。
	// 值域: "claude_code", "codex"。空=不限制。
	AllowedClients []string
	// FallbackGroupID 客户端不匹配 AllowedClients 时降级到的分组；nil=直接拒绝。
	FallbackGroupID *int
	Note            string
	SortWeight      int
	CreatedAt       time.Time
	UpdatedAt       time.Time

	// CurrentConcurrency / CurrentRPM 运行时观测指标（在途请求数 / 当前分钟请求数），
	// 仅管理员列表查询时由读取器填充，不落库。
	CurrentConcurrency int
	CurrentRPM         int

	// EffectiveRate 当前用户在此分组的实际计费倍率（用户专属 > 等级 > 分组档位），
	// 仅用户视角查询（ListAvailable / AvailableForUser）且倍率读取器已注入时填充，0 表示未解析。
	EffectiveRate float64
}

// ListFilter 描述管理员分组列表查询条件。
type ListFilter struct {
	Page     int
	PageSize int
	Keyword  string
	Platform string
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
	Name           string
	Platform       string
	RateMultiplier float64
	// AlphaSearchPrice 联网搜索按次覆盖价（USD/次）；nil=沿用全局设置。
	AlphaSearchPrice *float64
	IsExclusive      bool
	StatusVisible    bool
	AllowedClients   []string
	FallbackGroupID  *int
	Note             string
	SortWeight       int
}

// UpdateInput 描述更新分组输入。
type UpdateInput struct {
	Name           *string
	RateMultiplier *float64
	// AlphaSearchPrice 联网搜索按次覆盖价：非 nil 设值（含 0=免费），
	// nil 清空为 NULL（回落全局设置）。更新对该字段是权威写（编辑表单提交完整对象）。
	AlphaSearchPrice *float64
	IsExclusive      *bool
	StatusVisible    *bool
	// AllowedClients 权威写：编辑表单提交完整对象。nil=不修改，空数组=清除限制。
	AllowedClients *[]string
	// FallbackGroupID 权威写：nil=不修改，零值指针=清除降级分组。
	FallbackGroupID *int
	// ClearFallbackGroup 显式清除降级分组（FallbackGroupID 为 nil 时生效）。
	ClearFallbackGroup bool
	Note               *string
	SortWeight         *int
}
