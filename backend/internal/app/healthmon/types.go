// Package healthmon 提供基于真实流量的渠道健康监测（观测面）。
//
// 口径（Phase 0 锁定）：
//   - 仅 source=relay；排除 channel_test / account_test / task
//   - 429 计入 error_rate；client 4xx / canceled / precheck（余额不足、缺价等）不计入
//   - usage_logs 中的 billed 失败先从成功数扣除；SLA 失败再计入错误数
//   - idle（N==0）健康分为 nil，UI 显示「空闲」
//   - 不写调度状态；与 probe/outcome 控制面分离
package healthmon

import "time"

// 默认最小样本：低于此值标 low_sample，可算分但不告警。
const DefaultMinSample = 10

// Window 查询时间窗。
type Window string

const (
	Window5m  Window = "5m"
	Window1h  Window = "1h"
	Window6h  Window = "6h"
	Window24h Window = "24h"
)

// ParseWindow 解析时间窗；未知值回退 1h。
func ParseWindow(raw string) (Window, time.Duration) {
	switch Window(raw) {
	case Window5m:
		return Window5m, 5 * time.Minute
	case Window6h:
		return Window6h, 6 * time.Hour
	case Window24h:
		return Window24h, 24 * time.Hour
	default:
		return Window1h, time.Hour
	}
}

// ErrorClass 失败分类（展示 + SLA）。
type ErrorClass string

const (
	ClassAuth        ErrorClass = "auth"
	ClassRateLimit   ErrorClass = "rate_limit"
	ClassUpstream5xx ErrorClass = "upstream_5xx"
	ClassClient      ErrorClass = "client"
	ClassCanceled    ErrorClass = "canceled"
	ClassPrecheck    ErrorClass = "precheck"
	ClassOther       ErrorClass = "other"
)

// Sample 窗口样本摘要。
type Sample struct {
	N         int64 `json:"n"`
	S         int64 `json:"s"`
	E         int64 `json:"e"`
	Idle      bool  `json:"idle"`
	LowSample bool  `json:"low_sample"`
}

// Counts 错误分类计数（含不进 SLA 的 client 等）。
type Counts struct {
	Auth        int64 `json:"auth"`
	RateLimit   int64 `json:"rate_limit"`
	Upstream5xx int64 `json:"upstream_5xx"`
	Client      int64 `json:"client"`
	Canceled    int64 `json:"canceled"`
	Precheck    int64 `json:"precheck"`
	Other       int64 `json:"other"`
}

// Latency 延迟摘要（毫秒；仅成功样本）。
type Latency struct {
	AvgMs int64 `json:"avg_ms"`
	MaxMs int64 `json:"max_ms"`
}

// Availability 可调度摘要（运行态，非流量）。
type Availability struct {
	ChannelKeysAvailable int64 `json:"channel_keys_available"`
	ChannelKeysTotal     int64 `json:"channel_keys_total"`
}

// Overview 全局健康总览。
type Overview struct {
	Window       Window       `json:"window"`
	Sample       Sample       `json:"sample"`
	SuccessRate  float64      `json:"success_rate"`
	ErrorRate    float64      `json:"error_rate"`
	Counts       Counts       `json:"counts"`
	Latency      Latency      `json:"latency"`
	TTFT         Latency      `json:"ttft"`
	HealthScore  *int         `json:"health_score"` // idle 时 nil
	Availability Availability `json:"availability"`
}

// UserOverview 当前用户可见分组的健康总览。
// 它刻意不包含错误分类和渠道可调度数等内部运维信息。
type UserOverview struct {
	Window      Window
	Sample      Sample
	SuccessRate float64
	ErrorRate   float64
	Latency     Latency
	TTFT        Latency
	HealthScore *int
	UpdatedAt   time.Time
}

// UserHealthStatus 用户状态页使用的稳定状态枚举。
type UserHealthStatus string

const (
	UserHealthIdle      UserHealthStatus = "idle"
	UserHealthLowSample UserHealthStatus = "low_sample"
	UserHealthHealthy   UserHealthStatus = "healthy"
	UserHealthDegraded  UserHealthStatus = "degraded"
	UserHealthUnhealthy UserHealthStatus = "unhealthy"
)

// UserGroupStatus 当前用户可见分组的脱敏健康快照。
type UserGroupStatus struct {
	ID          int
	Name        string
	Platform    string
	Status      UserHealthStatus
	Window      Window
	Sample      Sample
	SuccessRate float64
	ErrorRate   float64
	Latency     Latency
	TTFT        Latency
	HealthScore *int
}

// EntityKind 实体类型。
type EntityKind string

const (
	EntityChannelKey EntityKind = "channel_key"
	EntityGroup      EntityKind = "group"
)

// EntityRow 实体健康行（channel_key / group）。
type EntityRow struct {
	ID          int        `json:"id"`
	Kind        EntityKind `json:"kind"`
	Name        string     `json:"name"`
	ChannelID   int        `json:"channel_id,omitempty"`
	ChannelName string     `json:"channel_name,omitempty"`
	// Platform 分组平台标识（仅 group 行）。
	Platform     string     `json:"platform,omitempty"`
	Type         string     `json:"type,omitempty"`
	SchedStatus  string     `json:"sched_status,omitempty"`
	HealthStatus string     `json:"health_status,omitempty"`
	Window       Window     `json:"window"`
	Sample       Sample     `json:"sample"`
	SuccessRate  float64    `json:"success_rate"`
	ErrorRate    float64    `json:"error_rate"`
	Counts       Counts     `json:"counts"`
	Latency      Latency    `json:"latency"`
	TTFT         Latency    `json:"ttft"`
	HealthScore  *int       `json:"health_score"`
	LastError    *LastError `json:"last_error,omitempty"`
}

// LastError 最近一次失败摘要。
type LastError struct {
	At      time.Time `json:"at"`
	Phase   string    `json:"phase"`
	Message string    `json:"message"`
}

// ListFilter 实体列表筛选。
type ListFilter struct {
	Window        Window
	OnlyUnhealthy bool
	// IDs 非空时仅返回这些实体 id（channel_key 或 group，取决于 scope）。
	IDs []int
}

// SuccessAgg 成功侧聚合行（store 输出；按 channel_key 或 group）。
type SuccessAgg struct {
	// DimID 维度主键：channel_key_id 或 group_id。
	DimID       int
	Count       int64
	AvgDuration float64
	MaxDuration float64
	AvgTTFT     float64
	MaxTTFT     float64
}

// FailureAgg 失败侧聚合行（store 输出）。
type FailureAgg struct {
	DimID int
	Class ErrorClass
	Count int64 // SUM(repeat_count)
}

// KeyMeta 密钥端点元数据。
type KeyMeta struct {
	ID           int
	Name         string
	ChannelID    int
	ChannelName  string
	Type         string
	Status       string
	HealthStatus string
}

// GroupMeta 分组元数据。
type GroupMeta struct {
	ID       int
	Name     string
	Platform string
}

// LastErrorRow 最近失败（store 输出）。
type LastErrorRow struct {
	// DimID channel_key_id 或 group_id。
	DimID   int
	At      time.Time
	Phase   string
	Message string
}
