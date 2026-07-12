package openaivideo

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/relay/task"
)

func testInfo(mapping map[string]string) *task.Info {
	return &task.Info{
		ChannelKey: &registry.ChannelKeySnapshot{
			KeyID: 1, Type: "openai_video", BaseURL: "https://up.example.com",
			ModelMapping:   mapping,
			HeaderOverride: map[string]string{"X-Extra": "1"},
		},
		APIKey:        "sk-up",
		RequestModel:  "sora-2",
		UpstreamModel: "sora-2",
	}
}

func TestParseSubmit(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
		wantModel   string
		wantSeconds int
		wantErr     bool
	}{
		{"JSON 数字秒数", "application/json", `{"model":"sora-2","seconds":8}`, "sora-2", 8, false},
		{"JSON 字符串秒数", "application/json", `{"model":"sora-2","seconds":"12"}`, "sora-2", 12, false},
		{"缺秒数按 0", "application/json", `{"model":"sora-2"}`, "sora-2", 0, false},
		{"缺 model 报错", "application/json", `{"seconds":4}`, "", 0, true},
		{"非 JSON 报错", "application/json", `hello`, "", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub, err := (Adaptor{}).ParseSubmit("", tc.contentType, []byte(tc.body))
			if tc.wantErr {
				if err == nil {
					t.Fatal("应报错")
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if sub.Model != tc.wantModel || sub.Seconds != tc.wantSeconds {
				t.Errorf("sub = %+v", sub)
			}
		})
	}
}

func TestBuildSubmitRequestRewritesModel(t *testing.T) {
	info := testInfo(map[string]string{"sora-2": "sora-2-upstream"})
	info.UpstreamModel = "sora-2-upstream"
	sub := &task.SubmitRequest{
		Model: "sora-2", Body: []byte(`{"model":"sora-2","prompt":"cat"}`),
		ContentType: "application/json",
	}
	req, err := (Adaptor{}).BuildSubmitRequest(context.Background(), info, sub)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.URL.String() != "https://up.example.com/v1/videos" {
		t.Errorf("url = %s", req.URL)
	}
	if req.Header.Get("Authorization") != "Bearer sk-up" || req.Header.Get("X-Extra") != "1" {
		t.Errorf("headers = %v", req.Header)
	}
	var body map[string]any
	_ = json.NewDecoder(req.Body).Decode(&body)
	if body["model"] != "sora-2-upstream" || body["prompt"] != "cat" {
		t.Errorf("body = %v", body)
	}
}

func TestParseQueryResponseStatusMapping(t *testing.T) {
	cases := []struct {
		body       string
		wantStatus string
		wantReason string
	}{
		{`{"id":"v1","status":"queued"}`, task.StatusQueued, ""},
		{`{"id":"v1","status":"in_progress","progress":40}`, task.StatusInProgress, ""},
		{`{"id":"v1","status":"completed","seconds":"8"}`, task.StatusSuccess, ""},
		{`{"id":"v1","status":"failed","error":{"message":"policy"}}`, task.StatusFailure, "policy"},
		{`{"id":"v1","status":"weird"}`, task.StatusInProgress, ""},
	}
	for _, tc := range cases {
		st, err := (Adaptor{}).ParseQueryResponse([]byte(tc.body))
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if st.Status != tc.wantStatus || st.FailReason != tc.wantReason {
			t.Errorf("body %s → %+v", tc.body, st)
		}
	}
}

func TestRenderTask(t *testing.T) {
	now := time.Unix(1700000000, 0)
	t.Run("有快照时覆盖 model/status", func(t *testing.T) {
		out := (Adaptor{}).RenderTask(&task.Task{
			TaskID: "v1", RequestModel: "sora-2", Status: task.StatusSuccess, Progress: 100,
			SubmitTime: now,
			Data:       []byte(`{"id":"v1","object":"video","model":"sora-2-upstream","status":"in_progress","progress":40,"size":"720x1280"}`),
		})
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatalf("非 JSON: %v", err)
		}
		if m["model"] != "sora-2" || m["status"] != "completed" || m["progress"] != float64(100) {
			t.Errorf("m = %v", m)
		}
		if m["size"] != "720x1280" {
			t.Error("上游快照其余字段应保留")
		}
	})
	t.Run("无快照最小重建", func(t *testing.T) {
		out := (Adaptor{}).RenderTask(&task.Task{
			TaskID: "v2", RequestModel: "sora-2", Status: task.StatusFailure,
			FailReason: "超时", SubmitTime: now, Seconds: 4,
		})
		s := string(out)
		for _, want := range []string{`"id":"v2"`, `"status":"failed"`, `"超时"`} {
			if !strings.Contains(s, want) {
				t.Errorf("out = %s 缺 %s", s, want)
			}
		}
	})
}

func TestBuildQueryAndContentRequest(t *testing.T) {
	info := testInfo(nil)
	q, err := (Adaptor{}).BuildQueryRequest(context.Background(), info, "v1")
	if err != nil || q.Method != http.MethodGet || q.URL.String() != "https://up.example.com/v1/videos/v1" {
		t.Errorf("query = %v, err = %v", q.URL, err)
	}
	c, err := (Adaptor{}).BuildContentRequest(context.Background(), info, "v1")
	if err != nil || c.URL.String() != "https://up.example.com/v1/videos/v1/content" {
		t.Errorf("content = %v, err = %v", c.URL, err)
	}
}
