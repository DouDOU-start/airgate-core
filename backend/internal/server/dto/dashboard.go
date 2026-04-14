package dto

// DashboardStatsResp 仪表盘统计响应
type DashboardStatsResp struct {
	// API 密钥
	TotalAPIKeys   int64 `json:"total_api_keys"`
	EnabledAPIKeys int64 `json:"enabled_api_keys"`

	// 账号
	TotalAccounts   int64 `json:"total_accounts"`
	EnabledAccounts int64 `json:"enabled_accounts"`
	ErrorAccounts   int64 `json:"error_accounts"`

	// 请求
	TodayRequests   int64 `json:"today_requests"`
	AllTimeRequests int64 `json:"alltime_requests"` //nolint:misspell

	// 用户
	TotalUsers    int64 `json:"total_users"`
	NewUsersToday int64 `json:"new_users_today"`

	// 今日 Token
	TodayTokens       int64   `json:"today_tokens"`
	TodayCost         float64 `json:"today_cost"`
	TodayStandardCost float64 `json:"today_standard_cost"`

	// 总 Token
	AllTimeTokens       int64   `json:"alltime_tokens"`        //nolint:misspell
	AllTimeCost         float64 `json:"alltime_cost"`          //nolint:misspell
	AllTimeStandardCost float64 `json:"alltime_standard_cost"` //nolint:misspell

	// 性能指标
	RPM           float64 `json:"rpm"`
	TPM           float64 `json:"tpm"`
	AvgDurationMs float64 `json:"avg_duration_ms"`
	ActiveUsers   int64   `json:"active_users"`
}

// DashboardStatsReq 仪表盘统计查询参数
type DashboardStatsReq struct {
	UserID int    `form:"user_id"`
	TZ     string `form:"tz"` // IANA 时区名，例如 Asia/Shanghai；为空时使用服务器本地时区
}

// DashboardTrendReq 仪表盘趋势查询参数
type DashboardTrendReq struct {
	Range       string `form:"range" binding:"required,oneof=today 7d 30d 90d custom"`
	Granularity string `form:"granularity" binding:"required,oneof=hour day"`
	StartDate   string `form:"start_date"`
	EndDate     string `form:"end_date"`
	UserID      int    `form:"user_id"`
	TZ          string `form:"tz"` // IANA 时区名；为空时使用服务器本地时区
}

// DashboardTrendResp 仪表盘趋势响应
type DashboardTrendResp struct {
	ModelDistribution []DashboardModelStats  `json:"model_distribution"`
	UserRanking       []DashboardUserRanking `json:"user_ranking"`
	TokenTrend        []DashboardTimeBucket  `json:"token_trend"`
	TopUsers          []DashboardUserTrend   `json:"top_users"`
}

// DashboardModelStats 模型分布统计
type DashboardModelStats struct {
	Model        string  `json:"model"`
	Requests     int64   `json:"requests"`
	Tokens       int64   `json:"tokens"`
	ActualCost   float64 `json:"actual_cost"`
	StandardCost float64 `json:"standard_cost"`
}

// DashboardUserRanking 用户消费排行
type DashboardUserRanking struct {
	UserID       int64   `json:"user_id"`
	Email        string  `json:"email"`
	Requests     int64   `json:"requests"`
	Tokens       int64   `json:"tokens"`
	ActualCost   float64 `json:"actual_cost"`
	StandardCost float64 `json:"standard_cost"`
}

// DashboardTimeBucket Token 趋势时间桶
type DashboardTimeBucket struct {
	Time         string  `json:"time"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CachedInput  int64   `json:"cached_input"`
	ActualCost   float64 `json:"actual_cost"`
	StandardCost float64 `json:"standard_cost"`
}

// DashboardUserTrend Top 用户使用趋势
type DashboardUserTrend struct {
	UserID int64                     `json:"user_id"`
	Email  string                    `json:"email"`
	Trend  []DashboardUserTrendPoint `json:"trend"`
}

// DashboardUserTrendPoint 用户趋势数据点
type DashboardUserTrendPoint struct {
	Time   string `json:"time"`
	Tokens int64  `json:"tokens"`
}
