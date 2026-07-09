package provider

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// 标准/彩虹易支付协议实现。
//
// 这是国内"易支付"协议家族的另一支：pid + key 凭证，GET 跳转 submit.php，
// 字段名 out_trade_no / money / trade_status，签名直接 MD5(sorted + key)。
//
// 与虎皮椒 V3 的核心区别：
//   - 凭证字段名：pid/key（虎皮椒是 appid/appsecret）
//   - 下单方式：拼参数 + 签名生成 GET URL，浏览器 302 跳转（虎皮椒是 POST do.html JSON）
//   - 签名拼接：在 sorted 字符串末尾追加 key，没有 & 分隔（虎皮椒一样）
//   - 回调字段：trade_status=TRADE_SUCCESS（虎皮椒是 status=OD）
//   - 子通道字段：type=alipay/wxpay/qqpay 取值与虎皮椒相同但叫法略有差异

func init() {
	Register(KindEpayCaihong, buildCaihong)
	RegisterKindMeta(KindMeta{
		Kind:             KindEpayCaihong,
		Name:             "彩虹易支付（标准）",
		Description:      "彩虹/码支付/各类 PHP 易支付平台，pid+key，GET submit.php 跳转",
		SupportedMethods: []string{MethodAlipay, MethodWxpay},
		FieldDescriptors: []FieldDescriptor{
			{Key: "pid", Label: "PID（商户 ID）", Type: "text", Required: true},
			{Key: "key", Label: "Key（商户密钥）", Type: "password", Required: true},
			{Key: "gateway", Label: "支付网关 URL", Type: "text", Required: true,
				Placeholder: "https://pay.example.com",
				Description: "易支付平台根 URL，不含 /submit.php"},
			{Key: "enabled_methods", Label: "启用的支付方式", Type: "method-multi", Required: true,
				Description: "勾选该商户号在彩虹平台上签约/启用的子通道；至少选一个"},
		},
	})
}

func buildCaihong(id string, enabled bool, config map[string]string) (Provider, error) {
	return &caihongProvider{
		id:             id,
		enabled:        enabled,
		pid:            strings.TrimSpace(config["pid"]),
		key:            strings.TrimSpace(config["key"]),
		gateway:        strings.TrimRight(strings.TrimSpace(config["gateway"]), "/"),
		enabledMethods: parseEnabledMethods(config["enabled_methods"], []string{MethodAlipay, MethodWxpay}),
	}, nil
}

type caihongProvider struct {
	id             string
	enabled        bool
	pid            string
	key            string
	gateway        string
	enabledMethods []string
}

func (p *caihongProvider) ID() string   { return p.id }
func (p *caihongProvider) Name() string { return "彩虹易支付 (" + p.id + ")" }
func (p *caihongProvider) Kind() string { return KindEpayCaihong }
func (p *caihongProvider) SupportedMethods() []string {
	if len(p.enabledMethods) == 0 {
		return []string{MethodAlipay, MethodWxpay}
	}
	return p.enabledMethods
}
func (p *caihongProvider) Enabled() bool {
	return p.enabled && p.pid != "" && p.key != "" && p.gateway != ""
}

// CreateOrder 彩虹易支付不需要服务端预下单，直接返回带签名的 GET 跳转 URL。
//
// 用户浏览器跳到这个 URL 后，由易支付平台引导付款。
// 与虎皮椒最大的差异是这里没有 HTTP 调用，签完名拼出 URL 就返回。
func (p *caihongProvider) CreateOrder(_ context.Context, in CreateOrderInput) (*CreateOrderResult, error) {
	if !p.Enabled() {
		return nil, ErrProviderDisabled
	}
	caihongType, ok := caihongMethodMap[in.Method]
	if !ok {
		return nil, ErrUnsupportedMethod
	}

	params := url.Values{}
	params.Set("pid", p.pid)
	params.Set("type", caihongType)
	params.Set("out_trade_no", in.OutTradeNo)
	params.Set("notify_url", in.NotifyURL)
	params.Set("return_url", in.ReturnURL)
	params.Set("name", in.Subject)
	params.Set("money", strconv.FormatFloat(in.Amount, 'f', 2, 64))
	if in.ClientIP != "" {
		params.Set("clientip", in.ClientIP)
	}

	sign := signCaihong(params, p.key)
	params.Set("sign", sign)
	params.Set("sign_type", "MD5")

	paymentURL := p.gateway + "/submit.php?" + params.Encode()

	return &CreateOrderResult{
		PaymentURL: paymentURL,
		Raw:        flattenForm(params),
	}, nil
}

// VerifyCallback 验证回调签名 + 解析订单状态
//
// 字段示例：pid, trade_no, out_trade_no, type, name, money, trade_status, sign, sign_type
// trade_status == "TRADE_SUCCESS" 表示支付成功。回调要求服务端响应纯文本 "success"。
func (p *caihongProvider) VerifyCallback(_ context.Context, req CallbackRequest) (*CallbackResult, error) {
	if !p.Enabled() {
		return nil, ErrProviderDisabled
	}
	form := req.Form
	gotSign := form.Get("sign")
	if gotSign == "" {
		return nil, ErrInvalidSignature
	}
	wantSign := signCaihong(form, p.key)
	if !strings.EqualFold(gotSign, wantSign) {
		return nil, ErrInvalidSignature
	}

	amount, _ := strconv.ParseFloat(form.Get("money"), 64)
	status := "pending"
	if form.Get("trade_status") == "TRADE_SUCCESS" {
		status = "paid"
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

// caihongMethodMap PayMethod → 彩虹 type 字段
var caihongMethodMap = map[string]string{
	MethodAlipay: "alipay",
	MethodWxpay:  "wxpay", // 注意：彩虹用 wxpay 而非 wechat
}

// signCaihong 标准易支付签名算法。
//
// 算法（与虎皮椒签名细节有差异，注意区分）：
//  1. 剔除 sign / sign_type 字段与所有空值字段
//  2. 按 key 字典序排序
//  3. 拼成 k1=v1&k2=v2&...&kN=vN
//  4. 直接追加 <key>（无分隔符）
//  5. 整体取 MD5 小写
//
// 实际上虎皮椒和彩虹的签名拼接规则一致，只是排除字段不同（虎皮椒排除 hash，彩虹排除 sign+sign_type）。
func signCaihong(params url.Values, key string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "sign" || k == "sign_type" {
			continue
		}
		v := params.Get(k)
		if v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(params.Get(k))
	}
	sb.WriteString(key)

	sum := md5.Sum([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

var _ Provider = (*caihongProvider)(nil)

// 占位避免 fmt 未使用警告（开发期）
var _ = fmt.Sprintf
