package dto

import "encoding/json"

// UpstreamLogResp 失败请求留痕响应（仅管理员）。
type UpstreamLogResp struct {
	ID           int64           `json:"id"`
	RequestID    string          `json:"request_id"`
	Source       string          `json:"source"` // 发起方：relay 用户转发 / channel_test 渠道测试
	Phase        string          `json:"phase,omitempty"`
	StatusCode   int             `json:"status_code"`
	ErrorType    string          `json:"error_type,omitempty"`
	ErrorCode    string          `json:"error_code,omitempty"`
	Message      string          `json:"message"`
	Attempts     int             `json:"attempts"`
	AttemptChain json.RawMessage `json:"attempt_chain,omitempty"` // errlog.AttemptHop 数组
	Billed       bool            `json:"billed"`
	Model        string          `json:"model,omitempty"`
	Endpoint     string          `json:"endpoint,omitempty"`
	Stream       bool            `json:"stream"`
	UserID       int             `json:"user_id,omitempty"`
	UserEmail    string          `json:"user_email,omitempty"`
	APIKeyID     int             `json:"api_key_id,omitempty"`
	GroupID      int             `json:"group_id,omitempty"`
	ChannelID    int             `json:"channel_id,omitempty"`
	ChannelName  string          `json:"channel_name,omitempty"`
	AccountID    int             `json:"account_id,omitempty"`
	AccountName  string          `json:"account_name,omitempty"`
	IPAddress    string          `json:"ip_address,omitempty"`
	UserAgent    string          `json:"user_agent,omitempty"`
	DurationMs   int64           `json:"duration_ms"`
	RepeatCount  int             `json:"repeat_count"`
	CreatedAt    string          `json:"created_at"`
}

// UpstreamLogQuery 失败请求留痕查询参数。
type UpstreamLogQuery struct {
	PageReq
	Source    string `form:"source" binding:"omitempty,oneof=relay channel_test"`
	Phase     string `form:"phase"`
	UserID    *int64 `form:"user_id"`
	APIKeyID  *int64 `form:"api_key_id"`
	ChannelID *int64 `form:"channel_id"`
	RequestID string `form:"request_id"`
	Model     string `form:"model"`
	StartDate string `form:"start_date"`
	EndDate   string `form:"end_date"`
}

// UserUpstreamLogResp 用户视角失败请求响应。
// 剥离渠道名/重试链/IP/UA 等运营信息（渠道拓扑属平台内部，不向用户暴露）；
// message 本就是请求当时返回给调用方的错误文案，可以透出。
type UserUpstreamLogResp struct {
	ID          int64  `json:"id"`
	RequestID   string `json:"request_id"`
	Phase       string `json:"phase,omitempty"`
	StatusCode  int    `json:"status_code"`
	ErrorType   string `json:"error_type,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
	Message     string `json:"message"`
	Attempts    int    `json:"attempts"`
	Billed      bool   `json:"billed"`
	Model       string `json:"model,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	Stream      bool   `json:"stream"`
	APIKeyID    int    `json:"api_key_id,omitempty"`
	DurationMs  int64  `json:"duration_ms"`
	RepeatCount int    `json:"repeat_count"`
	CreatedAt   string `json:"created_at"`
}

// ChannelFailureStatsQuery 渠道失败计数查询（ids 为逗号分隔渠道 ID）。
type ChannelFailureStatsQuery struct {
	IDs     string `form:"ids" binding:"required"`
	Minutes int    `form:"minutes"`
}

// ChannelFailureStatsResp 渠道失败计数响应（Redis 分钟桶汇总）。
type ChannelFailureStatsResp struct {
	Minutes  int                    `json:"minutes"`
	Channels []ChannelFailureCounts `json:"channels"`
}

// ChannelFailureCounts 单渠道失败计数。
type ChannelFailureCounts struct {
	ChannelID int              `json:"channel_id"`
	Total     int64            `json:"total"`
	ByVerdict map[string]int64 `json:"by_verdict"`
}
