package dto

// GroupResp 分组响应
type GroupResp struct {
	ID                int64                  `json:"id"`
	Name              string                 `json:"name"`
	Platform          string                 `json:"platform"`
	RateMultiplier    float64                `json:"rate_multiplier"`
	IsExclusive       bool                   `json:"is_exclusive"`
	StatusVisible     bool                   `json:"status_visible"`    // 是否在公开 /status 页展示
	SubscriptionType  string                 `json:"subscription_type"` // standard / subscription
	Quotas            map[string]interface{} `json:"quotas,omitempty"`  // 日/周/月限额
	ModelRouting      map[string][]int64     `json:"model_routing,omitempty"`
	ServiceTier       string                 `json:"service_tier,omitempty"`
	ForceInstructions string                 `json:"force_instructions,omitempty"`
	Note              string                 `json:"note,omitempty"`
	SortWeight        int                    `json:"sort_weight"`

	// 统计字段（仅管理员列表返回）
	TodayCost float64 `json:"today_cost"`
	TotalCost float64 `json:"total_cost"`

	TimeMixin
}

// CreateGroupReq 创建分组请求
type CreateGroupReq struct {
	Name           string  `json:"name" binding:"required"`
	Platform       string  `json:"platform"`
	RateMultiplier float64 `json:"rate_multiplier"`
	IsExclusive    bool    `json:"is_exclusive"`
	// StatusVisible 用指针区分"字段未提交"和"显式置 false"，缺省视为 true（在公开状态页可见）。
	StatusVisible     *bool                  `json:"status_visible"`
	SubscriptionType  string                 `json:"subscription_type" binding:"oneof=standard subscription"`
	Quotas            map[string]interface{} `json:"quotas"`
	ModelRouting      map[string][]int64     `json:"model_routing"`
	ServiceTier       string                 `json:"service_tier" binding:"omitempty,oneof=fast flex"`
	ForceInstructions string                 `json:"force_instructions"`
	Note              string                 `json:"note"`
	SortWeight        int                    `json:"sort_weight"`
}

// UpdateGroupReq 更新分组请求
type UpdateGroupReq struct {
	Name              *string                `json:"name"`
	RateMultiplier    *float64               `json:"rate_multiplier"`
	IsExclusive       *bool                  `json:"is_exclusive"`
	StatusVisible     *bool                  `json:"status_visible"`
	SubscriptionType  *string                `json:"subscription_type" binding:"omitempty,oneof=standard subscription"`
	Quotas            map[string]interface{} `json:"quotas"`
	ModelRouting      map[string][]int64     `json:"model_routing"`
	ServiceTier       *string                `json:"service_tier" binding:"omitempty,oneof=fast flex"`
	ForceInstructions *string                `json:"force_instructions"`
	Note              *string                `json:"note"`
	SortWeight        *int                   `json:"sort_weight"`
}
