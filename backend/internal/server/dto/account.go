package dto

// AccountResp 账号响应
type AccountResp struct {
	ID                 int64             `json:"id"`
	Name               string            `json:"name"`
	Platform           string            `json:"platform"`
	Type               string            `json:"type"`
	Credentials        map[string]string `json:"credentials"`
	Status             string            `json:"status"` // active / error / disabled
	Priority           int               `json:"priority"`
	MaxConcurrency     int               `json:"max_concurrency"`
	CurrentConcurrency int               `json:"current_concurrency"`
	ProxyID            *int64            `json:"proxy_id,omitempty"`
	RateMultiplier     float64           `json:"rate_multiplier"`
	ErrorMsg           string            `json:"error_msg,omitempty"`
	LastUsedAt         *string           `json:"last_used_at,omitempty"`
	GroupIDs           []int64           `json:"group_ids"`
	TimeMixin
}

// CreateAccountReq 创建账号请求
type CreateAccountReq struct {
	Name           string            `json:"name" binding:"required"`
	Platform       string            `json:"platform" binding:"required"`
	Type           string            `json:"type"` // 账号类型，如 "apikey", "oauth"
	Credentials    map[string]string `json:"credentials" binding:"required"`
	Priority       int               `json:"priority"`
	MaxConcurrency int               `json:"max_concurrency"`
	ProxyID        *int64            `json:"proxy_id"`
	RateMultiplier float64           `json:"rate_multiplier"`
	GroupIDs       []int64           `json:"group_ids"`
}

// UpdateAccountReq 更新账号请求
type UpdateAccountReq struct {
	Name           *string           `json:"name"`
	Type           *string           `json:"type"`
	Credentials    map[string]string `json:"credentials"`
	Status         *string           `json:"status" binding:"omitempty,oneof=active disabled"`
	Priority       *int              `json:"priority"`
	MaxConcurrency *int              `json:"max_concurrency"`
	ProxyID        *int64            `json:"proxy_id"`
	RateMultiplier *float64          `json:"rate_multiplier"`
	GroupIDs       []int64           `json:"group_ids"`
}

// AccountExportItem 导出文件中的单条账号。
// group_ids / proxy_id 仅为兼容旧导入文件保留，导出时不会再写出，导入时也会被忽略。
type AccountExportItem struct {
	Name           string            `json:"name"`
	Platform       string            `json:"platform"`
	Type           string            `json:"type,omitempty"`
	Credentials    map[string]string `json:"credentials"`
	Priority       int               `json:"priority"`
	MaxConcurrency int               `json:"max_concurrency"`
	RateMultiplier float64           `json:"rate_multiplier"`
	GroupIDs       []int64           `json:"group_ids,omitempty"`
	ProxyID        *int64            `json:"proxy_id,omitempty"`
}

// AccountExportFile 导出文件结构，仅包含可跨环境迁移的账号本体字段。
type AccountExportFile struct {
	Version    int                 `json:"version"`
	ExportedAt string              `json:"exported_at"`
	Count      int                 `json:"count"`
	Accounts   []AccountExportItem `json:"accounts"`
}

// ImportAccountsReq 批量导入请求
type ImportAccountsReq struct {
	Accounts []AccountExportItem `json:"accounts" binding:"required"`
}

// ImportItemErrorResp 导入失败项响应
type ImportItemErrorResp struct {
	Index   int    `json:"index"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// ImportAccountsResp 导入结果响应
type ImportAccountsResp struct {
	Imported int                   `json:"imported"`
	Failed   int                   `json:"failed"`
	Errors   []ImportItemErrorResp `json:"errors,omitempty"`
}

// BulkUpdateAccountsReq 批量更新账号请求。
// 所有可选字段：nil/缺失 = 不修改，仅 "account_ids" 必填。
// add_group_ids 为追加模式，与账号原有分组取并集。
type BulkUpdateAccountsReq struct {
	AccountIDs     []int    `json:"account_ids" binding:"required,min=1"`
	Status         *string  `json:"status" binding:"omitempty,oneof=active disabled"`
	Priority       *int     `json:"priority"`
	MaxConcurrency *int     `json:"max_concurrency"`
	RateMultiplier *float64 `json:"rate_multiplier"`
	GroupIDs       []int64  `json:"group_ids"`
	ProxyID        *int64   `json:"proxy_id"`
}

// BulkAccountIDsReq 仅携带账号 ID 列表的批量请求（用于删除、刷新令牌等）。
type BulkAccountIDsReq struct {
	AccountIDs []int `json:"account_ids" binding:"required,min=1"`
}

// BulkOpItemResp 批量操作单条结果。
type BulkOpItemResp struct {
	ID      int    `json:"id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// BulkOpResp 批量操作汇总响应。
type BulkOpResp struct {
	Success    int              `json:"success"`
	Failed     int              `json:"failed"`
	SuccessIDs []int            `json:"success_ids"`
	FailedIDs  []int            `json:"failed_ids"`
	Results    []BulkOpItemResp `json:"results"`
}

// CredentialSchemaResp 凭证字段 schema 响应
type CredentialSchemaResp struct {
	Fields       []CredentialFieldResp `json:"fields"`
	AccountTypes []AccountTypeResp     `json:"account_types,omitempty"`
}

// AccountTypeResp 账号类型定义
type AccountTypeResp struct {
	Key         string                `json:"key"`
	Label       string                `json:"label"`
	Description string                `json:"description"`
	Fields      []CredentialFieldResp `json:"fields"`
}

// CredentialFieldResp 凭证字段定义
type CredentialFieldResp struct {
	Key          string `json:"key"`
	Label        string `json:"label"`
	Type         string `json:"type"` // text / password / textarea / select
	Required     bool   `json:"required"`
	Placeholder  string `json:"placeholder"`
	EditDisabled bool   `json:"edit_disabled,omitempty"` // 编辑模式下隐藏该字段
}
