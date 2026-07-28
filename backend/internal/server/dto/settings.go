package dto

import "time"

// SettingResp 设置响应
type SettingResp struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Group string `json:"group"`
}

// UpdateSettingsReq 更新设置请求
type UpdateSettingsReq struct {
	Settings []SettingItem `json:"settings" binding:"required,min=1"`
}

// SettingItem 设置项
type SettingItem struct {
	Key   string `json:"key" binding:"required"`
	Value string `json:"value"`
	Group string `json:"group"`
}

// AdminAPIKeyResp 管理员 API Key 响应
type AdminAPIKeyResp struct {
	Hint string `json:"hint"`          // 脱敏显示，如 admin-ab12...ef56
	Key  string `json:"key,omitempty"` // 明文密钥（仅生成时返回一次）
}

// TestSMTPReq SMTP 测试请求
type TestSMTPReq struct {
	Host     string `json:"host" binding:"required"`
	Port     int    `json:"port" binding:"required"`
	Username string `json:"username"`
	Password string `json:"password"`
	UseTLS   bool   `json:"use_tls"`
	From     string `json:"from" binding:"required"`
	To       string `json:"to" binding:"required"`
}

// TestWeChatReq 微信公众号测试消息请求。
type TestWeChatReq struct {
	AppID      string `json:"app_id" binding:"required"`
	AppSecret  string `json:"app_secret" binding:"required"`
	TemplateID string `json:"template_id" binding:"required"`
	OpenID     string `json:"open_id" binding:"required"`
	DetailURL  string `json:"detail_url"`
}

// WeChatBindSessionResp 微信公众号管理员扫码绑定会话响应。
type WeChatBindSessionResp struct {
	ID        string    `json:"id"`
	OAuthURL  string    `json:"oauth_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// WeChatBindStatusResp 微信公众号管理员扫码绑定状态响应。
type WeChatBindStatusResp struct {
	Status     string `json:"status"`
	OpenIDHint string `json:"open_id_hint,omitempty"`
}
