package dto

import "time"

// ChannelKeyResp 渠道下的一把密钥端点响应。明文密钥永不出现，仅回 api_key_hint（尾 4 位）。
type ChannelKeyResp struct {
	ID                  int64    `json:"id"`
	CredentialID        int64    `json:"credential_id"`
	CredentialStatus    string   `json:"credential_status"`
	CredentialErrorMsg  string   `json:"credential_error_msg"`
	CredentialProtocols []string `json:"credential_protocols"`
	ChannelID           int64    `json:"channel_id"`
	// ChannelName / BaseURL 所属渠道的冗余展示字段，密钥视图（跨渠道平铺）用；渠道视图下与父渠道重复但无害。
	ChannelName    string            `json:"channel_name"`
	BaseURL        string            `json:"base_url"`
	Name           string            `json:"name"`
	Type           string            `json:"type"` // openai_compatible / anthropic / gemini / custom / openai_video / suno
	APIKeyHint     string            `json:"api_key_hint"`
	Models         []string          `json:"models"`
	ModelMapping   map[string]string `json:"model_mapping"`
	ParamOverride  map[string]any    `json:"param_override"`
	HeaderOverride map[string]string `json:"header_override"`
	Status         string            `json:"status"` // enabled / disabled_manual / disabled_auto
	ErrorMsg       string            `json:"error_msg"`
	Priority       int               `json:"priority"`
	Weight         int               `json:"weight"`
	MaxConcurrency int               `json:"max_concurrency"`
	MaxRPM         int               `json:"max_rpm"`
	CostRatio      float64           `json:"cost_ratio"`
	Tags           []string          `json:"tags"`
	TestModel      string            `json:"test_model"`
	ResponseTimeMs int               `json:"response_time_ms"`
	TestedAt       *time.Time        `json:"tested_at,omitempty"`
	LastUsedAt     *time.Time        `json:"last_used_at,omitempty"`
	GroupIDs       []int             `json:"group_ids"`
	// Balance 该把 key 的上游账户余额（USD）；是否能查询取决于上游是否提供兼容余额接口。
	Balance          float64    `json:"balance"`
	BalanceUpdatedAt *time.Time `json:"balance_updated_at,omitempty"`
	// BalanceCheckEnabled 是否参与主动余额刷新（进页自动/一键批量）；关闭后手动单把查询仍可用。
	BalanceCheckEnabled bool `json:"balance_check_enabled"`
	// ProbeEnabled 是否启用主动健康探针。
	ProbeEnabled bool   `json:"probe_enabled"`
	ProbeModel   string `json:"probe_model"`
	// HealthStatus 健康状态机：healthy / degraded / suspended / recovering。
	HealthStatus         string     `json:"health_status"`
	ConsecutiveFailures  int        `json:"consecutive_failures"`
	ConsecutiveSuccesses int        `json:"consecutive_successes"`
	LastProbeAt          *time.Time `json:"last_probe_at,omitempty"`
	// UpstreamRateEnabled 是否启用上游倍率探测。
	UpstreamRateEnabled    bool       `json:"upstream_rate_enabled"`
	UpstreamRatePath       string     `json:"upstream_rate_path"`
	UseUpstreamRateForCost bool       `json:"use_upstream_rate_for_cost"`
	UpstreamRate           float64    `json:"upstream_rate"`
	UpstreamRateAt         *time.Time `json:"upstream_rate_at,omitempty"`
	// CurrentConcurrency / CurrentRPM 运行时观测指标（在途请求数 / 当前分钟请求数），列表实时展示。
	CurrentConcurrency int `json:"current_concurrency"`
	CurrentRPM         int `json:"current_rpm"`
	// TotalCost / TotalRevenue 累计金额：成本 = Σ(total_cost×成本倍率快照)，收益 = Σ(actual_cost)。
	// TodayCost / TodayRevenue 为今日口径（按请求 tz 参数的当日零点起算）。
	TotalCost    float64 `json:"total_cost"`
	TotalRevenue float64 `json:"total_revenue"`
	TodayCost    float64 `json:"today_cost"`
	TodayRevenue float64 `json:"today_revenue"`
	// AvgFirstTokenMs 最近 5 分钟窗口的平均首字延迟（ms），仅统计非图像、流式返回过首字的请求；窗口内无样本为 0。
	AvgFirstTokenMs float64 `json:"avg_first_token_ms"`
	TimeMixin
}

