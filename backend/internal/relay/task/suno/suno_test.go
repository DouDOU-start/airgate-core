package suno

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/relay/task"
)

func testInfo() *task.Info {
	return &task.Info{
		Channel: &registry.ChannelSnapshot{
			ID: 1, Type: "suno", BaseURL: "https://up.example.com",
		},
		APIKey: "sk-up",
	}
}

func TestParseSubmit(t *testing.T) {
	cases := []struct {
		action    string
		wantModel string
		wantErr   bool
	}{
		{"music", "suno_music", false},
		{"MUSIC", "suno_music", false},
		{"lyrics", "suno_lyrics", false},
		{"video", "", true},
		{"", "", true},
	}
	for _, tc := range cases {
		sub, err := (Adaptor{}).ParseSubmit(tc.action, "application/json", []byte(`{"prompt":"x"}`))
		if tc.wantErr {
			if err == nil {
				t.Errorf("action %q 应报错", tc.action)
			}
			continue
		}
		if err != nil || sub.Model != tc.wantModel {
			t.Errorf("action %q → %+v, err = %v", tc.action, sub, err)
		}
	}
	if _, err := (Adaptor{}).ParseSubmit("music", "application/json", []byte(`not-json`)); err == nil {
		t.Error("非 JSON 体应报错")
	}
}

func TestParseSubmitResponse(t *testing.T) {
	id, st, err := (Adaptor{}).ParseSubmitResponse([]byte(`{"code":"success","message":"","data":"task-9"}`))
	if err != nil || id != "task-9" || st.Status != task.StatusSubmitted {
		t.Fatalf("id = %q, st = %+v, err = %v", id, st, err)
	}
	if _, _, err := (Adaptor{}).ParseSubmitResponse([]byte(`{"code":"fail","message":"no credit"}`)); err == nil {
		t.Error("code=fail 应报错")
	}
}

func TestParseBatchQueryResponse(t *testing.T) {
	body := `{"code":"success","data":[
		{"task_id":"a","status":"SUCCESS","progress":"100%","data":{"clips":[1]}},
		{"task_id":"b","status":"IN_PROGRESS","progress":"45%"}
	]}`
	sts, err := (Adaptor{}).ParseBatchQueryResponse([]byte(body))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if sts["a"].Status != task.StatusSuccess || sts["a"].Progress != 100 {
		t.Errorf("a = %+v", sts["a"])
	}
	if sts["b"].Status != task.StatusInProgress || sts["b"].Progress != 45 {
		t.Errorf("b = %+v", sts["b"])
	}
}

func TestRenderTaskEnvelope(t *testing.T) {
	now := time.Unix(1700000000, 0)
	out := (Adaptor{}).RenderTask(&task.Task{
		TaskID: "a", Action: "music", Status: task.StatusSuccess, Progress: 100,
		SubmitTime: now,
		Data:       []byte(`{"task_id":"a","status":"SUCCESS","data":{"clips":[{"audio_url":"http://x"}]}}`),
	})
	var env struct {
		Code string `json:"code"`
		Data struct {
			TaskID string          `json:"task_id"`
			Status string          `json:"status"`
			Action string          `json:"action"`
			Data   json.RawMessage `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("非 JSON: %v", err)
	}
	if env.Code != "success" || env.Data.TaskID != "a" || env.Data.Status != "SUCCESS" || env.Data.Action != "MUSIC" {
		t.Errorf("env = %+v", env)
	}
	if len(env.Data.Data) == 0 {
		t.Error("上游产物明细（clips）应透出")
	}
}

func TestBuildRequests(t *testing.T) {
	info := testInfo()
	s, err := (Adaptor{}).BuildSubmitRequest(context.Background(), info, &task.SubmitRequest{
		Action: "music", Body: []byte(`{}`), ContentType: "application/json",
	})
	if err != nil || s.URL.String() != "https://up.example.com/suno/submit/music" {
		t.Errorf("submit = %v, err = %v", s.URL, err)
	}
	if s.Header.Get("Authorization") != "Bearer sk-up" {
		t.Errorf("auth = %s", s.Header.Get("Authorization"))
	}
	q, err := (Adaptor{}).BuildQueryRequest(context.Background(), info, "a")
	if err != nil || q.URL.String() != "https://up.example.com/suno/fetch/a" {
		t.Errorf("query = %v", q.URL)
	}
	b, err := (Adaptor{}).BuildBatchQueryRequest(context.Background(), info, []string{"a", "b"})
	if err != nil || b.URL.String() != "https://up.example.com/suno/fetch" {
		t.Errorf("batch = %v", b.URL)
	}
}
