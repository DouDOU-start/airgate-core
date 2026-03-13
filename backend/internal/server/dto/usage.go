package dto

// UsageLogResp 使用记录响应
type UsageLogResp struct {
	ID                    int64   `json:"id"`
	UserID                int64   `json:"user_id"`
	APIKeyID              int64   `json:"api_key_id"`
	AccountID             int64   `json:"account_id"`
	GroupID               int64   `json:"group_id"`
	Platform              string  `json:"platform"`
	Model                 string  `json:"model"`
	InputTokens           int     `json:"input_tokens"`
	OutputTokens          int     `json:"output_tokens"`
	CacheTokens           int     `json:"cache_tokens"`
	InputCost             float64 `json:"input_cost"`
	OutputCost            float64 `json:"output_cost"`
	CacheCost             float64 `json:"cache_cost"`
	TotalCost             float64 `json:"total_cost"`
	ActualCost            float64 `json:"actual_cost"`
	RateMultiplier        float64 `json:"rate_multiplier"`
	AccountRateMultiplier float64 `json:"account_rate_multiplier"`
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
	Model     string  `json:"model"`
	Requests  int64   `json:"requests"`
	Tokens    int64   `json:"tokens"`
	TotalCost float64 `json:"total_cost"`
}

// UserStats 按用户统计
type UserStats struct {
	UserID    int64   `json:"user_id"`
	Email     string  `json:"email"`
	Requests  int64   `json:"requests"`
	TotalCost float64 `json:"total_cost"`
}

// AccountStats 按账号统计
type AccountStats struct {
	AccountID int64   `json:"account_id"`
	Name      string  `json:"name"`
	Requests  int64   `json:"requests"`
	TotalCost float64 `json:"total_cost"`
}

// GroupStats 按分组统计
type GroupStats struct {
	GroupID   int64   `json:"group_id"`
	Name      string  `json:"name"`
	Requests  int64   `json:"requests"`
	TotalCost float64 `json:"total_cost"`
}

// UsageStatsQuery 统计查询参数
type UsageStatsQuery struct {
	GroupBy   string `form:"group_by" binding:"required,oneof=model user account group"` // 聚合维度
	StartDate string `form:"start_date"`
	EndDate   string `form:"end_date"`
}
