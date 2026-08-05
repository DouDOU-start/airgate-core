package task_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/errlog"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/pipeline"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/relay/task"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"

	// 平台适配器自注册。
	_ "github.com/DouDOU-start/airgate-core/internal/relay/task/openaivideo"
	_ "github.com/DouDOU-start/airgate-core/internal/relay/task/suno"
	_ "github.com/DouDOU-start/airgate-core/internal/relay/task/xaivideo"
)

// ===== 测试替身 =====

// memStore 内存任务存储（实现 task.Store）。
type memStore struct {
	mu   sync.Mutex
	seq  int
	rows map[int]*task.Task

	insertErr error
}

func newMemStore() *memStore { return &memStore{rows: map[int]*task.Task{}} }

func (m *memStore) Insert(_ context.Context, t *task.Task) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.insertErr != nil {
		return 0, m.insertErr
	}
	m.seq++
	cp := *t
	cp.ID = m.seq
	m.rows[m.seq] = &cp
	return m.seq, nil
}

func (m *memStore) GetForUser(_ context.Context, platform, taskID string, userID int) (*task.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var found *task.Task
	for _, r := range m.rows {
		if r.Platform == platform && r.TaskID == taskID && r.UserID == userID {
			if found == nil || r.ID > found.ID {
				found = r
			}
		}
	}
	if found == nil {
		return nil, nil
	}
	cp := *found
	return &cp, nil
}

