// Package errfmt 按入口协议渲染网关自产错误体（预检/限流/failover 耗尽/鉴权等）。
//
// 纯透传网关的错误契约：上游返回的错误体一律原样透传（不经此包）；
// 网关自身产生的错误按入口协议出原生形态——
//
//	openai    {"error":{"message","type","code"},"request_id"}
//	anthropic {"type":"error","error":{"type","message"},"request_id"}
//	gemini    {"error":{"code":<int>,"message","status"}}
//
// 被 relay pipeline（转发期错误）与 server middleware（鉴权期错误）共用，
// 保证同一路径上的所有错误形态一致。
package errfmt

import (
	"net/http"
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
	case 499:
		return "CANCELLED"
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
