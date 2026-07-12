package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/errlog"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	openaiadaptor "github.com/DouDOU-start/airgate-core/internal/relay/adaptor/openai"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/errfmt"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

// ===== 测试替身 =====

// fakeSink 捕获 UsageRecord。
type fakeSink struct {
	mu      sync.Mutex
	records []billing.UsageRecord
}

func (f *fakeSink) Record(r billing.UsageRecord) {
	f.mu.Lock()
	f.records = append(f.records, r)
	f.mu.Unlock()
}

func (f *fakeSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.records)
}

func (f *fakeSink) last(t *testing.T) billing.UsageRecord {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.records) == 0 {
		t.Fatal("未捕获任何 UsageRecord")
	}
	return f.records[len(f.records)-1]
}

// fakeErrSink 捕获上游请求日志投递与错误计数。
type fakeErrSink struct {
	mu      sync.Mutex
	entries []errlog.Entry
	counts  []string // "channelID:verdict:phase"
}

func (f *fakeErrSink) Record(e errlog.Entry) {
	f.mu.Lock()
	f.entries = append(f.entries, e)
	f.mu.Unlock()
}

func (f *fakeErrSink) CountFailure(_ context.Context, channelID int, verdict, phase string) {
	f.mu.Lock()
	f.counts = append(f.counts, fmt.Sprintf("%d:%s:%s", channelID, verdict, phase))
	f.mu.Unlock()
}

func (f *fakeErrSink) lastEntry(t *testing.T) errlog.Entry {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.entries) == 0 {
		t.Fatal("未捕获任何上游请求日志")
	}
	return f.entries[len(f.entries)-1]
}

func (f *fakeErrSink) entryCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.entries)
}

// fakeChannelLoader 固定渠道快照。
type fakeChannelLoader struct{ snaps []registry.ChannelKeySnapshot }

func (f *fakeChannelLoader) LoadAllForRegistry(context.Context) ([]registry.ChannelKeySnapshot, error) {
	return f.snaps, nil
}

// fakePersister 记录状态落库（异步，需等待）。
type fakePersister struct {
	mu      sync.Mutex
	calls   []string // "id:status"
	errMsgs []string
	done    chan struct{}
}

func newFakePersister() *fakePersister { return &fakePersister{done: make(chan struct{}, 16)} }

func (f *fakePersister) PersistState(_ context.Context, id int, status string, errMsg string) error {
	f.mu.Lock()
	f.calls = append(f.calls, fmt.Sprintf("%d:%s", id, status))
	f.errMsgs = append(f.errMsgs, errMsg)
	f.mu.Unlock()
	f.done <- struct{}{}
	return nil
}

func (f *fakePersister) waitOne(t *testing.T) string {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(2 * time.Second):
		t.Fatal("等待异步落库超时")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func (f *fakePersister) lastErrMsg() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.errMsgs) == 0 {
		return ""
	}
	return f.errMsgs[len(f.errMsgs)-1]
}

func (f *fakePersister) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakePriceLoader 固定价目表。
type fakePriceLoader struct{ prices map[string]pricing.Price }

func (f *fakePriceLoader) LoadAllPrices(context.Context) (map[string]pricing.Price, error) {
	return f.prices, nil
}

// ===== 测试装配 =====

const testModel = "gpt-4o"

// anthModel / gemModel 原生协议入口测试模型。
const (
	anthModel = "claude-s"
	gemModel  = "gem-flash"
)

// perRequestModel 按次计费测试模型（USD 0.02 / 次）。
const perRequestModel = "flat-model"

// imgPerReqModel / imagenModel 图像按次计费测试模型（openai 生图 / gemini Imagen）。
const (
	imgPerReqModel = "img-flat"
	imagenModel    = "imagen-t"
)

// testPrice USD/1M：input 10 / output 30 / cached 5。
var testPrice = pricing.Price{Input: 10, Output: 30, CachedInput: 5}

func testKeyInfo() *auth.APIKeyInfo {
	return &auth.APIKeyInfo{
		KeyID:               11,
		UserID:              22,
		UserEmail:           "u@example.com",
		GroupID:             7,
		UserBalance:         100,
		GroupRateMultiplier: 2.0, // billing rate = 2
		SellRate:            0,   // billed 回退 actual
	}
}

type testEnv struct {
	pipe      *Pipeline
	engine    *gin.Engine
	sink      *fakeSink
	errSink   *fakeErrSink
	persister *fakePersister
	registry  *registry.Registry
}

// newTestEnv 组装管线：注册表注入指定快照（不接 DB），
// ConcurrencyManager/RPMCounter 传 nil redis（no-op），UsageSink 用 fake。
func newTestEnv(t *testing.T, snaps ...registry.ChannelKeySnapshot) *testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	persister := newFakePersister()
	reg := registry.New(&fakeChannelLoader{snaps: snaps}, persister)
	if err := reg.Reload(context.Background()); err != nil {
		t.Fatalf("注册表加载失败: %v", err)
	}

	cache := pricing.NewCache(&fakePriceLoader{prices: map[string]pricing.Price{
		testModel:       testPrice,
		anthModel:       testPrice,
		gemModel:        testPrice,
		perRequestModel: {PerRequest: 0.02},
		imgPerReqModel:  {PerRequest: 0.04},
		imagenModel:     {PerRequest: 0.03},
	}})
	if err := cache.Reload(context.Background()); err != nil {
		t.Fatalf("价目表加载失败: %v", err)
	}

	sink := &fakeSink{}
	errSink := &fakeErrSink{}
	pipe := New(Options{
		Registry:    reg,
		Pricing:     cache,
		Concurrency: scheduler.NewConcurrencyManager(nil),
		RPM:         scheduler.NewRPMCounter(nil),
		Calculator:  billing.NewCalculator(),
		Sink:        sink,
		ErrLog:      errSink,
		Settings:    NewSettingsReader(nil),
	})

	engine := gin.New()
	injectKey := func(c *gin.Context) { c.Set(middleware.CtxKeyKeyInfo, testKeyInfo()) }
	engine.POST("/v1/chat/completions", injectKey, pipe.HandleChatCompletions)
	engine.POST("/v1/responses", injectKey, pipe.HandleResponses)
	engine.POST("/v1/images/generations", injectKey, pipe.HandleImagesGenerations)
	engine.POST("/v1/images/edits", injectKey, pipe.HandleImagesEdits)
	engine.POST("/v1/messages", injectKey, pipe.HandleMessages)
	engine.POST("/v1/messages/count_tokens", injectKey, pipe.HandleMessagesCountTokens)
	engine.POST("/v1beta/models/:modelAction", injectKey, pipe.HandleGenerateContent)
	engine.GET("/v1/models", injectKey, pipe.HandleModels)
	engine.GET("/v1beta/models", injectKey, pipe.HandleGeminiModels)

	return &testEnv{pipe: pipe, engine: engine, sink: sink, errSink: errSink, persister: persister, registry: reg}
}

// testSnap 构造指向指定上游的渠道快照。
func testSnap(id int, baseURL string, mutate ...func(*registry.ChannelKeySnapshot)) registry.ChannelKeySnapshot {
	s := registry.ChannelKeySnapshot{
		KeyID:        id,
		ChannelID:    id,
		ChannelName:  fmt.Sprintf("ch-%d", id),
		Type:         "openai_compatible",
		BaseURL:      baseURL,
		APIKey:       fmt.Sprintf("sk-up-%d", id),
		Models:       map[string]struct{}{testModel: {}},
		ModelMapping: map[string]string{testModel: "gpt-4o-upstream"},
		Priority:     10,
		Weight:       10,
		CostRatio:    0.5,
		Status:       registry.StatusEnabled,
	}
	for _, m := range mutate {
		m(&s)
	}
	return s
}

func (e *testEnv) do(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

func (e *testEnv) doResponses(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

// newGoodUpstream 正常上游：非流式返回带 usage 的 JSON，流式返回 SSE。
// 校验 method/path/认证头/上游模型名，并记录收到的请求体。
func newGoodUpstream(t *testing.T, hits *atomic.Int32, lastBody *atomic.Value) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("上游收到异常路由: %s %s", r.Method, r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer sk-up-") {
			t.Errorf("上游认证头异常: %q", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		lastBody.Store(string(body))

		var req struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		_ = json.Unmarshal(body, &req)
		if req.Model != "gpt-4o-upstream" {
			t.Errorf("上游收到 model = %q, want gpt-4o-upstream（model_mapping 未生效）", req.Model)
		}

		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			lines := []string{
				`data: {"id":"c1","model":"gpt-4o-upstream","choices":[{"delta":{"content":"Hel"}}]}`,
				``,
				`data: {"id":"c1","model":"gpt-4o-upstream","choices":[{"delta":{"content":"lo"}}]}`,
				``,
				`data: {"id":"c1","model":"gpt-4o-upstream","choices":[],"usage":{"prompt_tokens":1000,"completion_tokens":500,"prompt_tokens_details":{"cached_tokens":200}}}`,
				``,
				`data: [DONE]`,
				``,
			}
			for _, line := range lines {
				_, _ = io.WriteString(w, line+"\n")
				flusher.Flush()
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp-1","model":"gpt-4o-upstream",`+
			`"usage":{"prompt_tokens":1000,"completion_tokens":500,"prompt_tokens_details":{"cached_tokens":200}},`+
			`"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
}

// newFailingUpstream 坏上游：恒返回指定状态码。
func newFailingUpstream(status int, retryAfter string, hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":{"message":"upstream says no","type":"rate_limit_error"}}`)
	}))
}

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// ===== 场景 =====

