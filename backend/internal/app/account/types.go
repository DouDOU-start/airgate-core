package account

import (
	"context"
	"time"
)

// 账号状态常量（与 ent schema 枚举一致）。
const (
	StateActive      = "active"
	StateRateLimited = "rate_limited"
	StateDegraded    = "degraded"
	StateDisabled    = "disabled"
)

// SensitiveCredentialKeys 敏感凭证字段（handler 脱敏用）。
var SensitiveCredentialKeys = []string{
	"access_token",
	"refresh_token",
	"id_token",
	"session_token",
	"api_key",
	"password",
	"client_secret",
}

// Repository 账号领域持久化接口。
// 写路径接收密文 CredentialsEnc + 冗余 Email；读路径 Account 仅带 CredentialsEnc，
// 明文 Credentials 由 Service 解密填充。
type Repository interface {
	List(context.Context, ListFilter) ([]Account, int64, error)
	ListAll(context.Context, ListFilter) ([]Account, error)
	// ListIDs 按筛选返回全部匹配 id（忽略分页），供运行时指标排序。
	ListIDs(context.Context, ListFilter) ([]int, error)
	// ListByIDs 按 id 批量取详情（不保证顺序，调用方重排）。
	ListByIDs(context.Context, []int) ([]Account, error)
	FindByID(context.Context, int, LoadOptions) (Account, error)
	Create(context.Context, PersistCreateInput) (Account, error)
	Update(context.Context, int, PersistUpdateInput) (Account, error)
	Delete(context.Context, int) error
	SaveCredentials(ctx context.Context, id int, credentialsEnc, email string) error
}

// ProxyRef 账号绑定的代理摘要。
// Password 仅 service 内部出站用（列表 API 不回显）。
type ProxyRef struct {
	ID       int
	Name     string
	Protocol string
	Address  string
	Port     int
	Status   string
	Username string
	Password string // 敏感：勿写入 dto
}

