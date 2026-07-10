// Package openaivideo 实现 openai_video 平台适配器（OpenAI 视频任务，Sora 形态）：
// 提交 POST {base}/v1/videos（JSON / multipart 透传，仅定点重写 model），
// 查询 GET {base}/v1/videos/{id}，成片 GET {base}/v1/videos/{id}/content。
package openaivideo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/pkg/multipartform"
	openaiadaptor "github.com/DouDOU-start/airgate-core/internal/relay/adaptor/openai"
	"github.com/DouDOU-start/airgate-core/internal/relay/task"
)

func init() {
	task.Register(task.PlatformOpenAIVideo, func() task.Adaptor { return Adaptor{} })
}

// Adaptor openai_video 平台适配器（无状态）。
type Adaptor struct{}

// ParseSubmit 从提交请求提取 model / seconds（JSON 顶层字段或 multipart 普通字段）。
func (Adaptor) ParseSubmit(_ string, contentType string, body []byte) (*task.SubmitRequest, error) {
	var model string
	var seconds int
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "multipart/") {
		fields, err := multipartform.ExtractFields(body, contentType, "model", "seconds")
		if err != nil {
			return nil, fmt.Errorf("multipart 请求体解析失败: %w", err)
		}
		model = fields["model"]
		seconds = parseSecondsString(fields["seconds"])
	} else {
		var probe struct {
			Model   string          `json:"model"`
			Seconds json.RawMessage `json:"seconds"`
		}
		if err := json.Unmarshal(body, &probe); err != nil {
			return nil, errors.New("请求体必须是 JSON 对象")
		}
		model = probe.Model
		seconds = parseSecondsRaw(probe.Seconds)
	}
	if model == "" {
		return nil, errors.New("缺少 model 字段")
	}
	if contentType == "" {
		contentType = "application/json"
	}
	return &task.SubmitRequest{
		Model:       model,
		Action:      "generate",
		Seconds:     seconds,
		Body:        body,
		ContentType: contentType,
	}, nil
}

// BuildSubmitRequest 构建上游提交请求：体透传，仅在渠道 model_mapping 生效时
// 定点重写 model（JSON 字段改写 / multipart 复用同步 openai 适配器的定点重写）。
func (Adaptor) BuildSubmitRequest(ctx context.Context, info *task.Info, req *task.SubmitRequest) (*http.Request, error) {
	body := req.Body
	if info.UpstreamModel != "" && info.UpstreamModel != info.RequestModel {
		var err error
		if strings.HasPrefix(strings.ToLower(req.ContentType), "multipart/") {
			body, err = openaiadaptor.RewriteMultipartModel(req.Body, req.ContentType, info.UpstreamModel)
		} else {
			body, err = rewriteJSONModel(req.Body, info.UpstreamModel)
		}
		if err != nil {
			return nil, err
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, videosURL(info.Channel.BaseURL), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", req.ContentType)
	setAuthHeaders(httpReq, info)
	return httpReq, nil
}

// ParseSubmitResponse 从上游提交响应提取 task_id 与初始状态（video 对象）。
func (Adaptor) ParseSubmitResponse(body []byte) (string, *task.Status, error) {
	st, id, err := parseVideoObject(body)
	if err != nil {
		return "", nil, err
	}
	if id == "" {
		return "", nil, errors.New("提交响应缺少任务 id")
	}
	return id, st, nil
}

// BuildQueryRequest 构建上游任务查询请求。
func (Adaptor) BuildQueryRequest(ctx context.Context, info *task.Info, taskID string) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, videosURL(info.Channel.BaseURL)+"/"+taskID, nil)
	if err != nil {
		return nil, err
	}
	setAuthHeaders(httpReq, info)
	return httpReq, nil
}

// ParseQueryResponse 归一化上游查询响应（video 对象）。
func (Adaptor) ParseQueryResponse(body []byte) (*task.Status, error) {
	st, _, err := parseVideoObject(body)
	return st, err
}

// BuildContentRequest 实现 task.ContentProxy：成片内容下载。
func (Adaptor) BuildContentRequest(ctx context.Context, info *task.Info, taskID string) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, videosURL(info.Channel.BaseURL)+"/"+taskID+"/content", nil)
	if err != nil {
		return nil, err
	}
	setAuthHeaders(httpReq, info)
	return httpReq, nil
}

