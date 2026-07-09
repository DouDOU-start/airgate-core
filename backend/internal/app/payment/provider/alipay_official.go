package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/smartwalle/alipay/v3"
)

// 支付宝官方渠道实现，使用 github.com/smartwalle/alipay/v3 SDK。
//
// 该 SDK 包装了支付宝开放平台 OpenAPI，自动处理 RSA2 签名、参数加密、响应解析。
// 我们这里只用其中两个接口：
//   - TradePagePay：PC 网页支付（用户跳转到支付宝收银台）
//   - TradePreCreate：当面付/二维码（返回 qr_code 字符串，前端渲染二维码）
//
// 默认走 TradePreCreate（二维码），与本插件其他渠道的体验一致。
// 如果将来要支持手机网站支付（WAP），加一个 TradeWapPay 分支即可。
//
// 凭证字段：
//   app_id              支付宝开放平台分配的 AppID
//   private_key         应用私钥（PKCS#1 或 PKCS#8 PEM）
//   alipay_public_key   支付宝平台公钥（用于验签回调）
//   is_sandbox          是否沙箱模式（默认 false）

func init() {
	Register(KindAlipayOfficial, buildAlipayOfficial)
	RegisterKindMeta(KindMeta{
		Kind:             KindAlipayOfficial,
		Name:             "支付宝官方",
		Description:      "直连支付宝开放平台，资金直达商户账户，需企业资质 + RSA 公私钥",
		SupportedMethods: []string{MethodAlipay},
		FieldDescriptors: []FieldDescriptor{
			{Key: "app_id", Label: "AppID", Type: "text", Required: true,
				Description: "在支付宝开放平台创建的应用 AppID"},
			{Key: "private_key", Label: "应用私钥（PEM）", Type: "textarea", Required: true,
				Description: "PKCS#1 或 PKCS#8 PEM 格式的应用私钥"},
			{Key: "alipay_public_key", Label: "支付宝公钥（PEM）", Type: "textarea", Required: true,
				Description: "支付宝平台公钥，用于回调验签"},
			{Key: "is_sandbox", Label: "沙箱模式", Type: "bool",
				Description: "勾选后调用沙箱网关，用于测试"},
		},
	})
}

func buildAlipayOfficial(id string, enabled bool, config map[string]string) (Provider, error) {
	p := &alipayOfficialProvider{
		id:         id,
		enabled:    enabled,
		appID:      strings.TrimSpace(config["app_id"]),
		privateKey: config["private_key"], // PEM 字段内部含换行，不能 trim
		publicKey:  config["alipay_public_key"],
		isSandbox:  parseBool(config["is_sandbox"]),
	}

	// 配置完整时立即初始化 SDK client；否则保留 nil，Enabled() 会返回 false
	if p.appID != "" && p.privateKey != "" && p.publicKey != "" {
		client, err := alipay.New(p.appID, p.privateKey, !p.isSandbox)
		if err != nil {
			// 初始化失败（例如私钥格式错误）也不阻塞插件加载，
			// 让 Enabled() 返回 false 即可，admin 在配置页能看到 Provider 但下单会拒绝
			return p, nil
		}
		if err := client.LoadAliPayPublicKey(p.publicKey); err != nil {
			return p, nil
		}
		p.client = client
	}
	return p, nil
}

type alipayOfficialProvider struct {
	id         string
	enabled    bool
	appID      string
	privateKey string
	publicKey  string
	isSandbox  bool
	client     *alipay.Client // nil 表示初始化失败或配置不完整
}

func (p *alipayOfficialProvider) ID() string                 { return p.id }
func (p *alipayOfficialProvider) Name() string               { return "支付宝官方 (" + p.id + ")" }
func (p *alipayOfficialProvider) Kind() string               { return KindAlipayOfficial }
func (p *alipayOfficialProvider) SupportedMethods() []string { return []string{MethodAlipay} }
func (p *alipayOfficialProvider) Enabled() bool {
	return p.enabled && p.client != nil
}

// CreateOrder 调用 alipay.trade.precreate 生成二维码字符串。
//
// 返回的 QRCodeContent 是 alipay 的标准 schema 字符串（https://qr.alipay.com/...），
// 前端用 qrcode 库直接编码即可。
//
// 不使用 TradePagePay（PC 跳转）的原因：
//   - 跳转 URL 用户体验差（必须新窗口打开收银台）
//   - 二维码方案统一：跟虎皮椒/微信官方都一样的扫码体验
//   - alipay.trade.precreate 接口对个人用户也开放（部分场景）
func (p *alipayOfficialProvider) CreateOrder(ctx context.Context, in CreateOrderInput) (*CreateOrderResult, error) {
	if !p.Enabled() {
		return nil, ErrProviderDisabled
	}
	if in.Method != MethodAlipay {
		return nil, ErrUnsupportedMethod
	}

	param := alipay.TradePreCreate{
		Trade: alipay.Trade{
			NotifyURL:   in.NotifyURL,
			ReturnURL:   in.ReturnURL,
			Subject:     in.Subject,
			OutTradeNo:  in.OutTradeNo,
			TotalAmount: strconv.FormatFloat(in.Amount, 'f', 2, 64),
			ProductCode: "FACE_TO_FACE_PAYMENT",
		},
	}
	rsp, err := p.client.TradePreCreate(ctx, param)
	if err != nil {
		return nil, fmt.Errorf("支付宝预下单失败: %w", err)
	}
	if rsp.Code != "10000" {
		return nil, fmt.Errorf("支付宝预下单失败: %s (%s)", rsp.Msg, rsp.Code)
	}

	return &CreateOrderResult{
		QRCodeContent: rsp.QRCode,
		Raw: map[string]string{
			"code":         string(rsp.Code),
			"msg":          rsp.Msg,
			"out_trade_no": rsp.OutTradeNo,
		},
	}, nil
}

// VerifyCallback 验证支付宝异步通知签名 + 解析订单状态
//
// 支付宝回调字段（form-urlencoded POST）：
//
//	notify_id, notify_type, notify_time, app_id, sign, sign_type,
//	trade_no, out_trade_no, trade_status, total_amount, ...
//
// trade_status 有几种值：WAIT_BUYER_PAY / TRADE_SUCCESS / TRADE_FINISHED / TRADE_CLOSED
// 我们只把 TRADE_SUCCESS 和 TRADE_FINISHED 视为已支付。
//
// 支付宝要求服务端响应纯文本 "success"。
func (p *alipayOfficialProvider) VerifyCallback(ctx context.Context, req CallbackRequest) (*CallbackResult, error) {
	if !p.Enabled() {
		return nil, ErrProviderDisabled
	}
	form := req.Form
	if form.Get("sign") == "" {
		return nil, ErrInvalidSignature
	}
	if err := p.client.VerifySign(ctx, form); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}

	amount, _ := strconv.ParseFloat(form.Get("total_amount"), 64)
	status := "pending"
	switch form.Get("trade_status") {
	case "TRADE_SUCCESS", "TRADE_FINISHED":
		status = "paid"
	case "TRADE_CLOSED":
		status = "failed"
	}

	return &CallbackResult{
		OutTradeNo: form.Get("out_trade_no"),
		Status:     status,
		ChannelTxn: form.Get("trade_no"),
		Amount:     amount,
		Raw:        flattenForm(form),
		Reply:      "success",
		ReplyType:  "text",
	}, nil
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

var _ Provider = (*alipayOfficialProvider)(nil)

// 占位避免 url/errors 未使用警告
var (
	_ = url.Parse
	_ = errors.New
)
