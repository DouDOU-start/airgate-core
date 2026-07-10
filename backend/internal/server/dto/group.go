package dto

// GroupResp 分组响应
type GroupResp struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Platform       string  `json:"platform"`
	RateMultiplier float64 `json:"rate_multiplier"`
	// EffectiveRate 当前用户在此分组的实际计费倍率（用户专属 > 等级 > 分组档位），
	// 仅用户视角接口返回；管理员列表恒为 0 并省略。
	EffectiveRate float64 `json:"effective_rate,omitempty"`
	IsExclusive   bool    `json:"is_exclusive"`
	StatusVisible bool    `json:"status_visible"` // 是否在公开 /status 页展示
	Note          string  `json:"note,omitempty"`
	SortWeight    int     `json:"sort_weight"`

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
	IsExclusive    bool    `json:"is_exclusive"`
	// StatusVisible 用指针区分"字段未提交"和"显式置 false"，缺省视为 true（在公开状态页可见）。
	StatusVisible *bool  `json:"status_visible"`
	Note          string `json:"note"`
	SortWeight    int    `json:"sort_weight"`
}

// UpdateGroupReq 更新分组请求
type UpdateGroupReq struct {
	Name           *string  `json:"name"`
	RateMultiplier *float64 `json:"rate_multiplier"`
	IsExclusive    *bool    `json:"is_exclusive"`
	StatusVisible  *bool    `json:"status_visible"`
	Note           *string  `json:"note"`
	SortWeight     *int     `json:"sort_weight"`
}
