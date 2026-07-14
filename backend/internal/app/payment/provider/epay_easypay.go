package provider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// callbackReplayWindow 回调时间戳/nonce 防重放的容忍窗口：超出此窗口的时间戳直接拒绝，
// 窗口内的 nonce 记为已消费，重复出现视为重放攻击。
const callbackReplayWindow = 5 * time.Minute

// easy-pay 对接实现。
//
// easy-pay 是一套独立部署的支付网关聚合系统，使用 REST JSON API + HMAC-SHA256 签名。
// 与易支付协议家族（MD5 签名 / form 表单）完全不同：
//   - 认证：app_id + app_secret，签名在 HTTP Header 中传递
//   - 下单：POST JSON 到 /api/v1/pay/create，返回 CodeURL / H5URL
//     （easy-pay 响应结构体 CreateOrderResult 未打 json tag，字段名走 Go 默认大写驼峰，
//     这与 easy-pay 自己文档写的 snake_case 示例不一致，属于上游 bug；一旦上游修了 tag，
//     这里的 Data.OrderNo/CodeURL/H5URL 会全部解析失败，需要跟着改。）
//   - 回调：POST JSON + HMAC-SHA256 Header 签名，回复 HTTP 2xx
//   - 金额：分（int64），本 Provider 自动做元↔分转换

func init() {
	Register(KindEpayEasyPay, buildEasyPay)
	RegisterKindMeta(KindMeta{
		Kind:             KindEpayEasyPay,
		Name:             "EasyPay（自建支付网关）",
		Description:      "对接自部署的 easy-pay 支付网关，REST API + HMAC-SHA256 签名",
		SupportedMethods: []string{MethodAlipay, MethodWxpay},
		FieldDescriptors: []FieldDescriptor{
			{Key: "app_id", Label: "App ID", Type: "text", Required: true,
				Description: "easy-pay 分配的商户 App ID"},
			{Key: "app_secret", Label: "App Secret", Type: "password", Required: true,
				Description: "easy-pay 分配的商户密钥，用于 HMAC-SHA256 签名"},
			{Key: "gateway", Label: "网关地址", Type: "text", Required: true,
				Placeholder: "https://pay.example.com",
				Description: "easy-pay 根 URL，不含路径"},
			{Key: "enabled_methods", Label: "启用的支付方式", Type: "method-multi", Required: true,
				Description: "勾选 easy-pay 已配置的支付通道；至少选一个"},
		},
	})
}

func buildEasyPay(id string, enabled bool, config map[string]string) (Provider, error) {
	return &easyPayProvider{
		id:             id,
		enabled:        enabled,
		appID:          strings.TrimSpace(config["app_id"]),
		appSecret:      strings.TrimSpace(config["app_secret"]),
		gateway:        strings.TrimRight(strings.TrimSpace(config["gateway"]), "/"),
		enabledMethods: parseEnabledMethods(config["enabled_methods"], []string{MethodAlipay, MethodWxpay}),
		client:         &http.Client{Timeout: 15 * time.Second},
	}, nil
}

type easyPayProvider struct {
	id             string
	enabled        bool
	appID          string
	appSecret      string
	gateway        string
	enabledMethods []string
	client         *http.Client
	seenNonces     sync.Map // nonce(string) -> 消费时间(time.Time)，防重放
}

func (p *easyPayProvider) ID() string   { return p.id }
func (p *easyPayProvider) Name() string { return "EasyPay (" + p.id + ")" }
func (p *easyPayProvider) Kind() string { return KindEpayEasyPay }
func (p *easyPayProvider) SupportedMethods() []string {
	if len(p.enabledMethods) == 0 {
		return []string{MethodAlipay, MethodWxpay}
	}
	return p.enabledMethods
}
func (p *easyPayProvider) Enabled() bool {
	return p.enabled && p.appID != "" && p.appSecret != "" && p.gateway != ""
}

// easyPayChannelMap 将内部 PayMethod 映射到 easy-pay 的 channel 字段
var easyPayChannelMap = map[string]string{
	MethodAlipay: "alipay",
	MethodWxpay:  "wechat",
}

// CreateOrder 调用 easy-pay /api/v1/pay/create 下单。
// easy-pay 金额单位为分，本方法自动将元转分。
func (p *easyPayProvider) CreateOrder(ctx context.Context, in CreateOrderInput) (*CreateOrderResult, error) {
	if !p.Enabled() {
		return nil, ErrProviderDisabled
	}
	ch, ok := easyPayChannelMap[in.Method]
	if !ok {
		return nil, ErrUnsupportedMethod
	}

	amountCents := int64(math.Round(in.Amount * 100))

	reqBody := easyPayCreateReq{
		MerchantOrderNo: in.OutTradeNo,
		Channel:         ch,
		TradeType:       "native",
		Subject:         in.Subject,
		Amount:          amountCents,
		NotifyURL:       in.NotifyURL,
		ExpireSeconds:   in.ExpireSeconds,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	apiPath := "/api/v1/pay/create"
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := randomNonce(32)
	sig := easyPaySign(p.appSecret, http.MethodPost, apiPath, ts, nonce, bodyBytes)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.gateway+apiPath, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-App-Id", p.appID)
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", sig)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 easy-pay 失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("easy-pay HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var apiResp easyPayCreateResp
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("解析 easy-pay 响应失败: %w; body=%s", err, truncate(string(respBody), 200))
	}
	if apiResp.Code != "OK" {
		return nil, fmt.Errorf("easy-pay 下单失败: code=%s, msg=%s", apiResp.Code, apiResp.Message)
	}

	result := &CreateOrderResult{
		Raw: map[string]string{
			"order_no": apiResp.Data.OrderNo,
		},
	}
	if apiResp.Data.CodeURL != "" {
		result.QRCodeContent = apiResp.Data.CodeURL
		result.PaymentURL = apiResp.Data.CodeURL
	}
	if apiResp.Data.H5URL != "" {
		result.PaymentURL = apiResp.Data.H5URL
	}
	return result, nil
}

