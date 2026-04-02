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
type Account struct {
	ID                 int
	Name               string
	Platform           string
	Type               string
	Credentials        map[string]string
	Status             string
	Priority           int
	MaxConcurrency     int
	CurrentConcurrency int
	RateMultiplier     float64
	ErrorMsg           string
	LastUsedAt         *time.Time
	GroupIDs           []int64
	Proxy              *Proxy
	Extra              map[string]any
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// UsageLog 使用记录聚合输入。
type UsageLog struct {
	Model        string
	InputTokens  int64
	OutputTokens int64
	TotalCost    float64
	ActualCost   float64
	DurationMs   int64
	CreatedAt    time.Time
}

// ListFilter 账号列表筛选条件。
type ListFilter struct {
	Page     int
	PageSize int
	Keyword  string
	Platform string
	Status   string
	GroupID  *int
	ProxyID  *int
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
}

// UpdateInput 更新账号输入。
type UpdateInput struct {
	Name           *string
	Type           *string
	Credentials    map[string]string
	Status         *string
	Priority       *int
	MaxConcurrency *int
	RateMultiplier *float64
	GroupIDs       []int64
	HasGroupIDs    bool
	ProxyID        *int64
	HasProxyID     bool
}

// ToggleResult 快速切换调度状态结果。
type ToggleResult struct {
	ID     int
	Status string
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
}

// StatsQuery 账号统计查询参数。
type StatsQuery struct {
	StartDate string
	EndDate   string
}

// PeriodStats 期间汇总。
type PeriodStats struct {
	Count        int     `json:"count"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalCost    float64 `json:"total_cost"`
	ActualCost   float64 `json:"actual_cost"`
}

// DailyStats 每日统计。
type DailyStats struct {
	Date       string  `json:"date"`
	Count      int     `json:"count"`
	TotalCost  float64 `json:"total_cost"`
	ActualCost float64 `json:"actual_cost"`
}

// ModelStats 模型分布统计。
type ModelStats struct {
	Model        string  `json:"model"`
	Count        int     `json:"count"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalCost    float64 `json:"total_cost"`
	ActualCost   float64 `json:"actual_cost"`
}

// PeakDay 峰值日期统计。
type PeakDay struct {
	Date       string  `json:"date"`
	Count      int     `json:"count"`
	TotalCost  float64 `json:"total_cost"`
	ActualCost float64 `json:"actual_cost"`
}

// StatsResult 账号统计结果。
type StatsResult struct {
	AccountID      int
	Name           string
	Platform       string
	Status         string
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

// Repository 账号领域的持久化接口。
type Repository interface {
	List(context.Context, ListFilter) ([]Account, int64, error)
	Create(context.Context, CreateInput) (Account, error)
	Update(context.Context, int, UpdateInput) (Account, error)
	Delete(context.Context, int) error
	FindByID(context.Context, int, LoadOptions) (Account, error)
	ListByPlatform(context.Context, string) ([]Account, error)
	FindUsageLogs(context.Context, int, time.Time, time.Time) ([]UsageLog, error)
	SaveCredentials(context.Context, int, map[string]string) error
	MarkError(context.Context, int, string) error
}
