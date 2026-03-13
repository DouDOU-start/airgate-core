package dto

// APIKeyResp API 密钥响应
type APIKeyResp struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Key         string   `json:"key,omitempty"` // 仅创建时返回完整密钥
	KeyPrefix   string   `json:"key_prefix"`    // sk-xxxx... 前缀展示
	UserID      int64    `json:"user_id"`
	GroupID     int64    `json:"group_id"`
	IPWhitelist []string `json:"ip_whitelist,omitempty"`
	IPBlacklist []string `json:"ip_blacklist,omitempty"`
	QuotaUSD    float64  `json:"quota_usd"`
	UsedQuota   float64  `json:"used_quota"`
	ExpiresAt   *string  `json:"expires_at,omitempty"`
	Status      string   `json:"status"`
	TimeMixin
}

// CreateAPIKeyReq 创建 API 密钥请求
type CreateAPIKeyReq struct {
	Name        string   `json:"name" binding:"required"`
	GroupID     int64    `json:"group_id" binding:"required"`
	IPWhitelist []string `json:"ip_whitelist"`
	IPBlacklist []string `json:"ip_blacklist"`
	QuotaUSD    float64  `json:"quota_usd"`
	ExpiresAt   *string  `json:"expires_at"`
}

// UpdateAPIKeyReq 更新 API 密钥请求
type UpdateAPIKeyReq struct {
	Name        *string  `json:"name"`
	GroupID     *int64   `json:"group_id"`
	IPWhitelist []string `json:"ip_whitelist"`
	IPBlacklist []string `json:"ip_blacklist"`
	QuotaUSD    *float64 `json:"quota_usd"`
	ExpiresAt   *string  `json:"expires_at"`
	Status      *string  `json:"status" binding:"omitempty,oneof=active disabled"`
}

// AdminUpdateAPIKeyReq 管理员更新密钥请求
type AdminUpdateAPIKeyReq struct {
	UpdateAPIKeyReq
}
