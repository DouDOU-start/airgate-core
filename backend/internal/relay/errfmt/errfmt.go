// Package errfmt 按入口协议渲染转发路径错误体（预检/限流/failover 耗尽/鉴权/上游错误）。
//
// 错误契约（语义保留、载体重建）：
//   - 网关自产错误（Render）：按入口协议出原生形态；
//   - 上游错误（ParseUpstream + RenderUpstream）：解析上游错误体提取语义字段
//     （message/type/code），再按入口协议重建载体——HTTP 状态码保留上游原值，
//     原始响应体不透传（只进失败留痕）。
//
// 三种协议的原生形态——
//
//	openai    {"error":{"message","type","code"},"request_id"}
//	anthropic {"type":"error","error":{"type","message"},"request_id"}
//	gemini    {"error":{"code":<int>,"message","status"}}
//
// 被 relay pipeline（转发期错误）与 server middleware（鉴权期错误）共用，
// 保证同一路径上的所有错误形态一致。
package errfmt

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// ProtocolForPath 按请求路径判定入口协议（供 handler 之前的中间件使用；
// handler 内部以显式设置的入口协议为准）。
func ProtocolForPath(path string) string {
	switch {
	case strings.HasPrefix(path, "/v1/messages"):
		return registry.ProtocolAnthropic
	case strings.HasPrefix(path, "/v1beta/"):
		return registry.ProtocolGemini
	default:
		return registry.ProtocolOpenAI
	}
}

// OpenAIError OpenAI 形态错误体：{"error":{"message","type","code"}}。
// request_id 为顶层兄弟字段（不拼进 message——下游 SDK 常对 message 做字符串匹配），
// 与 X-Request-ID 响应头双通道承载，供用户报修与后台失败日志互查。
type OpenAIError struct {
	Error     OpenAIErrorDetail `json:"error"`
	RequestID string            `json:"request_id,omitempty"`
}

// OpenAIErrorDetail OpenAI 错误明细。
type OpenAIErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// AnthropicError Anthropic 形态错误体：{"type":"error","error":{"type","message"}}。
type AnthropicError struct {
	Type      string               `json:"type"`
	Error     AnthropicErrorDetail `json:"error"`
	RequestID string               `json:"request_id,omitempty"`
}

// AnthropicErrorDetail Anthropic 错误明细。
type AnthropicErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// GeminiError Gemini 形态错误体：{"error":{"code","message","status"}}。
// 无 request_id 字段（严格贴合 Google 形态），排障互查依赖 X-Request-ID 响应头。
type GeminiError struct {
	Error GeminiErrorDetail `json:"error"`
}

// GeminiErrorDetail Gemini 错误明细（status 为 gRPC canonical code 名）。
type GeminiErrorDetail struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// Render 按入口协议渲染错误体（供 c.JSON / AbortWithStatusJSON 序列化）。
// errType/code 为网关内部错误分类（OpenAI 命名口径），非 openai 协议时映射为原生分类。
func Render(protocol string, status int, errType, code, message, requestID string) any {
	switch protocol {
	case registry.ProtocolAnthropic:
		return AnthropicError{
			Type: "error",
			Error: AnthropicErrorDetail{
				Type:    anthropicErrorType(errType, status),
				Message: message,
			},
			RequestID: requestID,
		}
	case registry.ProtocolGemini:
		return GeminiError{Error: GeminiErrorDetail{
			Code:    status,
			Message: message,
			Status:  geminiStatus(status),
		}}
	default:
		return OpenAIError{
			Error:     OpenAIErrorDetail{Message: message, Type: errType, Code: code},
			RequestID: requestID,
		}
	}
}

// anthropicErrorType 网关内部错误分类 → Anthropic 原生 error.type。
// 分类语义优先（authentication/rate_limit 直映射），其余按状态码归类；
// 无对应形态的分类（如 insufficient_quota 402）归 invalid_request_error，语义留在 message。
func anthropicErrorType(errType string, status int) string {
	switch errType {
	case "authentication_error":
		return "authentication_error"
	case "rate_limit_error":
		return "rate_limit_error"
	}
	switch {
	case status == http.StatusUnauthorized:
		return "authentication_error"
	case status == http.StatusForbidden:
		return "permission_error"
	case status == http.StatusNotFound:
		return "not_found_error"
	case status == http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status >= 500:
		return "api_error"
	default:
		return "invalid_request_error"
	}
}

// UpstreamError 上游错误体解析出的语义字段（载体重建的输入）。
type UpstreamError struct {
	// Message 上游错误描述（出口前须由调用方脱敏）。
	Message string
	// Type 上游 error.type（openai / anthropic 形态）。
	Type string
	// Code 上游 error.code（openai 字符串码）或 gemini error.status（gRPC canonical 名）。
	Code string
}

// upstreamSnippetLen 纯文本兜底时的截断长度（字节）。
const upstreamSnippetLen = 200