// TestForwardNonStream 非流式转发成功：响应透传（model 回写）+ 金额 = tokens×单价×倍率。
func TestForwardNonStream(t *testing.T) {
	var hits atomic.Int32
	var lastBody atomic.Value
	upstream := newGoodUpstream(t, &hits, &lastBody)
	defer upstream.Close()

	env := newTestEnv(t, testSnap(1, upstream.URL))
	w := env.do(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	if resp["model"] != "gpt-4o" {
		t.Errorf("响应 model = %v, want 回写为对外名 gpt-4o", resp["model"])
	}

	rec := env.sink.last(t)
	// 基础成本：input=(1000-200)/1e6×10=0.008；cached=200/1e6×5=0.001；output=500/1e6×30=0.015。
	const wantTotal = 0.008 + 0.001 + 0.015
	if !almostEqual(rec.TotalCost, wantTotal) {
		t.Errorf("TotalCost = %v, want %v", rec.TotalCost, wantTotal)
	}
	if !almostEqual(rec.ActualCost, wantTotal*2.0) { // billing rate 2.0
		t.Errorf("ActualCost = %v, want %v", rec.ActualCost, wantTotal*2.0)
	}
	if !almostEqual(rec.BilledCost, wantTotal*2.0) { // sell_rate=0 回退 actual
		t.Errorf("BilledCost = %v, want %v", rec.BilledCost, wantTotal*2.0)
	}
	if !almostEqual(rec.AccountRateMultiplier, 0.5) { // cost_ratio 0.5 快照
		t.Errorf("AccountRateMultiplier = %v, want 0.5", rec.AccountRateMultiplier)
	}
	if rec.InputTokens != 800 || rec.OutputTokens != 500 || rec.CachedInputTokens != 200 {
		t.Errorf("tokens = (%d,%d,%d), want (800,500,200)", rec.InputTokens, rec.OutputTokens, rec.CachedInputTokens)
	}
	if rec.ChannelID != 1 || rec.Model != "gpt-4o" || rec.Stream {
		t.Errorf("record 元数据异常: %+v", rec)
	}
	if rec.InputPrice != 10 || rec.OutputPrice != 30 || rec.CachedInputPrice != 5 {
		t.Errorf("单价快照异常: %+v", rec)
	}
	if rec.Endpoint != "/v1/chat/completions" {
		t.Errorf("Endpoint = %q", rec.Endpoint)
	}
}

// TestForwardStream 流式转发：逐行透传 + usage 旁路捕获 + include_usage 注入。
func TestForwardStream(t *testing.T) {
	var hits atomic.Int32
	var lastBody atomic.Value
	upstream := newGoodUpstream(t, &hits, &lastBody)
	defer upstream.Close()

	env := newTestEnv(t, testSnap(1, upstream.URL))
	w := env.do(t, `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"content":"Hel"`) || !strings.Contains(body, "data: [DONE]") {
		t.Errorf("SSE 未逐行透传:\n%s", body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}

	// include_usage 注入：客户端未带 stream_options，上游应看到注入值。
	sent, _ := lastBody.Load().(string)
	if !strings.Contains(sent, `"include_usage":true`) {
		t.Errorf("上游请求未注入 stream_options.include_usage: %s", sent)
	}

	rec := env.sink.last(t)
	if !rec.Stream {
		t.Error("record.Stream 应为 true")
	}
	if rec.InputTokens != 800 || rec.OutputTokens != 500 || rec.CachedInputTokens != 200 {
		t.Errorf("流式 usage 捕获失败: tokens = (%d,%d,%d)", rec.InputTokens, rec.OutputTokens, rec.CachedInputTokens)
	}
	if rec.FirstTokenMs < 0 {
		t.Errorf("FirstTokenMs = %d", rec.FirstTokenMs)
	}
}

// TestFailover429 坏渠道 429 → 本次请求硬排除 → 自动切换到好渠道成功；
// 无冷却状态：不落库任何状态变更，下次请求坏渠道仍照常参与调度。
func TestFailover429(t *testing.T) {
	var goodHits, badHits atomic.Int32
	var lastBody atomic.Value
	good := newGoodUpstream(t, &goodHits, &lastBody)
	defer good.Close()
	bad := newFailingUpstream(http.StatusTooManyRequests, "5", &badHits)
	defer bad.Close()

	env := newTestEnv(t,
		testSnap(1, good.URL, func(s *registry.ChannelKeySnapshot) { s.Priority = 1 }),
		testSnap(2, bad.URL, func(s *registry.ChannelKeySnapshot) { s.Priority = 100 }), // 高优先级先被选中
	)
	w := env.do(t, `{"model":"gpt-4o","messages":[]}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if badHits.Load() != 1 || goodHits.Load() != 1 {
		t.Errorf("hits bad=%d good=%d, want 1/1", badHits.Load(), goodHits.Load())
	}
	// 429 不产生任何状态落库（无冷却机制）。
	if got := env.persister.callCount(); got != 0 {
		t.Errorf("429 不应有状态落库, got %d 次", got)
	}
	if env.sink.count() != 1 {
		t.Errorf("UsageRecord 条数 = %d, want 1（仅成功渠道计费）", env.sink.count())
	}

	// 429 仅本次请求内排除：下次请求高优先级坏渠道仍被选中，再次 failover 成功。
	w2 := env.do(t, `{"model":"gpt-4o","messages":[]}`)
	if w2.Code != http.StatusOK {
		t.Fatalf("第二次请求 status = %d", w2.Code)
	}
	if badHits.Load() != 2 || goodHits.Load() != 2 {
		t.Errorf("第二次 hits bad=%d good=%d, want 2/2（429 渠道下次请求应照常调度）",
			badHits.Load(), goodHits.Load())
	}
}

// TestAllChannels429 两渠道全 429 → 返回 429 OpenAI 错误体 + Retry-After。
func TestAllChannels429(t *testing.T) {
	var hits1, hits2 atomic.Int32
	bad1 := newFailingUpstream(http.StatusTooManyRequests, "5", &hits1)
	defer bad1.Close()
	bad2 := newFailingUpstream(http.StatusTooManyRequests, "", &hits2)
	defer bad2.Close()

	env := newTestEnv(t, testSnap(1, bad1.URL), testSnap(2, bad2.URL))
	w := env.do(t, `{"model":"gpt-4o","messages":[]}`)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body = %s", w.Code, w.Body.String())
	}
	if hits1.Load() != 1 || hits2.Load() != 1 {
		t.Errorf("hits = (%d,%d), want 各 1 次", hits1.Load(), hits2.Load())
	}
	var resp errfmt.OpenAIError
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("错误体非 JSON: %v", err)
	}
	if resp.Error.Type != "rate_limit_error" || resp.Error.Code != "upstream_rate_limited" {
		t.Errorf("错误体 = %+v", resp.Error)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("缺少 Retry-After 头")
	}
	if env.sink.count() != 0 {
		t.Errorf("全败请求不应计费, got %d 条", env.sink.count())
	}
}

// TestUpstream500Failover 5xx 软排除后切换成功。
func TestUpstream500Failover(t *testing.T) {
	var goodHits, badHits atomic.Int32
	var lastBody atomic.Value
	good := newGoodUpstream(t, &goodHits, &lastBody)
	defer good.Close()
	bad := newFailingUpstream(http.StatusInternalServerError, "", &badHits)
	defer bad.Close()

	env := newTestEnv(t,
		testSnap(1, good.URL, func(s *registry.ChannelKeySnapshot) { s.Priority = 1 }),
		testSnap(2, bad.URL, func(s *registry.ChannelKeySnapshot) { s.Priority = 100 }),
	)
	w := env.do(t, `{"model":"gpt-4o","messages":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if badHits.Load() != 1 || goodHits.Load() != 1 {
		t.Errorf("hits bad=%d good=%d, want 1/1", badHits.Load(), goodHits.Load())
	}
}

// TestClientErrorRebuilt 普通 4xx 语义重建终止不重试：
// 上游 message/type 提取后按入口协议（openai）重建，HTTP 状态码保留原值。
func TestClientErrorRebuilt(t *testing.T) {
	var hits atomic.Int32
	bad := newFailingUpstream(http.StatusBadRequest, "", &hits)
	defer bad.Close()

	env := newTestEnv(t, testSnap(1, bad.URL))
	w := env.do(t, `{"model":"gpt-4o","messages":[]}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1（不重试）", hits.Load())
	}
	var resp errfmt.OpenAIError
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("重建后错误体非 JSON: %v", err)
	}
	if resp.Error.Message != "upstream says no" {
		t.Errorf("message = %q, want 保留上游语义", resp.Error.Message)
	}
	if resp.Error.Type != "rate_limit_error" {
		t.Errorf("type = %q, want 保留上游 error.type", resp.Error.Type)
	}
}

// TestBalancePrecheck 余额不足 402。
func TestBalancePrecheck(t *testing.T) {
	env := newTestEnv(t, testSnap(1, "http://127.0.0.1:0"))
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	broke := testKeyInfo()
	broke.UserBalance = 0
	engine.POST("/v1/chat/completions", func(c *gin.Context) { c.Set(middleware.CtxKeyKeyInfo, broke) }, env.pipe.HandleChatCompletions)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402; body = %s", w.Code, w.Body.String())
	}
}

// TestUnpricedModelRejected 缺价预检 400（缺价模型一律拒绝，无放行开关）。
func TestUnpricedModelRejected(t *testing.T) {
	env := newTestEnv(t, testSnap(1, "http://127.0.0.1:0", func(s *registry.ChannelKeySnapshot) {
		s.Models["unpriced-model"] = struct{}{}
	}))
	w := env.do(t, `{"model":"unpriced-model","messages":[]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "model_price_not_configured") {
		t.Errorf("错误码不符: %s", w.Body.String())
	}
}

// TestHandleModels /v1/models 按分组返回模型并集。
func TestHandleModels(t *testing.T) {
	env := newTestEnv(t,
		testSnap(1, "http://u1", func(s *registry.ChannelKeySnapshot) {
			s.Models = map[string]struct{}{"gpt-4o": {}, "gpt-4o-mini": {}}
		}),
		testSnap(2, "http://u2", func(s *registry.ChannelKeySnapshot) {
			s.Models = map[string]struct{}{"claude-x": {}}
			s.GroupIDs = map[int]struct{}{99: {}} // 非本分组渠道，不应出现
		}),
	)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	env.engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp modelList
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	got := make([]string, 0, len(resp.Data))
	for _, item := range resp.Data {
		got = append(got, item.ID)
		if item.Object != "model" || item.OwnedBy != "airgate" {
			t.Errorf("model item 格式异常: %+v", item)
		}
	}
	want := []string{"gpt-4o", "gpt-4o-mini"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("models = %v, want %v", got, want)
	}
	if resp.Object != "list" {
		t.Errorf("object = %q", resp.Object)
	}
}

// ===== 本轮缺陷修复的回归场景 =====

// TestRedirectNotFollowed 出口 client 不跟随重定向：3xx 原样透传终止，
// 不对 Location 目标发起第二次请求（防静默重复 POST / 跨域丢 Authorization）。
func TestRedirectNotFollowed(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	var redirectHits atomic.Int32
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectHits.Add(1)
		w.Header().Set("Location", target.URL+"/v1/chat/completions")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = io.WriteString(w, `{"error":{"message":"moved"}}`)
	}))
	defer redirector.Close()

	env := newTestEnv(t, testSnap(1, redirector.URL))
	w := env.do(t, `{"model":"gpt-4o","messages":[]}`)

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307 原样透传; body = %s", w.Code, w.Body.String())
	}
	if redirectHits.Load() != 1 {
		t.Errorf("上游收到 %d 次请求, want 1（不得自动重放 POST）", redirectHits.Load())
	}
	if targetHits.Load() != 0 {
		t.Errorf("重定向目标收到 %d 次请求, want 0（不跟随）", targetHits.Load())
	}
}

// TestStreamContentTypeMismatch stream=true 但上游 2xx 返回非 SSE（application/json）：
// 按非流式路径处理——usage 提取计费、model 回写，JSON 原样回给客户端。
func TestStreamContentTypeMismatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp-1","model":"gpt-4o-upstream",`+
			`"usage":{"prompt_tokens":1000,"completion_tokens":500,"prompt_tokens_details":{"cached_tokens":200}},`+
			`"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer upstream.Close()

	env := newTestEnv(t, testSnap(1, upstream.URL))
	w := env.do(t, `{"model":"gpt-4o","stream":true,"messages":[]}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	if resp["model"] != "gpt-4o" {
		t.Errorf("model = %v, want 回写为对外名（非流式解析路径未生效）", resp["model"])
	}

	rec := env.sink.last(t)
	if rec.InputTokens != 800 || rec.OutputTokens != 500 || rec.CachedInputTokens != 200 {
		t.Errorf("usage 未按非流式路径提取: tokens = (%d,%d,%d), want (800,500,200)",
			rec.InputTokens, rec.OutputTokens, rec.CachedInputTokens)
	}
	if rec.TotalCost <= 0 {
		t.Errorf("TotalCost = %v, want > 0（非 SSE 的 2xx 不得计 0 费）", rec.TotalCost)
	}
}