// ChannelResp 渠道响应（供应商级容器 + 其下密钥端点列表）。Balance / Total* 为各 key 汇总。
type ChannelResp struct {
	ID               int64            `json:"id"`
	Name             string           `json:"name"`
	BaseURL          string           `json:"base_url"`
	Balance          float64          `json:"balance"`
	BalanceUpdatedAt *time.Time       `json:"balance_updated_at,omitempty"`
	Keys             []ChannelKeyResp `json:"keys"`
	TotalCost        float64          `json:"total_cost"`
	TotalRevenue     float64          `json:"total_revenue"`
	TodayCost        float64          `json:"today_cost"`
	TodayRevenue     float64          `json:"today_revenue"`
	TimeMixin
}

// CreateChannelReq 创建渠道请求（仅 name/base_url；key 建后单独添加）。
type CreateChannelReq struct {
	Name    string `json:"name" binding:"required"`
	BaseURL string `json:"base_url" binding:"required"`
}

// UpdateChannelReq 更新渠道请求（partial，仅 name/base_url）。
type UpdateChannelReq struct {
	Name    *string `json:"name"`
	BaseURL *string `json:"base_url"`
}

// ChannelKeyReq 密钥端点写入请求（新增 POST /channels/:id/keys 与更新 PUT /channels/keys/:id 共用）。
// 新增时 type 或 types 至少提供一个且 api_key 必填；更新时 api_key 留空 = 保持原密钥不变，
// type/types 留空 = 不调整当前凭证支持的协议端点。
type ChannelKeyReq struct {
	Name             *string           `json:"name"`
	Type             string            `json:"type" binding:"omitempty,oneof=openai_compatible anthropic gemini custom openai_video suno"`
	Types            []string          `json:"types" binding:"omitempty,min=1,dive,oneof=openai_compatible anthropic gemini custom openai_video suno"`
	APIKey           string            `json:"api_key"`
	Models           []string          `json:"models"`
	ModelMapping     map[string]string `json:"model_mapping"`
	ParamOverride    map[string]any    `json:"param_override"`
	HeaderOverride   map[string]string `json:"header_override"`
	Status           *string           `json:"status" binding:"omitempty,oneof=enabled disabled_manual"`
	CredentialStatus *string           `json:"credential_status" binding:"omitempty,oneof=enabled disabled_manual"`
	Priority         *int              `json:"priority" binding:"omitempty,min=0,max=999"`
	Weight           *int              `json:"weight" binding:"omitempty,min=0"`
	MaxConcurrency   *int              `json:"max_concurrency" binding:"omitempty,min=0"`
	MaxRPM           *int              `json:"max_rpm" binding:"omitempty,min=0"`
	CostRatio        *float64          `json:"cost_ratio" binding:"omitempty,gte=0"`
	Tags             []string          `json:"tags"`
	TestModel        *string           `json:"test_model"`
	// BalanceCheckEnabled 省略 = 新增取默认 true / 更新不改。
	BalanceCheckEnabled *bool `json:"balance_check_enabled"`
	// ProbeEnabled 省略 = 新增取默认 false / 更新不改。
	ProbeEnabled *bool   `json:"probe_enabled"`
	ProbeModel   *string `json:"probe_model"`
	// UpstreamRateEnabled 省略 = 新增取默认 false / 更新不改。
	UpstreamRateEnabled    *bool   `json:"upstream_rate_enabled"`
	UpstreamRatePath       *string `json:"upstream_rate_path"`
	UseUpstreamRateForCost *bool   `json:"use_upstream_rate_for_cost"`
	GroupIDs               []int   `json:"group_ids"`
}

// TestChannelReq 密钥端点测试请求（model 缺省时取 key 的 test_model 或首个模型）。
// Endpoint 仅对 openai 协议 key 生效：chat_completions（缺省）/ responses。
type TestChannelReq struct {
	Model    string `json:"model"`
	Endpoint string `json:"endpoint" binding:"omitempty,oneof=chat_completions responses"`
}

// TestChannelResp 密钥端点测试响应。
type TestChannelResp struct {
	LatencyMs int    `json:"latency_ms"`
	Message   string `json:"message"`
}

// FetchChannelModelsResp 拉取上游模型列表响应。
type FetchChannelModelsResp struct {
	Models []string `json:"models"`
}

// RefreshChannelBalanceResp 刷新渠道余额响应。
type RefreshChannelBalanceResp struct {
	Balance          float64    `json:"balance"`
	BalanceUpdatedAt *time.Time `json:"balance_updated_at,omitempty"`
}

// FetchChannelModelsPreviewReq 预览拉取模型请求（key 未保存，直接给连接参数）。
type FetchChannelModelsPreviewReq struct {
	Type    string `json:"type" binding:"required,oneof=openai_compatible anthropic gemini custom openai_video suno"`
	BaseURL string `json:"base_url" binding:"required"`
	APIKey  string `json:"api_key" binding:"required"`
}

