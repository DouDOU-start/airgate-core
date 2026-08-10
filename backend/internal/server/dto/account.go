package dto

// AccountResp 账号响应。
//
// Credentials 已脱敏（敏感键替换为 "***" 或删除）。
// state：active / rate_limited / degraded / disabled
// state_until 仅 rate_limited / degraded 有值。
type AccountResp struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Platform    string            `json:"platform"`
	Type        string            `json:"type"`
	Credentials map[string]string `json:"credentials"`
	Email       string            `json:"email,omitempty"`
	State       string            `json:"state"`
	StateUntil  *string           `json:"state_until,omitempty"`
	// PlanType 订阅档位：plus / pro / team / free / max …（Codex 等）
	PlanType string `json:"plan_type,omitempty"`
	// SubscriptionActiveUntil 订阅有效期（RFC3339 / ISO 字符串）；无则省略。
	SubscriptionActiveUntil string `json:"subscription_active_until,omitempty"`
	Priority                int    `json:"priority"`
	Weight                  int    `json:"weight"`
	MaxConcurrency          int    `json:"max_concurrency"`
	// CurrentConcurrency / CurrentRPM 运行时观测（在途 / 当前分钟），列表实时展示。
	CurrentConcurrency int            `json:"current_concurrency"`
	CurrentRPM         int            `json:"current_rpm"`
	MaxRPM             int            `json:"max_rpm,omitempty"`
	ProxyID            *int64         `json:"proxy_id,omitempty"`
	ProxyName          string         `json:"proxy_name,omitempty"`
	RateMultiplier     float64        `json:"rate_multiplier"`
	ErrorMsg           string         `json:"error_msg,omitempty"`
	Extra              map[string]any `json:"extra,omitempty"`
	// Models 可服务模型白名单（extra.models）；空 = 使用平台默认目录。
	Models       []string          `json:"models"`
	ModelMapping map[string]string `json:"model_mapping"`
	LastUsedAt   *string           `json:"last_used_at,omitempty"`
	GroupIDs     []int             `json:"group_ids"`
	// TotalCost / TotalRevenue 累计金额：成本 = Σ(total_cost×倍率)，收益 = Σ(actual_cost)。
	// TodayCost / TodayRevenue 为今日口径（按调用方 tz）。
	TotalCost    float64 `json:"total_cost"`
	TotalRevenue float64 `json:"total_revenue"`
	TodayCost    float64 `json:"today_cost"`
	TodayRevenue float64 `json:"today_revenue"`
	// Usage 用量窗口快照（Codex / Claude OAuth 等；刷新接口更新）。
	Usage *AccountUsageResp `json:"usage,omitempty"`
	TimeMixin
}

// AccountUsageWindowResp 单个限流窗口。
type AccountUsageWindowResp struct {
	Key           string  `json:"key"`
	Label         string  `json:"label,omitempty"`
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes,omitempty"`
	ResetsAt      *string `json:"resets_at,omitempty"`
	LimitID       string  `json:"limit_id,omitempty"`
	LimitName     string  `json:"limit_name,omitempty"`
}

// AccountUsageCreditsResp 积分余额。
type AccountUsageCreditsResp struct {
	HasCredits bool   `json:"has_credits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance,omitempty"`
}

// AccountUsageResp 账号用量窗口。
type AccountUsageResp struct {
	CapturedAt            string                   `json:"captured_at"`
	Stale                 bool                     `json:"stale"`
	PlanType              string                   `json:"plan_type,omitempty"`
	Platform              string                   `json:"platform,omitempty"`
	Windows               []AccountUsageWindowResp `json:"windows"`
	Credits               *AccountUsageCreditsResp `json:"credits,omitempty"`
	ResetCreditsAvailable int                      `json:"reset_credits_available,omitempty"`
	Error                 string                   `json:"error,omitempty"`
}

// ConsumeUsageResetReq 消费限额重置积分。
type ConsumeUsageResetReq struct {
	// CreditID 可选；空则由上游自选一枚 available 积分。
	CreditID string `json:"credit_id"`
}

