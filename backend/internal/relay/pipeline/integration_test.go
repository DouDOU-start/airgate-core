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
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
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

// fakeChannelLoader 固定渠道快照。
type fakeChannelLoader struct{ snaps []registry.ChannelSnapshot }

func (f *fakeChannelLoader) LoadAllForRegistry(context.Context) ([]registry.ChannelSnapshot, error) {
	return f.snaps, nil
}

// fakePersister 记录状态落库（异步，需等待）。
type fakePersister struct {
	mu      sync.Mutex
	calls   []string // "id:status:hasUntil"
	errMsgs []string
	done    chan struct{}
}

func newFakePersister() *fakePersister { return &fakePersister{done: make(chan struct{}, 16)} }

func (f *fakePersister) PersistState(_ context.Context, id int, status string, until *time.Time, errMsg string) error {
	f.mu.Lock()
	f.calls = append(f.calls, fmt.Sprintf("%d:%s:%v", id, status, until != nil))
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

// perRequestModel 按次计费测试模型（USD 0.02 / 次）。
const perRequestModel = "flat-model"

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
	persister *fakePersister
	registry  *registry.Registry
}

// newTestEnv 组装管线：注册表注入指定快照（不接 DB），
// ConcurrencyManager/RPMCounter 传 nil redis（no-op），UsageSink 用 fake。
func newTestEnv(t *testing.T, snaps ...registry.ChannelSnapshot) *testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	persister := newFakePersister()
	reg := registry.New(&fakeChannelLoader{snaps: snaps}, persister)
	if err := reg.Reload(context.Background()); err != nil {
		t.Fatalf("注册表加载失败: %v", err)
	}

	cache := pricing.NewCache(&fakePriceLoader{prices: map[string]pricing.Price{
		testModel:       testPrice,
		perRequestModel: {PerRequest: 0.02},
	}})
	if err := cache.Reload(context.Background()); err != nil {
		t.Fatalf("价目表加载失败: %v", err)
	}

	sink := &fakeSink{}
	pipe := New(Options{
		Registry:    reg,
		Pricing:     cache,
		Concurrency: scheduler.NewConcurrencyManager(nil),
		RPM:         scheduler.NewRPMCounter(nil),
		Calculator:  billing.NewCalculator(),
		Sink:        sink,
		Settings:    NewSettingsReader(nil),
	})

	engine := gin.New()
	injectKey := func(c *gin.Context) { c.Set(middleware.CtxKeyKeyInfo, testKeyInfo()) }
	engine.POST("/v1/chat/completions", injectKey, pipe.HandleChatCompletions)
	engine.POST("/v1/responses", injectKey, pipe.HandleResponses)
	engine.GET("/v1/models", injectKey, pipe.HandleModels)

	return &testEnv{pipe: pipe, engine: engine, sink: sink, persister: persister, registry: reg}
}

