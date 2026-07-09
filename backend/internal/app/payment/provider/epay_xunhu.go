package provider

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 虎皮椒 V3 协议实现。
//
// 该协议被多个第三方平台兼容（虎皮椒、彩虹易支付二开等），是国内"易支付"协议家族里
// 最现代的一支。字段名 appid + appsecret，POST do.html 表单 JSON 响应。
//
// 与"标准/彩虹"易支付（pid + key + GET submit.php）的区别已经在 epay_caihong.go 处理。

func init() {
	Register(KindEpayXunhu, buildXunhu)
	RegisterKindMeta(KindMeta{
		Kind:        KindEpayXunhu,
		Name:        "虎皮椒 V3",
		Description: "虎皮椒及兼容平台（xunhupay/彩虹V3 二开等），POST do.html，appid+appsecret 签名",
		// 协议层支持的所有方式；admin 在配置时通过 enabled_methods 字段勾选实际启用的子集
		SupportedMethods: []string{MethodAlipay, MethodWxpay},
		FieldDescriptors: []FieldDescriptor{
			{Key: "appid", Label: "AppID", Type: "text", Required: true},
			{Key: "appsecret", Label: "AppSecret", Type: "password", Required: true},
			{Key: "gateway_url", Label: "支付网关 URL", Type: "text", Required: true,
				Placeholder: "https://api.xunhupay.com/payment/do.html",
				Description: "完整下单接口 URL（含 do.html）"},
			{Key: "enabled_methods", Label: "启用的支付方式", Type: "method-multi", Required: true,
				Description: "勾选该商户号在虎皮椒平台上签约/启用的子通道；至少选一个"},
		},
	})
}

func buildXunhu(id string, enabled bool, config map[string]string) (Provider, error) {
	return &xunhuProvider{
		id:             id,
		enabled:        enabled,
		appID:          strings.TrimSpace(config["appid"]),
		appSecret:      strings.TrimSpace(config["appsecret"]),
		gateway:        strings.TrimSpace(config["gateway_url"]),
		enabledMethods: parseEnabledMethods(config["enabled_methods"], []string{MethodAlipay, MethodWxpay}),
		client:         &http.Client{Timeout: 15 * time.Second},
	}, nil
}

type xunhuProvider struct {
	id             string
	enabled        bool
	appID          string
	appSecret      string
	gateway        string
	enabledMethods []string
	client         *http.Client
}

func (p *xunhuProvider) ID() string   { return p.id }
func (p *xunhuProvider) Name() string { return "虎皮椒 V3 (" + p.id + ")" }
func (p *xunhuProvider) Kind() string { return KindEpayXunhu }
func (p *xunhuProvider) SupportedMethods() []string {
	// 返回 admin 实际启用的子集（admin 在配置里勾选的 enabled_methods），
	// 没勾时退化为协议全集，避免 Provider 看起来"被禁用"
	if len(p.enabledMethods) == 0 {
		return []string{MethodAlipay, MethodWxpay}
	}
	return p.enabledMethods
}
func (p *xunhuProvider) Enabled() bool {
	return p.enabled && p.appID != "" && p.appSecret != "" && p.gateway != ""
}

