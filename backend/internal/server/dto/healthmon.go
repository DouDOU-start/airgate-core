package dto

// HealthmonOverviewQuery 健康监测总览查询。
type HealthmonOverviewQuery struct {
	Window string `form:"window"` // 5m|1h|6h|24h，默认 1h
}

// HealthmonEntitiesQuery 健康监测实体列表查询。
type HealthmonEntitiesQuery struct {
	Window        string `form:"window"`
	Scope         string `form:"scope"` // channel_key | group
	OnlyUnhealthy bool   `form:"only_unhealthy"`
	// IDs 逗号分隔的实体 id（channel_key 或 group），用于批量嵌入。
	IDs string `form:"ids"`
}

// HealthmonSample 窗口样本。
type HealthmonSample struct {
	N         int64 `json:"n"`
	S         int64 `json:"s"`
	E         int64 `json:"e"`
	Idle      bool  `json:"idle"`
	LowSample bool  `json:"low_sample"`
}

// HealthmonCounts 错误分类计数。
type HealthmonCounts struct {
	Auth        int64 `json:"auth"`
	RateLimit   int64 `json:"rate_limit"`
	Upstream5xx int64 `json:"upstream_5xx"`
	Client      int64 `json:"client"`
	Canceled    int64 `json:"canceled"`
	Precheck    int64 `json:"precheck"`
	Other       int64 `json:"other"`
}

// HealthmonLatency 延迟摘要（ms）。
type HealthmonLatency struct {
	AvgMs int64 `json:"avg_ms"`
	MaxMs int64 `json:"max_ms"`
}

// HealthmonAvailability 可调度摘要。
type HealthmonAvailability struct {
	ChannelKeysAvailable int64 `json:"channel_keys_available"`
	ChannelKeysTotal     int64 `json:"channel_keys_total"`
}

// HealthmonOverviewResp 总览响应。
type HealthmonOverviewResp struct {
	Window       string                `json:"window"`
	Sample       HealthmonSample       `json:"sample"`
	SuccessRate  float64               `json:"success_rate"`
	ErrorRate    float64               `json:"error_rate"`
	Counts       HealthmonCounts       `json:"counts"`
	Latency      HealthmonLatency      `json:"latency"`
	TTFT         HealthmonLatency      `json:"ttft"`
	HealthScore  *int                  `json:"health_score"`
	Availability HealthmonAvailability `json:"availability"`
}

// HealthmonLastError 最近失败。
type HealthmonLastError struct {
	At      string `json:"at"`
	Phase   string `json:"phase"`
	Message string `json:"message"`
}

// HealthmonEntityResp 实体健康行。
type HealthmonEntityResp struct {
	ID           int                 `json:"id"`
	Kind         string              `json:"kind"`
	Name         string              `json:"name"`
	ChannelID    int                 `json:"channel_id,omitempty"`
	ChannelName  string              `json:"channel_name,omitempty"`
	Platform     string              `json:"platform,omitempty"`
	Type         string              `json:"type,omitempty"`
	SchedStatus  string              `json:"sched_status,omitempty"`
	HealthStatus string              `json:"health_status,omitempty"`
	Window       string              `json:"window"`
	Sample       HealthmonSample     `json:"sample"`
	SuccessRate  float64             `json:"success_rate"`
	ErrorRate    float64             `json:"error_rate"`
	Counts       HealthmonCounts     `json:"counts"`
	Latency      HealthmonLatency    `json:"latency"`
	TTFT         HealthmonLatency    `json:"ttft"`
	HealthScore  *int                `json:"health_score"`
	LastError    *HealthmonLastError `json:"last_error,omitempty"`
}

// ChannelStatusLatency 用户状态页延迟摘要。当前数据层尚未计算真实分位数，
// 因此对外只返回平均值，不把 MAX 误标为 P95。
type ChannelStatusLatency struct {
	AvgMs int64 `json:"avg_ms"`
}

// ChannelStatusSample 仅公开样本状态，不公开平台精确请求量。
type ChannelStatusSample struct {
	Idle      bool `json:"idle"`
	LowSample bool `json:"low_sample"`
}

// ChannelStatusOverviewResp 当前用户可见分组的脱敏健康总览。
type ChannelStatusOverviewResp struct {
	Window      string               `json:"window"`
	Sample      ChannelStatusSample  `json:"sample"`
	SuccessRate float64              `json:"success_rate"`
	ErrorRate   float64              `json:"error_rate"`
	Latency     ChannelStatusLatency `json:"latency"`
	TTFT        ChannelStatusLatency `json:"ttft"`
	HealthScore *int                 `json:"health_score"`
	UpdatedAt   string               `json:"updated_at"`
}

// ChannelStatusGroupResp 当前用户可见分组的脱敏健康快照。
// 渠道/key 标识、调度状态、错误分类和错误原文仅保留在管理员接口。
type ChannelStatusGroupResp struct {
	ID          int                  `json:"id"`
	Name        string               `json:"name"`
	Platform    string               `json:"platform,omitempty"`
	Status      string               `json:"status"`
	Window      string               `json:"window"`
	Sample      ChannelStatusSample  `json:"sample"`
	SuccessRate float64              `json:"success_rate"`
	ErrorRate   float64              `json:"error_rate"`
	Latency     ChannelStatusLatency `json:"latency"`
	TTFT        ChannelStatusLatency `json:"ttft"`
	HealthScore *int                 `json:"health_score"`
}