func (m *memStore) ListForUser(_ context.Context, platform string, taskIDs []string, userID int) ([]*task.Task, error) {
	want := map[string]struct{}{}
	for _, id := range taskIDs {
		want[id] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*task.Task
	for _, r := range m.rows {
		if r.Platform != platform || r.UserID != userID {
			continue
		}
		if _, ok := want[r.TaskID]; !ok {
			continue
		}
		cp := *r
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *memStore) ListUnfinished(_ context.Context, limit int) ([]*task.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*task.Task
	for _, r := range m.rows {
		if !task.IsTerminal(r.Status) {
			cp := *r
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.Before(out[j].UpdatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *memStore) CountUnfinished(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.rows {
		if !task.IsTerminal(r.Status) {
			n++
		}
	}
	return n, nil
}

func (m *memStore) UpdateStatusCAS(_ context.Context, id int, upd task.StatusUpdate) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok || task.IsTerminal(r.Status) {
		return false, nil
	}
	r.Status = upd.Status
	r.Progress = upd.Progress
	r.FailReason = upd.FailReason
	if upd.Seconds > 0 {
		r.Seconds = upd.Seconds
	}
	if len(upd.Data) > 0 {
		r.Data = upd.Data
	}
	if upd.FinishTime != nil {
		ft := *upd.FinishTime
		r.FinishTime = &ft
	}
	r.UpdatedAt = time.Now()
	return true, nil
}

func (m *memStore) MarkSettled(_ context.Context, id int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok || r.Settled {
		return false, nil
	}
	r.Settled = true
	return true, nil
}

func (m *memStore) get(t *testing.T, id int) task.Task {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		t.Fatalf("任务 %d 不存在", id)
	}
	return *r
}

func (m *memStore) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rows)
}

// balanceOp 一次余额动账记录。
type balanceOp struct {
	kind    string // hold / adjust
	userID  int
	amount  float64
	remark  string
	idemKey string
}

// fakeBalance 余额动账替身：记录调用，可注入余额不足。
type fakeBalance struct {
	mu           sync.Mutex
	ops          []balanceOp
	insufficient bool
	idemSeen     map[string]bool
}

func newFakeBalance() *fakeBalance { return &fakeBalance{idemSeen: map[string]bool{}} }

func (f *fakeBalance) Hold(_ context.Context, userID int, amount float64, remark string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.insufficient {
		return task.ErrInsufficientBalance
	}
	f.ops = append(f.ops, balanceOp{kind: "hold", userID: userID, amount: amount, remark: remark})
	return nil
}

func (f *fakeBalance) Adjust(_ context.Context, userID int, amount float64, remark, idemKey string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if idemKey != "" && f.idemSeen[idemKey] {
		return nil // 幂等命中：不再记账
	}
	if idemKey != "" {
		f.idemSeen[idemKey] = true
	}
	f.ops = append(f.ops, balanceOp{kind: "adjust", userID: userID, amount: amount, remark: remark, idemKey: idemKey})
	return nil
}

func (f *fakeBalance) opsOf(kind string) []balanceOp {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []balanceOp
	for _, op := range f.ops {
		if op.kind == kind {
			out = append(out, op)
		}
	}
	return out
}

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

// fakeErrSink 捕获失败留痕。
type fakeErrSink struct {
	mu      sync.Mutex
	entries []errlog.Entry
}

func (f *fakeErrSink) Record(e errlog.Entry) {
	f.mu.Lock()
	f.entries = append(f.entries, e)
	f.mu.Unlock()
}

func (f *fakeErrSink) CountFailure(context.Context, int, string, string) {}

// fakeChannelLoader / fakePriceLoader 与 pipeline 测试同构。
type fakeChannelLoader struct{ snaps []registry.ChannelKeySnapshot }

func (f *fakeChannelLoader) LoadAllForRegistry(context.Context) ([]registry.ChannelKeySnapshot, error) {
	return f.snaps, nil
}

type fakePriceLoader struct{ prices map[string]pricing.Price }

func (f *fakePriceLoader) LoadAllPrices(context.Context) (map[string]pricing.Price, error) {
	return f.prices, nil
}

type fakeAccountLoader struct{ snaps []accountreg.Snapshot }

func (f *fakeAccountLoader) LoadAllForAccountRegistry(context.Context) ([]accountreg.Snapshot, error) {
	return f.snaps, nil
}

type fakeCPAForwarder struct {
	mu       sync.Mutex
	requests []cpa.ForwardRequest
	results  []cpa.ForwardResult
}

func (f *fakeCPAForwarder) Forward(_ context.Context, _ *gin.Context, req cpa.ForwardRequest) cpa.ForwardResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if len(f.results) == 0 {
		return cpa.ForwardResult{StatusCode: http.StatusBadGateway, Body: []byte(`{"error":{"message":"测试结果未配置"}}`)}
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result
}

// ===== 测试装配 =====

const (
	videoModel     = "sora-2"    // 按秒计价：0.1 USD/s
	videoFlatModel = "sora-flat" // 按次计价：0.5 USD/次
)

func testKeyInfo() *auth.APIKeyInfo {
	return &auth.APIKeyInfo{
		KeyID:               11,
		UserID:              22,
		UserEmail:           "u@example.com",
		GroupID:             7,
		UserBalance:         100,
		GroupRateMultiplier: 2.0, // billing rate = 2
	}
}

type testEnv struct {
	flow     *task.Flow
	engine   *gin.Engine
	store    *memStore
	balance  *fakeBalance
	sink     *fakeSink
	errSink  *fakeErrSink
	registry *registry.Registry
}

func newTestEnv(t *testing.T, snaps ...registry.ChannelKeySnapshot) *testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	reg := registry.New(&fakeChannelLoader{snaps: snaps}, nil)
	if err := reg.Reload(context.Background()); err != nil {
		t.Fatalf("注册表加载失败: %v", err)
	}
	cache := pricing.NewCache(&fakePriceLoader{prices: map[string]pricing.Price{
		videoModel: {
			VideoPerSecond:        0.1,
			VideoResolutionPrices: map[string]float64{"480p": 0.08, "720p": 0.1, "1080p": 0.25},
		},
		videoFlatModel: {PerRequest: 0.5},
		"suno_music":   {PerRequest: 0.2},
		"suno_lyrics":  {PerRequest: 0.05},
		"token-model":  {Input: 10, Output: 30}, // 无任务计价：提交应被拒
	}})
	if err := cache.Reload(context.Background()); err != nil {
		t.Fatalf("价目表加载失败: %v", err)
	}

	st := newMemStore()
	bal := newFakeBalance()
	sink := &fakeSink{}
	errSink := &fakeErrSink{}
	flow := task.NewFlow(task.Options{
		Registry:    reg,
		Pricing:     cache,
		Concurrency: scheduler.NewConcurrencyManager(nil),
		RPM:         scheduler.NewRPMCounter(nil),
		Calculator:  billing.NewCalculator(),
		Sink:        sink,
		ErrLog:      errSink,
		Settings:    pipeline.NewSettingsReader(nil),
		Store:       st,
		Balance:     bal,
	})

	engine := gin.New()
	injectKey := func(c *gin.Context) { c.Set(middleware.CtxKeyKeyInfo, testKeyInfo()) }
	engine.POST("/v1/videos", injectKey, flow.HandleVideoSubmit)
	engine.GET("/v1/videos/:task_id", injectKey, flow.HandleVideoGet)
	engine.GET("/v1/videos/:task_id/content", injectKey, flow.HandleVideoContent)
	engine.POST("/suno/submit/:action", injectKey, flow.HandleSunoSubmit)
	engine.POST("/suno/fetch", injectKey, flow.HandleSunoFetch)
	engine.GET("/suno/fetch/:task_id", injectKey, flow.HandleSunoFetchByID)

	return &testEnv{flow: flow, engine: engine, store: st, balance: bal, sink: sink, errSink: errSink, registry: reg}
}

