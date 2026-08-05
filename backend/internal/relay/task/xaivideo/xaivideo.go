// Package xaivideo 实现 xAI 原生视频任务响应的解析与重建。
// 实际 HTTP 请求由 task Flow/Poller 通过 CPA xAI executor 发出，适配器只负责
// 提取调度、计费字段以及维护 Grok Build 需要的原生响应形态。
package xaivideo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/relay/task"
)

const defaultDurationSeconds = 6

func init() {
	task.Register(task.PlatformXAIVideo, func() task.Adaptor { return Adaptor{} })
}

// Adaptor xAI 原生视频任务适配器（无状态）。
type Adaptor struct{}

// ParseSubmit 从 xAI 原生 JSON 请求提取 model、duration 和 resolution。
func (Adaptor) ParseSubmit(_ string, contentType string, body []byte) (*task.SubmitRequest, error) {
	var probe struct {
		Model      string          `json:"model"`
		Duration   json.RawMessage `json:"duration"`
		Seconds    json.RawMessage `json:"seconds"`
		Resolution string          `json:"resolution"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, errors.New("请求体必须是 JSON 对象")
	}
	model := strings.TrimSpace(probe.Model)
	if model == "" {
		return nil, errors.New("缺少 model 字段")
	}
	seconds := parsePositiveInt(probe.Duration)
	if seconds <= 0 {
		seconds = parsePositiveInt(probe.Seconds)
	}
	if seconds <= 0 {
		seconds = defaultDurationSeconds
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/json"
	}
	return &task.SubmitRequest{
		Model:       model,
		Action:      "generate",
		Seconds:     seconds,
		Resolution:  strings.ToLower(strings.TrimSpace(probe.Resolution)),
		Body:        body,
		ContentType: contentType,
	}, nil
}

// BuildSubmitRequest 不应被账号路径调用；HTTP 请求统一交给 CPA 构建。
func (Adaptor) BuildSubmitRequest(context.Context, *task.Info, *task.SubmitRequest) (*http.Request, error) {
	return nil, errors.New("xAI 视频账号请求必须通过 CPA 转发")
}

// ParseSubmitResponse 提取 xAI 提交响应中的 request_id。
func (Adaptor) ParseSubmitResponse(body []byte) (string, *task.Status, error) {
	st, requestID, err := parseVideoObject(body)
	if err != nil {
		return "", nil, err
	}
	if requestID == "" {
		return "", nil, errors.New("提交响应缺少 request_id")
	}
	return requestID, st, nil
}

// BuildQueryRequest 不应被账号路径调用；轮询统一交给 CPA 构建。
func (Adaptor) BuildQueryRequest(context.Context, *task.Info, string) (*http.Request, error) {
	return nil, errors.New("xAI 视频账号查询必须通过 CPA 转发")
}

// ParseQueryResponse 归一化 xAI 原生轮询响应。
func (Adaptor) ParseQueryResponse(body []byte) (*task.Status, error) {
	st, _, err := parseVideoObject(body)
	return st, err
}

// RenderTask 从本地任务快照重建 xAI 原生响应。
// 提交响应与查询响应都保留 request_id，查询终态使用 done/failed 词表。
func (Adaptor) RenderTask(t *task.Task) []byte {
	fields := map[string]json.RawMessage{}
	if len(t.Data) > 0 {
		_ = json.Unmarshal(t.Data, &fields)
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	setJSON := func(key string, value any) {
		if raw, err := json.Marshal(value); err == nil {
			fields[key] = raw
		}
	}
	setJSON("request_id", t.TaskID)
	setJSON("model", t.RequestModel)
	setJSON("status", nativeStatusName(t.Status))
	setJSON("progress", t.Progress)
	if t.Status == task.StatusFailure && t.FailReason != "" {
		if _, exists := fields["error"]; !exists {
			setJSON("error", map[string]string{"message": t.FailReason})
		}
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return []byte(fmt.Sprintf(`{"request_id":%q,"status":%q}`, t.TaskID, nativeStatusName(t.Status)))
	}
	return out
}

func parseVideoObject(body []byte) (*task.Status, string, error) {
	var probe struct {
		RequestID string          `json:"request_id"`
		ID        string          `json:"id"`
		Status    string          `json:"status"`
		Progress  json.RawMessage `json:"progress"`
		Seconds   json.RawMessage `json:"seconds"`
		Duration  json.RawMessage `json:"duration"`
		Video     *struct {
			Duration json.RawMessage `json:"duration"`
		} `json:"video"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, "", fmt.Errorf("响应不是合法 JSON 对象: %w", err)
	}
	requestID := strings.TrimSpace(probe.RequestID)
	if requestID == "" {
		requestID = strings.TrimSpace(probe.ID)
	}
	seconds := parsePositiveInt(probe.Seconds)
	if seconds <= 0 {
		seconds = parsePositiveInt(probe.Duration)
	}
	if seconds <= 0 && probe.Video != nil {
		seconds = parsePositiveInt(probe.Video.Duration)
	}
	st := &task.Status{
		Status:     normalizeStatus(probe.Status),
		Progress:   parseNonNegativeInt(probe.Progress),
		FailReason: parseErrorMessage(probe.Error),
		Seconds:    seconds,
		Raw:        json.RawMessage(body),
	}
	if st.Status == task.StatusFailure && st.FailReason == "" {
		st.FailReason = "上游视频任务失败"
	}
	return st, requestID, nil
}

func normalizeStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "pending", "queued", "submitted":
		return task.StatusSubmitted
	case "processing", "running", "in_progress":
		return task.StatusInProgress
	case "done", "completed", "success", "succeeded":
		return task.StatusSuccess
	case "failed", "error", "expired", "cancelled", "canceled":
		return task.StatusFailure
	default:
		return task.StatusInProgress
	}
}

func nativeStatusName(value string) string {
	switch value {
	case task.StatusSubmitted, task.StatusQueued:
		return "pending"
	case task.StatusInProgress:
		return "processing"
	case task.StatusSuccess:
		return "done"
	case task.StatusFailure:
		return "failed"
	default:
		return value
	}
}

func parsePositiveInt(raw json.RawMessage) int {
	value := parseNonNegativeInt(raw)
	if value <= 0 {
		return 0
	}
	return value
}

func parseNonNegativeInt(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var number float64
	if json.Unmarshal(raw, &number) == nil && number >= 0 {
		return int(number)
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		value, _ := strconv.Atoi(strings.TrimSpace(text))
		if value >= 0 {
			return value
		}
	}
	return 0
}

func parseErrorMessage(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var detail struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &detail) == nil {
		return strings.TrimSpace(detail.Message)
	}
	return ""
}
