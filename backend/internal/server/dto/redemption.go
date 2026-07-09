package dto

import "time"

// GenerateRedemptionCodesReq 批量生成兑换码请求。
type GenerateRedemptionCodesReq struct {
	Count     int        `json:"count" binding:"required,min=1,max=500"`
	Value     float64    `json:"value" binding:"required,gt=0"`
	Remark    string     `json:"remark" binding:"max=200"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// RedemptionCodeResp 兑换码（仅管理端可见；status 已现算 expired 虚拟状态）。
type RedemptionCodeResp struct {
	ID          int        `json:"id"`
	Code        string     `json:"code"`
	Value       float64    `json:"value"`
	Status      string     `json:"status"`
	Remark      string     `json:"remark,omitempty"`
	UsedByID    int        `json:"used_by_id,omitempty"`
	UsedByEmail string     `json:"used_by_email,omitempty"`
	UsedAt      *time.Time `json:"used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	TimeMixin
}

// RedemptionStatsResp 兑换码统计。
type RedemptionStatsResp struct {
	Total       int64   `json:"total"`
	Unused      int64   `json:"unused"`
	Used        int64   `json:"used"`
	Disabled    int64   `json:"disabled"`
	Expired     int64   `json:"expired"`
	UsedValue   float64 `json:"used_value"`
	UnusedValue float64 `json:"unused_value"`
}

// UpdateRedemptionCodeStatusReq 停用/恢复兑换码请求。
type UpdateRedemptionCodeStatusReq struct {
	Disabled *bool `json:"disabled" binding:"required"`
}

// RedeemReq 用户兑换请求。
type RedeemReq struct {
	Code string `json:"code" binding:"required,max=64"`
}

// RedeemResp 用户兑换结果。
type RedeemResp struct {
	Value   float64 `json:"value"`
	Balance float64 `json:"balance"`
}