// ParseUpstream 从上游错误体提取语义字段，依次尝试三种协议形态：
//
//	openai    {"error":{"message","type","code"}}
//	anthropic {"type":"error","error":{"type","message"}}
//	gemini    {"error":{"code":<int>,"message","status"}}
//	兜底      顶层 {"message":...} 或纯文本截断
//
// 全部提取失败（空体 / 无可用字段）时 message 为「上游返回状态码 N」，
// 原始响应体不进响应（只进失败留痕）。
func ParseUpstream(statusCode int, body []byte) UpstreamError {
	fallback := UpstreamError{Message: "上游返回状态码 " + strconv.Itoa(statusCode)}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return fallback
	}

	var probe struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Error   struct {
			Message string          `json:"message"`
			Type    string          `json:"type"`
			Code    json.RawMessage `json:"code"`
			Status  string          `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		// 非 JSON：纯文本截断作为 message；不可用（截断后为空）回落状态码文案。
		if s := textSnippet(trimmed); s != "" {
			return UpstreamError{Message: s}
		}
		return fallback
	}

	code := probe.Error.Status // gemini 的 canonical 名优先
	if code == "" {
		code = rawCodeString(probe.Error.Code)
	}
	if probe.Error.Message != "" || probe.Error.Type != "" || code != "" {
		up := UpstreamError{Message: probe.Error.Message, Type: probe.Error.Type, Code: code}
		if up.Message == "" {
			up.Message = fallback.Message
		}
		return up
	}
	if probe.Message != "" {
		return UpstreamError{Message: probe.Message}
	}
	return fallback
}

// rawCodeString 把上游 error.code 归一为字符串：字符串码去引号，数字码取字面量。
func rawCodeString(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(bytes.TrimSpace(raw))
}

// textSnippet 纯文本兜底截断（≤200 字节，非法 UTF-8 序列剔除）。
func textSnippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > upstreamSnippetLen {
		s = s[:upstreamSnippetLen]
	}
	return strings.TrimSpace(strings.ToValidUTF8(s, ""))
}

// RenderUpstream 按入口协议重建上游错误体（语义保留、载体重建）：
// HTTP 状态码保留上游原值；上游 type/code 能映射进入口协议字段则保留，
// 映射不上的按状态码取协议默认分类。
func RenderUpstream(protocol string, status int, up UpstreamError, requestID string) any {
	switch protocol {
	case registry.ProtocolAnthropic:
		return AnthropicError{
			Type: "error",
			Error: AnthropicErrorDetail{
				Type:    anthropicUpstreamType(up.Type, status),
				Message: up.Message,
			},
			RequestID: requestID,
		}
	case registry.ProtocolGemini:
		return GeminiError{Error: GeminiErrorDetail{
			Code:    status,
			Message: up.Message,
			Status:  geminiUpstreamStatus(up.Code, status),
		}}
	default:
		errType := up.Type
		if errType == "" {
			errType = openaiTypeForStatus(status)
		}
		return OpenAIError{
			Error:     OpenAIErrorDetail{Message: up.Message, Type: errType, Code: up.Code},
			RequestID: requestID,
		}
	}
}

// anthropicNativeErrorTypes Anthropic 原生 error.type 集合：上游给出的 type
// 属于该集合时原样保留，否则按状态码归类。
var anthropicNativeErrorTypes = map[string]struct{}{
	"invalid_request_error": {},
	"authentication_error":  {},
	"permission_error":      {},
	"not_found_error":       {},
	"request_too_large":     {},
	"rate_limit_error":      {},
	"timeout_error":         {},
	"billing_error":         {},
	"api_error":             {},
	"overloaded_error":      {},
}

// anthropicUpstreamType 上游 error.type → Anthropic 原生 error.type：
// 原生分类直接保留；映射不上的回落状态码归类（anthropicErrorType）。
func anthropicUpstreamType(errType string, status int) string {
	if _, ok := anthropicNativeErrorTypes[errType]; ok {
		return errType
	}
	return anthropicErrorType(errType, status)
}

// geminiUpstreamStatus 上游 error.status → Gemini status 字段：
// 形如 gRPC canonical 名（全大写下划线）时原样保留，否则按状态码推导。
func geminiUpstreamStatus(code string, status int) string {
	if isCanonicalCode(code) {
		return code
	}
	return geminiStatus(status)
}

// isCanonicalCode 判断字符串是否形如 gRPC canonical code 名（如 RESOURCE_EXHAUSTED）。
func isCanonicalCode(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'A' || r > 'Z') && r != '_' {
			return false
		}
	}
	return true
}

// openaiTypeForStatus 上游未给 error.type 时按状态码取 OpenAI 默认分类。
func openaiTypeForStatus(status int) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "authentication_error"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status >= 500:
		return "api_error"
	default:
		return "invalid_request_error"
	}
}

// geminiStatus HTTP 状态码 → gRPC canonical code 名（Google API 错误惯例）。
func geminiStatus(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusInternalServerError:
		return "INTERNAL"
	case http.StatusNotImplemented:
		return "UNIMPLEMENTED"
	case http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	default:
		if status >= 500 {
			// 502/503 等网关型 5xx。
			return "UNAVAILABLE"
		}
		// 402 余额不足等无 canonical 对应的 4xx。
		return "FAILED_PRECONDITION"
	}
}
