package dto

// APIKeyResp API 密钥响应
type APIKeyResp struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Key             string   `json:"key,omitempty"` // 仅创建时返回完整密钥
	KeyPrefix       string   `json:"key_prefix"`    // sk-xxxx... 前缀展示
	UserID          int64    `json:"user_id"`
	GroupID         *int64   `json:"group_id"`
	IPWhitelist     []string `json:"ip_whitelist,omitempty"`
	IPBlacklist     []string `json:"ip_blacklist,omitempty"`
	QuotaUSD        float64  `json:"quota_usd"`
	UsedQuota       float64  `json:"used_quota"`        // 账面已用（含 sell_rate markup）
	UsedQuotaActual float64  `json:"used_quota_actual"` // 真实成本已用（reseller 看板对比用，sum(actual_cost)）
	SellRate        float64  `json:"sell_rate"`         // 销售倍率，0 表示未启用
	MaxRate         float64  `json:"max_rate"`          // 最高计费倍率，0 表示不限制；实际扣费倍率超过时拒绝请求
	MaxConcurrency  int      `json:"max_concurrency"`   // API Key 级并发上限，0 表示不限制
	TodayCost       float64  `json:"today_cost"`
	ThirtyDayCost   float64  `json:"thirty_day_cost"`
	ExpiresAt       *string  `json:"expires_at,omitempty"`
	Status          string   `json:"status"`
	TimeMixin
}

// APIKeyListQuery API Key 列表查询参数。
type APIKeyListQuery struct {
	PageReq
	SearchScope string `form:"search_scope"`
}

// CreateAPIKeyReq 创建 API 密钥请求
type CreateAPIKeyReq struct {
	Name           string   `json:"name" binding:"required"`
	GroupID        int64    `json:"group_id" binding:"required"`
	IPWhitelist    []string `json:"ip_whitelist"`
	IPBlacklist    []string `json:"ip_blacklist"`
	QuotaUSD       float64  `json:"quota_usd" binding:"gte=0"`       // 0 表示无限额度，负数拒绝
	SellRate       float64  `json:"sell_rate" binding:"gte=0"`       // 可选，>0 启用 reseller markup，负数拒绝
	MaxRate        float64  `json:"max_rate" binding:"gte=0"`        // 可选，>0 时实际扣费倍率超过该值拒绝请求，负数拒绝
	MaxConcurrency int      `json:"max_concurrency" binding:"gte=0"` // 0 表示不限制并发
	ExpiresAt      *string  `json:"expires_at"`
}

// UpdateAPIKeyReq 更新 API 密钥请求
type UpdateAPIKeyReq struct {
	Name           *string  `json:"name"`
	GroupID        *int64   `json:"group_id"`
	IPWhitelist    []string `json:"ip_whitelist"`
	IPBlacklist    []string `json:"ip_blacklist"`
	QuotaUSD       *float64 `json:"quota_usd" binding:"omitempty,gte=0"`
	SellRate       *float64 `json:"sell_rate" binding:"omitempty,gte=0"`       // 动态调整：随时可改，不影响历史 used_quota 累加值；负数拒绝
	MaxRate        *float64 `json:"max_rate" binding:"omitempty,gte=0"`        // 最高计费倍率，指向 0 表示关闭限制；负数拒绝
	MaxConcurrency *int     `json:"max_concurrency" binding:"omitempty,gte=0"` // 0 关闭并发限制
	ExpiresAt      *string  `json:"expires_at"`
	Status         *string  `json:"status" binding:"omitempty,oneof=active disabled"`
}

// AdminUpdateAPIKeyReq 管理员更新密钥请求
type AdminUpdateAPIKeyReq struct {
	UpdateAPIKeyReq
}
