package account

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseAntigravityUsage转换配额窗口(t *testing.T) {
	now := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	snapshot, err := parseAntigravityUsage([]byte(`{
		"groups":[{
			"displayName":"Gemini 3 Flash",
			"buckets":[
				{
					"bucketId":"flash-5h",
					"displayName":"标准额度",
					"window":"5h",
					"remainingFraction":0.75,
					"resetTime":"2026-08-07T09:00:00Z"
				},
				{
					"bucket_id":"flash-weekly",
					"window":"7 days",
					"remaining_fraction":"20%",
					"reset_time":"2026-08-14T04:00:00Z"
				}
			]
		}]
	}`), now)
	if err != nil {
		t.Fatalf("解析 Antigravity 用量失败: %v", err)
	}
	if len(snapshot.Windows) != 2 {
		t.Fatalf("窗口数量 = %d，期望 2: %+v", len(snapshot.Windows), snapshot.Windows)
	}
	first := snapshot.Windows[0]
	if first.Key != "5h" || first.WindowMinutes != 300 || first.UsedPercent != 25 {
		t.Fatalf("首个窗口转换不正确: %+v", first)
	}
	if first.LimitID != "flash-5h" || first.LimitName != "Gemini 3 Flash · 标准额度" {
		t.Fatalf("首个窗口配额标识不正确: %+v", first)
	}
	if first.ResetsAt == nil || !first.ResetsAt.Equal(time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("首个窗口重置时间不正确: %+v", first.ResetsAt)
	}
	second := snapshot.Windows[1]
	if second.Key != "weekly" || second.WindowMinutes != 10080 || second.UsedPercent != 80 {
		t.Fatalf("第二个窗口转换不正确: %+v", second)
	}
}

func TestFetchAntigravityUsage支持地址回退(t *testing.T) {
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "临时不可用", http.StatusServiceUnavailable)
	}))
	defer failed.Close()

	succeeded := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Fatalf("请求方法 = %s，期望 POST", request.Method)
		}
		if request.Header.Get("Authorization") != "Bearer 测试访问令牌" {
			t.Fatalf("Authorization 请求头不正确: %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("User-Agent") != antigravityUsageUserAgent {
			t.Fatalf("User-Agent 请求头不正确: %q", request.Header.Get("User-Agent"))
		}
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("解析请求体失败: %v", err)
		}
		if body["project"] != "测试项目" {
			t.Fatalf("请求 project 不正确: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"groups":[{
				"displayName":"Gemini 3 Pro",
				"buckets":[{"bucketId":"pro-daily","window":"1d","remainingFraction":0.6}]
			}]
		}`))
	}))
	defer succeeded.Close()

	snapshot, err := fetchAntigravityUsageFromURLs(
		context.Background(),
		"测试访问令牌",
		"测试项目",
		"pro",
		&http.Client{Timeout: 2 * time.Second},
		[]string{failed.URL, succeeded.URL},
	)
	if err != nil {
		t.Fatalf("Antigravity 地址回退失败: %v", err)
	}
	if snapshot.PlanType != "pro" || len(snapshot.Windows) != 1 {
		t.Fatalf("Antigravity 用量快照不完整: %+v", snapshot)
	}
	if snapshot.Windows[0].Key != "daily" || snapshot.Windows[0].UsedPercent != 40 {
		t.Fatalf("Antigravity 日窗口转换不正确: %+v", snapshot.Windows[0])
	}
}

func TestFetchAntigravityUsage保留认证错误(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"unauthenticated:bad-credentials","error":"OAuth2 access token expired"}`))
	}))
	defer server.Close()

	_, err := fetchAntigravityUsageFromURLs(
		context.Background(),
		"过期访问令牌",
		"测试项目",
		"",
		server.Client(),
		[]string{server.URL},
	)
	if err == nil {
		t.Fatal("上游返回 403 时应返回错误")
	}
	var statusErr interface{ StatusCode() int }
	if !errors.As(err, &statusErr) || statusErr.StatusCode() != http.StatusForbidden {
		t.Fatalf("错误未保留 403 状态: %v", err)
	}
	if !strings.Contains(err.Error(), "bad-credentials") {
		t.Fatalf("错误未保留上游原文: %v", err)
	}
}

func TestAccountSupportsUsageRefresh包含Antigravity(t *testing.T) {
	item := Account{
		Platform: "antigravity",
		Type:     TypeOAuth,
		Credentials: map[string]string{
			"access_token": "测试访问令牌",
		},
	}
	if !accountSupportsUsageRefresh(item) {
		t.Fatal("Antigravity OAuth 账号应支持用量刷新")
	}
}
