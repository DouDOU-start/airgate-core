package dto

import (
	"time"

	"github.com/DouDOU-start/airgate-core/internal/app/payment/provider"
)

// PaymentMethodsResp 用户可用支付方式。
type PaymentMethodsResp struct {
	Methods    []provider.MethodInfo `json:"methods"`
	Configured bool                  `json:"configured"`
}

// CreatePaymentOrderReq 用户下单请求。
type CreatePaymentOrderReq struct {
	Amount  float64 `json:"amount" binding:"required,gt=0"`
	Method  string  `json:"method" binding:"required"`
	Subject string  `json:"subject"`
}

// PaymentOrderResp 充值订单响应。
type PaymentOrderResp struct {
	OutTradeNo    string     `json:"out_trade_no"`
	UserID        int        `json:"user_id"`
	UserEmail     string     `json:"user_email,omitempty"` // 仅管理端列表
	Method        string     `json:"method"`
	ProviderID    string     `json:"provider_id"`
	Amount        float64    `json:"amount"`
	Status        string     `json:"status"` // pending / paid / expired
	Subject       string     `json:"subject"`
	PaymentURL    string     `json:"payment_url,omitempty"`
	QRCodeContent string     `json:"qr_code_content,omitempty"`
	PaidAt        *time.Time `json:"paid_at,omitempty"`
	ExpiresAt     time.Time  `json:"expires_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// PaymentOrderListResp 用户充值记录。
type PaymentOrderListResp struct {
	List []PaymentOrderResp `json:"list"`
}

// PaymentOrderStatsResp 管理端订单统计。
type PaymentOrderStatsResp struct {
	Total       int64   `json:"total"`
	Paid        int64   `json:"paid"`
	Pending     int64   `json:"pending"`
	Expired     int64   `json:"expired"`
	TotalAmount float64 `json:"total_amount"`
	TodayAmount float64 `json:"today_amount"`
}

// AdminPaymentOrdersResp 管理端订单列表响应。
type AdminPaymentOrdersResp struct {
	List  []PaymentOrderResp    `json:"list"`
	Total int64                 `json:"total"`
	Stats PaymentOrderStatsResp `json:"stats"`
}

// PaymentProviderResp 服务商实例（config 中敏感字段已掩码为空串，
// sensitive_keys 标记「已配置、留空保持不变」的字段）。
type PaymentProviderResp struct {
	ID               string            `json:"id"`
	Kind             string            `json:"kind"`
	Name             string            `json:"name"`
	Enabled          bool              `json:"enabled"`
	Config           map[string]string `json:"config"`
	SupportedMethods []string          `json:"supported_methods"`
	IsRunning        bool              `json:"is_running"`
	SensitiveKeys    []string          `json:"sensitive_keys"`
}

// PaymentProvidersResp 服务商列表 + 可配置协议类型元信息（驱动前端动态表单）。
type PaymentProvidersResp struct {
	Providers []PaymentProviderResp `json:"providers"`
	Kinds     []provider.KindMeta   `json:"kinds"`
}

// UpsertPaymentProviderReq 新增/编辑服务商实例。
type UpsertPaymentProviderReq struct {
	ID         string            `json:"id"`
	OriginalID string            `json:"original_id"`
	Kind       string            `json:"kind" binding:"required"`
	Enabled    bool              `json:"enabled"`
	Config     map[string]string `json:"config" binding:"required"`
}

// UpsertPaymentProviderResp 保存结果（返回最终实例 ID，自动生成/重命名后可能变化）。
type UpsertPaymentProviderResp struct {
	ID string `json:"id"`
}