// BulkUpdateChannelsReq 批量操作请求（IDs 为渠道 ID）。
type BulkUpdateChannelsReq struct {
	IDs      []int  `json:"ids" binding:"required,min=1"`
	Action   string `json:"action" binding:"required,oneof=enable disable delete set_priority"`
	Priority *int   `json:"priority" binding:"omitempty,min=0,max=999"`
}

// BulkUpdateChannelsResp 批量操作响应。
type BulkUpdateChannelsResp struct {
	Affected int `json:"affected"`
}

// ChannelExportItem 渠道导出条目（渠道 + 其下密钥列表）。
type ChannelExportItem struct {
	Name    string                 `json:"name"`
	BaseURL string                 `json:"base_url"`
	Keys    []ChannelKeyExportItem `json:"keys"`
}

// ChannelKeyExportItem 密钥导出条目。api_key 置空（红线），api_key_hint 供参考。
type ChannelKeyExportItem struct {
	Name                   string            `json:"name"`
	Type                   string            `json:"type"`
	Types                  []string          `json:"types,omitempty"`
	APIKey                 string            `json:"api_key"`
	APIKeyHint             string            `json:"api_key_hint,omitempty"`
	Models                 []string          `json:"models"`
	ModelMapping           map[string]string `json:"model_mapping"`
	ParamOverride          map[string]any    `json:"param_override"`
	HeaderOverride         map[string]string `json:"header_override"`
	Status                 string            `json:"status"`
	Priority               int               `json:"priority"`
	Weight                 int               `json:"weight"`
	MaxConcurrency         int               `json:"max_concurrency"`
	MaxRPM                 int               `json:"max_rpm"`
	CostRatio              float64           `json:"cost_ratio"`
	Tags                   []string          `json:"tags"`
	TestModel              string            `json:"test_model"`
	BalanceCheckEnabled    bool              `json:"balance_check_enabled"`
	ProbeEnabled           bool              `json:"probe_enabled"`
	ProbeModel             string            `json:"probe_model"`
	UpstreamRateEnabled    bool              `json:"upstream_rate_enabled"`
	UpstreamRatePath       string            `json:"upstream_rate_path"`
	UseUpstreamRateForCost bool              `json:"use_upstream_rate_for_cost"`
	GroupIDs               []int             `json:"group_ids"`
}

// ImportChannelsReq 渠道导入请求（渠道数组，每渠道含 ≥1 把 key）。
type ImportChannelsReq []ImportChannelItem

// ImportChannelItem 导入的单个渠道。
type ImportChannelItem struct {
	Name    string                 `json:"name" binding:"required"`
	BaseURL string                 `json:"base_url" binding:"required"`
	Keys    []ImportChannelKeyItem `json:"keys" binding:"required,min=1,dive"`
}

// ImportChannelKeyItem 导入的单把密钥。api_key 为空时该 key 跳过不创建。
type ImportChannelKeyItem struct {
	Name                   string            `json:"name"`
	Type                   string            `json:"type" binding:"omitempty,oneof=openai_compatible anthropic gemini custom openai_video suno"`
	Types                  []string          `json:"types" binding:"omitempty,min=1,dive,oneof=openai_compatible anthropic gemini custom openai_video suno"`
	APIKey                 string            `json:"api_key"`
	Models                 []string          `json:"models"`
	ModelMapping           map[string]string `json:"model_mapping"`
	ParamOverride          map[string]any    `json:"param_override"`
	HeaderOverride         map[string]string `json:"header_override"`
	Status                 *string           `json:"status" binding:"omitempty,oneof=enabled disabled_manual"`
	Priority               *int              `json:"priority" binding:"omitempty,min=0,max=999"`
	Weight                 *int              `json:"weight" binding:"omitempty,min=0"`
	MaxConcurrency         *int              `json:"max_concurrency" binding:"omitempty,min=0"`
	MaxRPM                 *int              `json:"max_rpm" binding:"omitempty,min=0"`
	CostRatio              *float64          `json:"cost_ratio" binding:"omitempty,gte=0"`
	Tags                   []string          `json:"tags"`
	TestModel              *string           `json:"test_model"`
	BalanceCheckEnabled    *bool             `json:"balance_check_enabled"`
	ProbeEnabled           *bool             `json:"probe_enabled"`
	ProbeModel             *string           `json:"probe_model"`
	UpstreamRateEnabled    *bool             `json:"upstream_rate_enabled"`
	UpstreamRatePath       *string           `json:"upstream_rate_path"`
	UseUpstreamRateForCost *bool             `json:"use_upstream_rate_for_cost"`
	GroupIDs               []int             `json:"group_ids"`
}

// ImportChannelsResp 渠道导入响应。
type ImportChannelsResp struct {
	Channels int `json:"channels"`
	Keys     int `json:"keys"`
}
