package dto

import "time"

// InviteMeResp 用户端"我的邀请"聚合信息。
type InviteMeResp struct {
	Enabled              bool    `json:"enabled"`
	InviteCode           string  `json:"invite_code,omitempty"`
	InviterID            *int    `json:"inviter_id,omitempty"`
	EffectiveRatePercent float64 `json:"effective_rate_percent,omitempty"`
	InvitedCount         int     `json:"invited_count"`
	RebateBalance        float64 `json:"rebate_balance"`
	RebateTotal          float64 `json:"rebate_total"`
}

// InviteTransferResp 返利转入余额结果。
type InviteTransferResp struct {
	Transferred float64 `json:"transferred"`
	Balance     float64 `json:"balance"`
}

// InviteeResp 邀请关系条目。
type InviteeResp struct {
	InviterID       int       `json:"inviter_id"`
	InviterEmail    string    `json:"inviter_email,omitempty"`
	InviterUsername string    `json:"inviter_username,omitempty"`
	InviteeID       int       `json:"invitee_id"`
	InviteeEmail    string    `json:"invitee_email,omitempty"`
	InviteeUsername string    `json:"invitee_username,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	TotalRebate     float64   `json:"total_rebate"`
}

// InviteRebateLogResp 返利流水条目。
type InviteRebateLogResp struct {
	ID              int       `json:"id"`
	UserID          int       `json:"user_id"`
	UserEmail       string    `json:"user_email,omitempty"`
	Action          string    `json:"action"`
	Amount          float64   `json:"amount"`
	SourceUserID    *int      `json:"source_user_id,omitempty"`
	SourceUserEmail string    `json:"source_user_email,omitempty"`
	SourceOrderNo   *string   `json:"source_order_no,omitempty"`
	BalanceAfter    *float64  `json:"balance_after,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// SetInviteRateOverrideReq 管理端设置/清除用户专属返利比例。
// RatePercent 为 nil 表示清除（回退全局比例）。
type SetInviteRateOverrideReq struct {
	RatePercent *float64 `json:"rate_percent"`
}

// InviteOverrideEntryResp 专属返利比例覆盖列表条目。
type InviteOverrideEntryResp struct {
	UserID       int      `json:"user_id"`
	Email        string   `json:"email,omitempty"`
	Username     string   `json:"username,omitempty"`
	InviteCode   string   `json:"invite_code"`
	RatePercent  *float64 `json:"rate_percent,omitempty"`
	InvitedCount int      `json:"invited_count"`
}