// testSnap 构造指向指定上游的渠道快照。
func testSnap(id int, baseURL string, mutate ...func(*registry.ChannelSnapshot)) registry.ChannelSnapshot {
	s := registry.ChannelSnapshot{
		ID:           id,
		Name:         fmt.Sprintf("ch-%d", id),
		Type:         "openai_compatible",
		BaseURL:      baseURL,
		APIKeys:      []string{fmt.Sprintf("sk-up-%d", id)},
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
	if !almostEqual(rec.AccountCost, wantTotal*0.5) { // cost_ratio 0.5
		t.Errorf("AccountCost = %v, want %v", rec.AccountCost, wantTotal*0.5)
	}
	if rec.InputTokens != 800 || rec.OutputTokens != 500 || rec.CachedInputTokens != 200 {
		t.Errorf("tokens = (%d,%d,%d), want (800,500,200)", rec.InputTokens, rec.OutputTokens, rec.CachedInputTokens)
	}
	if rec.ChannelID != 1 || rec.Platform != "openai" || rec.Model != "gpt-4o" || rec.Stream {
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

// TestFailover429 坏渠道 429 → 冷却 + 硬排除 → 自动切换到好渠道成功。
func TestFailover429(t *testing.T) {
	var goodHits, badHits atomic.Int32
	var lastBody atomic.Value
	good := newGoodUpstream(t, &goodHits, &lastBody)
	defer good.Close()
	bad := newFailingUpstream(http.StatusTooManyRequests, "5", &badHits)
	defer bad.Close()

	env := newTestEnv(t,
		testSnap(1, good.URL, func(s *registry.ChannelSnapshot) { s.Priority = 1 }),
		testSnap(2, bad.URL, func(s *registry.ChannelSnapshot) { s.Priority = 100 }), // 高优先级先被选中
	)
	w := env.do(t, `{"model":"gpt-4o","messages":[]}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if badHits.Load() != 1 || goodHits.Load() != 1 {
		t.Errorf("hits bad=%d good=%d, want 1/1", badHits.Load(), goodHits.Load())
	}
	// 冷却异步落库：status 不变（enabled）、status_until 非空。
	if call := env.persister.waitOne(t); call != "2:enabled:true" {
		t.Errorf("冷却落库 = %q, want 2:enabled:true", call)
	}
	if env.sink.count() != 1 {
		t.Errorf("UsageRecord 条数 = %d, want 1（仅成功渠道计费）", env.sink.count())
	}

	// 冷却生效（hardExclude + MarkCooldown）：再次请求直接走好渠道。
	w2 := env.do(t, `{"model":"gpt-4o","messages":[]}`)
	if w2.Code != http.StatusOK {
		t.Fatalf("第二次请求 status = %d", w2.Code)
	}
	if badHits.Load() != 1 {
		t.Errorf("冷却中的渠道仍被调度: badHits = %d", badHits.Load())
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
	var resp errorBody
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
		testSnap(1, good.URL, func(s *registry.ChannelSnapshot) { s.Priority = 1 }),
		testSnap(2, bad.URL, func(s *registry.ChannelSnapshot) { s.Priority = 100 }),
	)
	w := env.do(t, `{"model":"gpt-4o","messages":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if badHits.Load() != 1 || goodHits.Load() != 1 {
		t.Errorf("hits bad=%d good=%d, want 1/1", badHits.Load(), goodHits.Load())
	}
}

// TestClientErrorPassthrough 普通 4xx 原样透传不重试。
func TestClientErrorPassthrough(t *testing.T) {
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
	if !strings.Contains(w.Body.String(), "upstream says no") {
		t.Errorf("未透传上游错误体: %s", w.Body.String())
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

// TestUnpricedModelRejected 缺价预检 400（默认 unpriced_model_allow=false）。
func TestUnpricedModelRejected(t *testing.T) {
	env := newTestEnv(t, testSnap(1, "http://127.0.0.1:0", func(s *registry.ChannelSnapshot) {
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
		testSnap(1, "http://u1", func(s *registry.ChannelSnapshot) {
			s.Models = map[string]struct{}{"gpt-4o": {}, "gpt-4o-mini": {}}
		}),
		testSnap(2, "http://u2", func(s *registry.ChannelSnapshot) {
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
	var resp errorBody
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("错误体非 JSON: %v", err)
	}
	if resp.Error.Code != "upstream_auth_failed" {
		t.Errorf("错误码 = %q, want upstream_auth_failed", resp.Error.Code)
	}

	// (a) 自动禁用落库原因已脱敏。
	call := env.persister.waitOne(t)
	if call != "1:disabled_auto:false" {
		t.Fatalf("落库调用 = %q, want 1:disabled_auto:false", call)
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

// TestClientErrorBodySanitized 上游 400（透传路径）回显渠道 key：
// 透传给客户端前做精确 key 替换，其余内容不动。
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
		t.Errorf("4xx 透传体泄漏明文 key: %s", body)
	}
	if !strings.Contains(body, "sk-***up-1") || !strings.Contains(body, "for request") {
		t.Errorf("4xx 透传体应仅替换 key、其余内容不动: %s", body)
	}
}

// TestKeyword400DoesNotDisableChannel 上游 400 回显关键词不再自动禁用渠道
// （防任意用户构造关键词字符串打禁渠道）。
func TestKeyword400DoesNotDisableChannel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Invalid value: 'insufficient_quota'. Supported values are ..."}}`)
	}))
	defer upstream.Close()

	env := newTestEnv(t, testSnap(1, upstream.URL))
	w := env.do(t, `{"model":"gpt-4o","messages":[]}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 透传", w.Code)
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
func TestExecutePanicRecovered(t *testing.T) {
	adaptor.Register("panic-test", func() adaptor.Adaptor { return panicAdaptor{} })

	var hits atomic.Int32
	var lastBody atomic.Value
	good := newGoodUpstream(t, &hits, &lastBody)
	defer good.Close()

	env := newTestEnv(t,
		testSnap(1, "http://ignored.invalid", func(s *registry.ChannelSnapshot) { s.Type = "panic-test" }),
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

	env := newTestEnv(t, testSnap(1, upstream.URL, func(s *registry.ChannelSnapshot) {
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
	if !strings.Contains(logs, "channel_id=1") || !strings.Contains(logs, "model=gpt-4o") {
		t.Errorf("告警日志缺少 channel_id/model 字段:\n%s", logs)
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
	if rec.ReasoningOutputTokens != 100 {
		t.Errorf("ReasoningOutputTokens = %d, want 100", rec.ReasoningOutputTokens)
	}
	if rec.ChannelID != 1 || rec.Platform != "openai" || rec.Model != "gpt-4o" || rec.Stream {
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
	if rec.ReasoningOutputTokens != 100 {
		t.Errorf("ReasoningOutputTokens = %d, want 100", rec.ReasoningOutputTokens)
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
		testSnap(1, good.URL, func(s *registry.ChannelSnapshot) { s.Priority = 1 }),
		testSnap(2, bad.URL, func(s *registry.ChannelSnapshot) { s.Priority = 100 }), // 高优先级先被选中
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
// （非 all-failed 的 5xx）+ 上游原始 body；全 404 不计费。
func TestForwardResponsesAll404(t *testing.T) {
	var hits1, hits2 atomic.Int32
	bad1 := newFailingUpstream(http.StatusNotFound, "", &hits1)
	defer bad1.Close()
	bad2 := newFailingUpstream(http.StatusNotFound, "", &hits2)
	defer bad2.Close()

	env := newTestEnv(t, testSnap(1, bad1.URL), testSnap(2, bad2.URL))
	w := env.doResponses(t, `{"model":"gpt-4o","input":"hi"}`)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 原状态码透传（非 5xx）; body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "upstream says no") {
		t.Errorf("未透传上游 404 原始 body: %s", w.Body.String())
	}
	if env.sink.count() != 0 {
		t.Errorf("全 404 请求不应计费, got %d 条", env.sink.count())
	}
}

// TestForwardChat404Passthrough 修3 回归：chat 端点 404 仍一次性透传不重试
// （404 failover 仅限 responses 端点，chat 行为不变）。
func TestForwardChat404Passthrough(t *testing.T) {
	var hits atomic.Int32
	bad := newFailingUpstream(http.StatusNotFound, "", &hits)
	defer bad.Close()

	env := newTestEnv(t, testSnap(1, bad.URL))
	w := env.do(t, `{"model":"gpt-4o","messages":[]}`)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 一次性透传", w.Code)
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1（chat 404 不重试）", hits.Load())
	}
	if !strings.Contains(w.Body.String(), "upstream says no") {
		t.Errorf("未透传上游 404 body: %s", w.Body.String())
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
