package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/requestaudit"
)

// channelNeedsCPATranslation 判断当前渠道是否需要跨协议转换。
// 媒体与任务端点不在文本白名单内；入口协议与上游原生协议一致时继续走 adaptor
// 原生直发，避免没有实际转换需求时引入额外重组。
func channelNeedsCPATranslation(endpoint, entryProtocol, channelType string) bool {
	if channelRoutingProtocolForEndpoint(endpoint) != registry.ProtocolTranslatedText {
		return false
	}
	nativeProtocol := registry.ProtocolForKeyType(channelType)
	return nativeProtocol != "" && nativeProtocol != entryProtocol
}

// executeChannelCPAAttempt 通过 CPA 执行一次跨协议渠道请求，并沿用渠道容量与审计口径。
func (p *Pipeline) executeChannelCPAAttempt(
	c *gin.Context,
	channel *registry.ChannelKeySnapshot,
	req *dto.ChatRequest,
	endpoint string,
	entryProtocol string,
	start time.Time,
	requestID string,
	rpmMinute int64,
	capacityID int,
	auditRequest *requestaudit.Handle,
) attemptResult {
	defer func() {
		go p.concurrency.ReleaseKeySlot(context.Background(), capacityID, requestID)
		if rec := recover(); rec != nil {
			p.rpm.DecrementKeyRPM(context.Background(), capacityID, rpmMinute)
			panic(rec)
		}
	}()

	if p.cpa == nil {
		return attemptResult{buildErr: errCPAUnavailable}
	}
	payload, err := req.Marshal()
	if err != nil {
		return attemptResult{buildErr: err}
	}
	platform, baseURL, err := channelCPAAuth(channel.Type, channel.BaseURL)
	if err != nil {
		return attemptResult{buildErr: err}
	}

	fwdReq := cpa.ForwardRequest{
		Account: cpa.AccountAuthInput{
			AccountID: channel.KeyID,
			Name:      channel.ChannelName + "/" + channel.KeyName,
			Platform:  platform,
			Type:      "api_key",
			Credentials: map[string]string{
				"api_key":  channel.APIKey,
				"base_url": baseURL,
			},
		},
		Model:            req.Model,
		UpstreamModel:    upstreamModel(channel, req.Model),
		Endpoint:         endpoint,
		EntryProtocol:    entryProtocol,
		Stream:           req.Stream,
		Payload:          payload,
		Headers:          channelCPAHeaders(c),
		RequestStartedAt: start,
	}

	ctx := c.Request.Context()
	baseTransport, err := p.accountAuditTransport("")
	if err != nil {
		return attemptResult{auditErr: fmt.Errorf("构造渠道 CPA 传输层失败: %w", err)}
	}
	var auditTransport *requestaudit.RoundTripper
	transport := baseTransport
	if auditRequest != nil {
		target := requestaudit.Target{
			RouteKind: "channel", ChannelID: channel.ChannelID, ChannelName: channel.ChannelName,
			ChannelKeyID: channel.KeyID, ChannelKeyName: channel.KeyName,
		}
		auditTransport = requestaudit.NewRoundTripper(transport, auditRequest, target)
		transport = auditTransport
	}
	transport = &channelOverrideTransport{
		base:    transport,
		headers: channel.HeaderOverride,
		params:  channel.ParamOverride,
	}
	//nolint:staticcheck // CPA 通过固定字符串上下文键接收最终网络层。
	ctx = context.WithValue(ctx, "cliproxy.roundtripper", transport)

	cancel := context.CancelFunc(func() {})
	if !req.Stream {
		ctx, cancel = context.WithTimeout(ctx, nonStreamTimeout)
	}
	defer cancel()

	result := p.cpa.Forward(ctx, c, fwdReq)
	if auditTransport != nil && req.Stream && result.Done && result.StreamErr == nil &&
		result.NetErr == nil && result.BuildErr == nil && result.StatusCode >= 200 && result.StatusCode < 300 {
		auditTransport.MarkLatestStreamCompleted()
	}
	if isAuditWriteError(result.NetErr) || isAuditWriteError(result.BuildErr) || isAuditWriteError(result.StreamErr) {
		return attemptResult{auditErr: requestaudit.ErrWrite}
	}
	requestFirstTokenMs := result.RequestFirstTokenMs
	if requestFirstTokenMs == 0 {
		requestFirstTokenMs = result.FirstTokenMs
	}
	return attemptResult{
		netErr:              result.NetErr,
		buildErr:            result.BuildErr,
		statusCode:          result.StatusCode,
		headers:             result.Headers,
		body:                result.Body,
		contentType:         result.ContentType,
		usage:               result.Usage,
		firstTokenMs:        requestFirstTokenMs,
		attemptFirstTokenMs: result.FirstTokenMs,
		requestFirstTokenMs: result.RequestFirstTokenMs,
		written:             result.Written,
		streamErr:           result.StreamErr,
		done:                result.Done,
	}
}