// TestStreamForcedIncludeUsage 客户端显式 include_usage=false 也强制注入 true：
// 计费 usage 照常捕获；客户端未请求 usage 时 usage-only chunk 不下发（[DONE] 照常）。
func TestStreamForcedIncludeUsage(t *testing.T) {
	var hits atomic.Int32
	var lastBody atomic.Value
	upstream := newGoodUpstream(t, &hits, &lastBody)
	defer upstream.Close()

	env := newTestEnv(t, testSnap(1, upstream.URL))
	w := env.do(t, `{"model":"gpt-4o","stream":true,"stream_options":{"include_usage":false},"messages":[{"role":"user","content":"hi"}]}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	// 上游必须收到强制改写后的 include_usage=true（零计费漏洞封堵）。
	sent, _ := lastBody.Load().(string)
	if !strings.Contains(sent, `"include_usage":true`) {
		t.Errorf("上游请求未强制 include_usage=true: %s", sent)
	}

	// 客户端未请求 usage：usage-only chunk 吞掉，[DONE] 照常。
	body := w.Body.String()
	if strings.Contains(body, `"prompt_tokens"`) {
		t.Errorf("客户端不应收到 usage chunk:\n%s", body)
	}
	if !strings.Contains(body, "data: [DONE]") || !strings.Contains(body, `"content":"Hel"`) {
		t.Errorf("内容 chunk 或 [DONE] 缺失:\n%s", body)
	}

	// 计费照常。
	rec := env.sink.last(t)
	if rec.InputTokens != 800 || rec.OutputTokens != 500 {
		t.Errorf("usage 捕获失败: tokens = (%d,%d)", rec.InputTokens, rec.OutputTokens)
	}
}

// TestStreamClientRequestedUsageChunkForwarded 客户端显式请求 include_usage=true 时
// usage chunk 照常下发。
func TestStreamClientRequestedUsageChunkForwarded(t *testing.T) {
	var hits atomic.Int32
	var lastBody atomic.Value
	upstream := newGoodUpstream(t, &hits, &lastBody)
	defer upstream.Close()

	env := newTestEnv(t, testSnap(1, upstream.URL))
	w := env.do(t, `{"model":"gpt-4o","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, `"prompt_tokens"`) {
		t.Errorf("客户端显式请求 usage 时应下发 usage chunk:\n%s", body)
	}
}

// TestAuthFailedSanitizedAndDistinctError 上游 401 回显渠道 key：
// (a) 自动禁用落库的 error_msg 已脱敏（sk-***+尾4位）且不含明文 key；
// (b) 全渠道认证失败返回 502 upstream_auth_failed（而非误导性的 503 无可用渠道）。
func TestAuthFailedSanitizedAndDistinctError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		// 模拟回显 Authorization 凭证的中转上游。
		_, _ = io.WriteString(w, `{"error":{"message":"Invalid token: sk-up-1"}}`)
	}))
	defer upstream.Close()

	env := newTestEnv(t, testSnap(1, upstream.URL))
	w := env.do(t, `{"model":"gpt-4o","messages":[]}`)

	// (b) 错误语义：502 upstream_auth_failed。
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", w.Code, w.Body.String())
	}
	var resp errfmt.OpenAIError
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("错误体非 JSON: %v", err)
	}
	if resp.Error.Code != "upstream_auth_failed" {
		t.Errorf("错误码 = %q, want upstream_auth_failed", resp.Error.Code)
	}

	// (a) 自动禁用落库原因已脱敏。
	call := env.persister.waitOne(t)
	if call != "1:disabled_auto" {
		t.Fatalf("落库调用 = %q, want 1:disabled_auto", call)
	}
	errMsg := env.persister.lastErrMsg()
	if strings.Contains(errMsg, "sk-up-1") {
		t.Errorf("error_msg 泄漏明文 key: %q", errMsg)
	}
	if !strings.Contains(errMsg, "sk-***up-1") {
		t.Errorf("error_msg 未按掩码格式替换: %q", errMsg)
	}
	if len(errMsg) > 300 {
		t.Errorf("error_msg 长度 = %d, want <= 300", len(errMsg))
	}
}

// TestClientErrorBodySanitized 上游 400（语义重建路径）回显渠道 key：
// 提取出的 message 写响应前做精确 key 替换，其余语义不动。
func TestClientErrorBodySanitized(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Invalid token: sk-up-1 for request"}}`)
	}))
	defer upstream.Close()

	env := newTestEnv(t, testSnap(1, upstream.URL))
	w := env.do(t, `{"model":"gpt-4o","messages":[]}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "sk-up-1") {
		t.Errorf("4xx 重建错误体泄漏明文 key: %s", body)
	}
	if !strings.Contains(body, "sk-***up-1") || !strings.Contains(body, "for request") {
		t.Errorf("4xx 重建错误体应仅替换 key、其余语义不动: %s", body)
	}
}

// TestClientError400DoesNotDisableChannel 上游 400（错误体含凭证类文案）不自动禁用渠道：
// 自动禁用仅由 401/403 状态码触发，错误体内容不参与判定
// （否则任意用户可构造回显文案打禁渠道）。
func TestClientError400DoesNotDisableChannel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Invalid value: 'insufficient_quota'. Supported values are ..."}}`)
	}))
	defer upstream.Close()

	env := newTestEnv(t, testSnap(1, upstream.URL))
	w := env.do(t, `{"model":"gpt-4o","messages":[]}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	// 渠道不得被禁用：再次请求仍会被调度（上游再次收到请求）。
	w2 := env.do(t, `{"model":"gpt-4o","messages":[]}`)
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("第二次 status = %d, want 400（渠道不应被自动禁用）", w2.Code)
	}
	if got := env.persister.callCount(); got != 0 {
		t.Errorf("不应有任何状态落库, got %d 次", got)
	}
}

// panicAdaptor BuildRequest 恒 panic 的适配器（模拟畸形数据触发未防护路径）。
type panicAdaptor struct{}

func (panicAdaptor) BuildRequest(context.Context, *adaptor.RelayInfo, *dto.ChatRequest) (*http.Request, error) {
	panic("boom")
}

func (panicAdaptor) ParseNonStreamResponse(*adaptor.RelayInfo, []byte) ([]byte, *dto.Usage) {
	return nil, nil
}

// TestExecutePanicRecovered execute 内 panic：渠道槽/RPM 释放路径不被跳过，
// panic 继续向上由 Recovery 中间件转 500，进程与后续请求不受影响。
// Pick 有协议过滤，借用 openai 协议组内的 custom 类型注册 panic 适配器（测后还原）。
func TestExecutePanicRecovered(t *testing.T) {
	adaptor.Register("custom", func() adaptor.Adaptor { return panicAdaptor{} })
	defer adaptor.Register("custom", func() adaptor.Adaptor { return openaiadaptor.Adaptor{} })

	var hits atomic.Int32
	var lastBody atomic.Value
	good := newGoodUpstream(t, &hits, &lastBody)
	defer good.Close()

	env := newTestEnv(t,
		testSnap(1, "http://ignored.invalid", func(s *registry.ChannelKeySnapshot) { s.Type = "custom" }),
	)
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.POST("/v1/chat/completions",
		func(c *gin.Context) { c.Set(middleware.CtxKeyKeyInfo, testKeyInfo()) },
		env.pipe.HandleChatCompletions)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[]}`))
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("panic 应被 Recovery 转 500, got %d", w.Code)
	}

	// 进程存活、管线可继续服务其他渠道。
	env2 := newTestEnv(t, testSnap(2, good.URL))
	if w2 := env2.do(t, `{"model":"gpt-4o","messages":[]}`); w2.Code != http.StatusOK {
		t.Fatalf("panic 后续请求 status = %d", w2.Code)
	}
}

// TestPerRequestPriceSnapshot 按次计费：InputPrice 快照位记 per_request 单价，
// 使 usage_log 的「单价 × 用量 = 成本」对账成立。
func TestPerRequestPriceSnapshot(t *testing.T) {
	var hits atomic.Int32
	var lastBody atomic.Value
	upstream := newGoodUpstream(t, &hits, &lastBody)
	defer upstream.Close()

	env := newTestEnv(t, testSnap(1, upstream.URL, func(s *registry.ChannelKeySnapshot) {
		s.Models[perRequestModel] = struct{}{}
		s.ModelMapping[perRequestModel] = "gpt-4o-upstream"
	}))
	w := env.do(t, `{"model":"`+perRequestModel+`","messages":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	rec := env.sink.last(t)
	if !almostEqual(rec.InputPrice, 0.02) {
		t.Errorf("InputPrice = %v, want 0.02（per_request 单价快照）", rec.InputPrice)
	}
	if !almostEqual(rec.InputCost, 0.02) || !almostEqual(rec.TotalCost, 0.02) {
		t.Errorf("成本 = (input %v, total %v), want 0.02", rec.InputCost, rec.TotalCost)
	}
}

// TestStreamUsageMissingWarns 流式成功但未捕获 usage：记 0 费并打 WARN（含渠道与模型），
// 让静默的计费缺口可被监控发现。
func TestStreamUsageMissingWarns(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, line := range []string{
			`data: {"id":"c1","choices":[{"delta":{"content":"hi"}}]}`,
			``,
			`data: [DONE]`,
			``,
		} {
			_, _ = io.WriteString(w, line+"\n")
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	env := newTestEnv(t, testSnap(1, upstream.URL))
	w := env.do(t, `{"model":"gpt-4o","stream":true,"messages":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	logs := buf.String()
	if !strings.Contains(logs, "relay_stream_usage_missing") {
		t.Errorf("缺少 relay_stream_usage_missing 告警日志:\n%s", logs)
	}
	if !strings.Contains(logs, "channel_key_id=1") || !strings.Contains(logs, "model=gpt-4o") {
		t.Errorf("告警日志缺少 channel_key_id/model 字段:\n%s", logs)
	}

	rec := env.sink.last(t)
	if rec.TotalCost != 0 || rec.InputTokens != 0 {
		t.Errorf("无 usage 流应记 0: %+v", rec)
	}
}

// ===== Responses API 端点 =====

// newResponsesUpstream Responses API mock 上游：
// 校验 path=/v1/responses、认证头、上游模型名，并断言请求体绝不含 stream_options；
// 非流式返回顶层 usage（含 input/output_tokens_details）；
// 流式返回 output_text.delta 事件 + response.completed 事件（usage 嵌套在 response.usage）。
func newResponsesUpstream(t *testing.T, hits *atomic.Int32, lastBody *atomic.Value) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
			t.Errorf("上游收到异常路由: %s %s", r.Method, r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer sk-up-") {
			t.Errorf("上游认证头异常: %q", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		lastBody.Store(string(body))
		// Responses 无 stream_options 概念：网关绝不注入，注入即污染上游请求。
		if strings.Contains(string(body), "stream_options") {
			t.Errorf("Responses 请求体不得含 stream_options: %s", body)
		}

		var req struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		_ = json.Unmarshal(body, &req)
		if req.Model != "gpt-4o-upstream" {
			t.Errorf("上游收到 model = %q, want gpt-4o-upstream（model_mapping 未生效）", req.Model)
		}

		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			// Responses 流：语义事件流，usage 只在 response.completed 事件的 response.usage；
			// 无 data: [DONE] 终止标志（流末即结束）。
			lines := []string{
				`event: response.output_text.delta`,
				`data: {"type":"response.output_text.delta","delta":"Hel"}`,
				``,
				`event: response.output_text.delta`,
				`data: {"type":"response.output_text.delta","delta":"lo"}`,
				``,
				`event: response.completed`,
				`data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-4o-upstream","usage":{"input_tokens":1000,"input_tokens_details":{"cached_tokens":200},"output_tokens":500,"output_tokens_details":{"reasoning_tokens":100},"total_tokens":1500}}}`,
				``,
			}
			for _, line := range lines {
				_, _ = io.WriteString(w, line+"\n")
				flusher.Flush()
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_1","object":"response","status":"completed","model":"gpt-4o-upstream",`+
			`"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}],`+
			`"usage":{"input_tokens":1000,"input_tokens_details":{"cached_tokens":200},`+
			`"output_tokens":500,"output_tokens_details":{"reasoning_tokens":100},"total_tokens":1500}}`)
	}))
}

