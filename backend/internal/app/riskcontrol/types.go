// Package riskcontrol 风控中心管理面服务：配置读写（含审核 key 加解密与掩码）、
// 审核日志查询、运行状态聚合、用户解封、命中哈希管理；同时作为 moderation.Engine
// 的 ConfigSource 与 UserBanner 实现。
package riskcontrol

import (
	"context"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/moderation"
)

// Record 审核日志展示记录（含关联用户当前状态，便于解封操作判断）。
type Record struct {
	ID                int64              `json:"id"`
	RequestID         string             `json:"request_id"`
	UserID            int                `json:"user_id"`
	UserEmail         string             `json:"user_email"`
	APIKeyID          int                `json:"api_key_id"`
	GroupID           int                `json:"group_id"`
	GroupName         string             `json:"group_name"`
	Endpoint          string             `json:"endpoint"`
	Protocol          string             `json:"protocol"`
	Model             string             `json:"model"`
	Mode              string             `json:"mode"`
	Action            string             `json:"action"`
	Flagged           bool               `json:"flagged"`
	HighestCategory   string             `json:"highest_category"`
	HighestScore      float64            `json:"highest_score"`
	MatchedKeyword    string             `json:"matched_keyword"`
	CategoryScores    map[string]float64 `json:"category_scores"`
	ThresholdSnapshot map[string]float64 `json:"threshold_snapshot"`
	InputExcerpt      string             `json:"input_excerpt"`
	InputHash         string             `json:"input_hash"`
	UpstreamLatencyMS int64              `json:"upstream_latency_ms"`
	QueueDelayMS      int64              `json:"queue_delay_ms"`
	Error             string             `json:"error"`
	ViolationCount    int                `json:"violation_count"`
	AutoBanned        bool               `json:"auto_banned"`
	EmailSent         bool               `json:"email_sent"`
	UserStatus        string             `json:"user_status"`
	CreatedAt         string             `json:"created_at"`
}

// 日志列表 result 过滤值。
const (
	ResultHit     = "hit"     // flagged=true
	ResultBlocked = "blocked" // action ∈ {block, keyword_block, hash_block}
	ResultPass    = "pass"    // flagged=false 且无错误
	ResultError   = "error"   // 审核出错
)

// ListFilter 审核日志查询条件。
type ListFilter struct {
	Page     int
	PageSize int
	Result   string
	GroupID  *int
	Endpoint string
	Search   string
	From     *time.Time
	To       *time.Time
}

// Repository 审核日志仓储（infra/store 实现；查询侧，写入侧见 moderation.LogStore）。
type Repository interface {
	List(ctx context.Context, filter ListFilter) ([]Record, int64, error)
}

// SettingsRepo 配置存取窄接口（store.SettingsStore 实现；直连 store 绕开
// app/settings 的敏感组屏蔽——本域即 risk_control 组的专用通道）。
type SettingsRepo interface {
	GroupValues(ctx context.Context, group string) (map[string]string, error)
	UpsertValue(ctx context.Context, group, key, value string) error
}

// UserRepo 封禁/解封所需的用户操作窄接口（store.UserStore 实现）。
type UserRepo interface {
	GetStatusAndRole(ctx context.Context, userID int) (status, role string, err error)
	UpdateStatus(ctx context.Context, userID int, status string) error
}

// ConfigView 配置回显视图：审核 key 只出掩码与健康状态，明文永不出网。
type ConfigView struct {
	RiskControlEnabled bool
	Config             moderation.Config // APIKeys 已清空
	APIKeyCount        int
	APIKeyMasks        []string
	APIKeyStatuses     []moderation.KeyStatus
}

// UpdateConfigInput 配置增量更新（nil 字段不改动）。
type UpdateConfigInput struct {
	RiskControlEnabled   *bool
	Enabled              *bool
	Mode                 *string
	BaseURL              *string
	Model                *string
	APIKeys              *[]string
	APIKeysMode          string // append（默认）/ replace
	DeleteAPIKeyHashes   []string
	ClearAPIKeys         bool
	TimeoutMS            *int
	SampleRate           *int
	AllGroups            *bool
	GroupIDs             *[]int
	RecordNonHits        *bool
	Thresholds           *map[string]float64
	WorkerCount          *int
	QueueSize            *int
	BlockStatus          *int
	BlockMessage         *string
	EmailOnHit           *bool
	AutoBanEnabled       *bool
	BanThreshold         *int
	ViolationWindowHours *int
	RetryCount           *int
	HitRetentionDays     *int
	NonHitRetentionDays  *int
	PreHashCheckEnabled  *bool
	BlockedKeywords      *[]string
	KeywordBlockingMode  *string
	ModelFilter          *moderation.ModelFilter
}

// UnbanResult 解封结果。
type UnbanResult struct {
	UserID int
	Status string
}
