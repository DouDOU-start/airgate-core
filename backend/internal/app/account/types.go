package account

import (
	"context"
	"net/http"
	"time"
)

// Proxy 账号绑定的代理信息。
type Proxy struct {
	ID       int
	Protocol string
	Address  string
	Port     int
	Username string
	Password string
}

// Account 账号领域对象。
//
// State 枚举：active / rate_limited / degraded / disabled
// StateUntil rate_limited / degraded 的到期时间；disabled 无到期；active 为 nil
type Account struct {
	ID                 int
	Name               string
	Platform           string
	Type               string
	Credentials        map[string]string
	State              string
	StateUntil         *time.Time
	Priority           int
	MaxConcurrency     int
	CurrentConcurrency int
	RateMultiplier     float64
	// ErrorMsg 进入当前非 active 状态的原因（给运维看）。
	ErrorMsg string
	// UpstreamIsPool 上游是账号池时置 true：池抖动会被降级到 degraded 不永久标错。
	UpstreamIsPool bool
	LastUsedAt     *time.Time
	GroupIDs       []int64
	Proxy          *Proxy
	Extra          map[string]any
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// ImageStats 仅 OpenAI 平台账号在列表查询路径上填充；其它平台 / 详情查询路径为 nil。
	// 取自 usage_log model 名前缀 "gpt-image" 的子集聚合。
	ImageStats *AccountImageStats
}

// AccountWindowStats 单个账号在某个时间窗口内的聚合统计。
// 对应 UI 上每个 usage window（如 5h / 7d）底下一行 "req | tokens | A $ | U $" 展示。
type AccountWindowStats struct {
	Requests    int64
	Tokens      int64
	AccountCost float64 // SUM(account_cost)，账号真实消耗（上游成本）
	UserCost    float64 // SUM(actual_cost)，用户扣费总额（平台计费）
}

// AccountImageStats 单账号生图请求计数。
//
// 用于账号列表页"今日 N · 累计 M"展示。仅 OpenAI 平台账号填充（Claude / Anthropic
// 等平台没有图像生成 endpoint，调用方按零值跳过）。
//
// "生图" 的判定与 stats.go::isImageModel 保持一致：model 名前缀 "gpt-image"。
type AccountImageStats struct {
	TodayCount int64 // 今日 00:00（服务器本地时区）至今的生图请求数
	TotalCount int64 // 全部历史生图请求数
}

// UsageLog 使用记录聚合输入。
type UsageLog struct {
	Model        string
	InputTokens  int64
	OutputTokens int64
	TotalCost    float64 // 原始上游定价（base, 不含任何倍率）
	AccountCost  float64 // 账号实际成本 = total × account_rate（"账号计费"统计的真值）
	ActualCost   float64 // 用户扣费 = total × billing_rate
	DurationMs   int64
	CreatedAt    time.Time
}

// ListFilter 账号列表筛选条件。
type ListFilter struct {
	Page        int
	PageSize    int
	Keyword     string
	Platform    string
	State       string // active / rate_limited / degraded / disabled
	AccountType string
	GroupID     *int
	ProxyID     *int
	IDs         []int
}

// ListResult 账号列表结果。
type ListResult struct {
	List     []Account
	Total    int64
	Page     int
	PageSize int
}

// CreateInput 创建账号输入。
type CreateInput struct {
	Name           string
	Platform       string
	Type           string
	Credentials    map[string]string
	Priority       int
	MaxConcurrency int
	ProxyID        *int64
	RateMultiplier float64
	GroupIDs       []int64
	UpstreamIsPool bool
}

// UpdateInput 更新账号输入。
//
// State 传 "active" / "disabled" 表示运维手动恢复 / 禁用；
// 其它 state 值（rate_limited / degraded）由调度状态机自行维护，不由 API 写入。
type UpdateInput struct {
	Name           *string
	Type           *string
	Credentials    map[string]string
	State          *string
	Priority       *int
	MaxConcurrency *int
	RateMultiplier *float64
	UpstreamIsPool *bool
	GroupIDs       []int64
	HasGroupIDs    bool
	ProxyID        *int64
	HasProxyID     bool
}

// ToggleResult 快速切换调度状态结果。
type ToggleResult struct {
	ID    int
	State string
}

// BulkUpdateInput 批量更新账号输入。
// 所有可选字段使用指针/HasXxx 标记：未设置表示「不修改」。
// GroupIDs 采用整体替换语义：HasGroupIDs=true 时会用新列表覆盖账号原有分组。
type BulkUpdateInput struct {
	IDs            []int
	State          *string
	Priority       *int
	MaxConcurrency *int
	RateMultiplier *float64
	GroupIDs       []int64
	HasGroupIDs    bool
	ProxyID        *int64
	HasProxyID     bool
}