// CreateOrder 调用虎皮椒 do.html 下单接口。
//
// in.Method 决定虎皮椒 type 字段：
//
//	alipay → "alipay" / wxpay → "wechat" / qqpay → "qqpay"
func (p *xunhuProvider) CreateOrder(ctx context.Context, in CreateOrderInput) (*CreateOrderResult, error) {
	if !p.Enabled() {
		return nil, ErrProviderDisabled
	}
	xunhuType, ok := xunhuMethodMap[in.Method]
	if !ok {
		return nil, ErrUnsupportedMethod
	}

	params := url.Values{}
	params.Set("version", "1.1")
	params.Set("lang", "zh-cn")
	params.Set("appid", p.appID)
	params.Set("trade_order_id", in.OutTradeNo)
	params.Set("total_fee", strconv.FormatFloat(in.Amount, 'f', 2, 64))
	params.Set("title", in.Subject)
	params.Set("time", strconv.FormatInt(time.Now().Unix(), 10))
	params.Set("notify_url", in.NotifyURL)
	params.Set("return_url", in.ReturnURL)
	params.Set("nonce_str", randomNonce(16))
	params.Set("type", xunhuType)
	if in.ClientIP != "" {
		params.Set("attach", in.ClientIP)
	}
	params.Set("hash", signXunhu(params, p.appSecret))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.gateway,
		strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求虎皮椒失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("虎皮椒 HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var apiResp struct {
		ErrCode   int    `json:"errcode"`
		ErrMsg    string `json:"errmsg"`
		URL       string `json:"url"`
		URLQrcode string `json:"url_qrcode"`
		Hash      string `json:"hash"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("解析虎皮椒响应失败: %w; body=%s", err, truncate(string(body), 200))
	}
	if apiResp.ErrCode != 0 {
		return nil, fmt.Errorf("虎皮椒下单失败: %s (errcode=%d)", apiResp.ErrMsg, apiResp.ErrCode)
	}

	return &CreateOrderResult{
		PaymentURL:    apiResp.URL,
		QRCodeContent: apiResp.URLQrcode,
		Raw: map[string]string{
			"errcode": strconv.Itoa(apiResp.ErrCode),
			"errmsg":  apiResp.ErrMsg,
			"hash":    apiResp.Hash,
		},
	}, nil
}

// VerifyCallback 验证虎皮椒回调签名 + 解析订单状态
func (p *xunhuProvider) VerifyCallback(_ context.Context, req CallbackRequest) (*CallbackResult, error) {
	if !p.Enabled() {
		return nil, ErrProviderDisabled
	}
	form := req.Form
	gotHash := form.Get("hash")
	if gotHash == "" {
		return nil, ErrInvalidSignature
	}
	wantHash := signXunhu(form, p.appSecret)
	if !strings.EqualFold(gotHash, wantHash) {
		return nil, ErrInvalidSignature
	}

	amount, _ := strconv.ParseFloat(form.Get("total_fee"), 64)
	status := "pending"
	if form.Get("status") == "OD" {
		status = "paid"
	}
	return &CallbackResult{
		OutTradeNo: form.Get("trade_order_id"),
		Status:     status,
		ChannelTxn: form.Get("transaction_id"),
		Amount:     amount,
		Raw:        flattenForm(form),
		Reply:      "success",
		ReplyType:  "text",
	}, nil
}

// xunhuMethodMap 把内部 PayMethod 映射到虎皮椒的 type 字段值
var xunhuMethodMap = map[string]string{
	MethodAlipay: "alipay",
	MethodWxpay:  "wechat",
}

// signXunhu 虎皮椒 V3 签名算法（与官方 PHP SDK 对齐）。
//
// 算法：
//  1. 剔除 hash 字段与所有空值字段
//  2. 按 key 字典序排序
//  3. 拼成 k1=v1&k2=v2&...&kN=vN
//  4. 直接追加 <appsecret>（无分隔符，这是官方 SDK 的关键细节，曾错误加 & 导致 errcode=40029）
//  5. 整体取 MD5 小写
func signXunhu(params url.Values, appsecret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "hash" {
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
	sb.WriteString(appsecret)

	sum := md5.Sum([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// flattenForm 把 url.Values 拍扁成 map[string]string，用于落库
func flattenForm(in url.Values) map[string]string {
	out := make(map[string]string, len(in))
	for k := range in {
		out[k] = in.Get(k)
	}
	return out
}

// randomNonce 生成 n 个字符的十六进制随机字符串
func randomNonce(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())[:n]
	}
	return hex.EncodeToString(b)[:n]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// 编译期断言
var _ Provider = (*xunhuProvider)(nil)

// 占位避免 errors 未使用警告（go vet 严格模式下）
var _ = errors.New
