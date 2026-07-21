package dto

import (
	appriskcontrol "github.com/DouDOU-start/airgate-core/internal/app/riskcontrol"
	"github.com/DouDOU-start/airgate-core/internal/moderation"
)

// RiskControlConfigResp 风控配置回显（审核 key 只出掩码与健康状态）。
type RiskControlConfigResp struct {
	RiskControlEnabled   bool                   `json:"risk_control_enabled"`
	Enabled              bool                   `json:"enabled"`
	Mode                 string                 `json:"mode"`
	BaseURL              string                 `json:"base_url"`
	Model                string                 `json:"model"`
	APIKeyCount          int                    `json:"api_key_count"`
	APIKeyMasks          []string               `json:"api_key_masks"`
	APIKeyStatuses       []moderation.KeyStatus `json:"api_key_statuses"`
	TimeoutMS            int                    `json:"timeout_ms"`
	SampleRate           int                    `json:"sample_rate"`
	AllGroups            bool                   `json:"all_groups"`
	GroupIDs             []int                  `json:"group_ids"`
	RecordNonHits        bool                   `json:"record_non_hits"`
	Thresholds           map[string]float64     `json:"thresholds"`
	Categories           []string               `json:"categories"`
	WorkerCount          int                    `json:"worker_count"`
	QueueSize            int                    `json:"queue_size"`
	BlockStatus          int                    `json:"block_status"`
	BlockMessage         string                 `json:"block_message"`
	EmailOnHit           bool                   `json:"email_on_hit"`
	AutoBanEnabled       bool                   `json:"auto_ban_enabled"`
	BanThreshold         int                    `json:"ban_threshold"`
	ViolationWindowHours int                    `json:"violation_window_hours"`
	RetryCount           int                    `json:"retry_count"`
	HitRetentionDays     int                    `json:"hit_retention_days"`
	NonHitRetentionDays  int                    `json:"non_hit_retention_days"`
	PreHashCheckEnabled  bool                   `json:"pre_hash_check_enabled"`
	BlockedKeywords      []string               `json:"blocked_keywords"`
	KeywordBlockingMode  string                 `json:"keyword_blocking_mode"`
	ModelFilter          moderation.ModelFilter `json:"model_filter"`
}

// UpdateRiskControlConfigReq 配置增量更新请求（缺省字段不改动）。
type UpdateRiskControlConfigReq struct {
	RiskControlEnabled   *bool                   `json:"risk_control_enabled"`
	Enabled              *bool                   `json:"enabled"`
	Mode                 *string                 `json:"mode"`
	BaseURL              *string                 `json:"base_url"`
	Model                *string                 `json:"model"`
	APIKeys              *[]string               `json:"api_keys"`
	APIKeysMode          string                  `json:"api_keys_mode"`
	DeleteAPIKeyHashes   []string                `json:"delete_api_key_hashes"`
	ClearAPIKeys         bool                    `json:"clear_api_keys"`
	TimeoutMS            *int                    `json:"timeout_ms"`
	SampleRate           *int                    `json:"sample_rate"`
	AllGroups            *bool                   `json:"all_groups"`
	GroupIDs             *[]int                  `json:"group_ids"`
	RecordNonHits        *bool                   `json:"record_non_hits"`
	Thresholds           *map[string]float64     `json:"thresholds"`
	WorkerCount          *int                    `json:"worker_count"`
	QueueSize            *int                    `json:"queue_size"`
	BlockStatus          *int                    `json:"block_status"`
	BlockMessage         *string                 `json:"block_message"`
	EmailOnHit           *bool                   `json:"email_on_hit"`
	AutoBanEnabled       *bool                   `json:"auto_ban_enabled"`
	BanThreshold         *int                    `json:"ban_threshold"`
	ViolationWindowHours *int                    `json:"violation_window_hours"`
	RetryCount           *int                    `json:"retry_count"`
	HitRetentionDays     *int                    `json:"hit_retention_days"`
	NonHitRetentionDays  *int                    `json:"non_hit_retention_days"`
	PreHashCheckEnabled  *bool                   `json:"pre_hash_check_enabled"`
	BlockedKeywords      *[]string               `json:"blocked_keywords"`
	KeywordBlockingMode  *string                 `json:"keyword_blocking_mode"`
	ModelFilter          *moderation.ModelFilter `json:"model_filter"`
}

// TestRiskControlKeysReq 审核 key 探活/试审请求。
type TestRiskControlKeysReq struct {
	APIKeys   []string `json:"api_keys"`
	BaseURL   string   `json:"base_url"`
	Model     string   `json:"model"`
	TimeoutMS int      `json:"timeout_ms"`
	Prompt    string   `json:"prompt"`
	Images    []string `json:"images"`
}

// RiskControlLogsQuery 审核日志查询参数。
type RiskControlLogsQuery struct {
	PageReq
	Result   string `form:"result" binding:"omitempty,oneof=hit blocked pass error"`
	GroupID  string `form:"group_id"`
	Endpoint string `form:"endpoint"`
	Search   string `form:"search"`
	From     string `form:"from"`
	To       string `form:"to"`
}

// RiskControlLogResp 审核日志行（app 层 Record 自带序列化标签，直接复用）。
type RiskControlLogResp = appriskcontrol.Record

// RiskControlUnbanResp 解封结果。
type RiskControlUnbanResp struct {
	UserID int    `json:"user_id"`
	Status string `json:"status"`
}

// DeleteRiskControlHashReq 删除单条命中哈希请求。
type DeleteRiskControlHashReq struct {
	InputHash string `json:"input_hash" binding:"required"`
}

// DeleteRiskControlHashResp 删除结果。
type DeleteRiskControlHashResp struct {
	InputHash string `json:"input_hash"`
	Deleted   bool   `json:"deleted"`
}

// ClearRiskControlHashesResp 清空结果。
type ClearRiskControlHashesResp struct {
	Deleted int64 `json:"deleted"`
}