// BulkResultItem 批量操作单条结果。
type BulkResultItem struct {
	ID      int    `json:"id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// BulkResult 批量操作汇总结果。
type BulkResult struct {
	Success    int              `json:"success"`
	Failed     int              `json:"failed"`
	SuccessIDs []int            `json:"success_ids"`
	FailedIDs  []int            `json:"failed_ids"`
	Results    []BulkResultItem `json:"results"`
}

// Model 模型信息。
type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CredentialField 凭证字段定义。
type CredentialField struct {
	Key          string
	Label        string
	Type         string
	Required     bool
	Placeholder  string
	EditDisabled bool
}

// AccountType 账号类型定义。
type AccountType struct {
	Key         string
	Label       string
	Description string
	Fields      []CredentialField
}

// CredentialSchema 凭证字段 schema。
type CredentialSchema struct {
	Fields       []CredentialField
	AccountTypes []AccountType
}

// QuotaRefreshResult 刷新额度结果。
type QuotaRefreshResult struct {
	PlanType                string
	Email                   string
	SubscriptionActiveUntil string
	// ReauthWarning 非空表示 refresh_token 已失效、字段是从存量 access_token 降级解析得到，
	// 调用方应提示用户尽快重新授权。内容为可展示的原因文案。
	ReauthWarning string
}

// StatsQuery 账号统计查询参数。
type StatsQuery struct {
	StartDate string
	EndDate   string
	TZ        string // IANA 时区名；为空时使用服务器本地时区
}

// PeriodStats 期间汇总。
//
// 三个 cost 字段语义：
//   - TotalCost   = SUM(usage_log.total_cost)   原始上游定价
//   - AccountCost = SUM(usage_log.account_cost) 账号实际成本 = total × account_rate（"账号计费"）
//   - ActualCost  = SUM(usage_log.actual_cost)  用户扣费     = total × billing_rate
//
// ImageCount / ImageCost 把 model 名为 "gpt-image*" 的请求单独再聚一次，
// 不影响 Count/cost（它们仍然是全部请求总和）。给后台展示"今日生图 N 张 / $X"用。
type PeriodStats struct {
	Count        int     `json:"count"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalCost    float64 `json:"total_cost"`
	AccountCost  float64 `json:"account_cost"`
	ActualCost   float64 `json:"actual_cost"`
	ImageCount   int     `json:"image_count"`
	ImageCost    float64 `json:"image_cost"`
}

// DailyStats 每日统计。
type DailyStats struct {
	Date        string  `json:"date"`
	Count       int     `json:"count"`
	TotalCost   float64 `json:"total_cost"`
	AccountCost float64 `json:"account_cost"`
	ActualCost  float64 `json:"actual_cost"`
}

// ModelStats 模型分布统计。
type ModelStats struct {
	Model        string  `json:"model"`
	Count        int     `json:"count"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalCost    float64 `json:"total_cost"`
	AccountCost  float64 `json:"account_cost"`
	ActualCost   float64 `json:"actual_cost"`
}

// PeakDay 峰值日期统计。
type PeakDay struct {
	Date        string  `json:"date"`
	Count       int     `json:"count"`
	TotalCost   float64 `json:"total_cost"`
	AccountCost float64 `json:"account_cost"`
	ActualCost  float64 `json:"actual_cost"`
}

// StatsResult 账号统计结果。
type StatsResult struct {
	AccountID      int
	Name           string
	Platform       string
	State          string
	StartDate      string
	EndDate        string
	TotalDays      int
	Today          PeriodStats
	Range          PeriodStats
	DailyTrend     []DailyStats
	Models         []ModelStats
	ActiveDays     int
	AvgDurationMs  int64
	PeakCostDay    PeakDay
	PeakRequestDay PeakDay
}

// ConnectivityTest 账号连通性测试计划。
type ConnectivityTest struct {
	AccountName string
	AccountType string
	ModelID     string
	run         func(context.Context, http.ResponseWriter) error
}

// Run 执行连通性测试。
func (t *ConnectivityTest) Run(ctx context.Context, writer http.ResponseWriter) error {
	return t.run(ctx, writer)
}

// LoadOptions 查询账号时的关联加载选项。
type LoadOptions struct {
	WithGroups bool
	WithProxy  bool
}

// ImportSummary 批量导入结果。
type ImportSummary struct {
	Imported int               `json:"imported"`
	Failed   int               `json:"failed"`
	Errors   []ImportItemError `json:"errors,omitempty"`
}

// ImportItemError 单条导入失败信息。
type ImportItemError struct {
	Index   int    `json:"index"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// Repository 账号领域的持久化接口。
//
// 注意：状态机相关的写入（rate_limited / disabled 自动转移）不通过 Repository，
// 而是走 scheduler.StateMachine.Apply。这里只暴露管理员视角的 CRUD 与只读查询。
type Repository interface {
	List(context.Context, ListFilter) ([]Account, int64, error)
	ListAll(context.Context, ListFilter) ([]Account, error)
	Create(context.Context, CreateInput) (Account, error)
	Update(context.Context, int, UpdateInput) (Account, error)
	Delete(context.Context, int) error
	FindByID(context.Context, int, LoadOptions) (Account, error)
	ListByPlatform(context.Context, string) ([]Account, error)
	FindUsageLogs(context.Context, int, time.Time, time.Time) ([]UsageLog, error)
	// BatchWindowStats 批量聚合统计，没有记录的账号不出现在返回 map 中。
	BatchWindowStats(ctx context.Context, accountIDs []int, startTime time.Time) (map[int]AccountWindowStats, error)
	// BatchImageStats 批量统计指定账号的生图请求数（model 前缀 "gpt-image"）。
	// 同时返回 [todayStart, now] 区间和 全部历史 两个计数。无记录的账号不出现在返回 map 中。
	BatchImageStats(ctx context.Context, accountIDs []int, todayStart time.Time) (map[int]AccountImageStats, error)
	SaveCredentials(context.Context, int, map[string]string) error
}