// VerifyCallback 验证 easy-pay 异步通知的 HMAC-SHA256 签名并解析支付结果。
//
// easy-pay 回调格式：
//   - POST JSON body，签名在 Header 中（X-Signature / X-Timestamp / X-Nonce）
//   - 签名算法：HMAC-SHA256(secret, "POST\n{path}\n{ts}\n{nonce}\n{body}")
//   - path 是 easy-pay 发送时使用的 notify_url 的路径部分
func (p *easyPayProvider) VerifyCallback(_ context.Context, req CallbackRequest) (*CallbackResult, error) {
	if !p.Enabled() {
		return nil, ErrProviderDisabled
	}

	gotSig := req.Headers.Get("X-Signature")
	ts := req.Headers.Get("X-Timestamp")
	nonce := req.Headers.Get("X-Nonce")
	if gotSig == "" || ts == "" || nonce == "" {
		return nil, ErrInvalidSignature
	}

	// easy-pay 签名时使用的 path 是 notify_url 的路径部分，
	// 即 core 回调路由 /api/v1/payment/notify/{provider_id}
	callbackPath := "/api/v1/payment/notify/" + p.id
	wantSig := easyPaySign(p.appSecret, http.MethodPost, callbackPath, ts, nonce, req.Body)
	if !hmac.Equal([]byte(gotSig), []byte(wantSig)) {
		return nil, ErrInvalidSignature
	}
	if err := p.checkReplay(ts, nonce); err != nil {
		return nil, err
	}

	var payload easyPayNotifyPayload
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		return nil, fmt.Errorf("解析回调 body 失败: %w", err)
	}

	// easy-pay 金额为分，转回元
	amount := float64(payload.Amount) / 100.0

	status := "pending"
	if payload.Status == "paid" {
		status = "paid"
	}

	return &CallbackResult{
		OutTradeNo: payload.MerchantOrderNo,
		Status:     status,
		ChannelTxn: payload.ChannelOrderNo,
		Amount:     amount,
		Raw: map[string]string{
			"order_no":         payload.OrderNo,
			"channel":          payload.Channel,
			"channel_order_no": payload.ChannelOrderNo,
			"status":           payload.Status,
			"paid_at":          payload.PaidAt,
		},
		Reply:     `{"code":"OK"}`,
		ReplyType: "json",
	}, nil
}

// checkReplay 拒绝超出时间窗口的时间戳，并保证同一个 nonce 在窗口内只被消费一次。
// 签名本身只证明请求来自持有 appSecret 的一方，无法阻止一条被截获的合法请求被反复重放；
// 这里补上时间戳新鲜度 + nonce 去重，把重放窗口收窄到 callbackReplayWindow 内的一次性消费。
func (p *easyPayProvider) checkReplay(ts, nonce string) error {
	tsUnix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return ErrInvalidSignature
	}
	now := time.Now()
	if now.Sub(time.Unix(tsUnix, 0)).Abs() > callbackReplayWindow {
		return ErrInvalidSignature
	}

	cutoff := now.Add(-callbackReplayWindow)
	p.seenNonces.Range(func(k, v any) bool {
		if v.(time.Time).Before(cutoff) {
			p.seenNonces.Delete(k)
		}
		return true
	})
	if _, loaded := p.seenNonces.LoadOrStore(nonce, now); loaded {
		return ErrInvalidSignature
	}
	return nil
}

// --- 签名 ---

// easyPaySign 计算 easy-pay HMAC-SHA256 签名，与 easy-pay 的 sign.Compute 算法一致。
func easyPaySign(appSecret, method, path, timestamp, nonce string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write([]byte(strings.ToUpper(method)))
	mac.Write([]byte("\n"))
	mac.Write([]byte(path))
	mac.Write([]byte("\n"))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write([]byte(nonce))
	mac.Write([]byte("\n"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// --- 请求/响应结构 ---

type easyPayCreateReq struct {
	MerchantOrderNo string `json:"merchant_order_no"`
	Channel         string `json:"channel"`
	TradeType       string `json:"trade_type"`
	Subject         string `json:"subject"`
	Amount          int64  `json:"amount"`
	NotifyURL       string `json:"notify_url,omitempty"`
	ExpireSeconds   int    `json:"expire_seconds,omitempty"`
}

type easyPayCreateResp struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    struct {
		OrderNo string `json:"OrderNo"`
		CodeURL string `json:"CodeURL"`
		H5URL   string `json:"H5URL"`
	} `json:"data"`
}

type easyPayNotifyPayload struct {
	OrderNo         string `json:"order_no"`
	MerchantOrderNo string `json:"merchant_order_no"`
	Channel         string `json:"channel"`
	ChannelOrderNo  string `json:"channel_order_no"`
	Amount          int64  `json:"amount"`
	Currency        string `json:"currency"`
	Status          string `json:"status"`
	PaidAt          string `json:"paid_at"`
}

var _ Provider = (*easyPayProvider)(nil)