// TestForwardResponsesNonStream Responses 非流式：响应透传（model 回写）+ 顶层 usage
// 计费金额正确（input/output_tokens_details 生效；reasoning 记账不叠加计费）。
func TestForwardResponsesNonStream(t *testing.T) {
	var hits atomic.Int32
	var lastBody atomic.Value
	upstream := newResponsesUpstream(t, &hits, &lastBody)
	defer upstream.Close()

	env := newTestEnv(t, testSnap(1, upstream.URL))
	w := env.doResponses(t, `{"model":"gpt-4o","input":"hi"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	if resp["model"] != "gpt-4o" {
		t.Errorf("响应 model = %v, want 回写为对外名 gpt-4o", resp["model"])
	}

	rec := env.sink.last(t)
	// input=(1000-200)/1e6×10=0.008；cached=200/1e6×5=0.001；output=500/1e6×30=0.015。
	const wantTotal = 0.008 + 0.001 + 0.015
	if !almostEqual(rec.TotalCost, wantTotal) {
		t.Errorf("TotalCost = %v, want %v", rec.TotalCost, wantTotal)
	}
	if !almostEqual(rec.ActualCost, wantTotal*2.0) { // billing rate 2.0
		t.Errorf("ActualCost = %v, want %v", rec.ActualCost, wantTotal*2.0)
	}
	if rec.InputTokens != 800 || rec.OutputTokens != 500 || rec.CachedInputTokens != 200 {
		t.Errorf("tokens = (%d,%d,%d), want (800,500,200)", rec.InputTokens, rec.OutputTokens, rec.CachedInputTokens)
	}
	if rec.ChannelID != 1 || rec.Model != "gpt-4o" || rec.Stream {
		t.Errorf("record 元数据异常: %+v", rec)
	}
	if rec.Endpoint != "/v1/responses" {
		t.Errorf("Endpoint = %q, want /v1/responses", rec.Endpoint)
	}
}

// TestForwardResponsesStream Responses 流式：逐事件透传 + 从 response.completed 事件
// 捕获 usage 计费；上游请求体不含 stream_options。
func TestForwardResponsesStream(t *testing.T) {
	var hits atomic.Int32
	var lastBody atomic.Value
	upstream := newResponsesUpstream(t, &hits, &lastBody)
	defer upstream.Close()

	env := newTestEnv(t, testSnap(1, upstream.URL))
	w := env.doResponses(t, `{"model":"gpt-4o","input":"hi","stream":true}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	// 逐事件透传：delta 事件与 completed 事件均下发（completed 是正常内容事件，不吞）。
	if !strings.Contains(body, `"delta":"Hel"`) || !strings.Contains(body, "response.completed") {
		t.Errorf("Responses SSE 未逐事件透传:\n%s", body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}

	// 关键：Responses 流式绝不注入 stream_options（否则污染上游请求）。
	sent, _ := lastBody.Load().(string)
	if strings.Contains(sent, "stream_options") {
		t.Errorf("Responses 流式请求体含 stream_options: %s", sent)
	}

	rec := env.sink.last(t)
	if !rec.Stream {
		t.Error("record.Stream 应为 true")
	}
	if rec.InputTokens != 800 || rec.OutputTokens != 500 || rec.CachedInputTokens != 200 {
		t.Errorf("流式 usage 从 completed 事件捕获失败: tokens = (%d,%d,%d)",
			rec.InputTokens, rec.OutputTokens, rec.CachedInputTokens)
	}
	const wantTotal = 0.008 + 0.001 + 0.015
	if !almostEqual(rec.TotalCost, wantTotal) {
		t.Errorf("流式 TotalCost = %v, want %v（应从 completed 事件计费）", rec.TotalCost, wantTotal)
	}
}

// TestForwardResponses404Failover 修3：仅 Responses 端点——渠道 A 只实现 /v1/chat/completions
// 对 /v1/responses 回 404 → 软排除换渠道 → 渠道 B（支持 responses）成功。
func TestForwardResponses404Failover(t *testing.T) {
	var goodHits, badHits atomic.Int32
	var lastBody atomic.Value
	good := newResponsesUpstream(t, &goodHits, &lastBody)
	defer good.Close()
	bad := newFailingUpstream(http.StatusNotFound, "", &badHits)
	defer bad.Close()

	env := newTestEnv(t,
		testSnap(1, good.URL, func(s *registry.ChannelKeySnapshot) { s.Priority = 1 }),
		testSnap(2, bad.URL, func(s *registry.ChannelKeySnapshot) { s.Priority = 100 }), // 高优先级先被选中
	)
	w := env.doResponses(t, `{"model":"gpt-4o","input":"hi"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（404 应 failover 到支持 responses 的渠道）; body = %s", w.Code, w.Body.String())
	}
	if badHits.Load() != 1 || goodHits.Load() != 1 {
		t.Errorf("hits bad=%d good=%d, want 1/1", badHits.Load(), goodHits.Load())
	}
	if env.sink.count() != 1 {
		t.Errorf("UsageRecord 条数 = %d, want 1（仅成功渠道计费）", env.sink.count())
	}
}

// TestForwardResponsesAll404 修3：两 Responses 渠道全回 404 → 客户端收到 404 原状态码
// （非 all-failed 的 5xx），错误体为按 404 语义重建的入口协议形态；全 404 不计费。
func TestForwardResponsesAll404(t *testing.T) {
	var hits1, hits2 atomic.Int32
	bad1 := newFailingUpstream(http.StatusNotFound, "", &hits1)
	defer bad1.Close()
	bad2 := newFailingUpstream(http.StatusNotFound, "", &hits2)
	defer bad2.Close()

	env := newTestEnv(t, testSnap(1, bad1.URL), testSnap(2, bad2.URL))
	w := env.doResponses(t, `{"model":"gpt-4o","input":"hi"}`)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 原状态码保留（非 5xx）; body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "upstream says no") {
		t.Errorf("重建错误体未保留上游 404 语义: %s", w.Body.String())
	}
	if env.sink.count() != 0 {
		t.Errorf("全 404 请求不应计费, got %d 条", env.sink.count())
	}
}

// TestForwardChat404Rebuilt 修3 回归：chat 端点 404 仍一次性语义重建终止不重试
// （404 failover 仅限 responses 端点，chat 行为不变）。
func TestForwardChat404Rebuilt(t *testing.T) {
	var hits atomic.Int32
	bad := newFailingUpstream(http.StatusNotFound, "", &hits)
	defer bad.Close()

	env := newTestEnv(t, testSnap(1, bad.URL))
	w := env.do(t, `{"model":"gpt-4o","messages":[]}`)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 一次性终止", w.Code)
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1（chat 404 不重试）", hits.Load())
	}
	if !strings.Contains(w.Body.String(), "upstream says no") {
		t.Errorf("重建错误体未保留上游 404 语义: %s", w.Body.String())
	}
}

// TestForwardResponsesMissingModel Responses 缺 model 字段 → 400。
func TestForwardResponsesMissingModel(t *testing.T) {
	env := newTestEnv(t, testSnap(1, "http://127.0.0.1:0"))
	w := env.doResponses(t, `{"input":"hi"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "missing_model") {
		t.Errorf("错误码不符: %s", w.Body.String())
	}
}

// ===== 上游请求日志（errlog）埋点守卫 =====

// TestErrLogTerminalPathsLeaveTrace 守卫：所有失败终止路径必产生上游请求日志。
// 未来在 forward.go 新增失败分支时，本表必须同步补场景（防悄悄重回无痕状态）。
func TestErrLogTerminalPathsLeaveTrace(t *testing.T) {
	t.Run("余额预检 402", func(t *testing.T) {
		env := newTestEnv(t, testSnap(1, "http://127.0.0.1:0"))
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[]}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		broke := testKeyInfo()
		broke.UserBalance = 0
		engine := gin.New()
		engine.POST("/v1/chat/completions", func(c *gin.Context) { c.Set(middleware.CtxKeyKeyInfo, broke) }, env.pipe.HandleChatCompletions)
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusPaymentRequired {
			t.Fatalf("status = %d, want 402", w.Code)
		}
		e := env.errSink.lastEntry(t)
		if e.Phase != errlog.PhasePrecheckBalance || e.Source != errlog.SourceRelay {
			t.Errorf("entry = %+v, want precheck_balance failure", e)
		}
		if e.UserID != broke.UserID || e.Model != "gpt-4o" {
			t.Errorf("entry 归属异常: %+v", e)
		}
	})

	t.Run("缺价预检 400", func(t *testing.T) {
		env := newTestEnv(t, testSnap(1, "http://127.0.0.1:0", func(s *registry.ChannelKeySnapshot) {
			s.Models["unpriced-model"] = struct{}{}
		}))
		w := env.do(t, `{"model":"unpriced-model","messages":[]}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
		e := env.errSink.lastEntry(t)
		if e.Phase != errlog.PhasePrecheckPrice || e.ErrorCode != "model_price_not_configured" {
			t.Errorf("entry = %+v, want precheck_price", e)
		}
	})

	t.Run("5xx 耗尽 upstream_exhausted 且带完整重试链", func(t *testing.T) {
		var hits atomic.Int32
		bad := newFailingUpstream(http.StatusInternalServerError, "", &hits)
		defer bad.Close()
		env := newTestEnv(t,
			testSnap(1, bad.URL), testSnap(2, bad.URL), testSnap(3, bad.URL), testSnap(4, bad.URL))
		w := env.do(t, `{"model":"gpt-4o","messages":[]}`)
		if w.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502; body=%s", w.Code, w.Body.String())
		}
		e := env.errSink.lastEntry(t)
		if e.Phase != errlog.PhaseUpstreamExhausted || e.ErrorCode != "upstream_error" {
			t.Errorf("entry = %+v, want upstream_exhausted/upstream_error", e)
		}
		if e.Attempts != 3 || len(e.Chain) != 3 {
			t.Errorf("attempts = %d, chain = %d, want 3/3", e.Attempts, len(e.Chain))
		}
		for i, hop := range e.Chain {
			if hop.Seq != i+1 || hop.Verdict != "transient" || hop.UpstreamStat != http.StatusInternalServerError {
				t.Errorf("hop[%d] = %+v", i, hop)
			}
			if hop.ChannelID == 0 || hop.ChannelName == "" {
				t.Errorf("hop[%d] 缺渠道快照: %+v", i, hop)
			}
		}
		// 渠道×verdict 计数器应逐 attempt 恒 INCR。
		verdictCounts := 0
		for _, cnt := range env.errSink.counts {
			if strings.Contains(cnt, ":transient:") {
				verdictCounts++
			}
		}
		if verdictCounts != 3 {
			t.Errorf("transient 计数 = %d, want 3", verdictCounts)
		}
	})

	t.Run("4xx 透传 upstream_client_error", func(t *testing.T) {
		var hits atomic.Int32
		bad := newFailingUpstream(http.StatusBadRequest, "", &hits)
		defer bad.Close()
		env := newTestEnv(t, testSnap(1, bad.URL))
		w := env.do(t, `{"model":"gpt-4o","messages":[]}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
		e := env.errSink.lastEntry(t)
		if e.Phase != errlog.PhaseUpstreamClientError || e.StatusCode != http.StatusBadRequest {
			t.Errorf("entry = %+v, want upstream_client_error 400", e)
		}
		if len(e.Chain) != 1 || e.Chain[0].Verdict != "clientError" {
			t.Errorf("chain = %+v, want 单跳 clientError", e.Chain)
		}
	})

	t.Run("无可用渠道 queue_timeout", func(t *testing.T) {
		env := newTestEnv(t) // 无渠道
		w := env.do(t, `{"model":"gpt-4o","messages":[]}`)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", w.Code)
		}
		e := env.errSink.lastEntry(t)
		if e.Phase != errlog.PhaseQueueTimeout || e.ErrorCode != "no_available_channel" {
			t.Errorf("entry = %+v, want queue_timeout/no_available_channel", e)
		}
	})

	t.Run("成功请求不产生失败留痕", func(t *testing.T) {
		var hits atomic.Int32
		var lastBody atomic.Value
		good := newGoodUpstream(t, &hits, &lastBody)
		defer good.Close()
		env := newTestEnv(t, testSnap(1, good.URL))
		w := env.do(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
		}
		if n := env.errSink.entryCount(); n != 0 {
			t.Errorf("成功请求产生了 %d 条失败留痕: %+v", n, env.errSink.entries)
		}
	})
}

// ===== 原生协议入口（纯透传）：/v1/messages 与 /v1beta generateContent =====

// anthSnap 构造 anthropic 类型渠道快照。
func anthSnap(id int, baseURL string, mutate ...func(*registry.ChannelKeySnapshot)) registry.ChannelKeySnapshot {
	s := testSnap(id, baseURL, func(s *registry.ChannelKeySnapshot) {
		s.Type = "anthropic"
		s.Models = map[string]struct{}{anthModel: {}}
		s.ModelMapping = map[string]string{anthModel: "claude-upstream"}
	})
	for _, m := range mutate {
		m(&s)
	}
	return s
}

// gemSnap 构造 gemini 类型渠道快照。
func gemSnap(id int, baseURL string, mutate ...func(*registry.ChannelKeySnapshot)) registry.ChannelKeySnapshot {
	s := testSnap(id, baseURL, func(s *registry.ChannelKeySnapshot) {
		s.Type = "gemini"
		s.Models = map[string]struct{}{gemModel: {}}
		s.ModelMapping = map[string]string{gemModel: "gemini-upstream"}
	})
	for _, m := range mutate {
		m(&s)
	}
	return s
}

func (e *testEnv) doMessages(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

func (e *testEnv) doGemini(t *testing.T, modelAction, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/"+modelAction, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

// newAnthropicUpstream 假 Anthropic 上游：校验 /v1/messages 路由、x-api-key 认证、
// 上游模型名（model_mapping），记录收到的请求体；
// 非流式回原生 message（含双档缓存写 usage），流式回原生 SSE 事件流。
func newAnthropicUpstream(t *testing.T, hits *atomic.Int32, lastBody *atomic.Value) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Errorf("上游收到异常路由: %s %s", r.Method, r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("x-api-key"), "sk-up-") {
			t.Errorf("上游 x-api-key 异常: %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Error("缺少 anthropic-version 头")
		}
		body, _ := io.ReadAll(r.Body)
		lastBody.Store(string(body))

		var req struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		_ = json.Unmarshal(body, &req)
		if req.Model != "claude-upstream" {
			t.Errorf("上游收到 model = %q, want claude-upstream（model_mapping 未生效）", req.Model)
		}

		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			lines := []string{
				`event: message_start`,
				`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":800,"cache_read_input_tokens":200}}}`,
				``,
				`event: content_block_delta`,
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`,
				``,
				`event: content_block_delta`,
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`,
				``,
				`event: message_delta`,
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":500}}`,
				``,
				`event: message_stop`,
				`data: {"type":"message_stop"}`,
				``,
			}
			for _, line := range lines {
				_, _ = io.WriteString(w, line+"\n")
				flusher.Flush()
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-upstream",`+
			`"content":[{"type":"text","text":"Hello"}],"stop_reason":"end_turn",`+
			`"usage":{"input_tokens":800,"output_tokens":500,"cache_read_input_tokens":200,`+
			`"cache_creation_input_tokens":7,"cache_creation":{"ephemeral_5m_input_tokens":3,"ephemeral_1h_input_tokens":4}}}`)
	}))
}

// newGeminiUpstream 假 Gemini 上游：校验 generateContent 路由（model 在 URL、流式带
// alt=sse）、x-goog-api-key 认证，记录收到的请求体；
// 非流式回原生 generateContent 响应，流式回每帧完整 JSON chunk 的 SSE。
func newGeminiUpstream(t *testing.T, hits *atomic.Int32, lastBody *atomic.Value) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		stream := strings.HasSuffix(r.URL.Path, ":streamGenerateContent")
		if !stream && !strings.HasSuffix(r.URL.Path, ":generateContent") {
			t.Errorf("上游收到异常路由: %s %s", r.Method, r.URL.Path)
		}
		if !strings.Contains(r.URL.Path, "/models/gemini-upstream:") {
			t.Errorf("上游 URL 未用映射后的模型名: %s", r.URL.Path)
		}
		if stream && r.URL.Query().Get("alt") != "sse" {
			t.Errorf("流式请求缺少 alt=sse: %s", r.URL.RawQuery)
		}
		if !strings.HasPrefix(r.Header.Get("x-goog-api-key"), "sk-up-") {
			t.Errorf("上游 x-goog-api-key 异常: %q", r.Header.Get("x-goog-api-key"))
		}
		body, _ := io.ReadAll(r.Body)
		lastBody.Store(string(body))
		if strings.Contains(string(body), `"model"`) {
			t.Errorf("Gemini 请求体不得携带 model 字段: %s", body)
		}

		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			lines := []string{
				`data: {"candidates":[{"content":{"parts":[{"text":"Hel"}],"role":"model"}}]}`,
				``,
				`data: {"candidates":[{"content":{"parts":[{"text":"lo"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1000,"candidatesTokenCount":400,"thoughtsTokenCount":100,"cachedContentTokenCount":200,"totalTokenCount":1500}}`,
				``,
			}
			for _, line := range lines {
				_, _ = io.WriteString(w, line+"\n")
				flusher.Flush()
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"},"finishReason":"STOP"}],`+
			`"usageMetadata":{"promptTokenCount":1000,"candidatesTokenCount":400,"thoughtsTokenCount":100,"cachedContentTokenCount":200,"totalTokenCount":1500},`+
			`"modelVersion":"gemini-upstream"}`)
	}))
}

