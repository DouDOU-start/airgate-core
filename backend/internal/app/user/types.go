package user

import (
	"context"
	"time"
)

// User 用户领域对象。
type User struct {
	ID                    int
	Email                 string
	Username              string
	PasswordHash          string
	Balance               float64
	Role                  string
	MaxConcurrency        int
	GroupRates            map[int64]float64
	AllowedGroupIDs       []int64
	TierID                *int64
	TierName              string
	TierRates             map[int64]float64
	BalanceAlertThreshold float64
	BalanceAlertNotified  bool
	Status                string
	CreatedAt             time.Time
	UpdatedAt             time.Time

	// CurrentConcurrency / CurrentRPM 运行时观测指标（当前在途请求数 / 当前分钟请求数），
	// 仅管理员列表查询时由 RuntimeStatsReader 填充，不落库。
	CurrentConcurrency int
	CurrentRPM         int
}

// ConcurrencyReader 用户在途并发数批量读取（由 scheduler.ConcurrencyManager 实现）。
type ConcurrencyReader interface {
	GetUserCurrentCounts(ctx context.Context, userIDs []int) map[int]int
}

// RPMReader 用户当前分钟 RPM 批量读取（由 scheduler.RPMCounter 实现）。
type RPMReader interface {
	GetUserRPMs(ctx context.Context, userIDs []int) map[int]int
}

// ListFilter 用户列表筛选。
type ListFilter struct {
	Page     int
	PageSize int
	Keyword  string
	Status   string
	Role     string
	// TierID 按用户等级筛选，0 表示不筛选。
	TierID int64
	// SortBy 排序字段，取值见 SortBy* 常量；空值或未识别值按 SortByCreatedAt 处理。
	SortBy string
	// SortOrder 排序方向："asc" / "desc"；空值或未识别值按 desc 处理。
	SortOrder string
}

const (
	// SortByCreatedAt 按创建时间排序（默认）。
	SortByCreatedAt = "created_at"
	// SortByBalance 按余额排序（DB 字段，直接下推排序）。
	SortByBalance = "balance"
	// SortByConcurrency 按当前在途并发数排序（Redis 运行时指标，需先取全量 id 再批量查）。
	SortByConcurrency = "concurrency"
	// SortByRPM 按当前分钟 RPM 排序（同上）。
	SortByRPM = "rpm"

	// sortOrderAsc 升序标识。
	sortOrderAsc = "asc"

	// maxRuntimeStatSortCandidates 并发/RPM 排序允许的最大候选用户数（筛选后）。
	// 超过该值直接拒绝排序请求（返回 ErrTooManySortCandidates），避免单次请求
	// 触发超大规模 Redis pipeline；调用方应提示管理员先用筛选条件缩小范围。
	maxRuntimeStatSortCandidates = 5000
)

// IsRuntimeStatSort 判断是否为运行时指标排序（需要 Redis 批量查询而非 DB 排序）。
func (f ListFilter) IsRuntimeStatSort() bool {
	return f.SortBy == SortByConcurrency || f.SortBy == SortByRPM
}

// ListResult 用户列表结果。
type ListResult struct {
	List     []User
	Total    int64
	Page     int
	PageSize int
}

// CreateInput 创建用户输入。
type CreateInput struct {
	Email          string
	Password       string
	Username       string
	Role           string
	MaxConcurrency int
	GroupRates     map[int64]float64
	TierID         *int64
}

// UpdateInput 更新用户输入。
type UpdateInput struct {
	Username           *string
	Password           *string
	Role               *string
	MaxConcurrency     *int
	GroupRates         map[int64]float64
	HasGroupRates      bool
	AllowedGroupIDs    []int64
	HasAllowedGroupIDs bool
	// TierID 用户等级归属；HasTier 区分"未提交"与"变更"，TierID 为 nil 时清除归属。
	TierID  *int64
	HasTier bool
	Status  *string
}

// BalanceChange 余额变更输入。before/after 由 store 在事务内以行锁重读现算，
// 不再由调用方传入（避免无锁读-算-写的丢失更新）。
type BalanceChange struct {
	Action string
	Amount float64
	Remark string
	// IdempotencyKey 可选幂等键（如 "epay:<out_trade_no>"）。非空时同一键的变更
	// 只入账一次，重复提交返回当前用户状态而不再变更余额。
	IdempotencyKey string
}