// ConsumeUsageResetResp 重置额度结果。
type ConsumeUsageResetResp struct {
	Code         string            `json:"code"`
	WindowsReset int               `json:"windows_reset"`
	Account      AccountResp       `json:"account"`
	Usage        *AccountUsageResp `json:"usage,omitempty"`
}

// AccountTestReq 账号连通性测试请求。
type AccountTestReq struct {
	ModelID     string `json:"model_id"`
	Prompt      string `json:"prompt"`
	TestMode    string `json:"test_mode"`
	Duration    int    `json:"duration"`
	AspectRatio string `json:"aspect_ratio"`
	Resolution  string `json:"resolution"`
}

// AccountTestModelResp 测试可选模型。
type AccountTestModelResp struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	Kind          string `json:"kind"`
	DefaultPrompt string `json:"default_prompt"`
}

// AccountUsageStatsResp 账号使用统计（对齐 sub2api）。
type AccountUsageStatsResp struct {
	History []AccountDayHistoryResp `json:"history"`
	Summary AccountUsageSummaryResp `json:"summary"`
	Models  []AccountModelStatResp  `json:"models"`
}

// AccountDayHistoryResp 按日历史。
type AccountDayHistoryResp struct {
	Date       string  `json:"date"`
	Label      string  `json:"label"`
	Requests   int64   `json:"requests"`
	Tokens     int64   `json:"tokens"`
	Cost       float64 `json:"cost"`
	ActualCost float64 `json:"actual_cost"`
	UserCost   float64 `json:"user_cost"`
}

// AccountDayHighlightResp 峰值/今日摘要。
type AccountDayHighlightResp struct {
	Date     string  `json:"date"`
	Label    string  `json:"label"`
	Cost     float64 `json:"cost"`
	UserCost float64 `json:"user_cost"`
	Requests int64   `json:"requests"`
	Tokens   int64   `json:"tokens,omitempty"`
}

// AccountUsageSummaryResp 汇总。
type AccountUsageSummaryResp struct {
	Days              int                      `json:"days"`
	ActualDaysUsed    int                      `json:"actual_days_used"`
	TotalCost         float64                  `json:"total_cost"`
	TotalUserCost     float64                  `json:"total_user_cost"`
	TotalStandardCost float64                  `json:"total_standard_cost"`
	TotalRequests     int64                    `json:"total_requests"`
	TotalTokens       int64                    `json:"total_tokens"`
	AvgDailyCost      float64                  `json:"avg_daily_cost"`
	AvgDailyUserCost  float64                  `json:"avg_daily_user_cost"`
	AvgDailyRequests  float64                  `json:"avg_daily_requests"`
	AvgDailyTokens    float64                  `json:"avg_daily_tokens"`
	AvgDurationMs     float64                  `json:"avg_duration_ms"`
	Today             *AccountDayHighlightResp `json:"today,omitempty"`
	HighestCostDay    *AccountDayHighlightResp `json:"highest_cost_day,omitempty"`
	HighestRequestDay *AccountDayHighlightResp `json:"highest_request_day,omitempty"`
}

// AccountModelStatResp 模型分布。
type AccountModelStatResp struct {
	Model      string  `json:"model"`
	Requests   int64   `json:"requests"`
	Tokens     int64   `json:"tokens"`
	TotalCost  float64 `json:"total_cost"`
	ActualCost float64 `json:"actual_cost"`
}

// CreateAccountReq 创建账号请求。
// Type 仅 oauth | api_key（历史 refresh_token/setup_token/apikey 会归一）。
type CreateAccountReq struct {
	Name           string            `json:"name" binding:"required"`
	Platform       string            `json:"platform" binding:"required"`
	Type           string            `json:"type"`
	Credentials    map[string]string `json:"credentials" binding:"required"`
	Priority       int               `json:"priority"`
	Weight         int               `json:"weight"`
	MaxConcurrency int               `json:"max_concurrency"`
	ProxyID        *int64            `json:"proxy_id"`
	RateMultiplier float64           `json:"rate_multiplier"`
	Extra          map[string]any    `json:"extra,omitempty"`
	GroupIDs       []int             `json:"group_ids"`
}