// videoSnap 构造视频渠道快照。
// videoSnap 构造视频渠道快照，默认绑定分组 7（与测试 keyInfo 的 GroupID 对应）。
func videoSnap(id int, baseURL string, mutate ...func(*registry.ChannelKeySnapshot)) registry.ChannelKeySnapshot {
	s := registry.ChannelKeySnapshot{
		KeyID:       id,
		ChannelID:   id,
		ChannelName: fmt.Sprintf("vch-%d", id),
		Type:        "openai_video",
		BaseURL:     baseURL,
		APIKey:      fmt.Sprintf("sk-video-%d", id),
		Models: map[string]struct{}{
			videoModel: {}, videoFlatModel: {},
		},
		Priority:  50,
		Weight:    10,
		Status:    registry.StatusEnabled,
		CostRatio: 1.0,
		GroupIDs:  map[int]struct{}{7: {}},
	}
	for _, m := range mutate {
		m(&s)
	}
	return s
}

// sunoSnap 构造 suno 渠道快照，默认绑定分组 7（与测试 keyInfo 的 GroupID 对应）。
func sunoSnap(id int, baseURL string) registry.ChannelKeySnapshot {
	return registry.ChannelKeySnapshot{
		KeyID:       id,
		ChannelID:   id,
		ChannelName: fmt.Sprintf("sch-%d", id),
		Type:        "suno",
		BaseURL:     baseURL,
		APIKey:      fmt.Sprintf("sk-suno-%d", id),
		Models: map[string]struct{}{
			"suno_music": {}, "suno_lyrics": {},
		},
		Priority: 50, Weight: 10,
		Status: registry.StatusEnabled, CostRatio: 1.0,
		GroupIDs: map[int]struct{}{7: {}},
	}
}

