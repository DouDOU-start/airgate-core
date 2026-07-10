package dto

// TierResp 用户等级响应
type TierResp struct {
	ID         int64             `json:"id"`
	Name       string            `json:"name"`
	Rates      map[int64]float64 `json:"rates"` // 等级在各分组的计费倍率（按 group_id 键）
	Note       string            `json:"note,omitempty"`
	SortWeight int               `json:"sort_weight"`
	UserCount  int               `json:"user_count"` // 归属此等级的用户数（列表返回）
	TimeMixin
}

// CreateTierReq 创建等级请求
type CreateTierReq struct {
	Name       string            `json:"name" binding:"required"`
	Rates      map[int64]float64 `json:"rates"`
	Note       string            `json:"note"`
	SortWeight int               `json:"sort_weight"`
}

// UpdateTierReq 更新等级请求（rates 提交即整体替换）
type UpdateTierReq struct {
	Name       *string           `json:"name"`
	Rates      map[int64]float64 `json:"rates"`
	Note       *string           `json:"note"`
	SortWeight *int              `json:"sort_weight"`
}
