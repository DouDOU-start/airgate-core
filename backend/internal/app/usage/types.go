package usage

import "context"

// ListFilter 使用记录列表筛选。
type ListFilter struct {
	Page      int
	PageSize  int
	UserID    *int64
	APIKeyID  *int64
	AccountID *int64
	GroupID   *int64
	Platform  string
	Model     string
	StartDate string
	EndDate   string
}

// StatsFilter 聚合统计筛选。
type StatsFilter struct {
	UserID    *int64
	APIKeyID  *int64
	Platform  string
	Model     string
	StartDate string
	EndDate   string
}

// TrendFilter 趋势统计筛选。
type TrendFilter struct {
	StatsFilter
	Granularity        string
	DefaultRecentHours int
}

// LogRecord 使用记录领域对象。
type LogRecord struct {
	ID                    int64
	UserID                int64
	UserEmail             string
	APIKeyID              int64
	APIKeyName            string
	APIKeyHint            string
	APIKeyDeleted         bool
	AccountID             int64
	AccountName           string
	GroupID               int64
	Platform              string
	Model                 string
	InputTokens           int
	OutputTokens          int
	CachedInputTokens     int
	ReasoningOutputTokens int
	InputPrice            float64
	OutputPrice           float64
	CachedInputPrice      float64
	InputCost             float64
	OutputCost            float64
	CachedInputCost       float64
	TotalCost             float64
	ActualCost            float64
	RateMultiplier        float64
	AccountRateMultiplier float64
	ServiceTier           string
	Stream                bool
	DurationMs            int64
	FirstTokenMs          int64
	UserAgent             string
	IPAddress             string
	CreatedAt             string
}

// ListResult 使用记录列表结果。
type ListResult struct {
	List     []LogRecord
	Total    int64
	Page     int
	PageSize int
}

// Summary 汇总统计。
type Summary struct {
	TotalRequests   int64
	TotalTokens     int64
	TotalCost       float64
	TotalActualCost float64
}

// ModelStats 按模型统计。
type ModelStats struct {
	Model      string `json:"model"`
	Requests   int64  `json:"requests"`
	Tokens     int64  `json:"tokens"`
	TotalCost  float64
	ActualCost float64
}

// UserStats 按用户统计。
type UserStats struct {
	UserID     int64  `json:"user_id"`
	Email      string `json:"email"`
	Requests   int64  `json:"requests"`
	Tokens     int64  `json:"tokens"`
	TotalCost  float64
	ActualCost float64
}

// AccountStats 按账号统计。
type AccountStats struct {
	AccountID  int64  `json:"account_id"`
	Name       string `json:"name"`
	Requests   int64  `json:"requests"`
	Tokens     int64  `json:"tokens"`
	TotalCost  float64
	ActualCost float64
}

// GroupStats 按分组统计。
type GroupStats struct {
	GroupID    int64  `json:"group_id"`
	Name       string `json:"name"`
	Requests   int64  `json:"requests"`
	Tokens     int64  `json:"tokens"`
	TotalCost  float64
	ActualCost float64
}

// StatsResult 管理员统计结果。
type StatsResult struct {
	Summary
	ByModel   []ModelStats
	ByUser    []UserStats
	ByAccount []AccountStats
	ByGroup   []GroupStats
}

// TrendEntry 趋势聚合的原始项。
type TrendEntry struct {
	CreatedAt         string
	InputTokens       int64
	OutputTokens      int64
	CachedInputTokens int64
	ActualCost        float64
	StandardCost      float64
}

// TrendBucket 趋势时间桶。
type TrendBucket struct {
	Time          string  `json:"time"`
	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	CacheCreation int64   `json:"cache_creation"`
	CacheRead     int64   `json:"cache_read"`
	ActualCost    float64 `json:"actual_cost"`
	StandardCost  float64 `json:"standard_cost"`
}

// Repository 使用记录仓储接口。
type Repository interface {
	ListUser(context.Context, int64, ListFilter) ([]LogRecord, int64, error)
	ListAdmin(context.Context, ListFilter) ([]LogRecord, int64, error)
	SummaryUser(context.Context, int64, StatsFilter) (Summary, error)
	SummaryAdmin(context.Context, StatsFilter) (Summary, error)
	StatsByModel(context.Context, StatsFilter) ([]ModelStats, error)
	StatsByUser(context.Context, StatsFilter) ([]UserStats, error)
	StatsByAccount(context.Context, StatsFilter) ([]AccountStats, error)
	StatsByGroup(context.Context, StatsFilter) ([]GroupStats, error)
	TrendEntries(context.Context, TrendFilter) ([]TrendEntry, error)
}