// TestForwardMessagesNonStream /v1/messages 非流式：原生请求体透传（工具等字段原样进上游）、
// 响应 model 回写对外名且原生结构不被翻译、usage 归一化落账（含双档缓存写）。
func TestForwardMessagesNonStream(t *testing.T) {
	var hits atomic.Int32
	var lastBody atomic.Value
	upstream := newAnthropicUpstream(t, &hits, &lastBody)
	defer upstream.Close()

	env := newTestEnv(t, anthSnap(1, upstream.URL))
	w := env.doMessages(t, `{"model":"`+anthModel+`","max_tokens":64,`+
		`"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],`+
		`"tools":[{"name":"get_weather","input_schema":{"type":"object"}}],"metadata":{"user_id":"u1"}}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	// 请求体透传：原生字段原样进上游（tools/metadata 不被丢弃或翻译）。
	sent, _ := lastBody.Load().(string)
	for _, want := range []string{`"tools"`, `"get_weather"`, `"metadata"`, `"max_tokens":64`} {
		if !strings.Contains(sent, want) {
			t.Errorf("上游请求体缺少透传字段 %s: %s", want, sent)
		}
	}

	// 响应保持 Anthropic 原生形态，model 回写对外名。
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	if resp["model"] != anthModel {
		t.Errorf("响应 model = %v, want 回写为对外名 %s", resp["model"], anthModel)
	}
	if resp["type"] != "message" || resp["stop_reason"] != "end_turn" {
		t.Errorf("响应原生结构被改写: %v", resp)
	}

	rec := env.sink.last(t)
	// 归一化：PromptTokens=800+200=1000（含缓存读）→ input=(1000-200)=800。
	if rec.InputTokens != 800 || rec.OutputTokens != 500 || rec.CachedInputTokens != 200 {
		t.Errorf("tokens = (%d,%d,%d), want (800,500,200)", rec.InputTokens, rec.OutputTokens, rec.CachedInputTokens)
	}
	if rec.CacheCreationTokens != 7 || rec.CacheCreation5mTokens != 3 || rec.CacheCreation1hTokens != 4 {
		t.Errorf("双档缓存写明细 = (%d,%d,%d), want (7,3,4)",
			rec.CacheCreationTokens, rec.CacheCreation5mTokens, rec.CacheCreation1hTokens)
	}
	const wantTotal = 0.008 + 0.001 + 0.015
	if !almostEqual(rec.TotalCost, wantTotal) {
		t.Errorf("TotalCost = %v, want %v", rec.TotalCost, wantTotal)
	}
	if !almostEqual(rec.ActualCost, wantTotal*2.0) {
		t.Errorf("ActualCost = %v, want %v", rec.ActualCost, wantTotal*2.0)
	}
	if rec.Model != anthModel || rec.Endpoint != "/v1/messages" || rec.Stream {
		t.Errorf("record 元数据异常: %+v", rec)
	}
}

// TestForwardMessagesStream /v1/messages 流式：原生 SSE 字节级透传（event 行/事件结构
// 无一改写、不注入 [DONE]）+ 观察器旁路捕获 usage 落账。
func TestForwardMessagesStream(t *testing.T) {
	var hits atomic.Int32
	var lastBody atomic.Value
	upstream := newAnthropicUpstream(t, &hits, &lastBody)
	defer upstream.Close()

	env := newTestEnv(t, anthSnap(1, upstream.URL))
	w := env.doMessages(t, `{"model":"`+anthModel+`","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	// 原生事件原样下发：event 行保留、无 OpenAI chunk、无伪造 [DONE]。
	for _, want := range []string{"event: message_start", `"text":"Hel"`, "event: message_stop"} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE 未原样透传（缺 %s）:\n%s", want, body)
		}
	}
	if strings.Contains(body, "chat.completion.chunk") || strings.Contains(body, "data: [DONE]") {
		t.Errorf("原生流被翻译/注入:\n%s", body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	// 请求体透传：不得注入 stream_options（OpenAI 专属字段）。
	sent, _ := lastBody.Load().(string)
	if strings.Contains(sent, "stream_options") {
		t.Errorf("anthropic 请求体被注入 stream_options: %s", sent)
	}

	rec := env.sink.last(t)
	if !rec.Stream {
		t.Error("record.Stream 应为 true")
	}
	if rec.InputTokens != 800 || rec.OutputTokens != 500 || rec.CachedInputTokens != 200 {
		t.Errorf("流式 usage 观察失败: tokens = (%d,%d,%d)", rec.InputTokens, rec.OutputTokens, rec.CachedInputTokens)
	}
	const wantTotal = 0.008 + 0.001 + 0.015
	if !almostEqual(rec.TotalCost, wantTotal) {
		t.Errorf("TotalCost = %v, want %v", rec.TotalCost, wantTotal)
	}
	// 完整流（message_stop 已达）不应有失败留痕。
	if n := env.errSink.entryCount(); n != 0 {
		t.Errorf("完整流产生了 %d 条失败留痕", n)
	}
}

// TestForwardMessagesFailover anthropic 渠道 429 硬排除 → 换同协议渠道成功；
// 同模型的 openai 渠道存在但绝不被 /v1/messages 调度（协议隔离）。
func TestForwardMessagesFailover(t *testing.T) {
	var goodHits, badHits, oaiHits atomic.Int32
	var lastBody atomic.Value
	good := newAnthropicUpstream(t, &goodHits, &lastBody)
	defer good.Close()
	bad := newFailingUpstream(http.StatusTooManyRequests, "5", &badHits)
	defer bad.Close()
	oai := newFailingUpstream(http.StatusOK, "", &oaiHits) // 若被调度即 hits>0
	defer oai.Close()

	env := newTestEnv(t,
		anthSnap(1, good.URL, func(s *registry.ChannelKeySnapshot) { s.Priority = 1 }),
		anthSnap(2, bad.URL, func(s *registry.ChannelKeySnapshot) { s.Priority = 100 }), // 高优先级先选中
		testSnap(3, oai.URL, func(s *registry.ChannelKeySnapshot) { // openai 渠道也声明同模型
			s.Priority = 200
			s.Models = map[string]struct{}{anthModel: {}}
		}),
	)
	w := env.doMessages(t, `{"model":"`+anthModel+`","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if badHits.Load() != 1 || goodHits.Load() != 1 {
		t.Errorf("hits bad=%d good=%d, want 1/1", badHits.Load(), goodHits.Load())
	}
	if oaiHits.Load() != 0 {
		t.Errorf("openai 渠道被 anthropic 协议入口调度: hits=%d", oaiHits.Load())
	}
	// 429 不产生任何状态落库（无冷却机制）。
	if got := env.persister.callCount(); got != 0 {
		t.Errorf("429 不应有状态落库, got %d 次", got)
	}
	if env.sink.count() != 1 {
		t.Errorf("UsageRecord 条数 = %d, want 1（仅成功渠道计费）", env.sink.count())
	}
}

// TestUpstreamClientErrorRebuiltNativeShape 上游 4xx 语义重建按入口协议出原生形态：
// anthropic 入口重建 Anthropic 错误体（上游原生 type 保留），
// gemini 入口重建 Gemini 错误体（上游 canonical status 保留）；HTTP 状态码保留原值。
func TestUpstreamClientErrorRebuiltNativeShape(t *testing.T) {
	t.Run("anthropic 入口", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens required"}}`)
		}))
		defer upstream.Close()

		env := newTestEnv(t, anthSnap(1, upstream.URL))
		w := env.doMessages(t, `{"model":"`+anthModel+`","max_tokens":8,"messages":[]}`)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 原状态码保留", w.Code)
		}
		var resp errfmt.AnthropicError
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("错误体非 JSON: %v", err)
		}
		if resp.Type != "error" || resp.Error.Type != "invalid_request_error" {
			t.Errorf("错误体非 Anthropic 原生形态: %s", w.Body.String())
		}
		if resp.Error.Message != "max_tokens required" {
			t.Errorf("message = %q, want 保留上游语义", resp.Error.Message)
		}
	})

	t.Run("gemini 入口", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"code":400,"message":"invalid contents","status":"INVALID_ARGUMENT"}}`)
		}))
		defer upstream.Close()

		env := newTestEnv(t, gemSnap(1, upstream.URL))
		w := env.doGemini(t, gemModel+":generateContent", `{"contents":[]}`)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 原状态码保留", w.Code)
		}
		var resp errfmt.GeminiError
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("错误体非 JSON: %v", err)
		}
		if resp.Error.Code != 400 || resp.Error.Status != "INVALID_ARGUMENT" {
			t.Errorf("错误体非 Gemini 原生形态: %s", w.Body.String())
		}
		if resp.Error.Message != "invalid contents" {
			t.Errorf("message = %q, want 保留上游语义", resp.Error.Message)
		}
	})

	t.Run("上游错误体不可解析回落状态码文案", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
		}))
		defer upstream.Close()

		env := newTestEnv(t, testSnap(1, upstream.URL))
		w := env.do(t, `{"model":"gpt-4o","messages":[]}`)

		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", w.Code)
		}
		var resp errfmt.OpenAIError
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("错误体非 JSON: %v", err)
		}
		if resp.Error.Message != "上游返回状态码 422" {
			t.Errorf("message = %q, want 状态码兜底文案", resp.Error.Message)
		}
	})
}

// TestMessagesNoChannelAnthropicErrorShape 协议隔离 + 错误形态：仅 openai 渠道
// 服务该模型时 /v1/messages 无可用渠道，错误体为 Anthropic 原生形态。
func TestMessagesNoChannelAnthropicErrorShape(t *testing.T) {
	env := newTestEnv(t, testSnap(1, "http://127.0.0.1:0", func(s *registry.ChannelKeySnapshot) {
		s.Models = map[string]struct{}{anthModel: {}}
	}))
	w := env.doMessages(t, `{"model":"`+anthModel+`","max_tokens":8,"messages":[]}`)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
	var resp errfmt.AnthropicError
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("错误体非 JSON: %v", err)
	}
	if resp.Type != "error" || resp.Error.Type != "api_error" {
		t.Errorf("错误体非 Anthropic 形态: %s", w.Body.String())
	}
}

// TestForwardGenerateContentNonStream gemini 非流式：model 取自 URL、映射发生在
// 上游 URL 层、请求体透传（generationConfig 等原样）、usage 落账（thoughts 并入输出）。
func TestForwardGenerateContentNonStream(t *testing.T) {
	var hits atomic.Int32
	var lastBody atomic.Value
	upstream := newGeminiUpstream(t, &hits, &lastBody)
	defer upstream.Close()

	env := newTestEnv(t, gemSnap(1, upstream.URL))
	w := env.doGemini(t, gemModel+":generateContent",
		`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"temperature":0.5},"safetySettings":[{"category":"X","threshold":"BLOCK_NONE"}]}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	sent, _ := lastBody.Load().(string)
	for _, want := range []string{`"generationConfig"`, `"temperature":0.5`, `"safetySettings"`} {
		if !strings.Contains(sent, want) {
			t.Errorf("上游请求体缺少透传字段 %s: %s", want, sent)
		}
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	if _, ok := resp["candidates"]; !ok {
		t.Errorf("响应原生结构被改写: %v", resp)
	}
	if resp["modelVersion"] != gemModel {
		t.Errorf("modelVersion = %v, want 回写对外名 %s", resp["modelVersion"], gemModel)
	}

	rec := env.sink.last(t)
	// promptTokenCount=1000（含缓存 200）→ input=800；输出 = 400 正文 + 100 思考 = 500。
	if rec.InputTokens != 800 || rec.OutputTokens != 500 || rec.CachedInputTokens != 200 {
		t.Errorf("tokens = (%d,%d,%d), want (800,500,200)", rec.InputTokens, rec.OutputTokens, rec.CachedInputTokens)
	}
	const wantTotal = 0.008 + 0.001 + 0.015
	if !almostEqual(rec.TotalCost, wantTotal) {
		t.Errorf("TotalCost = %v, want %v", rec.TotalCost, wantTotal)
	}
	if rec.Model != gemModel || rec.Stream {
		t.Errorf("record 元数据异常: %+v", rec)
	}
}

