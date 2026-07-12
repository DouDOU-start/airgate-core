// Package provider 抽象不同的支付服务商（虎皮椒/彩虹易支付/支付宝官方/微信官方等）。
//
// 设计要点：
//   - Provider 与 PayMethod 是两件事：
//   - Provider 是"一套对接协议 + 一份凭证"（虎皮椒、彩虹、支付宝官方 ...）
//   - PayMethod 是用户最终用什么方式付款（支付宝/微信/QQ/...）
//   - 一个 Provider 可承载多个 PayMethod（虎皮椒同时支持 alipay/wxpay/qqpay）
//   - 一个 PayMethod 可由多个 Provider 提供（用户选 alipay 时可走聚合或官方）
//   - service 层不直接持有 Provider，而是通过 Router 按 method 选 Provider。
//
// 这种分层让前端永远只看到"支付宝/微信/银行卡"等对用户友好的支付方式按钮，
// 而后台决定每种方式背后实际走哪家服务商。新增/替换服务商不影响前端 UI。
package provider

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// Provider 通用支付服务商接口。
//
// 实现注意：
//  1. 所有方法需线程安全（可能被多个 goroutine 并发调用）
//  2. ID() 在整个进程内必须唯一，作为回调路径的一部分
//  3. SupportedMethods() 在 Enabled() == false 时也应正确返回，UI 据此计算可选方法
type Provider interface {
	// ID 进程内唯一标识，例如 "xunhu" / "caihong" / "alipay_official" / "wxpay_v3"
	// 用于回调路由：/api/v1/payment/notify/{provider_id}
	ID() string

	// Name 用于在 admin 配置页和订单表中展示的人类可读名称
	Name() string

	// Kind 协议家族常量（当前内置 KindEpayEasyPay），决定后端按何种协议处理
	Kind() string

	// SupportedMethods 此 Provider 能服务的 PayMethod 列表
	// 必须返回至少一个 method；返回的顺序也是 UI 展示顺序的参考
	SupportedMethods() []string

	// Enabled 当前是否启用 + 配置完整可用
	// 配置不完整（例如缺 appid）也应当返回 false，避免在 Router 选中后才报错
	Enabled() bool

	// CreateOrder 调用渠道下单接口
	// in.Method 是用户选的 PayMethod，必须在 SupportedMethods() 内
	// Provider 内部根据 method 映射到自家协议的字段（例如虎皮椒映射到 type 字段）
	CreateOrder(ctx context.Context, in CreateOrderInput) (*CreateOrderResult, error)

	// VerifyCallback 验证并解析支付平台异步通知
	// req 同时包含 form / body / headers，因为不同协议从不同位置取签名
	VerifyCallback(ctx context.Context, req CallbackRequest) (*CallbackResult, error)
}

// CreateOrderInput service 层下单时传给 Provider 的入参。
type CreateOrderInput struct {
	OutTradeNo    string  // 商户订单号（本插件生成）
	Amount        float64 // 金额（元）
	Subject       string  // 订单标题
	Method        string  // 用户选的 PayMethod，必须在 Provider.SupportedMethods() 内
	NotifyURL     string  // 异步通知地址（service 层已拼好，含 provider_id）
	ReturnURL     string  // 同步跳回地址
	ClientIP      string  // 用户 IP
	ExpireSeconds int     // 订单过期秒数，0 表示不传（由渠道决定默认值）
}

// CreateOrderResult Provider 下单后的返回结果。
type CreateOrderResult struct {
	// PaymentURL 跳转付款链接（PC 网页支付，可在新窗口打开 / 渲染成二维码）
	PaymentURL string

	// QRCodeContent 二维码原始内容
	// 微信 Native 返回的是 weixin:// 协议的字符串，必须由前端渲染成图片
	// 部分易支付渠道也会直接返回二维码内容
	// 当此字段非空时，前端优先用它生成二维码，而非 PaymentURL
	QRCodeContent string

	// ClientPayload 给前端额外的字段，例如 Stripe 的 client_secret、银联的 tn 字段等
	// 当前所有渠道都不需要，预留扩展位
	ClientPayload map[string]string

	// Raw 渠道返回的原始字段，service 层可选择性写入 payment_orders.notify_payload
	Raw map[string]string
}

// CallbackRequest core 异步回调代理过来的原始请求。
type CallbackRequest struct {
	Form    url.Values  // 解析后的表单字段（form-urlencoded）
	Body    []byte      // 原始请求体（用于 V3 接口验签）
	Headers http.Header // 请求头（微信 V3 / Stripe 的签名在 header 里）
}

// CallbackResult Provider 验签后归一化的回调结果。
type CallbackResult struct {
	OutTradeNo string            // 商户订单号
	Status     string            // 归一化状态：paid / pending / failed
	ChannelTxn string            // 渠道流水号（用于审计 + 退款关联）
	Amount     float64           // 渠道告知的实际付款金额（用于二次校验）
	Raw        map[string]string // 原始字段
	// Reply 给支付平台的同步响应内容
	// 不同平台要求不同：易支付要求 "success"，微信 V3 要求 JSON，支付宝要求 "success"
	Reply     string
	ReplyType string // text / xml / json
}

// 协议家族常量（Kind() 返回值）
//
// 目前仅内置 EasyPay 一种实现。其余服务商（虎皮椒/彩虹易支付/支付宝官方/
// 微信官方等）的抽象骨架保留，后续新增实现时在此追加对应 Kind 常量并在实现文件
// init() 里注册即可，无需改动 Registry / Factory / 前端。
const (
	KindEpayEasyPay = "epay_easypay"
)

// 错误常量
var (
	// ErrProviderDisabled Provider 被禁用或配置不完整
	ErrProviderDisabled = errors.New("payment provider disabled")

	// ErrInvalidSignature 回调验签失败
	ErrInvalidSignature = errors.New("invalid signature")

	// ErrUnsupportedMethod 用户传的 PayMethod 不在 SupportedMethods 内
	ErrUnsupportedMethod = errors.New("unsupported pay method for this provider")

	// ErrNoProviderAvailable Router 找不到任何可用 Provider 来处理这个 method
	ErrNoProviderAvailable = errors.New("no available payment provider")
)