// UpdateAccountReq 更新账号请求。
// State 只允许 "active" / "disabled"（运维手动恢复 / 禁用）。
type UpdateAccountReq struct {
	Name           *string           `json:"name"`
	Type           *string           `json:"type"`
	Credentials    map[string]string `json:"credentials"`
	State          *string           `json:"state" binding:"omitempty,oneof=active disabled"`
	Priority       *int              `json:"priority"`
	Weight         *int              `json:"weight"`
	MaxConcurrency *int              `json:"max_concurrency"`
	ProxyID        *int64            `json:"proxy_id"`
	RateMultiplier *float64          `json:"rate_multiplier"`
	Extra          map[string]any    `json:"extra,omitempty"`
	GroupIDs       []int             `json:"group_ids"`
	// Models 可服务模型白名单；字段出现则写入（空数组清除，回退平台默认）。
	Models       *[]string          `json:"models"`
	ModelMapping *map[string]string `json:"model_mapping"`
}

// AccountExportItem 导出文件中的单条账号（明文 credentials）。
// GroupIDs 和 ProxyID 仅用于兼容读取旧版导出文件，导入时会忽略。
type AccountExportItem struct {
	Name           string            `json:"name"`
	Platform       string            `json:"platform"`
	Type           string            `json:"type,omitempty"`
	Credentials    map[string]string `json:"credentials"`
	Priority       int               `json:"priority"`
	Weight         int               `json:"weight"`
	MaxConcurrency int               `json:"max_concurrency"`
	RateMultiplier float64           `json:"rate_multiplier"`
	GroupIDs       []int             `json:"group_ids,omitempty"`
	ProxyID        *int64            `json:"proxy_id,omitempty"`
	Extra          map[string]any    `json:"extra,omitempty"`
}

// AccountExportFile 导出文件结构。
type AccountExportFile struct {
	Version    int                 `json:"version"`
	ExportedAt string              `json:"exported_at"`
	Count      int                 `json:"count"`
	Accounts   []AccountExportItem `json:"accounts"`
}

// ImportAccountsReq 批量导入请求。
type ImportAccountsReq struct {
	Accounts []AccountExportItem `json:"accounts" binding:"required"`
}