func doJSON(t *testing.T, engine *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

// ===== 视频提交 =====

func TestVideoSubmitSuccess(t *testing.T) {
	var gotAuth, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"video_123","object":"video","status":"queued","progress":0,"model":"sora-2-upstream","seconds":"8"}`))
	}))
	defer upstream.Close()

	env := newTestEnv(t, videoSnap(1, upstream.URL, func(s *registry.ChannelKeySnapshot) {
		s.ModelMapping = map[string]string{videoModel: "sora-2-upstream"}
	}))
	w := doJSON(t, env.engine, http.MethodPost, "/v1/videos", `{"model":"sora-2","prompt":"a cat","seconds":8}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if gotPath != "/v1/videos" {
		t.Errorf("上游路径 = %s", gotPath)
	}
	if gotAuth != "Bearer sk-video-1" {
		t.Errorf("上游认证 = %s", gotAuth)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	if resp["id"] != "video_123" {
		t.Errorf("id = %v", resp["id"])
	}
	// model 回写对外名（隐藏 model_mapping）
	if resp["model"] != videoModel {
		t.Errorf("model = %v, want %s", resp["model"], videoModel)
	}

	// 预扣：est = 0.1×8 = 0.8，hold = 0.8×2 = 1.6
	holds := env.balance.opsOf("hold")
	if len(holds) != 1 || holds[0].amount != 1.6 || holds[0].userID != 22 {
		t.Fatalf("holds = %+v", holds)
	}
	// 落库
	row := env.store.get(t, 1)
	if row.TaskID != "video_123" || row.Platform != task.PlatformOpenAIVideo || row.HoldAmount != 1.6 ||
		row.EstTotal != 0.8 || row.RateMultiplier != 2.0 || row.ChannelID != 1 || row.Seconds != 8 {
		t.Errorf("row = %+v", row)
	}
	if len(env.balance.opsOf("adjust")) != 0 {
		t.Error("成功提交不应有退款/结算动账")
	}
}

func TestXAIVideoSubmitUsesOAuthAccountAndPersistsBinding(t *testing.T) {
	const model = "grok-imagine-video"
	accounts := accountreg.New(&fakeAccountLoader{snaps: []accountreg.Snapshot{{
		ID: 333, Name: "Grok OAuth", Platform: "xai", Type: "oauth",
		Credentials: map[string]string{"access_token": "oauth-token"},
		Priority:    50, Weight: 10, MaxConcurrency: 2, State: accountreg.StateActive,
		Models: map[string]struct{}{model: {}}, GroupIDs: map[int]struct{}{7: {}},
	}}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatalf("账号注册表加载失败: %v", err)
	}
	prices := pricing.NewCache(&fakePriceLoader{prices: map[string]pricing.Price{
		model: {VideoPerSecond: 0.07, VideoResolutionPrices: map[string]float64{"720p": 0.07}},
	}})
	if err := prices.Reload(context.Background()); err != nil {
		t.Fatalf("价目表加载失败: %v", err)
	}
	forwarder := &fakeCPAForwarder{results: []cpa.ForwardResult{{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body:       []byte(`{"request_id":"vid_oauth_123"}`),
	}}}
	store := newMemStore()
	balance := newFakeBalance()
	flow := task.NewFlow(task.Options{
		Accounts: accounts, CPA: forwarder, Pricing: prices,
		Concurrency: scheduler.NewConcurrencyManager(nil), RPM: scheduler.NewRPMCounter(nil),
		Calculator: billing.NewCalculator(), Settings: pipeline.NewSettingsReader(nil),
		Store: store, Balance: balance,
	})
	engine := gin.New()
	injectKey := func(c *gin.Context) { c.Set(middleware.CtxKeyKeyInfo, testKeyInfo()) }
	engine.POST("/v1/videos/generations", injectKey, flow.HandleXAIVideoSubmit)
	engine.GET("/v1/videos/:task_id", injectKey, flow.HandleVideoGet)

	w := doJSON(t, engine, http.MethodPost, "/v1/videos/generations",
		`{"model":"grok-imagine-video","prompt":"海面日落","duration":6,"resolution":"720p"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("提交状态 = %d，响应 = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"request_id":"vid_oauth_123"`) {
		t.Fatalf("提交响应缺少 request_id: %s", w.Body.String())
	}
	row := store.get(t, 1)
	if row.Platform != task.PlatformXAIVideo || row.AccountID != 333 || row.ChannelKeyID != 0 {
		t.Fatalf("任务账号归属错误: %+v", row)
	}
	if math.Abs(row.EstTotal-0.42) > 1e-9 || math.Abs(row.HoldAmount-0.84) > 1e-9 {
		t.Fatalf("视频预扣错误: est=%v hold=%v", row.EstTotal, row.HoldAmount)
	}
	if len(forwarder.requests) != 1 || forwarder.requests[0].Endpoint != adaptor.EndpointXAIVideosGenerations || forwarder.requests[0].Account.AccountID != 333 {
		t.Fatalf("CPA 转发参数错误: %+v", forwarder.requests)
	}

	get := doJSON(t, engine, http.MethodGet, "/v1/videos/vid_oauth_123", "")
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"status":"pending"`) {
		t.Fatalf("本地任务查询错误: status=%d body=%s", get.Code, get.Body.String())
	}
}

func TestVideoSubmitResolutionPrice(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"video_hd","object":"video","status":"queued","progress":0}`))
	}))
	defer upstream.Close()

	env := newTestEnv(t, videoSnap(1, upstream.URL))
	w := doJSON(t, env.engine, http.MethodPost, "/v1/videos",
		`{"model":"sora-2","prompt":"a cat","duration":8,"resolution":"1080P"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	// 1080p：0.25 × 8 秒 = 2.0；billing rate 2×，预扣 4.0。
	holds := env.balance.opsOf("hold")
	if len(holds) != 1 || holds[0].amount != 4.0 {
		t.Fatalf("holds = %+v", holds)
	}
	row := env.store.get(t, 1)
	if row.EstTotal != 2.0 || row.Seconds != 8 || row.Resolution != "1080p" {
		t.Errorf("row = %+v", row)
	}
}

func TestVideoSubmitInsufficientBalance(t *testing.T) {
	env := newTestEnv(t, videoSnap(1, "http://127.0.0.1:1"))
	env.balance.insufficient = true
	w := doJSON(t, env.engine, http.MethodPost, "/v1/videos", `{"model":"sora-2","seconds":4}`)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if env.store.count() != 0 {
		t.Error("不应落库")
	}
}

func TestVideoSubmitNoTaskPriceRejected(t *testing.T) {
	env := newTestEnv(t, videoSnap(1, "http://127.0.0.1:1", func(s *registry.ChannelKeySnapshot) {
		s.Models["token-model"] = struct{}{}
	}))
	w := doJSON(t, env.engine, http.MethodPost, "/v1/videos", `{"model":"token-model"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "model_price_not_configured") {
		t.Errorf("body = %s", w.Body.String())
	}
	if len(env.balance.opsOf("hold")) != 0 {
		t.Error("缺价拒绝不应预扣")
	}
}