// RenderTask 由本地任务行重建 OpenAI video 对象：有上游快照时以快照为底、
// 覆盖 model（隐藏 model_mapping）与 status/progress（本地行为事实源）；
// 无快照时构建最小合法对象。
func (Adaptor) RenderTask(t *task.Task) []byte {
	fields := map[string]json.RawMessage{}
	if len(t.Data) > 0 {
		_ = json.Unmarshal(t.Data, &fields)
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	setJSON := func(k string, v any) {
		if raw, err := json.Marshal(v); err == nil {
			fields[k] = raw
		}
	}
	if _, ok := fields["id"]; !ok {
		setJSON("id", t.TaskID)
	}
	if _, ok := fields["object"]; !ok {
		setJSON("object", "video")
	}
	if _, ok := fields["created_at"]; !ok {
		setJSON("created_at", t.SubmitTime.Unix())
	}
	if _, ok := fields["seconds"]; !ok && t.Seconds > 0 {
		setJSON("seconds", strconv.Itoa(t.Seconds))
	}
	setJSON("model", t.RequestModel)
	setJSON("status", upstreamStatusName(t.Status))
	setJSON("progress", t.Progress)
	if t.Status == task.StatusFailure {
		if _, ok := fields["error"]; !ok && t.FailReason != "" {
			setJSON("error", map[string]string{"message": t.FailReason})
		}
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return []byte(`{"id":"` + t.TaskID + `","object":"video"}`)
	}
	return out
}

// parseVideoObject 解析 OpenAI video 对象为归一化状态与任务 ID。
func parseVideoObject(body []byte) (*task.Status, string, error) {
	var probe struct {
		ID       string          `json:"id"`
		Status   string          `json:"status"`
		Progress json.RawMessage `json:"progress"`
		Seconds  json.RawMessage `json:"seconds"`
		Error    *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, "", fmt.Errorf("响应不是合法 JSON 对象: %w", err)
	}
	st := &task.Status{
		Status:   normalizeStatus(probe.Status),
		Progress: parseSecondsRaw(probe.Progress),
		Seconds:  parseSecondsRaw(probe.Seconds),
		Raw:      json.RawMessage(body),
	}
	if probe.Error != nil {
		st.FailReason = probe.Error.Message
	}
	if st.Status == task.StatusFailure && st.FailReason == "" {
		st.FailReason = "上游任务失败"
	}
	return st, probe.ID, nil
}

// normalizeStatus 上游状态词表 → 内部状态。未知非空状态按 in_progress 处理
// （保守：轮询继续跟进，不误判终态）。
func normalizeStatus(s string) string {
	switch strings.ToLower(s) {
	case "queued", "pending":
		return task.StatusQueued
	case "in_progress", "processing", "running":
		return task.StatusInProgress
	case "completed", "succeeded", "success":
		return task.StatusSuccess
	case "failed", "error", "cancelled", "canceled":
		return task.StatusFailure
	case "":
		return task.StatusSubmitted
	default:
		return task.StatusInProgress
	}
}

// upstreamStatusName 内部状态 → OpenAI video 状态词表（RenderTask 出口）。
func upstreamStatusName(s string) string {
	switch s {
	case task.StatusSubmitted, task.StatusQueued:
		return "queued"
	case task.StatusInProgress:
		return "in_progress"
	case task.StatusSuccess:
		return "completed"
	case task.StatusFailure:
		return "failed"
	default:
		return s
	}
}

// parseSecondsRaw 解析可能为数字或字符串的整数字段（OpenAI seconds 为字符串 "8"）。
func parseSecondsRaw(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return int(n)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return parseSecondsString(s)
	}
	return 0
}

// parseSecondsString 字符串秒数解析；非法返回 0。
func parseSecondsString(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return 0
}

// rewriteJSONModel 定点重写 JSON 体的 model 字段（其余字段原样保留）。
func rewriteJSONModel(body []byte, upstreamModel string) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, errors.New("请求体必须是 JSON 对象")
	}
	raw, err := json.Marshal(upstreamModel)
	if err != nil {
		return nil, err
	}
	fields["model"] = raw
	return json.Marshal(fields)
}

// setAuthHeaders 设置认证头 + 渠道 header_override。
func setAuthHeaders(req *http.Request, info *task.Info) {
	req.Header.Set("Authorization", "Bearer "+info.APIKey)
	for k, v := range info.Channel.HeaderOverride {
		req.Header.Set(k, v)
	}
}

// videosURL 拼接上游视频任务端点（{base}/v1/videos，/v1 不重复拼接）。
func videosURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return base + "/videos"
}