// TestForwardGenerateContentStream gemini 流式（:streamGenerateContent 定流式 +
// 上游强制 alt=sse）：SSE 原样透传 + usageMetadata 观察落账。
func TestForwardGenerateContentStream(t *testing.T) {
	var hits atomic.Int32
	var lastBody atomic.Value
	upstream := newGeminiUpstream(t, &hits, &lastBody)
	defer upstream.Close()

	env := newTestEnv(t, gemSnap(1, upstream.URL))
	w := env.doGemini(t, gemModel+":streamGenerateContent?alt=sse",
		`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"text":"Hel"`) || !strings.Contains(body, `"finishReason":"STOP"`) {
		t.Errorf("Gemini SSE 未原样透传:\n%s", body)
	}
	if strings.Contains(body, "data: [DONE]") || strings.Contains(body, "chat.completion.chunk") {
		t.Errorf("原生流被翻译/注入:\n%s", body)
	}

	rec := env.sink.last(t)
	if !rec.Stream {
		t.Error("record.Stream 应为 true")
	}
	if rec.InputTokens != 800 || rec.OutputTokens != 500 || rec.CachedInputTokens != 200 {
		t.Errorf("流式 usage 观察失败: tokens = (%d,%d,%d)", rec.InputTokens, rec.OutputTokens, rec.CachedInputTokens)
	}
	// 完整流（finishReason 已达）不应有失败留痕。
	if n := env.errSink.entryCount(); n != 0 {
		t.Errorf("完整流产生了 %d 条失败留痕", n)
	}
}

