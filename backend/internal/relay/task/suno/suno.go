// Package suno 实现 suno 平台适配器（Suno-API 社区协议）：
// 提交 POST {base}/suno/submit/{music|lyrics}，
// 查询 GET {base}/suno/fetch/{id}、批量 POST {base}/suno/fetch。
// 计费模型名由 action 合成（suno_music / suno_lyrics）：渠道模型列表与
// 价目表（per_request_price）须配置这两个模型名。
package suno

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/relay/task"
)

func init() {
	task.Register(task.PlatformSuno, func() task.Adaptor { return Adaptor{} })
}

// Adaptor suno 平台适配器（无状态）。
type Adaptor struct{}

// 支持的动作集合（URL path 段，小写归一）。
var validActions = map[string]struct{}{"music": {}, "lyrics": {}}

// ParseSubmit 校验 action 并合成计费模型名；请求体原样透传。
func (Adaptor) ParseSubmit(action, contentType string, body []byte) (*task.SubmitRequest, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	if _, ok := validActions[action]; !ok {
		return nil, errors.New("不支持的 action（仅支持 music / lyrics）")
	}
	if !json.Valid(body) {
		return nil, errors.New("请求体必须是合法 JSON")
	}
	if contentType == "" {
		contentType = "application/json"
	}
	return &task.SubmitRequest{
		Model:       "suno_" + action,
		Action:      action,
		Body:        body,
		ContentType: contentType,
	}, nil
}

// BuildSubmitRequest 构建上游提交请求（体透传；suno 无 model 字段，无需重写）。
func (Adaptor) BuildSubmitRequest(ctx context.Context, info *task.Info, req *task.SubmitRequest) (*http.Request, error) {
	url := sunoBase(info.Channel.BaseURL) + "/submit/" + req.Action
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(req.Body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", req.ContentType)
	setAuthHeaders(httpReq, info)
	return httpReq, nil
}

// envelope Suno-API 通用响应包裹。
type envelope struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// ParseSubmitResponse 提交响应：{"code":"success","data":"<task_id>"}。
func (Adaptor) ParseSubmitResponse(body []byte) (string, *task.Status, error) {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return "", nil, fmt.Errorf("提交响应不是合法 JSON: %w", err)
	}
	if !strings.EqualFold(env.Code, "success") {
		return "", nil, fmt.Errorf("上游提交失败: code=%s message=%s", env.Code, env.Message)
	}
	var taskID string
	if err := json.Unmarshal(env.Data, &taskID); err != nil || taskID == "" {
		return "", nil, errors.New("提交响应缺少任务 ID")
	}
	return taskID, &task.Status{Status: task.StatusSubmitted}, nil
}

// BuildQueryRequest 单任务查询（GET {base}/suno/fetch/{id}）。
func (Adaptor) BuildQueryRequest(ctx context.Context, info *task.Info, taskID string) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, sunoBase(info.Channel.BaseURL)+"/fetch/"+taskID, nil)
	if err != nil {
		return nil, err
	}
	setAuthHeaders(httpReq, info)
	return httpReq, nil
}

// ParseQueryResponse 单任务查询响应：{"code":"success","data":{item}}。
func (Adaptor) ParseQueryResponse(body []byte) (*task.Status, error) {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("查询响应不是合法 JSON: %w", err)
	}
	if !strings.EqualFold(env.Code, "success") {
		return nil, fmt.Errorf("上游查询失败: code=%s message=%s", env.Code, env.Message)
	}
	st, _, err := parseItem(env.Data)
	return st, err
}

// BuildBatchQueryRequest 实现 task.BatchQuerying：POST {base}/suno/fetch {"ids":[...]}。
func (Adaptor) BuildBatchQueryRequest(ctx context.Context, info *task.Info, taskIDs []string) (*http.Request, error) {
	payload, err := json.Marshal(map[string][]string{"ids": taskIDs})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, sunoBase(info.Channel.BaseURL)+"/fetch", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	setAuthHeaders(httpReq, info)
	return httpReq, nil
}

// ParseBatchQueryResponse 批量查询响应：{"code":"success","data":[items]}。
func (Adaptor) ParseBatchQueryResponse(body []byte) (map[string]*task.Status, error) {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("批量查询响应不是合法 JSON: %w", err)
	}
	if !strings.EqualFold(env.Code, "success") {
		return nil, fmt.Errorf("上游批量查询失败: code=%s message=%s", env.Code, env.Message)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(env.Data, &items); err != nil {
		return nil, errors.New("批量查询响应 data 不是数组")
	}
	out := make(map[string]*task.Status, len(items))
	for _, raw := range items {
		st, id, err := parseItem(raw)
		if err != nil || id == "" {
			continue
		}
		out[id] = st
	}
	return out, nil
}

