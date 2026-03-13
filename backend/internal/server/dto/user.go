package dto

// UserResp 用户响应
type UserResp struct {
	ID             int64             `json:"id"`
	Email          string            `json:"email"`
	Username       string            `json:"username"`
	Balance        float64           `json:"balance"`
	Role           string            `json:"role"` // admin / user
	MaxConcurrency int               `json:"max_concurrency"`
	TOTPEnabled    bool              `json:"totp_enabled"`
	GroupRates     map[int64]float64 `json:"group_rates,omitempty"` // 用户专属分组倍率
	Status         string            `json:"status"`
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
	Username       *string           `json:"username"`
	Role           *string           `json:"role" binding:"omitempty,oneof=admin user"`
	MaxConcurrency *int              `json:"max_concurrency"`
	GroupRates     map[int64]float64 `json:"group_rates"`
	Status         *string           `json:"status" binding:"omitempty,oneof=active disabled"`
}

// AdjustBalanceReq 余额调整请求
type AdjustBalanceReq struct {
	Action string  `json:"action" binding:"required,oneof=set add subtract"`
	Amount float64 `json:"amount" binding:"required"`
}