// TestForwardGeminiFailover gemini 渠道 500 软排除 → 换同协议渠道成功。
func TestForwardGeminiFailover(t *testing.T) {
	var goodHits, badHits atomic.Int32
	var lastBody atomic.Value
	good := newGeminiUpstream(t, &goodHits, &lastBody)
	defer good.Close()
	bad := newFailingUpstream(http.StatusInternalServerError, "", &badHits)
	defer bad.Close()

	env := newTestEnv(t,
		gemSnap(1, good.URL, func(s *registry.ChannelKeySnapshot) { s.Priority = 1 }),
		gemSnap(2, bad.URL, func(s *registry.ChannelKeySnapshot) { s.Priority = 100 }),
	)
	w := env.doGemini(t, gemModel+":generateContent", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if badHits.Load() != 1 || goodHits.Load() != 1 {
		t.Errorf("hits bad=%d good=%d, want 1/1", badHits.Load(), goodHits.Load())
	}
	if env.sink.count() != 1 {
		t.Errorf("UsageRecord 条数 = %d, want 1", env.sink.count())
	}
}

// TestGenerateContentGeminiErrorShape gemini 入口的网关自产错误按 Gemini 原生形态出：
// 未知动词 404 NOT_FOUND；缺价模型 400 INVALID_ARGUMENT。
func TestGenerateContentGeminiErrorShape(t *testing.T) {
	env := newTestEnv(t, gemSnap(1, "http://127.0.0.1:0", func(s *registry.ChannelKeySnapshot) {
		s.Models["unpriced-gem"] = struct{}{}
	}))

	t.Run("未知动词 404", func(t *testing.T) {
		// countTokens/predict 已是受支持动词，用仍不支持的 embedContent 验证未知动词形态。
		w := env.doGemini(t, gemModel+":embedContent", `{"content":{"parts":[]}}`)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body = %s", w.Code, w.Body.String())
		}
		var resp errfmt.GeminiError
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("错误体非 JSON: %v", err)
		}
		if resp.Error.Code != 404 || resp.Error.Status != "NOT_FOUND" {
			t.Errorf("错误体非 Gemini 形态: %s", w.Body.String())
		}
	})

	t.Run("缺价模型 400", func(t *testing.T) {
		w := env.doGemini(t, "unpriced-gem:generateContent", `{"contents":[]}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
		}
		var resp errfmt.GeminiError
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("错误体非 JSON: %v", err)
		}
		if resp.Error.Code != 400 || resp.Error.Status != "INVALID_ARGUMENT" {
			t.Errorf("错误体非 Gemini 形态: %s", w.Body.String())
		}
	})
}

// ===== 图像与辅助端点（generations / edits / predict / countTokens / v1beta models） =====

func (e *testEnv) doImagesGenerations(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

func (e *testEnv) doImagesEdits(t *testing.T, body []byte, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

// imgSnap 构造服务图像模型的 openai_compatible 渠道快照。
func imgSnap(id int, baseURL string, mutate ...func(*registry.ChannelKeySnapshot)) registry.ChannelKeySnapshot {
	s := testSnap(id, baseURL, func(s *registry.ChannelKeySnapshot) {
		s.Models = map[string]struct{}{imgPerReqModel: {}, testModel: {}}
		s.ModelMapping = map[string]string{imgPerReqModel: "gpt-image-upstream", testModel: "gpt-4o-upstream"}
	})
	for _, m := range mutate {
		m(&s)
	}
	return s
}

// TestForwardImagesGenerationsPerImageBilling 生图非流式 + 按次计费：
// data 张数（以响应为准，非请求 n）× per_request 单价落账；InputPrice 快照为按次单价。
func TestForwardImagesGenerationsPerImageBilling(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/images/generations" {
			t.Errorf("上游收到异常路由: %s %s", r.Method, r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer sk-up-") {
			t.Errorf("上游认证头异常: %q", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model string `json:"model"`
			N     int    `json:"n"`
		}
		_ = json.Unmarshal(body, &req)
		if req.Model != "gpt-image-upstream" {
			t.Errorf("上游收到 model = %q, want gpt-image-upstream（model_mapping 未生效）", req.Model)
		}
		// 请求 n=3，实际只产出 2 张：计费须以响应为准。
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"created":1,"data":[{"b64_json":"QUJD"},{"b64_json":"REVG"}]}`)
	}))
	defer upstream.Close()

	env := newTestEnv(t, imgSnap(1, upstream.URL))
	w := env.doImagesGenerations(t, `{"model":"`+imgPerReqModel+`","prompt":"a cat","n":3}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1", hits.Load())
	}
	if !strings.Contains(w.Body.String(), `"b64_json"`) {
		t.Errorf("响应未透传: %s", w.Body.String())
	}

	rec := env.sink.last(t)
	// 按次×张数：0.04 × 2 = 0.08；billing rate 2.0 → actual 0.16。
	if !almostEqual(rec.TotalCost, 0.08) {
		t.Errorf("TotalCost = %v, want 0.08（0.04×2 张）", rec.TotalCost)
	}
	if !almostEqual(rec.ActualCost, 0.16) {
		t.Errorf("ActualCost = %v, want 0.16", rec.ActualCost)
	}
	if !almostEqual(rec.InputPrice, 0.04) {
		t.Errorf("InputPrice = %v, want 0.04（per_request 单价快照）", rec.InputPrice)
	}
	if rec.Calls != 2 {
		t.Errorf("Calls = %d, want 2（张数落账，供对账）", rec.Calls)
	}
	if rec.Endpoint != "/v1/images/generations" || rec.Model != imgPerReqModel || rec.Stream {
		t.Errorf("record 元数据异常: %+v", rec)
	}
}

// TestForwardImagesGenerationsTokenBilling 生图 + token 计费（per_request 未配置）：
// gpt-image 系 usage（input/output_tokens + input_tokens_details）归一化按 token 落账，
// 张数不参与计费。
func TestForwardImagesGenerationsTokenBilling(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"created":1,"data":[{"b64_json":"QUJD"},{"b64_json":"REVG"}],`+
			`"usage":{"input_tokens":1000,"output_tokens":500,"total_tokens":1500,`+
			`"input_tokens_details":{"image_tokens":800,"text_tokens":200}}}`)
	}))
	defer upstream.Close()

	env := newTestEnv(t, imgSnap(1, upstream.URL))
	w := env.doImagesGenerations(t, `{"model":"`+testModel+`","prompt":"a cat"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	rec := env.sink.last(t)
	// token 计费：input 1000/1e6×10=0.01；output 500/1e6×30=0.015（张数不参与）。
	const wantTotal = 0.01 + 0.015
	if !almostEqual(rec.TotalCost, wantTotal) {
		t.Errorf("TotalCost = %v, want %v", rec.TotalCost, wantTotal)
	}
	if rec.InputTokens != 1000 || rec.OutputTokens != 500 {
		t.Errorf("tokens = (%d,%d), want (1000,500)", rec.InputTokens, rec.OutputTokens)
	}
}

// newImageStreamUpstream 图像流 mock 上游（gpt-image 系 stream:true 事件流）：
// partial_image ×2 + completed（携带整幅 b64 与 usage）。eventPrefix 区分
// image_generation / image_edit 事件族。
func newImageStreamUpstream(t *testing.T, wantPath, eventPrefix string, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != wantPath {
			t.Errorf("上游收到异常路由: %s %s, want %s", r.Method, r.URL.Path, wantPath)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		lines := []string{
			`event: ` + eventPrefix + `.partial_image`,
			`data: {"type":"` + eventPrefix + `.partial_image","partial_image_index":0,"b64_json":"UEFSVDA="}`,
			``,
			`event: ` + eventPrefix + `.partial_image`,
			`data: {"type":"` + eventPrefix + `.partial_image","partial_image_index":1,"b64_json":"UEFSVDE="}`,
			``,
			`event: ` + eventPrefix + `.completed`,
			`data: {"type":"` + eventPrefix + `.completed","b64_json":"RklOQUw=","size":"1024x1024",` +
				`"usage":{"input_tokens":150,"output_tokens":4160,"total_tokens":4310,` +
				`"input_tokens_details":{"image_tokens":100,"text_tokens":50}}}`,
			``,
		}
		for _, line := range lines {
			_, _ = io.WriteString(w, line+"\n")
			flusher.Flush()
		}
	}))
}

// TestForwardImagesGenerationsStream 生图流式（stream:true）：SSE 原样透传
// （partial/completed 事件无一被吞、不注入 [DONE]）+ 观察器旁路捕获
// completed 的 usage 与张数；按次计费 = 单价 × completed 事件数。
func TestForwardImagesGenerationsStream(t *testing.T) {
	var hits atomic.Int32
	upstream := newImageStreamUpstream(t, "/v1/images/generations", "image_generation", &hits)
	defer upstream.Close()

	env := newTestEnv(t, imgSnap(1, upstream.URL))
	w := env.doImagesGenerations(t, `{"model":"`+imgPerReqModel+`","prompt":"a cat","stream":true}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"event: image_generation.partial_image", `"UEFSVDA="`, "event: image_generation.completed", `"RklOQUw="`} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE 未原样透传（缺 %s）:\n%s", want, body)
		}
	}
	if strings.Contains(body, "data: [DONE]") {
		t.Errorf("图像流被注入 [DONE]:\n%s", body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}

	rec := env.sink.last(t)
	if !rec.Stream {
		t.Error("record.Stream 应为 true")
	}
	// 按次：0.04 × 1 个 completed 事件 = 0.04；token 计量与张数照常落账。
	if rec.Calls != 1 {
		t.Errorf("Calls = %d, want 1（completed 事件数）", rec.Calls)
	}
	if !almostEqual(rec.TotalCost, 0.04) {
		t.Errorf("TotalCost = %v, want 0.04", rec.TotalCost)
	}
	if rec.InputTokens != 150 || rec.OutputTokens != 4160 {
		t.Errorf("tokens = (%d,%d), want (150,4160)（usage 观察落账）", rec.InputTokens, rec.OutputTokens)
	}
	// 完整流（completed 已达）不应有失败留痕。
	if n := env.errSink.entryCount(); n != 0 {
		t.Errorf("完整图像流产生了 %d 条失败留痕", n)
	}
}

