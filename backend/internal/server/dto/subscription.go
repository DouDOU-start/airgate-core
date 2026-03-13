package dto

// SubscriptionResp 订阅响应
type SubscriptionResp struct {
	ID          int64                  `json:"id"`
	UserID      int64                  `json:"user_id"`
	GroupID     int64                  `json:"group_id"`
	GroupName   string                 `json:"group_name"`
	EffectiveAt string                 `json:"effective_at"`
	ExpiresAt   string                 `json:"expires_at"`
	Usage       map[string]interface{} `json:"usage"`  // 日/周/月使用量窗口
	Status      string                 `json:"status"` // active / expired / suspended
	TimeMixin
}

// SubscriptionProgressResp 订阅进度响应
type SubscriptionProgressResp struct {
	GroupID   int64        `json:"group_id"`
	GroupName string       `json:"group_name"`
	Daily     *UsageWindow `json:"daily,omitempty"`
	Weekly    *UsageWindow `json:"weekly,omitempty"`
	Monthly   *UsageWindow `json:"monthly,omitempty"`
}

// UsageWindow 使用量窗口
type UsageWindow struct {
	Used  float64 `json:"used"`
	Limit float64 `json:"limit"`
	Reset string  `json:"reset"` // 下次重置时间
}

// AssignSubscriptionReq 分配订阅请求
type AssignSubscriptionReq struct {
	UserID    int64  `json:"user_id" binding:"required"`
	GroupID   int64  `json:"group_id" binding:"required"`
	ExpiresAt string `json:"expires_at" binding:"required"`
}

// BulkAssignReq 批量分配订阅请求
type BulkAssignReq struct {
	UserIDs   []int64 `json:"user_ids" binding:"required,min=1"`
	GroupID   int64   `json:"group_id" binding:"required"`
	ExpiresAt string  `json:"expires_at" binding:"required"`
}

// AdjustSubscriptionReq 调整订阅期限请求
type AdjustSubscriptionReq struct {
	ExpiresAt *string `json:"expires_at"`
	Status    *string `json:"status" binding:"omitempty,oneof=active suspended"`
}
