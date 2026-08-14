// Package streamerr 识别 SSE/JSON 流中的协议级错误事件，并把错误语义
// 归一为 HTTP 状态码，供转发管线在真实内容下发前安全执行 failover。
package streamerr

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// Event 是从上游流中识别出的错误事件。
type Event struct {
	StatusCode int
	Body       []byte
}

type errorDetail struct {
	Type    string          `json:"type"`
	Code    json.RawMessage `json:"code"`
	Status  string          `json:"status"`
	Message string          `json:"message"`
}

type eventEnvelope struct {
	Type     string          `json:"type"`
	Code     json.RawMessage `json:"code"`
	Status   string          `json:"status"`
	Message  string          `json:"message"`
	Error    json.RawMessage `json:"error"`
	Response *struct {
		Status string          `json:"status"`
		Error  json.RawMessage `json:"error"`
	} `json:"response"`
}

// Detect 从一段 SSE 帧或原始 JSON 中识别协议级错误事件。
// 普通内容事件返回 false；错误体会复制后返回，避免引用调用方复用的 chunk 缓冲。
func Detect(payload []byte) (Event, bool) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return Event{}, false
	}

	foundDataLine := false
	eventHint := ""
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	scanner.Buffer(make([]byte, 0, 4<<10), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "event:") {
			eventHint = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		foundDataLine = true
		data := bytes.TrimSpace([]byte(strings.TrimPrefix(line, "data:")))
		if event, ok := detectJSON(data, eventHint); ok {
			return event, true
		}
	}
	if foundDataLine {
		return Event{}, false
	}
	return detectJSON(trimmed, "")
}

func detectJSON(data []byte, eventHint string) (Event, bool) {
	if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return Event{}, false
	}
	var envelope eventEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Event{}, false
	}

	detail, hasDetail := parseDetail(envelope.Error)
	if !hasDetail && envelope.Response != nil {
		detail, hasDetail = parseDetail(envelope.Response.Error)
	}
	eventType := strings.ToLower(strings.TrimSpace(envelope.Type))
	hint := strings.ToLower(strings.TrimSpace(eventHint))
	errorSignal := rawErrorSignal(envelope.Error)
	if errorSignal == "" && envelope.Response != nil {
		errorSignal = rawErrorSignal(envelope.Response.Error)
	}
	isErrorEvent := isErrorEventType(eventType) || isErrorEventType(hint) || hasDetail || errorSignal != ""
	if !isErrorEvent {
		return Event{}, false
	}

	signals := []string{
		hint,
		eventType,
		rawCode(envelope.Code),
		envelope.Status,
		envelope.Message,
		detail.Type,
		rawCode(detail.Code),
		detail.Status,
		detail.Message,
		errorSignal,
	}
	if envelope.Response != nil {
		signals = append(signals, envelope.Response.Status)
	}
	return Event{
		StatusCode: inferStatus(strings.ToLower(strings.Join(signals, " "))),
		Body:       append([]byte(nil), data...),
	}, true
}

func isErrorEventType(eventType string) bool {
	return eventType == "error" || strings.HasSuffix(eventType, ".failed") ||
		strings.HasSuffix(eventType, "_error")
}

func parseDetail(raw json.RawMessage) (errorDetail, bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errorDetail{}, false
	}
	var detail errorDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		return errorDetail{}, false
	}
	return detail, detail.Type != "" || len(detail.Code) > 0 || detail.Status != "" || detail.Message != ""
}

func rawCode(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return string(bytes.TrimSpace(raw))
}

func rawErrorSignal(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("{}")) {
		return ""
	}
	var value string
	if json.Unmarshal(trimmed, &value) == nil {
		return strings.TrimSpace(value)
	}
	return string(trimmed)
}

func inferStatus(signals string) int {
	switch {
	case containsAny(signals, "request_too_large", "payload_too_large"):
		return http.StatusRequestEntityTooLarge
	case containsAny(signals, "context_too_large", "context_length", "invalid_request", "invalid_argument"):
		return http.StatusBadRequest
	case containsAny(signals, "rate_limit", "resource_exhausted", "too_many_requests"):
		return http.StatusTooManyRequests
	case containsAny(signals, "authentication", "invalid_api_key", "unauthorized", "unauthenticated"):
		return http.StatusUnauthorized
	case containsAny(signals, "permission", "forbidden"):
		return http.StatusForbidden
	case containsAny(signals, "billing", "insufficient_balance", "insufficient_quota", "payment_required"):
		return http.StatusPaymentRequired
	case containsAny(signals, "not_found"):
		return http.StatusNotFound
	case containsAny(signals,
		"server_is_overloaded", "overloaded", "service_unavailable", "server_error",
		"api_error", "internal_error", "unavailable", "timeout_error"):
		return http.StatusServiceUnavailable
	default:
		// 已明确是协议级错误事件但无法精确分类时，按临时上游故障处理；
		// 这样可在真实内容尚未下发时切换调度单元，而不会错误禁用凭证。
		return http.StatusBadGateway
	}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
