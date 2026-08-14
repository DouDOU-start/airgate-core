package requestaudit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect/sql/schema"
	_ "github.com/mattn/go-sqlite3"

	"github.com/DouDOU-start/airgate-core/ent/enttest"
)

const testSecret = "1111111111111111111111111111111111111111111111111111111111111111"

func openTestService(t *testing.T) *Service {
	return openTestServiceWithOptions(t, Options{})
}

func TestCloneRequestInput按只读承诺复用请求体(t *testing.T) {
	body := []byte("immutable-body")
	borrowed := cloneRequestInput(RequestInput{Body: body, BodyImmutable: true})
	if len(borrowed.Body) == 0 || &borrowed.Body[0] != &body[0] {
		t.Fatal("只读请求体应直接复用原切片")
	}

	owned := cloneRequestInput(RequestInput{Body: body})
	if len(owned.Body) == 0 || &owned.Body[0] == &body[0] {
		t.Fatal("未声明只读时仍应防御性复制")
	}
	body[0] = 'X'
	if string(owned.Body) != "immutable-body" {
		t.Fatalf("防御性副本被调用方修改污染：%q", owned.Body)
	}
}

func Test快速上游审计不在发包前读取原请求体(t *testing.T) {
	service := openTestServiceWithOptions(t, Options{
		AsyncEnabled: true, QueueSize: 4, WorkerCount: 1, MaxPendingBytes: 1 << 20,
	})
	service.StartBackground()
	started := make(chan struct{})
	release := make(chan struct{})
	service.submitAsync(asyncJob{name: "阻塞异步读取", run: func(context.Context) error {
		close(started)
		<-release
		return nil
	}})
	<-started

	handle, err := service.StartFast(context.Background(), RequestInput{
		RequestID: "req-no-pre-read", Protocol: "openai", Model: "gpt-5",
	})
	if err != nil {
		t.Fatal(err)
	}
	original := &countingReadCloser{Reader: strings.NewReader(`{"input":"真实发包正文"}`)}
	req, err := http.NewRequest(http.MethodPost, "https://api.example.com/v1/responses", original)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = int64(len(`{"input":"真实发包正文"}`))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(`{"input":"真实发包正文"}`)), nil
	}
	if _, err := handle.BeginAttemptFast(context.Background(), Target{RouteKind: "account", AccountID: 1}, req); err != nil {
		t.Fatalf("创建快速上游审计失败: %v", err)
	}
	if original.ReadCount() != 0 {
		t.Fatalf("发包前读取了原请求体 %d 次", original.ReadCount())
	}

	close(release)
	flushCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.Flush(flushCtx); err != nil {
		t.Fatal(err)
	}
}

type countingReadCloser struct {
	*strings.Reader
	mu    sync.Mutex
	reads int
}

func (r *countingReadCloser) Read(p []byte) (int, error) {
	r.mu.Lock()
	r.reads++
	r.mu.Unlock()
	return r.Reader.Read(p)
}

func (r *countingReadCloser) Close() error { return nil }

func (r *countingReadCloser) ReadCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads
}

func openTestServiceWithOptions(t *testing.T, options Options) *Service {
	t.Helper()
	db := enttest.Open(t, "sqlite3", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared&_fk=1",
		enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	service := New(db, testSecret, options)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := service.CloseWithContext(ctx); err != nil {
			t.Errorf("关闭请求审计工作池失败: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Errorf("关闭测试数据库失败: %v", err)
		}
	})
	return service
}