// TestForwardImagesGenerationsStreamTokenBilling 生图流式 + token 计费：
// completed 事件的 usage 按 token 落账（张数不参与计费、但照常落 Calls 列）。
func TestForwardImagesGenerationsStreamTokenBilling(t *testing.T) {
	var hits atomic.Int32
	upstream := newImageStreamUpstream(t, "/v1/images/generations", "image_generation", &hits)
	defer upstream.Close()

	env := newTestEnv(t, imgSnap(1, upstream.URL))
	w := env.doImagesGenerations(t, `{"model":"`+testModel+`","prompt":"a cat","stream":true}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	rec := env.sink.last(t)
	// input 150/1e6×10=0.0015；output 4160/1e6×30=0.1248。
	const wantTotal = 0.0015 + 0.1248
	if !almostEqual(rec.TotalCost, wantTotal) {
		t.Errorf("TotalCost = %v, want %v", rec.TotalCost, wantTotal)
	}
	if rec.Calls != 1 {
		t.Errorf("Calls = %d, want 1（张数照常落账）", rec.Calls)
	}
}

// TestForwardImagesEditsMultipart 图像编辑 multipart 透传（渠道带 model_mapping）：
// model 普通字段定点重写为上游名，其余字段与文件字节原样、boundary/Content-Type 不变；
// 按张数计费。
func TestForwardImagesEditsMultipart(t *testing.T) {
	sentBody, sentCT := buildMultipart(t,
		map[string]string{"model": imgPerReqModel, "prompt": "make it blue"},
		map[string][]byte{"image": []byte("PNGDATA")},
	)

	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/images/edits" {
			t.Errorf("上游收到异常路由: %s %s", r.Method, r.URL.Path)
		}
		// boundary 沿用原值 → Content-Type 头逐字节不变。
		if got := r.Header.Get("Content-Type"); got != sentCT {
			t.Errorf("Content-Type = %q, want 原样 %q", got, sentCT)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer sk-up-") {
			t.Errorf("上游认证头异常: %q", r.Header.Get("Authorization"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("上游解析 multipart 失败: %v", err)
			return
		}
		// model_mapping 定点重写生效，其余字段与文件字节原样。
		if got := r.FormValue("model"); got != "gpt-image-upstream" {
			t.Errorf("model = %q, want 重写为 gpt-image-upstream", got)
		}
		if got := r.FormValue("prompt"); got != "make it blue" {
			t.Errorf("prompt = %q, want 原样透传", got)
		}
		file, _, err := r.FormFile("image")
		if err != nil {
			t.Errorf("上游读文件 part 失败: %v", err)
		} else {
			fileBytes, _ := io.ReadAll(file)
			_ = file.Close()
			if !bytes.Equal(fileBytes, []byte("PNGDATA")) {
				t.Errorf("文件字节被改写: %q", fileBytes)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"created":1,"data":[{"b64_json":"QUJD"}]}`)
	}))
	defer upstream.Close()

	env := newTestEnv(t, imgSnap(1, upstream.URL))
	w := env.doImagesEdits(t, sentBody, sentCT)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1", hits.Load())
	}
	rec := env.sink.last(t)
	// 0.04 × 1 张 = 0.04。
	if !almostEqual(rec.TotalCost, 0.04) {
		t.Errorf("TotalCost = %v, want 0.04", rec.TotalCost)
	}
	if rec.Endpoint != "/v1/images/edits" || rec.Model != imgPerReqModel {
		t.Errorf("record 元数据异常: %+v", rec)
	}

	t.Run("非 multipart Content-Type 400", func(t *testing.T) {
		w := env.doImagesEdits(t, []byte(`{"model":"x"}`), "application/json")
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_multipart") {
			t.Errorf("status = %d, body = %s, want 400 invalid_multipart", w.Code, w.Body.String())
		}
	})
}

// TestForwardImagesEditsMultipartNoMappingIdentity 渠道无 model_mapping 时
// multipart 体逐字节原样直发（零重组守卫）。
func TestForwardImagesEditsMultipartNoMappingIdentity(t *testing.T) {
	sentBody, sentCT := buildMultipart(t,
		map[string]string{"model": imgPerReqModel, "prompt": "x"},
		map[string][]byte{"image": []byte("PNGDATA")},
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != sentCT {
			t.Errorf("Content-Type = %q, want 原样 %q", got, sentCT)
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Equal(body, sentBody) {
			t.Error("无映射时 multipart 体必须逐字节原样透传（不得重组）")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"created":1,"data":[{"b64_json":"QUJD"}]}`)
	}))
	defer upstream.Close()

	env := newTestEnv(t, imgSnap(1, upstream.URL, func(s *registry.ChannelKeySnapshot) {
		delete(s.ModelMapping, imgPerReqModel) // 无映射 → 原样直发路径
	}))
	w := env.doImagesEdits(t, sentBody, sentCT)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

// TestForwardImagesEditsStream 图像编辑流式：multipart 的 stream=true 字段定流式，
// SSE 原样透传（image_edit 事件族）+ 观察器计量按张落账。
func TestForwardImagesEditsStream(t *testing.T) {
	var hits atomic.Int32
	upstream := newImageStreamUpstream(t, "/v1/images/edits", "image_edit", &hits)
	defer upstream.Close()

	body, ct := buildMultipart(t,
		map[string]string{"model": imgPerReqModel, "stream": "true"},
		map[string][]byte{"image": []byte("PNGDATA")},
	)
	env := newTestEnv(t, imgSnap(1, upstream.URL))
	w := env.doImagesEdits(t, body, ct)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	got := w.Body.String()
	if !strings.Contains(got, "event: image_edit.completed") || !strings.Contains(got, `"RklOQUw="`) {
		t.Errorf("edits SSE 未原样透传:\n%s", got)
	}

	rec := env.sink.last(t)
	if !rec.Stream || rec.Calls != 1 {
		t.Errorf("record = stream:%v calls:%d, want stream:true calls:1", rec.Stream, rec.Calls)
	}
	if !almostEqual(rec.TotalCost, 0.04) {
		t.Errorf("TotalCost = %v, want 0.04", rec.TotalCost)
	}
	if n := env.errSink.entryCount(); n != 0 {
		t.Errorf("完整图像流产生了 %d 条失败留痕", n)
	}
}

// TestForwardPredictPerImageBilling gemini :predict（Imagen）：
// URL 动词分支 + predictions 张数 × per_request 计费；请求体原样透传。
func TestForwardPredictPerImageBilling(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if !strings.HasSuffix(r.URL.Path, "/models/imagen-upstream:predict") {
			t.Errorf("上游收到异常路由: %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("x-goog-api-key"), "sk-up-") {
			t.Errorf("上游 x-goog-api-key 异常: %q", r.Header.Get("x-goog-api-key"))
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"instances"`) || !strings.Contains(string(body), `"sampleCount":4`) {
			t.Errorf("上游请求体未透传: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"predictions":[{"bytesBase64Encoded":"QQ=="},{"bytesBase64Encoded":"Qg=="},`+
			`{"bytesBase64Encoded":"Qw=="},{"bytesBase64Encoded":"RA=="}]}`)
	}))
	defer upstream.Close()

	env := newTestEnv(t, gemSnap(1, upstream.URL, func(s *registry.ChannelKeySnapshot) {
		s.Models[imagenModel] = struct{}{}
		s.ModelMapping[imagenModel] = "imagen-upstream"
	}))
	w := env.doGemini(t, imagenModel+":predict", `{"instances":[{"prompt":"a cat"}],"parameters":{"sampleCount":4}}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1", hits.Load())
	}
	rec := env.sink.last(t)
	// 0.03 × 4 张 = 0.12；billing rate 2.0 → actual 0.24。
	if !almostEqual(rec.TotalCost, 0.12) {
		t.Errorf("TotalCost = %v, want 0.12（0.03×4 张）", rec.TotalCost)
	}
	if !almostEqual(rec.ActualCost, 0.24) {
		t.Errorf("ActualCost = %v, want 0.24", rec.ActualCost)
	}
	if rec.Calls != 4 {
		t.Errorf("Calls = %d, want 4（张数落账，供对账）", rec.Calls)
	}
	if rec.Model != imagenModel || rec.Stream {
		t.Errorf("record 元数据异常: %+v", rec)
	}
}

// TestCountTokensZeroBilling countTokens 两端点（gemini/anthropic）：
// 透传成功但零计费——不产生 usage_log，也无失败留痕。
func TestCountTokensZeroBilling(t *testing.T) {
	t.Run("gemini :countTokens", func(t *testing.T) {
		var hits atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			if !strings.HasSuffix(r.URL.Path, "/models/gemini-upstream:countTokens") {
				t.Errorf("上游收到异常路由: %s", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"totalTokens":31}`)
		}))
		defer upstream.Close()

		env := newTestEnv(t, gemSnap(1, upstream.URL))
		w := env.doGemini(t, gemModel+":countTokens", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"totalTokens":31`) {
			t.Errorf("响应未透传: %s", w.Body.String())
		}
		if env.sink.count() != 0 {
			t.Errorf("countTokens 不应计费, got %d 条 UsageRecord", env.sink.count())
		}
		if n := env.errSink.entryCount(); n != 0 {
			t.Errorf("成功请求产生了 %d 条失败留痕", n)
		}
	})

	t.Run("anthropic /v1/messages/count_tokens", func(t *testing.T) {
		var hits atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			if r.URL.Path != "/v1/messages/count_tokens" {
				t.Errorf("上游收到异常路由: %s", r.URL.Path)
			}
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"claude-upstream"`) {
				t.Errorf("model_mapping 未生效: %s", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"input_tokens":2095}`)
		}))
		defer upstream.Close()

		env := newTestEnv(t, anthSnap(1, upstream.URL))
		req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens",
			strings.NewReader(`{"model":"`+anthModel+`","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		env.engine.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"input_tokens":2095`) {
			t.Errorf("响应未透传: %s", w.Body.String())
		}
		if env.sink.count() != 0 {
			t.Errorf("count_tokens 不应计费, got %d 条 UsageRecord", env.sink.count())
		}
	})

	t.Run("零计费端点余额预检照常（防滥用）", func(t *testing.T) {
		env := newTestEnv(t, anthSnap(1, "http://127.0.0.1:0"))
		broke := testKeyInfo()
		broke.UserBalance = 0
		engine := gin.New()
		engine.POST("/v1/messages/count_tokens",
			func(c *gin.Context) { c.Set(middleware.CtxKeyKeyInfo, broke) },
			env.pipe.HandleMessagesCountTokens)
		req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens",
			strings.NewReader(`{"model":"`+anthModel+`","messages":[]}`))
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusPaymentRequired {
			t.Fatalf("status = %d, want 402; body = %s", w.Code, w.Body.String())
		}
	})
}

// TestGeminiModelsList GET /v1beta/models：Gemini 原生形态最小合法子集
// （name=models/<id>），分组过滤与 /v1/models 同口径。
func TestGeminiModelsList(t *testing.T) {
	env := newTestEnv(t,
		gemSnap(1, "http://u1"),
		gemSnap(2, "http://u2", func(s *registry.ChannelKeySnapshot) {
			s.Models = map[string]struct{}{"other-gem": {}}
			s.GroupIDs = map[int]struct{}{99: {}} // 非本分组渠道，不应出现
		}),
	)
	req := httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
	w := httptest.NewRecorder()
	env.engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Models []struct {
			Name                       string   `json:"name"`
			DisplayName                string   `json:"displayName"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	if len(resp.Models) != 1 {
		t.Fatalf("models 条数 = %d, want 1; body = %s", len(resp.Models), w.Body.String())
	}
	m := resp.Models[0]
	if m.Name != "models/"+gemModel || m.DisplayName != gemModel {
		t.Errorf("model item = %+v, want name=models/%s", m, gemModel)
	}
	if len(m.SupportedGenerationMethods) == 0 {
		t.Error("缺少 supportedGenerationMethods")
	}
}
