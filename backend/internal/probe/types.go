// Package probe 提供渠道密钥端点的主动健康探针与定时余额同步。
//
// 依赖约束：本包禁止 import ent 或 internal/app/channel（防环）；
// 数据读写均经窄接口由外部注入。
package probe

import (
	"context"
	"time"
)

// HealthStatus 密钥端点健康状态。
type HealthStatus string

const (
	HealthHealthy    HealthStatus = "healthy"
	HealthDegraded   HealthStatus = "degraded"
	HealthSuspended  HealthStatus = "suspended"
	HealthRecovering HealthStatus = "recovering"
)

// 分级阈值默认值。
const (
	DefaultDegradeThreshold = 3 // 连续失败 >= 此值 → degraded
	DefaultSuspendThreshold = 6 // 连续失败 >= 此值 → suspended
	DefaultRecoverThreshold = 3 // 恢复中连续成功 >= 此值 → healthy

	DefaultProbeInterval       = 60 * time.Second  // 探针调度轮询间隔
	DefaultBalanceSyncInterval = 5 * time.Minute    // 余额同步轮询间隔
	DefaultBalanceStaleDur     = 10 * time.Minute   // 余额超过此时长视为过期
	DefaultProbeKeyInterval    = 60 * time.Second   // 同一 key 两次探测的最小间隔
	DefaultBillingProbeInterval = 30 * time.Minute  // 上游倍率探测间隔
)

// KeyHealthState 单把 key 的健康状态快照（探针调度用）。
type KeyHealthState struct {
	KeyID               int
	HealthStatus        HealthStatus
	ConsecutiveFailures int
	ConsecutiveSuccesses int
	LastProbeAt         *time.Time
}

// Tester 密钥端点连通性测试（由 channel service 适配）。
type Tester interface {
	TestKey(ctx context.Context, keyID int) error
}

// Store 健康状态持久化（由 channel store 适配）。
type Store interface {
	ListProbeTargets(ctx context.Context) ([]KeyHealthState, error)
	UpdateHealthState(ctx context.Context, keyID int, health string, failures, successes int) error
	UpdateProbeTime(ctx context.Context, keyID int, at time.Time) error
}

// BalanceSyncer 单把 key 的余额刷新（由 channel service 适配）。
type BalanceSyncer interface {
	SyncBalance(ctx context.Context, keyID int) error
}

// BalanceStore 余额同步目标查询（由 channel store 适配）。
type BalanceStore interface {
	ListBalanceSyncTargets(ctx context.Context, staleBefore time.Time) ([]int, error)
}

// RegistryMutator 注册表内存状态更新（由 registry 适配）。
type RegistryMutator interface {
	MarkAutoDisabled(keyID int, reason string)
	MarkRecovered(keyID int)
	UpdateHealth(keyID int, health string)
}

// BillingProbeResult 上游倍率探测结果。
type BillingProbeResult struct {
	RateMultiplier float64
}

// BillingProbeTarget 上游倍率探测目标（含解密后的 API Key 与 base_url）。
type BillingProbeTarget struct {
	KeyID   int
	BaseURL string
	APIKey  string
}

// BillingProber 上游倍率探测器（由 channel service 适配）。
type BillingProber interface {
	ProbeBilling(ctx context.Context, keyID int) (*BillingProbeResult, error)
}

// BillingProbeStore 上游倍率探测目标查询与结果持久化。
type BillingProbeStore interface {
	ListBillingProbeTargets(ctx context.Context) ([]BillingProbeTarget, error)
	UpdateUpstreamRate(ctx context.Context, keyID int, rate float64, at time.Time) error
}