// ImportItemErrorResp 导入失败项响应。
type ImportItemErrorResp struct {
	Index   int    `json:"index"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// ImportAccountsResp 导入结果响应。
type ImportAccountsResp struct {
	Imported int                   `json:"imported"`
	Failed   int                   `json:"failed"`
	Errors   []ImportItemErrorResp `json:"errors,omitempty"`
}

// BulkUpdateAccountsReq 批量更新账号请求。
type BulkUpdateAccountsReq struct {
	AccountIDs     []int    `json:"account_ids" binding:"required,min=1"`
	State          *string  `json:"state" binding:"omitempty,oneof=active disabled"`
	Priority       *int     `json:"priority"`
	Weight         *int     `json:"weight"`
	MaxConcurrency *int     `json:"max_concurrency"`
	RateMultiplier *float64 `json:"rate_multiplier"`
	GroupIDs       []int    `json:"group_ids"`
	ProxyID        *int64   `json:"proxy_id"`
	// Models 批量写入模型白名单；字段出现则覆盖（空数组清除）。
	Models       *[]string          `json:"models"`
	ModelMapping *map[string]string `json:"model_mapping"`
}

// BulkAccountIDsReq 仅携带账号 ID 列表的批量请求（删除等）。
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

// CredentialSchemaResp 凭证字段 schema 响应。
type CredentialSchemaResp struct {
	Fields       []CredentialFieldResp `json:"fields"`
	AccountTypes []AccountTypeResp     `json:"account_types,omitempty"`
}

// AccountTypeResp 账号类型定义。
type AccountTypeResp struct {
	Key         string                `json:"key"`
	Label       string                `json:"label"`
	Description string                `json:"description"`
	Fields      []CredentialFieldResp `json:"fields"`
}

// CredentialFieldResp 凭证字段定义。
type CredentialFieldResp struct {
	Key          string `json:"key"`
	Label        string `json:"label"`
	Type         string `json:"type"` // text / password / textarea / select
	Required     bool   `json:"required"`
	Placeholder  string `json:"placeholder"`
	EditDisabled bool   `json:"edit_disabled,omitempty"`
}

// StartOAuthReq 发起 OAuth 登录请求。
type StartOAuthReq struct {
	Name           string  `json:"name"`
	ProxyURL       string  `json:"proxy_url"`
	ProxyID        *int64  `json:"proxy_id"`
	GroupIDs       []int   `json:"group_ids"`
	Priority       int     `json:"priority"`
	Weight         int     `json:"weight"`
	MaxConcurrency int     `json:"max_concurrency"`
	RateMultiplier float64 `json:"rate_multiplier"`
	ProjectID      string  `json:"project_id"`
	// Mode browser（默认）。Codex 已不再支持 device。
	Mode string `json:"mode"`
	// AccountID 重新授权目标账号；>0 时完成后更新该账号凭证，不新建。
	AccountID int `json:"account_id"`
}

// CodexImportRefreshReq Codex RT 导入。
type CodexImportRefreshReq struct {
	RefreshToken   string  `json:"refresh_token" binding:"required"`
	ClientID       string  `json:"client_id"`
	Name           string  `json:"name"`
	ProxyURL       string  `json:"proxy_url"`
	ProxyID        *int64  `json:"proxy_id"`
	GroupIDs       []int   `json:"group_ids"`
	Priority       int     `json:"priority"`
	Weight         int     `json:"weight"`
	MaxConcurrency int     `json:"max_concurrency"`
	RateMultiplier float64 `json:"rate_multiplier"`
	// AccountID 重新授权目标账号。
	AccountID int `json:"account_id"`
}

// AntigravityImportRefreshReq Antigravity RT 导入。
type AntigravityImportRefreshReq struct {
	RefreshToken   string  `json:"refresh_token" binding:"required"`
	Name           string  `json:"name"`
	ProxyURL       string  `json:"proxy_url"`
	ProxyID        *int64  `json:"proxy_id"`
	GroupIDs       []int   `json:"group_ids"`
	Priority       int     `json:"priority"`
	Weight         int     `json:"weight"`
	MaxConcurrency int     `json:"max_concurrency"`
	RateMultiplier float64 `json:"rate_multiplier"`
	// AccountID 重新授权目标账号。
	AccountID int `json:"account_id"`
}

// CodexImportSessionReq Codex Session 导入。
type CodexImportSessionReq struct {
	Session        string  `json:"session" binding:"required"`
	Name           string  `json:"name"`
	ProxyURL       string  `json:"proxy_url"`
	ProxyID        *int64  `json:"proxy_id"`
	GroupIDs       []int   `json:"group_ids"`
	Priority       int     `json:"priority"`
	Weight         int     `json:"weight"`
	MaxConcurrency int     `json:"max_concurrency"`
	RateMultiplier float64 `json:"rate_multiplier"`
	// AccountID 重新授权目标账号。
	AccountID int `json:"account_id"`
}

// OAuthSessionResp 交互式 OAuth 会话状态。
type OAuthSessionResp struct {
	ID                      string `json:"id"`
	Platform                string `json:"platform"`
	Status                  string `json:"status"` // pending / completed / failed
	Flow                    string `json:"flow"`   // paste_code / device
	Message                 string `json:"message,omitempty"`
	Error                   string `json:"error,omitempty"`
	AuthorizeURL            string `json:"authorize_url,omitempty"`
	UserCode                string `json:"user_code,omitempty"`
	VerificationURI         string `json:"verification_uri,omitempty"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	AccountID               int    `json:"account_id,omitempty"`
	AccountName             string `json:"account_name,omitempty"`
	CreatedAt               string `json:"created_at"`
}

// CompleteOAuthReq 粘贴 authorization code 完成授权。
type CompleteOAuthReq struct {
	Code string `json:"code" binding:"required"`
}
