package bark

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPushPostsJSONToServer(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("Content-Type = %q", ct)
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
	}))
	defer server.Close()

	client := New()
	err := client.Push(context.Background(), server.URL, "device-key-xyz", Message{
		Title: "渠道暂停",
		Body:  "测试内容",
		URL:   "https://example.com/admin/channels",
		Group: "AirGate",
		Sound: "alarm",
		Level: "timeSensitive",
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if gotPath != "/push" {
		t.Fatalf("path = %q, want /push", gotPath)
	}
	if gotBody["device_key"] != "device-key-xyz" {
		t.Fatalf("device_key = %#v", gotBody["device_key"])
	}
	if gotBody["title"] != "渠道暂停" || gotBody["body"] != "测试内容" {
		t.Fatalf("title/body = %#v %#v", gotBody["title"], gotBody["body"])
	}
	if gotBody["group"] != "AirGate" || gotBody["level"] != "timeSensitive" {
		t.Fatalf("group/level = %#v %#v", gotBody["group"], gotBody["level"])
	}
}

func TestPushUsesDefaultServerAndRejectsEmptyKey(t *testing.T) {
	client := New()
	if err := client.Push(context.Background(), "", "", Message{Body: "x"}); err == nil {
		t.Fatal("空 device key 应失败")
	}
	if err := client.Push(context.Background(), "", "key", Message{}); err == nil {
		t.Fatal("空标题与内容应失败")
	}
}

func TestPushManyAggregatesErrors(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if body["device_key"] == "bad-key" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":400,"message":"bad"}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
	}))
	defer server.Close()

	client := New()
	err := client.PushMany(context.Background(), server.URL, []string{"good-key", "bad-key"}, Message{
		Title: "t", Body: "b",
	})
	if err == nil {
		t.Fatal("期望聚合错误")
	}
	if !strings.Contains(err.Error(), "bad-key") && !strings.Contains(err.Error(), "bad") {
		t.Fatalf("错误信息应包含失败原因：%v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestPushURL(t *testing.T) {
	if got := pushURL(""); got != "https://api.day.app/push" {
		t.Fatalf("default = %q", got)
	}
	if got := pushURL("https://bark.example.com/"); got != "https://bark.example.com/push" {
		t.Fatalf("custom = %q", got)
	}
}
