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
	BalanceAlertThreshold float64
	BalanceAlertNotified  bool
	Status                string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// ListFilter 用户列表筛选。
type ListFilter struct {
	Page     int
	PageSize int
	Keyword  string
	Status   string
	Role     string
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
	Status             *string
}

// BalanceChange 余额变更输入。
type BalanceChange struct {
	Action string
	Amount float64
	Remark string
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
	Status             *string
}

// BalanceUpdate 余额更新数据。
type BalanceUpdate struct {
	Action        string
	Amount        float64
	BeforeBalance float64
	AfterBalance  float64
	Remark        string
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
	EmailExists(context.Context, string) (bool, error)
	ListWithGroupRateOverride(ctx context.Context, groupID int64) ([]GroupRateOverride, error)
	Create(context.Context, Mutation) (User, error)
	Update(context.Context, int, Mutation) (User, error)
	UpdateBalance(context.Context, int, BalanceUpdate) (User, error)
	Delete(context.Context, int) error
	ListBalanceLogs(context.Context, int, int, int) ([]BalanceLog, int64, error)
	// ListAPIKeys 查询用户的 API Key 列表。
	// todayStart 必须由调用方按用户时区计算好。
	ListAPIKeys(ctx context.Context, userID, page, pageSize int, todayStart time.Time) ([]APIKey, int64, error)
	GetAPIKeyName(ctx context.Context, keyID int) (string, error)
	GetAPIKeyInfo(ctx context.Context, keyID int) (APIKeyBrief, error)
	UpdateBalanceAlert(ctx context.Context, userID int, threshold float64) error
	SetBalanceAlertNotified(ctx context.Context, userID int, notified bool) error
}
