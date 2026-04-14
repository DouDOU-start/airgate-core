package dto

// UserResp 用户响应
type UserResp struct {
	ID                    int64             `json:"id"`
	Email                 string            `json:"email"`
	Username              string            `json:"username"`
	Balance               float64           `json:"balance"`
	Role                  string            `json:"role"` // admin / user
	MaxConcurrency        int               `json:"max_concurrency"`
	GroupRates            map[int64]float64 `json:"group_rates,omitempty"`       // 用户专属分组倍率
	AllowedGroupIDs       []int64           `json:"allowed_group_ids,omitempty"` // 已分配的专属分组 ID
	BalanceAlertThreshold float64           `json:"balance_alert_threshold"`
	Status                string            `json:"status"`
	APIKeyID              int64             `json:"api_key_id,omitempty"`        // API Key 登录时返回
	APIKeyName            string            `json:"api_key_name,omitempty"`      // API Key 登录时返回
	APIKeyQuotaUSD        float64           `json:"api_key_quota_usd,omitempty"` // Key 额度（0=不限）
	APIKeyUsedQuota       float64           `json:"api_key_used_quota,omitempty"`
	APIKeyExpiresAt       string            `json:"api_key_expires_at,omitempty"` // RFC3339
	// APIKeyRate 对客户展示的最终倍率：sell_rate>0 时取 sell_rate，否则取分组 rate_multiplier。
	// 故意不区分来源以避免泄漏 reseller 的定价模型。
	APIKeyRate float64 `json:"api_key_rate,omitempty"`
	// APIKeyPlatform 当前 Key 所属分组平台（如 anthropic / openai），用于前端 CCS 导入识别客户端类型。
	APIKeyPlatform string `json:"api_key_platform,omitempty"`
	TimeMixin
}

// UpdateProfileReq 用户更新资料请求
type UpdateProfileReq struct {
	Username string `json:"username"`
}

// ChangePasswordReq 修改密码请求
type ChangePasswordReq struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6"`
}

// CreateUserReq 管理员创建用户请求
type CreateUserReq struct {
	Email          string            `json:"email" binding:"required,email"`
	Password       string            `json:"password" binding:"required,min=6"`
	Username       string            `json:"username"`
	Role           string            `json:"role" binding:"oneof=admin user"`
	MaxConcurrency int               `json:"max_concurrency"`
	GroupRates     map[int64]float64 `json:"group_rates"`
}

// UpdateUserReq 管理员更新用户请求
type UpdateUserReq struct {
	Username        *string           `json:"username"`
	Password        *string           `json:"password" binding:"omitempty,min=6"`
	Role            *string           `json:"role" binding:"omitempty,oneof=admin user"`
	MaxConcurrency  *int              `json:"max_concurrency"`
	GroupRates      map[int64]float64 `json:"group_rates"`
	AllowedGroupIDs *[]int64          `json:"allowed_group_ids"` // nil=不修改, []=清空, [1,2]=设置
	Status          *string           `json:"status" binding:"omitempty,oneof=active disabled"`
}

// AdjustBalanceReq 余额调整请求
type AdjustBalanceReq struct {
	Action string  `json:"action" binding:"required,oneof=set add subtract"`
	Amount float64 `json:"amount" binding:"required"`
	Remark string  `json:"remark"`
}

// GroupRateOverrideResp 分组专属倍率条目。
type GroupRateOverrideResp struct {
	UserID   int64   `json:"user_id"`
	Email    string  `json:"email"`
	Username string  `json:"username"`
	Rate     float64 `json:"rate"`
}

// SetGroupRateOverrideReq 设置分组专属倍率请求。
type SetGroupRateOverrideReq struct {
	Rate float64 `json:"rate" binding:"required,gt=0"`
}

// BalanceLogResp 余额变更日志响应
type BalanceLogResp struct {
	ID            int64   `json:"id"`
	Action        string  `json:"action"`
	Amount        float64 `json:"amount"`
	BeforeBalance float64 `json:"before_balance"`
	AfterBalance  float64 `json:"after_balance"`
	Remark        string  `json:"remark"`
	CreatedAt     string  `json:"created_at"`
}
