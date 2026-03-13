package dto

// DashboardStatsResp 仪表盘统计响应
type DashboardStatsResp struct {
	TotalUsers    int64   `json:"total_users"`
	TotalAccounts int64   `json:"total_accounts"`
	TotalGroups   int64   `json:"total_groups"`
	TotalAPIKeys  int64   `json:"total_api_keys"`
	TotalRequests int64   `json:"total_requests"` // 今日请求数
	TotalTokens   int64   `json:"total_tokens"`   // 今日 Token 数
	TotalRevenue  float64 `json:"total_revenue"`  // 今日收入
	ActivePlugins int64   `json:"active_plugins"`
}
