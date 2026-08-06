package xaivideo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/relay/task"
)

func TestParseSubmitAndResponse(t *testing.T) {
	ad := Adaptor{}
	sub, err := ad.ParseSubmit("", "application/json", []byte(`{
		"model":"grok-imagine-video-1.5-preview",
		"prompt":"生成海面日落",
		"duration":6,
		"resolution":"720p"
	}`))
	if err != nil {
		t.Fatalf("解析提交请求失败: %v", err)
	}
	if sub.Model != "grok-imagine-video-1.5-preview" || sub.Seconds != 6 || sub.Resolution != "720p" {
		t.Fatalf("提交字段解析错误: %+v", sub)
	}

	id, st, err := ad.ParseSubmitResponse([]byte(`{"request_id":"vid_123"}`))
	if err != nil {
		t.Fatalf("解析提交响应失败: %v", err)
	}
	if id != "vid_123" || st.Status != task.StatusSubmitted {
		t.Fatalf("提交响应解析错误: id=%q status=%q", id, st.Status)
	}
}

func TestParseSubmitUsesXAIDefaultDuration(t *testing.T) {
	sub, err := (Adaptor{}).ParseSubmit("", "application/json", []byte(`{
		"model":"grok-imagine-video",
		"prompt":"海面日落"
	}`))
	if err != nil {
		t.Fatalf("解析提交请求失败: %v", err)
	}
	if sub.Seconds != defaultDurationSeconds {
		t.Fatalf("默认时长 = %d，期望 %d", sub.Seconds, defaultDurationSeconds)
	}
}

func TestParseDoneResponseAndRenderNativeStatus(t *testing.T) {
	ad := Adaptor{}
	body := []byte(`{
		"status":"done",
		"progress":100,
		"video":{"url":"https://example.com/video.mp4","duration":8}
	}`)
	st, err := ad.ParseQueryResponse(body)
	if err != nil {
		t.Fatalf("解析查询响应失败: %v", err)
	}
	if st.Status != task.StatusSuccess || st.Progress != 100 || st.Seconds != 8 {
		t.Fatalf("查询状态解析错误: %+v", st)
	}

	rendered := ad.RenderTask(&task.Task{
		TaskID: "vid_123", Status: task.StatusSuccess, Progress: 100,
		RequestModel: "grok-imagine-video", Data: body, SubmitTime: time.Now(),
	})
	var got struct {
		RequestID string `json:"request_id"`
		Status    string `json:"status"`
		Video     struct {
			URL string `json:"url"`
		} `json:"video"`
	}
	if err := json.Unmarshal(rendered, &got); err != nil {
		t.Fatalf("重建响应不是合法 JSON: %v", err)
	}
	if got.RequestID != "vid_123" || got.Status != "done" || got.Video.URL == "" {
		t.Fatalf("原生响应重建错误: %s", rendered)
	}
}

func TestParseFailedResponse(t *testing.T) {
	st, err := (Adaptor{}).ParseQueryResponse([]byte(`{
		"status":"failed",
		"error":{"message":"内容审核未通过"}
	}`))
	if err != nil {
		t.Fatalf("解析失败响应失败: %v", err)
	}
	if st.Status != task.StatusFailure || st.FailReason != "内容审核未通过" {
		t.Fatalf("失败状态解析错误: %+v", st)
	}
}

func TestBuildChannelRequestsKeepXAIVideoPaths(t *testing.T) {
	ad := Adaptor{}
	info := &task.Info{
		ChannelKey: &registry.ChannelKeySnapshot{
			BaseURL: "https://upstream.example/v1",
			APIKey:  "sk-upstream",
			HeaderOverride: map[string]string{
				"X-Upstream": "cascade",
			},
		},
		APIKey:        "sk-upstream",
		RequestModel:  "grok-video",
		UpstreamModel: "grok-video-upstream",
	}
	sub := &task.SubmitRequest{
		Model:       "grok-video",
		Body:        []byte(`{"model":"grok-video","prompt":"海面日落"}`),
		ContentType: "application/json",
	}

	submit, err := ad.BuildSubmitRequest(context.Background(), info, sub)
	if err != nil {
		t.Fatalf("构建提交请求失败: %v", err)
	}
	if submit.Method != http.MethodPost || submit.URL.Path != "/v1/videos/generations" {
		t.Fatalf("提交请求 = %s %s", submit.Method, submit.URL.Path)
	}
	if submit.Header.Get("Authorization") != "Bearer sk-upstream" || submit.Header.Get("X-Upstream") != "cascade" {
		t.Fatalf("提交请求头错误: %+v", submit.Header)
	}
	body, err := io.ReadAll(submit.Body)
	if err != nil {
		t.Fatalf("读取提交请求体失败: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil || fields["model"] != "grok-video-upstream" || fields["prompt"] != "海面日落" {
		t.Fatalf("提交请求体未保持语义: %s", body)
	}

	query, err := ad.BuildQueryRequest(context.Background(), info, "vid_123")
	if err != nil {
		t.Fatalf("构建查询请求失败: %v", err)
	}
	if query.Method != http.MethodGet || query.URL.Path != "/v1/videos/vid_123" {
		t.Fatalf("查询请求 = %s %s", query.Method, query.URL.Path)
	}
}
