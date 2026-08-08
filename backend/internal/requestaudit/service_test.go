package requestaudit

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect/sql/schema"
	_ "github.com/mattn/go-sqlite3"

	"github.com/DouDOU-start/airgate-core/ent/enttest"
)

const testSecret = "1111111111111111111111111111111111111111111111111111111111111111"

func openTestService(t *testing.T) *Service {
	t.Helper()
	db := enttest.Open(t, "sqlite3", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared&_fk=1",
		enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("关闭测试数据库失败: %v", err)
		}
	})
	return New(db, testSecret)
}

func TestRedactBase64Images保留结构并移除图片原文(t *testing.T) {
	image := strings.Repeat("a", 512)
	body := []byte(`{"model":"gpt-image","prompt":"画一只猫","input_image":"data:image/png;base64,` + image + `","nested":{"b64_json":"` + image + `"},"source":{"type":"base64","media_type":"image/jpeg","data":"` + image + `"}}`)

	redacted := string(redactBase64Images(body))
	if strings.Contains(redacted, image) {
		t.Fatal("审计副本仍包含 base64 图片原文")
	}
	if !strings.Contains(redacted, "画一只猫") || !strings.Contains(redacted, "sha256=") || !strings.Contains(redacted, "原始字符数=") {
		t.Fatalf("脱敏后未保留结构或摘要信息: %s", redacted)
	}
}

func Test审计完整保存并解密客户端与上游请求(t *testing.T) {
	service := openTestService(t)
	ctx := context.Background()
	handle, err := service.Start(ctx, RequestInput{
		RequestID: "req-audit-1", UserID: 7, UserEmail: "user@example.com", APIKeyID: 9,
		GroupID: 3, Client: "codex", Protocol: "openai", Endpoint: "responses",
		Model: "gpt-5", Stream: true, Method: http.MethodPost, Path: "/v1/responses",
		Headers: http.Header{"Authorization": []string{"Bearer client-secret"}, "X-Test": []string{"one", "two"}},
		Body:    []byte(`{"model":"gpt-5","input":"你好"}`),
	})
	if err != nil {
		t.Fatalf("创建审计主记录失败: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, "https://api.example.com/v1/responses", strings.NewReader(`{"model":"gpt-5","input":"你好","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer oauth-real-token")
	attempt, err := handle.BeginAttempt(ctx, Target{
		RouteKind: "account", AccountID: 12, AccountName: "生产 Codex", AccountEmail: "codex@example.com",
		AccountPlatform: "codex", AccountType: "oauth",
	}, req)
	if err != nil {
		t.Fatalf("创建上游审计失败: %v", err)
	}
	actualBody, err := io.ReadAll(req.Body)
	if err != nil || !strings.Contains(string(actualBody), "stream") {
		t.Fatalf("审计后真实请求体未恢复: body=%s err=%v", actualBody, err)
	}
	attempt.Finish(AttemptFinish{StatusCode: 429, Verdict: "rateLimited", ResponseStarted: true})
	handle.Finish(200, 128, true)

	detail, err := service.Get(ctx, handle.ID())
	if err != nil {
		t.Fatalf("读取审计详情失败: %v", err)
	}
	if detail.UserEmail != "user@example.com" || len(detail.Attempts) != 1 {
		t.Fatalf("审计详情不完整: %+v", detail)
	}
	if !strings.Contains(detail.InboundHeaders.Content, "client-secret") {
		t.Fatalf("客户端完整 Header 未解密: %s", detail.InboundHeaders.Content)
	}
	if !strings.Contains(detail.Attempts[0].Headers.Content, "oauth-real-token") {
		t.Fatalf("最终 OAuth Header 未解密: %s", detail.Attempts[0].Headers.Content)
	}
	if detail.Attempts[0].AccountEmail != "codex@example.com" || detail.Attempts[0].StatusCode != 429 {
		t.Fatalf("账号或尝试结果不完整: %+v", detail.Attempts[0])
	}

	stored, err := service.db.RequestAuditLog.Get(ctx, handle.ID())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.InboundHeadersEnc, "client-secret") || strings.Contains(stored.InboundBodyEnc, "你好") {
		t.Fatal("数据库中出现敏感明文")
	}

	items, total, err := service.List(ctx, ListFilter{Page: 1, PageSize: 20, StatusCode: 429, Keyword: "codex@example.com"})
	if err != nil {
		t.Fatalf("按 429 尝试与账号邮箱查询失败: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].StatusCode != 200 {
		t.Fatalf("429 后最终成功的请求未被筛出: total=%d items=%+v", total, items)
	}

	// 先按 24h 阈值清理：当前记录应被保留。
	cutoff := time.Now().Add(-24 * time.Hour)
	deletedOld, err := service.Clear(ctx, &cutoff)
	if err != nil {
		t.Fatalf("按时间清空请求审计失败: %v", err)
	}
	if deletedOld != 0 {
		t.Fatalf("24h 内记录不应被删: deleted=%d", deletedOld)
	}
	// 再清空全部。
	deleted, err := service.Clear(ctx, nil)
	if err != nil {
		t.Fatalf("清空请求审计失败: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	items, total, err = service.List(ctx, ListFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("清空后列表查询失败: %v", err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("清空后仍有数据: total=%d items=%+v", total, items)
	}
	if attempts, err := service.db.RequestAuditAttempt.Query().Count(ctx); err != nil {
		t.Fatalf("统计 attempt 失败: %v", err)
	} else if attempts != 0 {
		t.Fatalf("清空后 attempt 残留 %d 条", attempts)
	}
}

func TestRoundTripper捕获Token注入后的最终请求(t *testing.T) {
	service := openTestService(t)
	handle, err := service.Start(context.Background(), RequestInput{
		RequestID: "req-rt", Protocol: "openai", Endpoint: "responses", Model: "gpt-5",
		Headers: http.Header{}, Body: []byte(`{"input":"原始"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "Bearer executor-token" {
			t.Fatalf("底层发包未收到 executor 注入的 Token: %q", req.Header.Get("Authorization"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Request:    req,
		}, nil
	})
	client := &http.Client{Transport: NewRoundTripper(base, handle, Target{
		RouteKind: "account", AccountID: 33, AccountName: "Codex OAuth", AccountPlatform: "codex", AccountType: "oauth",
	})}
	req, _ := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"input":"实际"}`))
	req.Header.Set("Authorization", "Bearer executor-token")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("审计 RoundTripper 发包失败: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	handle.Finish(200, 11, true)

	detail, err := service.Get(context.Background(), handle.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Attempts) != 1 || !strings.Contains(detail.Attempts[0].Headers.Content, "executor-token") {
		t.Fatalf("未捕获 Token 注入后的最终请求: %+v", detail.Attempts)
	}
	if !detail.Attempts[0].Finished || !detail.Attempts[0].StreamCompleted {
		t.Fatalf("响应读完后尝试未完成收尾: %+v", detail.Attempts[0])
	}
}

func Test取消上下文后仍可完成审计收尾(t *testing.T) {
	service := openTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	handle, err := service.Start(ctx, RequestInput{RequestID: "req-cancel", Protocol: "openai", Model: "gpt-5", Headers: http.Header{}})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	handle.Finish(499, 0, true)

	detail, err := service.Get(context.Background(), handle.ID())
	if err != nil {
		t.Fatal(err)
	}
	if detail.StatusCode != 499 || !detail.Completed {
		t.Fatalf("取消后的审计收尾丢失: %+v", detail.ListItem)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