func Test快速审计异步补写完整记录(t *testing.T) {
	service := openTestServiceWithOptions(t, Options{
		AsyncEnabled: true, QueueSize: 16, WorkerCount: 2, MaxPendingBytes: 1 << 20,
	})
	service.StartBackground()
	ctx := context.Background()
	handle, err := service.StartFast(ctx, RequestInput{
		RequestID: "req-fast", UserID: 7, UserEmail: "fast@example.com", APIKeyID: 9,
		GroupID: 3, Protocol: "openai", Endpoint: "responses", Model: "gpt-5",
		Method: http.MethodPost, Path: "/v1/responses",
		Headers: http.Header{"Authorization": []string{"Bearer client-fast"}},
		Body:    []byte(`{"model":"gpt-5","input":"异步审计"}`),
	})
	if err != nil {
		t.Fatalf("创建快速审计主记录失败: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.example.com/v1/responses", strings.NewReader(`{"input":"实际请求"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer upstream-fast")
	attempt, err := handle.BeginAttemptFast(ctx, Target{RouteKind: "account", AccountID: 12}, req)
	if err != nil {
		t.Fatalf("创建快速上游审计失败: %v", err)
	}
	attempt.Finish(AttemptFinish{StatusCode: 200, Verdict: "success", ResponseStarted: true, StreamCompleted: true})
	handle.Finish(200, 128, true)

	flushCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.Flush(flushCtx); err != nil {
		t.Fatalf("等待异步审计写入失败: %v", err)
	}
	detail, err := service.Get(ctx, handle.ID())
	if err != nil {
		t.Fatal(err)
	}
	if !detail.Completed || detail.StatusCode != 200 || len(detail.Attempts) != 1 {
		t.Fatalf("异步审计记录不完整: %+v", detail)
	}
	if !strings.Contains(detail.InboundHeaders.Content, "client-fast") ||
		!strings.Contains(detail.Attempts[0].Headers.Content, "upstream-fast") {
		t.Fatalf("异步审计密文补写不完整: %+v", detail)
	}
}

func Test异步队列满时同步兜底(t *testing.T) {
	service := openTestServiceWithOptions(t, Options{
		AsyncEnabled: true, QueueSize: 1, WorkerCount: 1, MaxPendingBytes: 1 << 20,
	})
	service.StartBackground()
	started := make(chan struct{})
	release := make(chan struct{})
	service.submitAsync(asyncJob{name: "阻塞任务", run: func(context.Context) error {
		close(started)
		<-release
		return nil
	}})
	<-started
	service.submitAsync(asyncJob{name: "排队任务", run: func(context.Context) error { return nil }})
	var fallbackRan atomic.Bool
	service.submitAsync(asyncJob{name: "同步兜底任务", run: func(context.Context) error {
		fallbackRan.Store(true)
		return nil
	}})
	if !fallbackRan.Load() {
		t.Fatal("队列已满时没有执行同步兜底")
	}
	if got := service.AsyncStats().SynchronousFallback; got != 1 {
		t.Fatalf("同步兜底次数 = %d，期望 1", got)
	}
	close(release)
	flushCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.Flush(flushCtx); err != nil {
		t.Fatalf("排空异步队列失败: %v", err)
	}
}

func Test异步载荷补写前详情返回等待状态(t *testing.T) {
	service := openTestServiceWithOptions(t, Options{
		AsyncEnabled: true, QueueSize: 4, WorkerCount: 1, MaxPendingBytes: 1 << 20,
	})
	service.StartBackground()
	started := make(chan struct{})
	release := make(chan struct{})
	service.submitAsync(asyncJob{name: "阻塞载荷补写", run: func(context.Context) error {
		close(started)
		<-release
		return nil
	}})
	<-started
	handle, err := service.StartFast(context.Background(), RequestInput{
		RequestID: "req-pending", Protocol: "openai", Model: "gpt-5",
		Headers: http.Header{"X-Test": []string{"pending"}}, Body: []byte(`{"input":"pending"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := service.Get(context.Background(), handle.ID())
	if err != nil {
		t.Fatalf("载荷补写期间读取详情失败: %v", err)
	}
	if !detail.InboundHeaders.Pending || !detail.InboundBody.Pending || detail.InboundBody.Bytes == 0 {
		t.Fatalf("载荷补写期间未返回等待状态: %+v", detail)
	}
	close(release)
	flushCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.Flush(flushCtx); err != nil {
		t.Fatal(err)
	}
}

func Test关闭异步工作池会排空已提交任务(t *testing.T) {
	service := openTestServiceWithOptions(t, Options{
		AsyncEnabled: true, QueueSize: 16, WorkerCount: 2, MaxPendingBytes: 1 << 20,
	})
	service.StartBackground()
	var completed atomic.Int64
	for range 10 {
		service.submitAsync(asyncJob{name: "关闭排空测试", run: func(context.Context) error {
			time.Sleep(time.Millisecond)
			completed.Add(1)
			return nil
		}})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.CloseWithContext(ctx); err != nil {
		t.Fatalf("关闭异步工作池失败: %v", err)
	}
	if completed.Load() != 10 {
		t.Fatalf("关闭后完成任务数 = %d，期望 10", completed.Load())
	}
	stats := service.AsyncStats()
	if !stats.Closed || stats.PendingJobs != 0 {
		t.Fatalf("关闭后的工作池状态异常: %+v", stats)
	}
}

func Test并发提交与关闭不会遗漏任务(t *testing.T) {
	service := openTestServiceWithOptions(t, Options{
		AsyncEnabled: true, QueueSize: 8, WorkerCount: 2, MaxPendingBytes: 1 << 20,
	})
	service.StartBackground()
	const total = 64
	start := make(chan struct{})
	var submitted atomic.Int64
	var completed atomic.Int64
	done := make(chan struct{}, total)
	for range total {
		go func() {
			<-start
			service.submitAsync(asyncJob{name: "并发关闭测试", run: func(context.Context) error {
				completed.Add(1)
				return nil
			}})
			submitted.Add(1)
			done <- struct{}{}
		}()
	}
	close(start)
	closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := service.CloseWithContext(closeCtx); err != nil {
		t.Fatalf("并发关闭异步工作池失败: %v", err)
	}
	for range total {
		<-done
	}
	if submitted.Load() != total || completed.Load() != total {
		t.Fatalf("并发关闭后任务不完整: submitted=%d completed=%d", submitted.Load(), completed.Load())
	}
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

func TestRedactBase64Images普通大请求走快速路径(t *testing.T) {
	body := []byte(`{"model":"gpt-5","tools":[{"name":"view_image","description":"读取 image/base64 数据"}],"input":"` + strings.Repeat("普通代码上下文", 100_000) + `"}`)
	redacted := redactBase64Images(body)
	if len(redacted) == 0 || &redacted[0] != &body[0] {
		t.Fatal("不含图片的请求应直接复用原始审计切片")
	}
}

func TestRedactBase64Images大小写变体仍会脱敏(t *testing.T) {
	image := strings.Repeat("A", 512)
	body := []byte(`{"TYPE":"IMAGE","DATA":"DATA:IMAGE/PNG;BASE64,` + image + `"}`)
	redacted := string(redactBase64Images(body))
	if strings.Contains(redacted, image) || !strings.Contains(redacted, "sha256=") {
		t.Fatalf("大小写变体未正确脱敏：%s", redacted)
	}
}

func TestRedactBase64Images转义DataURI仍会脱敏(t *testing.T) {
	image := strings.Repeat("A", 512)
	body := []byte(`{"image":"\u0064ata\u003aimage/png\u003bbase64,` + image + `"}`)
	redacted := string(redactBase64Images(body))
	if strings.Contains(redacted, image) || !strings.Contains(redacted, "sha256=") {
		t.Fatalf("转义 Data URI 未正确脱敏：%s", redacted)
	}
}

func BenchmarkRedactBase64Images普通大请求(b *testing.B) {
	body := []byte(`{"model":"gpt-5","tools":[{"name":"view_image","description":"读取 image/base64 数据"}],"input":"` + strings.Repeat("普通代码上下文", 100_000) + `"}`)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if redacted := redactBase64Images(body); len(redacted) != len(body) {
			b.Fatalf("快速路径意外改写请求：got=%d want=%d", len(redacted), len(body))
		}
	}
}

func BenchmarkRedactBase64Images全量JSON基线(b *testing.B) {
	body := []byte(`{"model":"gpt-5","tools":[{"name":"view_image","description":"读取 image/base64 数据"}],"input":"` + strings.Repeat("普通代码上下文", 100_000) + `"}`)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if redacted := redactBase64ImagesFullDecodeForBenchmark(body); len(redacted) != len(body) {
			b.Fatalf("基线意外改写请求：got=%d want=%d", len(redacted), len(body))
		}
	}
}

// redactBase64ImagesFullDecodeForBenchmark 保留优化前的全量 JSON 树路径，
// 用于量化普通大请求快速过滤的收益，不进入生产调用链。
func redactBase64ImagesFullDecodeForBenchmark(body []byte) []byte {
	if len(body) == 0 || !json.Valid(body) {
		return body
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || !redactJSONValue(value, "") {
		return body
	}
	redacted, err := json.Marshal(value)
	if err != nil {
		return body
	}
	return redacted
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
