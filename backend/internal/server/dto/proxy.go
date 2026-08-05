package dto

// ProxyResp 代理响应（不含 password）。
type ProxyResp struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"` // http / socks5
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	Status   string `json:"status"` // active / disabled
	TimeMixin
}

// CreateProxyReq 创建代理请求。
type CreateProxyReq struct {
	Name     string `json:"name" binding:"required"`
	Protocol string `json:"protocol" binding:"required,oneof=http socks5"`
	Address  string `json:"address" binding:"required"`
	Port     int    `json:"port" binding:"required,min=1,max=65535"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// UpdateProxyReq 更新代理请求。
type UpdateProxyReq struct {
	Name     *string `json:"name"`
	Protocol *string `json:"protocol" binding:"omitempty,oneof=http socks5"`
	Address  *string `json:"address"`
	Port     *int    `json:"port" binding:"omitempty,min=1,max=65535"`
	Username *string `json:"username"`
	Password *string `json:"password"`
	Status   *string `json:"status" binding:"omitempty,oneof=active disabled"`
}

// TestProxyResp 测试代理响应。
type TestProxyResp struct {
	Success     bool   `json:"success"`
	Latency     int64  `json:"latency_ms"`
	ErrorMsg    string `json:"error_msg,omitempty"`
	IPAddress   string `json:"ip_address,omitempty"`
	Country     string `json:"country,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
	City        string `json:"city,omitempty"`
}
