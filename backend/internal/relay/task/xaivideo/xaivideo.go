// Package xaivideo 实现 xAI 原生视频任务响应的解析与重建。
// 实际 HTTP 请求由 task Flow/Poller 通过 CPA xAI executor 发出，适配器只负责
// 提取调度、计费字段以及维护 Grok Build 需要的原生响应形态。
package xaivideo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

// BuildSubmitRequest 构建 xAI 原生视频渠道请求。账号路径仍由 CPA 构建，
// 此方法只用于当前 AirGate 没有本地账号、需要级联到上游渠道的场景。
func (Adaptor) BuildSubmitRequest(ctx context.Context, info *task.Info, req *task.SubmitRequest) (*http.Request, error) {
	body := req.Body
	if info.UpstreamModel != "" && info.UpstreamModel != info.RequestModel {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
			return nil, errors.New("请求体必须是 JSON 对象")
		}
		model, err := json.Marshal(info.UpstreamModel)
		if err != nil {
			return nil, err
		}
		fields["model"] = model
		body, err = json.Marshal(fields)
		if err != nil {
			return nil, err
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, videosURL(info.ChannelKey.BaseURL)+"/generations", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", req.ContentType)
	setAuthHeaders(httpReq, info)
	return httpReq, nil
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

// BuildQueryRequest 构建 xAI 原生视频渠道查询请求。
func (Adaptor) BuildQueryRequest(ctx context.Context, info *task.Info, taskID string) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, videosURL(info.ChannelKey.BaseURL)+"/"+url.PathEscape(taskID), nil)
	if err != nil {
		return nil, err
	}
	setAuthHeaders(httpReq, info)
	return httpReq, nil
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

// setAuthHeaders 设置级联渠道认证头与管理员配置的覆盖头。
func setAuthHeaders(req *http.Request, info *task.Info) {
	req.Header.Set("Authorization", "Bearer "+info.APIKey)
	for key, value := range info.ChannelKey.HeaderOverride {
		req.Header.Set(key, value)
	}
}

// videosURL 拼接 xAI 原生视频端点根路径，避免 base_url 已含 /v1 时重复追加。
func videosURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return base + "/videos"
}
