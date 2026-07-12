package dto

// DashboardStatsResp 仪表盘统计响应
type DashboardStatsResp struct {
	// API 密钥
	TotalAPIKeys   int64 `json:"total_api_keys"`
	EnabledAPIKeys int64 `json:"enabled_api_keys"`

	// 渠道（渠道为供应商容器；启用/停用口径下沉到密钥端点 ChannelKey）
	TotalChannels int64 `json:"total_channels"`
	EnabledKeys   int64 `json:"enabled_keys"`
	DisabledKeys  int64 `json:"disabled_keys"`

	// 请求
	TodayRequests      int64 `json:"today_requests"`
	TodayImageRequests int64 `json:"today_image_requests"`
	AllTimeRequests    int64 `json:"alltime_requests"` //nolint:misspell

	// 用户
	TotalUsers    int64 `json:"total_users"`
	NewUsersToday int64 `json:"new_users_today"`

	// 今日 Token（cost=实际扣费；standard=1 倍率标准价；channel=渠道成本）
	TodayTokens       int64   `json:"today_tokens"`
	TodayCost         float64 `json:"today_cost"`
	TodayStandardCost float64 `json:"today_standard_cost"`
	TodayChannelCost  float64 `json:"today_channel_cost"`

	// 总 Token
	AllTimeTokens       int64   `json:"alltime_tokens"`        //nolint:misspell
	AllTimeCost         float64 `json:"alltime_cost"`          //nolint:misspell
	AllTimeStandardCost float64 `json:"alltime_standard_cost"` //nolint:misspell
	AllTimeChannelCost  float64 `json:"alltime_channel_cost"`  //nolint:misspell

	// 性能指标
	RPM float64 `json:"rpm"`
	TPM float64 `json:"tpm"`
	// AvgFirstTokenMs 仅在非图像（chat / completion / streaming）请求上聚合：
	// 图像生成是一次性返回不存在 first-token，纳入会拉高分母失真。前端展示时
	// 默认按 chat 语境解读，不需要再带后缀。
	AvgFirstTokenMs float64 `json:"avg_first_token_ms"`
	AvgDurationMs   float64 `json:"avg_duration_ms"`
	// AvgImageDurationMs 仅在 gpt-image 家族请求上聚合，没有图像请求时为 0；
	// 前端在 0 时隐藏对应的副标。
	AvgImageDurationMs float64 `json:"avg_image_duration_ms"`
	ActiveUsers        int64   `json:"active_users"`
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
	// ChannelID 渠道过滤（渠道消耗统计弹窗用）；0 表示不过滤。
	ChannelID int `form:"channel_id"`
	// ChannelKeyID 密钥端点过滤（key 消耗统计弹窗用）；0 表示不过滤。
	ChannelKeyID int    `form:"channel_key_id"`
	TZ           string `form:"tz"` // IANA 时区名；为空时使用服务器本地时区
}

// DashboardTrendResp 仪表盘趋势响应
type DashboardTrendResp struct {
	ModelDistribution []DashboardModelStats  `json:"model_distribution"`
	UserRanking       []DashboardUserRanking `json:"user_ranking"`
	TokenTrend        []DashboardTimeBucket  `json:"token_trend"`
	TopUsers          []DashboardUserTrend   `json:"top_users"`
}

// DashboardModelStats 模型分布统计（channel_cost = Σ(total_cost×渠道成本倍率快照)）
type DashboardModelStats struct {
	Model        string  `json:"model"`
	Requests     int64   `json:"requests"`
	Tokens       int64   `json:"tokens"`
	ActualCost   float64 `json:"actual_cost"`
	StandardCost float64 `json:"standard_cost"`
	ChannelCost  float64 `json:"channel_cost"`
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

// DashboardTimeBucket Token 趋势时间桶（channel_cost 口径同 DashboardModelStats）
type DashboardTimeBucket struct {
	Time          string  `json:"time"`
	Requests      int64   `json:"requests"`
	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	CachedInput   int64   `json:"cached_input"`
	CacheCreation int64   `json:"cache_creation"`
	ActualCost    float64 `json:"actual_cost"`
	StandardCost  float64 `json:"standard_cost"`
	ChannelCost   float64 `json:"channel_cost"`
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
