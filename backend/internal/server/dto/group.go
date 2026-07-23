package dto

// GroupResp 分组响应
type GroupResp struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Platform       string  `json:"platform"`
	RateMultiplier float64 `json:"rate_multiplier"`
	// AlphaSearchPrice codex 联网搜索按次覆盖价（USD/次）；null=沿用全局设置。
	AlphaSearchPrice *float64 `json:"alpha_search_price"`
	// EffectiveRate 当前用户在此分组的实际计费倍率（用户专属 > 等级 > 分组档位），
	// 仅用户视角接口返回；管理员列表恒为 0 并省略。
	EffectiveRate float64 `json:"effective_rate,omitempty"`
	IsExclusive   bool    `json:"is_exclusive"`
	StatusVisible bool    `json:"status_visible"`
	// AllowedClients 客户端白名单；空=不限制。
	AllowedClients []string `json:"allowed_clients"`
	// FallbackGroupID 客户端不匹配时降级到的分组；null=直接拒绝。
	FallbackGroupID *int64 `json:"fallback_group_id"`
	Note            string `json:"note,omitempty"`
	SortWeight      int    `json:"sort_weight"`

	// 统计字段（仅管理员列表返回），实扣口径（actual_cost 汇总）
	TodayCost float64 `json:"today_cost"`
	TotalCost float64 `json:"total_cost"`

	// CurrentConcurrency / CurrentRPM 运行时观测指标（在途请求数 / 当前分钟请求数），列表实时展示。
	CurrentConcurrency int `json:"current_concurrency"`
	CurrentRPM         int `json:"current_rpm"`

	TimeMixin
}

// CreateGroupReq 创建分组请求
type CreateGroupReq struct {
	Name           string  `json:"name" binding:"required"`
	Platform       string  `json:"platform"`
	RateMultiplier float64 `json:"rate_multiplier"`
	// AlphaSearchPrice 联网搜索按次覆盖价（USD/次）；缺省/null=沿用全局设置。
	AlphaSearchPrice *float64 `json:"alpha_search_price"`
	IsExclusive      bool     `json:"is_exclusive"`
	// StatusVisible 用指针区分"字段未提交"和"显式置 false"，缺省视为 true（在公开状态页可见）。
	StatusVisible   *bool    `json:"status_visible"`
	AllowedClients  []string `json:"allowed_clients"`
	FallbackGroupID *int     `json:"fallback_group_id"`
	Note            string   `json:"note"`
	SortWeight      int      `json:"sort_weight"`
}

// UpdateGroupReq 更新分组请求
type UpdateGroupReq struct {
	Name           *string  `json:"name"`
	RateMultiplier *float64 `json:"rate_multiplier"`
	// AlphaSearchPrice 联网搜索按次覆盖价：编辑表单提交完整对象，
	// 有值设价（含 0=免费），null/缺省清空为沿用全局设置。
	AlphaSearchPrice *float64  `json:"alpha_search_price"`
	IsExclusive      *bool     `json:"is_exclusive"`
	StatusVisible    *bool     `json:"status_visible"`
	AllowedClients   *[]string `json:"allowed_clients"`
	FallbackGroupID  *int      `json:"fallback_group_id"`
	Note             *string   `json:"note"`
	SortWeight       *int      `json:"sort_weight"`
}

// GroupAllowedUserResp 获准访问专属分组的用户条目。
type GroupAllowedUserResp struct {
	UserID   int64  `json:"user_id"`
	Email    string `json:"email"`
	Username string `json:"username"`
}