func TestVideoSubmitFailoverOn429(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"video_ok","status":"queued"}`))
	}))
	defer good.Close()

	// bad 渠道更高优先级，先被选中。
	env := newTestEnv(t,
		videoSnap(1, bad.URL, func(s *registry.ChannelKeySnapshot) { s.Priority = 90 }),
		videoSnap(2, good.URL),
	)
	w := doJSON(t, env.engine, http.MethodPost, "/v1/videos", `{"model":"sora-2","seconds":4}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	row := env.store.get(t, 1)
	if row.ChannelID != 2 {
		t.Errorf("channel = %d, want failover 到 2", row.ChannelID)
	}
	// 预扣只发生一次，failover 不重复扣。
	if holds := env.balance.opsOf("hold"); len(holds) != 1 {
		t.Errorf("holds = %d, want 1", len(holds))
	}
}

func TestVideoSubmitClientErrorRefunds(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad prompt","type":"invalid_request_error"}}`))
	}))
	defer upstream.Close()

	env := newTestEnv(t, videoSnap(1, upstream.URL))
	w := doJSON(t, env.engine, http.MethodPost, "/v1/videos", `{"model":"sora-2","seconds":4}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "bad prompt") {
		t.Errorf("body = %s（应保留上游语义）", w.Body.String())
	}
	// hold = 0.1×4×2 = 0.8；clientError 全额退回。
	adjusts := env.balance.opsOf("adjust")
	if len(adjusts) != 1 || adjusts[0].amount != 0.8 {
		t.Fatalf("adjusts = %+v", adjusts)
	}
	if env.store.count() != 0 {
		t.Error("失败提交不应落库")
	}
}

func TestVideoSubmitAllFailedRefunds(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	env := newTestEnv(t, videoSnap(1, upstream.URL))
	w := doJSON(t, env.engine, http.MethodPost, "/v1/videos", `{"model":"sora-2","seconds":4}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	adjusts := env.balance.opsOf("adjust")
	if len(adjusts) != 1 || adjusts[0].amount != 0.8 {
		t.Fatalf("adjusts = %+v", adjusts)
	}
}

// ===== 视频查询 / 内容代理 =====

