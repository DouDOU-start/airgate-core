package provider

import (
	"bytes"
	"context"
	"crypto/rsa"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/downloader"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"
)

// 微信支付官方 V3 渠道实现，使用官方 SDK github.com/wechatpay-apiv3/wechatpay-go。
//
// 这是 4 个 Provider 里最复杂的一个：
//   - 凭证多：商户号 / AppID / 商户证书序列号 / APIv3 密钥 / 商户 RSA 私钥
//   - 平台证书需要自动下载并定期刷新（SDK 内置 downloader 处理）
//   - 回调采用 AES-256-GCM 加密 + RSA-SHA256 签名（SDK NotifyHandler 处理）
//
// 我们选 Native 支付（扫码）：返回 code_url 让前端渲染二维码。
// 与本插件其他渠道（虎皮椒/支付宝官方）的二维码体验完全统一。
//
// 凭证字段：
//   mch_id        商户号
//   app_id        AppID
//   serial_no     商户证书序列号
//   apiv3_key     APIv3 密钥
//   private_key   商户私钥（PEM 字符串）

func init() {
	Register(KindWxpayOfficial, buildWxpayOfficial)
	RegisterKindMeta(KindMeta{
		Kind:             KindWxpayOfficial,
		Name:             "微信支付官方 V3",
		Description:      "直连微信商户平台 APIv3，Native 扫码支付，需企业资质",
		SupportedMethods: []string{MethodWxpay},
		FieldDescriptors: []FieldDescriptor{
			{Key: "mch_id", Label: "商户号", Type: "text", Required: true},
			{Key: "app_id", Label: "AppID", Type: "text", Required: true,
				Description: "商户号绑定的公众号/小程序/移动应用 AppID"},
			{Key: "serial_no", Label: "商户证书序列号", Type: "text", Required: true},
			{Key: "apiv3_key", Label: "APIv3 密钥", Type: "password", Required: true},
			{Key: "private_key", Label: "商户私钥（PEM）", Type: "textarea", Required: true,
				Description: "PKCS#8 格式 RSA 私钥（apiclient_key.pem 文件内容）"},
		},
	})
}

func buildWxpayOfficial(id string, enabled bool, config map[string]string) (Provider, error) {
	p := &wxpayOfficialProvider{
		id:         id,
		enabled:    enabled,
		mchID:      strings.TrimSpace(config["mch_id"]),
		appID:      strings.TrimSpace(config["app_id"]),
		serialNo:   strings.TrimSpace(config["serial_no"]),
		apiV3Key:   strings.TrimSpace(config["apiv3_key"]),
		privateKey: config["private_key"], // PEM 含换行不能 trim
	}

	if p.fieldsComplete() {
		// initOnce 在 Enabled() 第一次被调用时延迟初始化 SDK client，
		// 避免在插件加载阶段 panic（例如 PEM 解析失败）影响整个进程
		// 这里直接尝试一次，失败也不阻塞
		_ = p.tryInit()
	}
	return p, nil
}

type wxpayOfficialProvider struct {
	id         string
	enabled    bool
	mchID      string
	appID      string
	serialNo   string
	apiV3Key   string
	privateKey string

	initOnce      sync.Once
	initErr       error
	rsaPrivateKey *rsa.PrivateKey
	apiClient     *core.Client
	notifyHandler *notify.Handler
}

func (p *wxpayOfficialProvider) ID() string                 { return p.id }
func (p *wxpayOfficialProvider) Name() string               { return "微信支付官方 (" + p.id + ")" }
func (p *wxpayOfficialProvider) Kind() string               { return KindWxpayOfficial }
func (p *wxpayOfficialProvider) SupportedMethods() []string { return []string{MethodWxpay} }
func (p *wxpayOfficialProvider) Enabled() bool {
	if !p.enabled || !p.fieldsComplete() {
		return false
	}
	if err := p.tryInit(); err != nil {
		return false
	}
	return p.apiClient != nil && p.notifyHandler != nil
}

func (p *wxpayOfficialProvider) fieldsComplete() bool {
	return p.mchID != "" && p.appID != "" && p.serialNo != "" &&
		p.apiV3Key != "" && p.privateKey != ""
}

