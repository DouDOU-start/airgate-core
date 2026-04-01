package dto

// UsageLogResp 使用记录响应
type UsageLogResp struct {
	ID                    int64   `json:"id"`
	UserID                int64   `json:"user_id"`
	UserEmail             string  `json:"user_email,omitempty"`
	APIKeyID              int64   `json:"api_key_id"`
	APIKeyName            string  `json:"api_key_name,omitempty"`
	APIKeyHint            string  `json:"api_key_hint,omitempty"`
	APIKeyDeleted         bool    `json:"api_key_deleted"`
	AccountID             int64   `json:"account_id"`
	AccountName           string  `json:"account_name,omitempty"`
	GroupID               int64   `json:"group_id"`
	Platform              string  `json:"platform"`
	Model                 string  `json:"model"`
	InputTokens           int     `json:"input_tokens"`
	OutputTokens          int     `json:"output_tokens"`
	CachedInputTokens     int     `json:"cached_input_tokens"`
	ReasoningOutputTokens int     `json:"reasoning_output_tokens"`
	InputCost             float64 `json:"input_cost"`
	OutputCost            float64 `json:"output_cost"`
	CachedInputCost       float64 `json:"cached_input_cost"`
	TotalCost             float64 `json:"total_cost"`
	ActualCost            float64 `json:"actual_cost"`
	RateMultiplier        float64 `json:"rate_multiplier"`
	AccountRateMultiplier float64 `json:"account_rate_multiplier"`
	ServiceTier           string  `json:"service_tier,omitempty"`
	Stream                bool    `json:"stream"`
	DurationMs            int64   `json:"duration_ms"`
	FirstTokenMs          int64   `json:"first_token_ms"`
	UserAgent             string  `json:"user_agent,omitempty"`
	IPAddress             string  `json:"ip_address,omitempty"`
	CreatedAt             string  `json:"created_at"`
}

// UsageQuery 使用记录查询参数
type UsageQuery struct {
	PageReq
	UserID    *int64 `form:"user_id"`
	APIKeyID  *int64 `form:"api_key_id"`
	AccountID *int64 `form:"account_id"`
	GroupID   *int64 `form:"group_id"`
	Platform  string `form:"platform"`
	Model     string `form:"model"`
	StartDate string `form:"start_date"`
	EndDate   string `form:"end_date"`
}

// UsageFilterQuery 使用记录筛选参数（不含分页，用于聚合统计）
type UsageFilterQuery struct {
	Platform  string `form:"platform"`
	Model     string `form:"model"`
	StartDate string `form:"start_date"`
	EndDate   string `form:"end_date"`
}

// UsageStatsResp 聚合统计响应
type UsageStatsResp struct {
	TotalRequests   int64          `json:"total_requests"`
	TotalTokens     int64          `json:"total_tokens"`
	TotalCost       float64        `json:"total_cost"`
	TotalActualCost float64        `json:"total_actual_cost"`
	ByModel         []ModelStats   `json:"by_model,omitempty"`
	ByUser          []UserStats    `json:"by_user,omitempty"`
	ByAccount       []AccountStats `json:"by_account,omitempty"`
	ByGroup         []GroupStats   `json:"by_group,omitempty"`
}

// ModelStats 按模型统计
type ModelStats struct {
	Model      string  `json:"model"`
	Requests   int64   `json:"requests"`
	Tokens     int64   `json:"tokens"`
	TotalCost  float64 `json:"total_cost"`
	ActualCost float64 `json:"actual_cost"`
}

// UserStats 按用户统计
type UserStats struct {
	UserID     int64   `json:"user_id"`
	Email      string  `json:"email"`
	Requests   int64   `json:"requests"`
	Tokens     int64   `json:"tokens"`
	TotalCost  float64 `json:"total_cost"`
	ActualCost float64 `json:"actual_cost"`
}

// AccountStats 按账号统计
type AccountStats struct {
	AccountID  int64   `json:"account_id"`
	Name       string  `json:"name"`
	Requests   int64   `json:"requests"`
	Tokens     int64   `json:"tokens"`
	TotalCost  float64 `json:"total_cost"`
	ActualCost float64 `json:"actual_cost"`
}

// GroupStats 按分组统计
type GroupStats struct {
	GroupID    int64   `json:"group_id"`
	Name       string  `json:"name"`
	Requests   int64   `json:"requests"`
	Tokens     int64   `json:"tokens"`
	TotalCost  float64 `json:"total_cost"`
	ActualCost float64 `json:"actual_cost"`
}

// UsageStatsQuery 统计查询参数
type UsageStatsQuery struct {
	GroupBy   string `form:"group_by" binding:"required"` // 聚合维度，支持逗号分隔多值（如 model,group）
	UserID    *int64 `form:"user_id"`
	Platform  string `form:"platform"`
	Model     string `form:"model"`
	StartDate string `form:"start_date"`
	EndDate   string `form:"end_date"`
}

// UsageTrendQuery Token 趋势查询参数
type UsageTrendQuery struct {
	Granularity string `form:"granularity" binding:"required,oneof=hour day"`
	UserID      *int64 `form:"user_id"`
	Platform    string `form:"platform"`
	Model       string `form:"model"`
	StartDate   string `form:"start_date"`
	EndDate     string `form:"end_date"`
}

// UsageTrendBucket Token 趋势时间桶
type UsageTrendBucket struct {
	Time          string  `json:"time"`
	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	CacheCreation int64   `json:"cache_creation"`
	CacheRead     int64   `json:"cache_read"`
	ActualCost    float64 `json:"actual_cost"`
	StandardCost  float64 `json:"standard_cost"`
}