// Account 账号领域对象。
//
// Credentials 为解密后的明文；CredentialsEnc 仅 store→service 传递密文。
// Export 路径同样返回明文 Credentials。
type Account struct {
	ID             int
	Name           string
	Platform       string
	Type           string
	Credentials    map[string]string
	CredentialsEnc string // 内部传递密文，service 解密后清空
	Email          string
	State          string
	StateUntil     *time.Time
	Priority       int
	Weight         int
	MaxConcurrency int
	// CurrentConcurrency / CurrentRPM 运行时观测（在途请求数 / 当前分钟 RPM），列表填充。
	CurrentConcurrency int
	CurrentRPM         int
	// MaxRPM 可选上限（extra.max_rpm）；0 表示不限制，仅展示 current_rpm。
	MaxRPM         int
	RateMultiplier float64
	ErrorMsg       string
	UpstreamIsPool bool
	LastUsedAt     *time.Time
	GroupIDs       []int64
	Proxy          *ProxyRef
	Extra          map[string]any
	// Usage 用量窗口快照（来自 extra.usage，列表/刷新接口填充；不落独立列）。
	Usage *UsageSnapshot
	// PlanType 订阅档位（plus/pro/team/free…）：优先 credentials.plan_type，其次 usage.plan_type。
	PlanType string
	// SubscriptionActiveUntil 订阅有效期（ISO/RFC3339 等；Codex 来自 id_token claim）。
	// 非敏感，列表可回显；无值时为空。
	SubscriptionActiveUntil string
	// Models 可服务模型白名单（来自 extra.models）；空表示使用平台默认目录。
	Models []string
	// ModelMapping maps external request models to provider-facing model names.
	ModelMapping map[string]string
	// TotalCost / TotalRevenue 累计金额（账号成本 / 平台真实收入），
	// TodayCost / TodayRevenue 为今日口径；列表由 UsageStatsRepository 填充，不落库。
	TotalCost    float64
	TotalRevenue float64
	TodayCost    float64
	TodayRevenue float64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ListFilter 账号列表筛选条件。
type ListFilter struct {
	Page        int
	PageSize    int
	Keyword     string
	Platform    string
	State       string
	AccountType string
	GroupID     *int
	Ungrouped   bool
	ProxyID     *int
	IDs         []int
	// SortBy 排序字段，取值见 SortBy* 常量；空值按创建时间倒序。
	SortBy string
	// SortOrder 排序方向："asc" / "desc"；空值按 desc。
	SortOrder string
	// TZ 调用方 IANA 时区名，决定今日金额口径的当日起点；为空时用服务器本地时区。
	TZ string
}

const (
	// SortByCreatedAt 按创建时间排序（默认，DB 下推）。
	SortByCreatedAt = "created_at"
	// SortByPriority 按优先级排序（DB）。
	SortByPriority = "priority"
	// SortByWeight 按权重排序（DB）。
	SortByWeight = "weight"
	// SortByConcurrency 按当前在途并发数排序（Redis 运行时指标）。
	SortByConcurrency = "concurrency"
	// SortByRPM 按当前分钟 RPM 排序（Redis 运行时指标）。
	SortByRPM = "rpm"

	sortOrderAsc = "asc"

	// maxRuntimeStatSortCandidates 并发/RPM 排序允许的最大候选账号数。
	maxRuntimeStatSortCandidates = 5000
)

// IsRuntimeStatSort 判断是否为运行时指标排序（需 Redis 批量查询）。
func (f ListFilter) IsRuntimeStatSort() bool {
	return f.SortBy == SortByConcurrency || f.SortBy == SortByRPM
}

// ListResult 账号列表分页结果。
type ListResult struct {
	List     []Account
	Total    int64
	Page     int
	PageSize int
}

// CreateInput 创建账号输入（Credentials 为明文，由 service 加密后落库）。
type CreateInput struct {
	Name           string
	Platform       string
	Type           string
	Credentials    map[string]string
	Priority       int
	Weight         int
	MaxConcurrency int
	ProxyID        *int64
	RateMultiplier float64
	GroupIDs       []int64
	UpstreamIsPool bool
	Extra          map[string]any
}

// UpdateInput 更新账号输入。
//
// State 仅允许 "active" / "disabled"；rate_limited / degraded 由调度维护。
// GroupIDs / ProxyID / Extra 需配合 HasXxx 表示「本次要改」。
type UpdateInput struct {
	Name           *string
	Type           *string
	Credentials    map[string]string
	State          *string
	Priority       *int
	Weight         *int
	MaxConcurrency *int
	RateMultiplier *float64
	UpstreamIsPool *bool
	GroupIDs       []int64
	HasGroupIDs    bool
	ProxyID        *int64
	HasProxyID     bool
	Extra          map[string]any
	HasExtra       bool
	// Models 非 nil 时写入 extra.models（空切片=清除白名单，回退平台默认）。
	Models *[]string
	// ModelMapping is stored in extra.model_mapping; an empty map clears it.
	ModelMapping *map[string]string
}

// PersistCreateInput store 侧创建输入（密文凭证）。
type PersistCreateInput struct {
	Name           string
	Platform       string
	Type           string
	CredentialsEnc string
	Email          string
	Priority       int
	Weight         int
	MaxConcurrency int
	ProxyID        *int64
	RateMultiplier float64
	GroupIDs       []int64
	UpstreamIsPool bool
	Extra          map[string]any
}

// PersistUpdateInput store 侧更新输入（密文凭证）。
type PersistUpdateInput struct {
	Name            *string
	Type            *string
	CredentialsEnc  *string
	Email           *string
	State           *string
	Priority        *int
	Weight          *int
	MaxConcurrency  *int
	RateMultiplier  *float64
	UpstreamIsPool  *bool
	GroupIDs        []int64
	HasGroupIDs     bool
	ProxyID         *int64
	HasProxyID      bool
	Extra           map[string]any
	HasExtra        bool
	ClearStateUntil bool
	ClearErrorMsg   bool
}

// LoadOptions 查询关联加载选项。
type LoadOptions struct {
	WithGroups bool
	WithProxy  bool
}

// ToggleResult 快速切换调度状态结果。
type ToggleResult struct {
	ID    int
	State string
}

// BulkUpdateInput 批量更新输入；未设置字段表示不修改。
type BulkUpdateInput struct {
	IDs            []int
	State          *string
	Priority       *int
	Weight         *int
	MaxConcurrency *int
	RateMultiplier *float64
	GroupIDs       []int64
	HasGroupIDs    bool
	ProxyID        *int64
	HasProxyID     bool
	// Models 非 nil 时批量写入 extra.models。
	Models       *[]string
	ModelMapping *map[string]string
}

// BulkResultItem 批量操作单条结果。
type BulkResultItem struct {
	ID      int    `json:"id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// BulkResult 批量操作汇总。
type BulkResult struct {
	Success    int              `json:"success"`
	Failed     int              `json:"failed"`
	SuccessIDs []int            `json:"success_ids"`
	FailedIDs  []int            `json:"failed_ids"`
	Results    []BulkResultItem `json:"results"`
}

// ImportItemError 单条导入失败信息。
type ImportItemError struct {
	Index   int    `json:"index"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// ImportResult 批量导入结果。
type ImportResult struct {
	Imported int               `json:"imported"`
	Failed   int               `json:"failed"`
	Errors   []ImportItemError `json:"errors,omitempty"`
}