// tryInit 加载私钥 + 注册自动下载平台证书的 downloader + 构造 API client + notify handler。
// 多次调用幂等。
func (p *wxpayOfficialProvider) tryInit() error {
	p.initOnce.Do(func() {
		key, err := utils.LoadPrivateKey(p.privateKey)
		if err != nil {
			p.initErr = fmt.Errorf("加载商户私钥失败: %w", err)
			return
		}
		p.rsaPrivateKey = key

		ctx := context.Background()
		client, err := core.NewClient(ctx,
			option.WithWechatPayAutoAuthCipher(p.mchID, p.serialNo, key, p.apiV3Key),
		)
		if err != nil {
			p.initErr = fmt.Errorf("创建微信支付 client 失败: %w", err)
			return
		}
		p.apiClient = client

		// notify handler 链路：
		//   downloader 维护平台证书自动下载/刷新
		//     → CertificateVisitor 提供证书查询
		//     → SHA256WithRSAVerifier 包装成 auth.Verifier（用于回调签名验证）
		//     → NotifyHandler 串联 verifier + AES-GCM 解密
		visitor := downloader.MgrInstance().GetCertificateVisitor(p.mchID)
		verifier := verifiers.NewSHA256WithRSAVerifier(visitor)
		p.notifyHandler, err = notify.NewRSANotifyHandler(p.apiV3Key, verifier)
		if err != nil {
			p.initErr = fmt.Errorf("创建微信支付 notify handler 失败: %w", err)
			return
		}
	})
	return p.initErr
}

// CreateOrder 调用 Native 下单接口，返回二维码 url（weixin://...）
//
// 微信 Native 返回的 code_url 是 weixin:// 协议字符串，前端直接用 qrcode 库
// 编码成图片即可，用户用微信扫码完成支付。
func (p *wxpayOfficialProvider) CreateOrder(ctx context.Context, in CreateOrderInput) (*CreateOrderResult, error) {
	if !p.Enabled() {
		return nil, ErrProviderDisabled
	}
	if in.Method != MethodWxpay {
		return nil, ErrUnsupportedMethod
	}

	svc := native.NativeApiService{Client: p.apiClient}
	totalCents := int64(in.Amount * 100) // 微信单位是分
	currency := "CNY"

	resp, _, err := svc.Prepay(ctx, native.PrepayRequest{
		Appid:       core.String(p.appID),
		Mchid:       core.String(p.mchID),
		Description: core.String(in.Subject),
		OutTradeNo:  core.String(in.OutTradeNo),
		NotifyUrl:   core.String(in.NotifyURL),
		Amount: &native.Amount{
			Total:    core.Int64(totalCents),
			Currency: &currency,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("微信 Native 下单失败: %w", err)
	}
	if resp == nil || resp.CodeUrl == nil {
		return nil, fmt.Errorf("微信 Native 下单返回空 code_url")
	}

	return &CreateOrderResult{
		QRCodeContent: *resp.CodeUrl,
		Raw: map[string]string{
			"code_url": *resp.CodeUrl,
		},
	}, nil
}

// VerifyCallback 验证微信支付异步通知签名 + 解密回调内容 + 解析订单状态
//
// 微信 V3 回调与其他渠道很不一样：
//   - 签名在 HTTP header 里（Wechatpay-Signature 等）
//   - 请求体是 AES-GCM 加密的 JSON
//   - SDK 的 NotifyHandler 一站式完成验签 + 解密 + 反序列化
//
// 因为 NotifyHandler.ParseNotifyRequest 需要 *http.Request，我们这里
// 用 CallbackRequest 里的 Body + Headers 重新构造一个 http.Request 喂进去。
func (p *wxpayOfficialProvider) VerifyCallback(ctx context.Context, req CallbackRequest) (*CallbackResult, error) {
	if !p.Enabled() {
		return nil, ErrProviderDisabled
	}

	// 重新构造一个 http.Request 给 SDK 用
	httpReq := &http.Request{
		Header: req.Headers,
		Body:   io.NopCloser(bytes.NewReader(req.Body)),
	}

	var txn payments.Transaction
	if _, err := p.notifyHandler.ParseNotifyRequest(ctx, httpReq, &txn); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}

	status := "pending"
	if txn.TradeState != nil {
		switch *txn.TradeState {
		case "SUCCESS":
			status = "paid"
		case "REFUND", "CLOSED", "REVOKED", "PAYERROR":
			status = "failed"
		}
	}

	var (
		outTradeNo, channelTxn string
		amount                 float64
	)
	if txn.OutTradeNo != nil {
		outTradeNo = *txn.OutTradeNo
	}
	if txn.TransactionId != nil {
		channelTxn = *txn.TransactionId
	}
	if txn.Amount != nil && txn.Amount.Total != nil {
		amount = float64(*txn.Amount.Total) / 100.0 // 分转元
	}

	// 微信 V3 要求响应 JSON：{"code":"SUCCESS","message":"成功"}
	return &CallbackResult{
		OutTradeNo: outTradeNo,
		Status:     status,
		ChannelTxn: channelTxn,
		Amount:     amount,
		Raw: map[string]string{
			"trade_state": derefString(txn.TradeState),
			"trade_type":  derefString(txn.TradeType),
		},
		Reply:     `{"code":"SUCCESS","message":"成功"}`,
		ReplyType: "json",
	}, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

var _ Provider = (*wxpayOfficialProvider)(nil)
