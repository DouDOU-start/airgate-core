package dto

import "time"

// ChannelResp 渠道响应。api_keys 明文永不出现在任何响应，
// 仅回 api_keys_count 与 api_key_hints（尾 4 位提示）。
type ChannelResp struct {
	ID             int64             `json:"id"`
	Name           string            `json:"name"`
	Type           string            `json:"type"` // openai_compatible / anthropic / gemini / custom
	BaseURL        string            `json:"base_url"`
	APIKeysCount   int               `json:"api_keys_count"`
	APIKeyHints    []string          `json:"api_key_hints"`
	Models         []string          `json:"models"`
	ModelMapping   map[string]string `json:"model_mapping"`
	ParamOverride  map[string]any    `json:"param_override"`
	HeaderOverride map[string]string `json:"header_override"`
	Status         string            `json:"status"` // enabled / disabled_manual / disabled_auto
	StatusUntil    *time.Time        `json:"status_until,omitempty"`
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
	// Balance 上游账户余额（USD，多 key 求和）；仅 openai_compatible 中转站可查。
	Balance          float64    `json:"balance"`
	BalanceUpdatedAt *time.Time `json:"balance_updated_at,omitempty"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
	GroupIDs         []int      `json:"group_ids"`
	// CurrentConcurrency / CurrentRPM 运行时观测指标（在途请求数 / 当前分钟请求数），列表实时展示。
	CurrentConcurrency int `json:"current_concurrency"`
	CurrentRPM         int `json:"current_rpm"`
	// TotalCost / TotalRevenue 累计金额统计：渠道成本 = Σ(total_cost×成本倍率快照)，收益 = Σ(actual_cost)。
	TotalCost    float64 `json:"total_cost"`
	TotalRevenue float64 `json:"total_revenue"`
	TimeMixin
}

// CreateChannelReq 创建渠道请求。
type CreateChannelReq struct {
	Name    string   `json:"name" binding:"required"`
	Type    string   `json:"type" binding:"required,oneof=openai_compatible anthropic gemini custom"`
	BaseURL string   `json:"base_url" binding:"required"`
	APIKeys []string `json:"api_keys" binding:"required,min=1"`
	// Models 可空：创建时可不配模型（渠道不会被调度命中），建后在「模型」弹窗维护。
	Models         []string          `json:"models"`
	ModelMapping   map[string]string `json:"model_mapping"`
	ParamOverride  map[string]any    `json:"param_override"`
	HeaderOverride map[string]string `json:"header_override"`
	Status         *string           `json:"status" binding:"omitempty,oneof=enabled disabled_manual"`
	Priority       *int              `json:"priority" binding:"omitempty,min=0,max=999"`
	Weight         *int              `json:"weight" binding:"omitempty,min=0"`
	MaxConcurrency *int              `json:"max_concurrency" binding:"omitempty,min=0"`
	MaxRPM         *int              `json:"max_rpm" binding:"omitempty,min=0"`
	CostRatio      *float64          `json:"cost_ratio" binding:"omitempty,gte=0"`
	Tags           []string          `json:"tags"`
	TestModel      string            `json:"test_model"`
	GroupIDs       []int             `json:"group_ids"`
}

// UpdateChannelReq 更新渠道请求（partial）：
//   - 指针字段缺省 = 不改；
//   - api_keys/models 提供非空数组 = 整组替换，留空 = 不改；
//   - model_mapping/param_override/header_override/tags/group_ids
//     提供（含空集合）= 整组替换。
type UpdateChannelReq struct {
	Name           *string           `json:"name"`
	Type           *string           `json:"type" binding:"omitempty,oneof=openai_compatible anthropic gemini custom"`
	BaseURL        *string           `json:"base_url"`
	APIKeys        []string          `json:"api_keys"`
	Models         []string          `json:"models"`
	ModelMapping   map[string]string `json:"model_mapping"`
	ParamOverride  map[string]any    `json:"param_override"`
	HeaderOverride map[string]string `json:"header_override"`
	Status         *string           `json:"status" binding:"omitempty,oneof=enabled disabled_manual"`
	Priority       *int              `json:"priority" binding:"omitempty,min=0,max=999"`
	Weight         *int              `json:"weight" binding:"omitempty,min=0"`
	MaxConcurrency *int              `json:"max_concurrency" binding:"omitempty,min=0"`
	MaxRPM         *int              `json:"max_rpm" binding:"omitempty,min=0"`
	CostRatio      *float64          `json:"cost_ratio" binding:"omitempty,gte=0"`
	Tags           []string          `json:"tags"`
	TestModel      *string           `json:"test_model"`
	GroupIDs       []int             `json:"group_ids"`
}

// TestChannelReq 渠道测试请求（model 缺省时取渠道 test_model 或首个模型）。
// Endpoint 仅对 openai 协议渠道生效：chat_completions（缺省）/ responses。
type TestChannelReq struct {
	Model    string `json:"model"`
	Endpoint string `json:"endpoint" binding:"omitempty,oneof=chat_completions responses"`
}

// TestChannelResp 渠道测试响应。
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

// FetchChannelModelsPreviewReq 预览拉取模型请求（渠道未保存，直接给连接参数）。
type FetchChannelModelsPreviewReq struct {
	Type    string `json:"type" binding:"required,oneof=openai_compatible anthropic gemini custom"`
	BaseURL string `json:"base_url" binding:"required"`
	APIKey  string `json:"api_key" binding:"required"`
}

// BulkUpdateChannelsReq 批量操作请求。
type BulkUpdateChannelsReq struct {
	IDs      []int  `json:"ids" binding:"required,min=1"`
	Action   string `json:"action" binding:"required,oneof=enable disable delete set_priority"`
	Priority *int   `json:"priority" binding:"omitempty,min=0,max=999"`
}

// BulkUpdateChannelsResp 批量操作响应。
type BulkUpdateChannelsResp struct {
	Affected int `json:"affected"`
}
