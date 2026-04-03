package dto

// LoginReq 登录请求
type LoginReq struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

// LoginResp 登录响应
type LoginResp struct {
	Token string   `json:"token"`
	User  UserResp `json:"user"`
}

// RegisterReq 注册请求
type RegisterReq struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
	Username string `json:"username"`
}

// RefreshResp Token 刷新响应
type RefreshResp struct {
	Token string `json:"token"`
}