// RenderTask 单任务查询响应重建：{"code":"success","data":{item}}。
func (Adaptor) RenderTask(t *task.Task) []byte {
	out, err := json.Marshal(map[string]any{
		"code":    "success",
		"message": "",
		"data":    renderItem(t),
	})
	if err != nil {
		return []byte(`{"code":"success","message":"","data":null}`)
	}
	return out
}

// RenderTaskList 批量查询响应重建：{"code":"success","data":[items]}。
func (Adaptor) RenderTaskList(ts []*task.Task) []byte {
	items := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		items = append(items, renderItem(t))
	}
	out, err := json.Marshal(map[string]any{
		"code":    "success",
		"message": "",
		"data":    items,
	})
	if err != nil {
		return []byte(`{"code":"success","message":"","data":[]}`)
	}
	return out
}

// renderItem 由本地任务行重建 Suno-API 任务项：有上游快照时透出其 data 明细
// （clips/歌词等产物），状态/进度以本地行为事实源。
func renderItem(t *task.Task) map[string]any {
	item := map[string]any{
		"task_id":     t.TaskID,
		"action":      strings.ToUpper(t.Action),
		"status":      upstreamStatusName(t.Status),
		"fail_reason": t.FailReason,
		"progress":    strconv.Itoa(t.Progress) + "%",
		"submit_time": t.SubmitTime.Unix(),
	}
	if t.FinishTime != nil {
		item["finish_time"] = t.FinishTime.Unix()
	}
	if len(t.Data) > 0 {
		var snapshot struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(t.Data, &snapshot); err == nil && len(snapshot.Data) > 0 {
			item["data"] = snapshot.Data
		}
	}
	return item
}

// parseItem 解析 Suno-API 任务项为归一化状态。
func parseItem(raw json.RawMessage) (*task.Status, string, error) {
	var item struct {
		TaskID     string `json:"task_id"`
		Status     string `json:"status"`
		FailReason string `json:"fail_reason"`
		Progress   string `json:"progress"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, "", fmt.Errorf("任务项不是合法 JSON 对象: %w", err)
	}
	st := &task.Status{
		Status:     normalizeStatus(item.Status),
		Progress:   parseProgress(item.Progress),
		FailReason: item.FailReason,
		Raw:        raw,
	}
	if st.Status == task.StatusFailure && st.FailReason == "" {
		st.FailReason = "上游任务失败"
	}
	return st, item.TaskID, nil
}

// normalizeStatus Suno-API 状态词表（大写）→ 内部状态；未知非空按 in_progress。
func normalizeStatus(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "SUBMITTED", "NOT_START":
		return task.StatusSubmitted
	case "QUEUED":
		return task.StatusQueued
	case "IN_PROGRESS":
		return task.StatusInProgress
	case "SUCCESS":
		return task.StatusSuccess
	case "FAILURE":
		return task.StatusFailure
	case "":
		return task.StatusSubmitted
	default:
		return task.StatusInProgress
	}
}

// upstreamStatusName 内部状态 → Suno-API 状态词表（RenderTask 出口）。
func upstreamStatusName(s string) string {
	switch s {
	case task.StatusSubmitted:
		return "SUBMITTED"
	case task.StatusQueued:
		return "QUEUED"
	case task.StatusInProgress:
		return "IN_PROGRESS"
	case task.StatusSuccess:
		return "SUCCESS"
	case task.StatusFailure:
		return "FAILURE"
	default:
		return strings.ToUpper(s)
	}
}

// parseProgress "45%" → 45；非法返回 0。
func parseProgress(s string) int {
	s = strings.TrimSuffix(strings.TrimSpace(s), "%")
	if s == "" {
		return 0
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 0 && n <= 100 {
		return n
	}
	return 0
}

// setAuthHeaders 设置认证头 + 渠道 header_override。
func setAuthHeaders(req *http.Request, info *task.Info) {
	req.Header.Set("Authorization", "Bearer "+info.APIKey)
	for k, v := range info.Channel.HeaderOverride {
		req.Header.Set(k, v)
	}
}

// sunoBase 拼接 Suno-API 前缀（{base}/suno，/suno 不重复拼接）。
func sunoBase(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/suno") {
		return base
	}
	return base + "/suno"
}