func channelCPAAuth(channelType, rawBaseURL string) (platform, baseURL string, err error) {
	baseURL = strings.TrimRight(strings.TrimSpace(rawBaseURL), "/")
	if baseURL == "" {
		return "", "", fmt.Errorf("渠道 base_url 为空")
	}
	switch channelType {
	case "openai_compatible":
		platform = "openai-compatibility"
		if !strings.HasSuffix(baseURL, "/v1") {
			baseURL += "/v1"
		}
	case "anthropic":
		platform = "anthropic"
		baseURL = strings.TrimSuffix(baseURL, "/v1")
	case "gemini":
		platform = "gemini"
		baseURL = strings.TrimSuffix(strings.TrimSuffix(baseURL, "/v1beta"), "/v1")
	default:
		return "", "", fmt.Errorf("渠道类型 %s 不支持 CPA 文本翻译", channelType)
	}
	return platform, baseURL, nil
}

func channelCPAHeaders(c *gin.Context) http.Header {
	headers := make(http.Header)
	if c != nil && c.Request != nil {
		headers = c.Request.Header.Clone()
	}
	for _, name := range []string{"Authorization", "Proxy-Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
		headers.Del(name)
	}
	headers.Set("Content-Type", "application/json")
	return headers
}

// channelOverrideTransport 在 CPA 完成协议翻译与认证注入后应用渠道原生参数和请求头覆盖。
// 包装层位于审计层外侧，确保审计保存的是最终实际发出的请求。
type channelOverrideTransport struct {
	base    http.RoundTripper
	headers map[string]string
	params  map[string]any
}

func (t *channelOverrideTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t == nil || t.base == nil {
		return nil, fmt.Errorf("渠道 CPA 传输层未配置")
	}
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	for key, value := range t.headers {
		clone.Header.Set(key, value)
	}
	if len(t.params) > 0 && clone.Body != nil && strings.Contains(strings.ToLower(clone.Header.Get("Content-Type")), "json") {
		body, err := io.ReadAll(clone.Body)
		if err != nil {
			return nil, fmt.Errorf("读取 CPA 翻译后的渠道请求体失败: %w", err)
		}
		_ = clone.Body.Close()
		parsed, err := dto.ParseChatRequest(body)
		if err != nil {
			return nil, fmt.Errorf("CPA 翻译后的渠道请求体不是 JSON 对象: %w", err)
		}
		if err := applyChannelParamOverride(parsed, t.params); err != nil {
			return nil, err
		}
		body, err = parsed.Marshal()
		if err != nil {
			return nil, fmt.Errorf("序列化渠道参数覆盖后的请求体失败: %w", err)
		}
		clone.Body = io.NopCloser(bytes.NewReader(body))
		clone.ContentLength = int64(len(body))
		clone.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}
	return t.base.RoundTrip(clone)
}

// applyChannelParamOverride 兼容两种存量格式：
//  1. 字段平铺，nil 表示删除；
//  2. 管理端结构 {"set": {...}, "remove": [...]}。
//
// 执行顺序为平铺字段、set、remove，因此显式删除始终优先。
func applyChannelParamOverride(req *dto.ChatRequest, override map[string]any) error {
	for key, value := range override {
		if key == "set" || key == "remove" {
			continue
		}
		if err := applyChannelParamValue(req, key, value); err != nil {
			return err
		}
	}
	if setValues, ok := override["set"].(map[string]any); ok {
		for key, value := range setValues {
			if err := applyChannelParamValue(req, key, value); err != nil {
				return err
			}
		}
	}
	switch removeValues := override["remove"].(type) {
	case []string:
		for _, key := range removeValues {
			req.Remove(key)
		}
	case []any:
		for _, rawKey := range removeValues {
			if key, ok := rawKey.(string); ok {
				req.Remove(key)
			}
		}
	}
	return nil
}

func applyChannelParamValue(req *dto.ChatRequest, key string, value any) error {
	if value == nil {
		req.Remove(key)
		return nil
	}
	if err := req.Set(key, value); err != nil {
		return fmt.Errorf("应用渠道参数覆盖 %s 失败: %w", key, err)
	}
	return nil
}