// ToggleResult 用户状态切换结果。
type ToggleResult struct {
	ID     int
	Status string
}

// BalanceLog 余额日志领域对象。
type BalanceLog struct {
	ID            int64
	Action        string
	Amount        float64
	BeforeBalance float64
	AfterBalance  float64
	Remark        string
	CreatedAt     string
}

// BalanceLogList 余额日志分页结果。
type BalanceLogList struct {
	List     []BalanceLog
	Total    int64
	Page     int
	PageSize int
}

// APIKey 用户 API Key 领域对象。
// APIKeyBrief API Key 概要（用于 API Key 登录场景展示）。
type APIKeyBrief struct {
	Name      string
	QuotaUSD  float64
	UsedQuota float64
	ExpiresAt *time.Time
	// SellRate 当前 Key 自身的销售倍率（>0 表示启用 markup，否则按分组倍率结算）
	SellRate float64
	// GroupRate 所属分组的费率倍率（未绑定分组时为 0）
	GroupRate float64
	// Platform 所属分组的平台标识（如 anthropic / openai），未绑定分组时为空
	Platform string
}

type APIKey struct {
	ID            int
	Name          string
	KeyHint       string
	KeyHash       string
	UserID        int
	GroupID       *int
	IPWhitelist   []string
	IPBlacklist   []string
	QuotaUSD      float64
	UsedQuota     float64
	TodayCost     float64
	ThirtyDayCost float64
	ExpiresAt     *time.Time
	Status        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// APIKeyList API Key 分页结果。
type APIKeyList struct {
	List     []APIKey
	Total    int64
	Page     int
	PageSize int
}

// Mutation 用户持久化变更。
type Mutation struct {
	Email              *string
	Username           *string
	PasswordHash       *string
	Role               *string
	MaxConcurrency     *int
	GroupRates         map[int64]float64
	HasGroupRates      bool
	AllowedGroupIDs    []int64
	HasAllowedGroupIDs bool
	TierID             *int64
	HasTier            bool
	Status             *string
}

// GroupRateOverride 表示某个用户对某个分组的专属倍率。
type GroupRateOverride struct {
	UserID   int
	Email    string
	Username string
	Rate     float64
}

// Repository 用户持久化接口。
type Repository interface {
	FindByID(context.Context, int, bool) (User, error)
	List(context.Context, ListFilter) ([]User, int64, error)
	// ListIDs 按筛选条件（忽略分页）返回全部匹配用户 id，用于运行时指标排序场景先取全量候选集。
	ListIDs(context.Context, ListFilter) ([]int, error)
	// ListByIDs 按 id 批量取用户详情（顺序不保证与入参一致，由调用方重排）。
	ListByIDs(ctx context.Context, ids []int) ([]User, error)
	EmailExists(context.Context, string) (bool, error)
	ListWithGroupRateOverride(ctx context.Context, groupID int64) ([]GroupRateOverride, error)
	Create(context.Context, Mutation) (User, error)
	Update(context.Context, int, Mutation) (User, error)
	// UpdateBalance 单事务原子更新余额并写流水（before/after 由 store 行锁重读现算）；
	// subtract 余额不足返回 ErrInsufficientBalance，非法 action 返回 ErrInvalidBalanceAction。
	UpdateBalance(context.Context, int, BalanceChange) (User, error)
	Delete(context.Context, int) error
	ListBalanceLogs(context.Context, int, int, int) ([]BalanceLog, int64, error)
	// ListAPIKeys 查询用户的 API Key 列表。
	// todayStart 必须由调用方按用户时区计算好。
	ListAPIKeys(ctx context.Context, userID, page, pageSize int, todayStart time.Time) ([]APIKey, int64, error)
	GetAPIKeyInfo(ctx context.Context, keyID int) (APIKeyBrief, error)
	UpdateBalanceAlert(ctx context.Context, userID int, threshold float64) error
	SetBalanceAlertNotified(ctx context.Context, userID int, notified bool) error
}