func TestVideoGetReadsLocalSnapshot(t *testing.T) {
	env := newTestEnv(t, videoSnap(1, "http://127.0.0.1:1"))
	now := time.Now()
	_, _ = env.store.Insert(context.Background(), &task.Task{
		TaskID: "video_9", Platform: task.PlatformOpenAIVideo, Status: task.StatusInProgress,
		Progress: 40, RequestModel: videoModel, UserID: 22, ChannelID: 1,
		SubmitTime: now, UpdatedAt: now,
	})

	w := doJSON(t, env.engine, http.MethodGet, "/v1/videos/video_9", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "in_progress" || resp["id"] != "video_9" {
		t.Errorf("resp = %v", resp)
	}

	// 非本人任务 → 404
	w = doJSON(t, env.engine, http.MethodGet, "/v1/videos/other", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestVideoContentProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/videos/video_9/content" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("MP4DATA"))
	}))
	defer upstream.Close()

	env := newTestEnv(t, videoSnap(1, upstream.URL))
	now := time.Now()
	_, _ = env.store.Insert(context.Background(), &task.Task{
		TaskID: "video_9", Platform: task.PlatformOpenAIVideo, Status: task.StatusSuccess,
		RequestModel: videoModel, UserID: 22, ChannelID: 1, SubmitTime: now, UpdatedAt: now,
	})

	w := doJSON(t, env.engine, http.MethodGet, "/v1/videos/video_9/content", "")
	if w.Code != http.StatusOK || w.Body.String() != "MP4DATA" {
		t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("Content-Type = %s", ct)
	}
}

func TestVideoContentNotReady(t *testing.T) {
	env := newTestEnv(t, videoSnap(1, "http://127.0.0.1:1"))
	now := time.Now()
	_, _ = env.store.Insert(context.Background(), &task.Task{
		TaskID: "video_9", Platform: task.PlatformOpenAIVideo, Status: task.StatusInProgress,
		RequestModel: videoModel, UserID: 22, ChannelID: 1, SubmitTime: now, UpdatedAt: now,
	})
	w := doJSON(t, env.engine, http.MethodGet, "/v1/videos/video_9/content", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// ===== Suno =====

func TestSunoSubmitAndFetch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/suno/submit/music" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"code":"success","message":"","data":"suno-task-1"}`))
	}))
	defer upstream.Close()

	env := newTestEnv(t, sunoSnap(1, upstream.URL))
	w := doJSON(t, env.engine, http.MethodPost, "/suno/submit/music", `{"prompt":"a happy song","mv":"chirp-v4"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Code string `json:"code"`
		Data struct {
			TaskID string `json:"task_id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	if resp.Code != "success" || resp.Data.TaskID != "suno-task-1" || resp.Data.Status != "SUBMITTED" {
		t.Errorf("resp = %+v", resp)
	}
	// 预扣 = 0.2 × 2 = 0.4
	holds := env.balance.opsOf("hold")
	if len(holds) != 1 || holds[0].amount != 0.4 {
		t.Fatalf("holds = %+v", holds)
	}

	// 单查
	w = doJSON(t, env.engine, http.MethodGet, "/suno/fetch/suno-task-1", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "suno-task-1") {
		t.Fatalf("fetch status = %d, body = %s", w.Code, w.Body.String())
	}

	// 批量查
	w = doJSON(t, env.engine, http.MethodPost, "/suno/fetch", `{"ids":["suno-task-1","missing"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("batch status = %d", w.Code)
	}
	var batch struct {
		Data []map[string]any `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &batch)
	if len(batch.Data) != 1 {
		t.Errorf("batch data = %+v（缺席 ID 应静默）", batch.Data)
	}
}

func TestSunoSubmitInvalidActionRejected(t *testing.T) {
	env := newTestEnv(t, sunoSnap(1, "http://127.0.0.1:1"))
	w := doJSON(t, env.engine, http.MethodPost, "/suno/submit/video", `{"prompt":"x"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
	// suno 入口错误形态 {"code":"fail",...}
	if !strings.Contains(w.Body.String(), `"code":"fail"`) {
		t.Errorf("body = %s", w.Body.String())
	}
}
