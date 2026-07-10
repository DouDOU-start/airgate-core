package task

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/errlog"
	"github.com/DouDOU-start/airgate-core/internal/relay/pipeline"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// ===== 轮询器测试替身（与 integration_test 的 task_test 外部包互不复用）=====

// pollStore 内存任务存储。
type pollStore struct {
	mu   sync.Mutex
	seq  int
	rows map[int]*Task
}

func newPollStore() *pollStore { return &pollStore{rows: map[int]*Task{}} }

func (m *pollStore) seed(t *Task) *Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	cp := *t
	cp.ID = m.seq
	if cp.Status == "" {
		cp.Status = StatusInProgress
	}
	m.rows[m.seq] = &cp
	return &cp
}

func (m *pollStore) Insert(_ context.Context, t *Task) (int, error) {
	return m.seed(t).ID, nil
}

func (m *pollStore) GetForUser(context.Context, string, string, int) (*Task, error) {
	return nil, nil
}

func (m *pollStore) ListForUser(context.Context, string, []string, int) ([]*Task, error) {
	return nil, nil
}

func (m *pollStore) ListUnfinished(_ context.Context, limit int) ([]*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Task
	for _, r := range m.rows {
		if !IsTerminal(r.Status) {
			cp := *r
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *pollStore) CountUnfinished(context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.rows {
		if !IsTerminal(r.Status) {
			n++
		}
	}
	return n, nil
}

func (m *pollStore) UpdateStatusCAS(_ context.Context, id int, upd StatusUpdate) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok || IsTerminal(r.Status) {
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
	return true, nil
}

func (m *pollStore) MarkSettled(_ context.Context, id int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok || r.Settled {
		return false, nil
	}
	r.Settled = true
	return true, nil
}

func (m *pollStore) get(t *testing.T, id int) Task {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		t.Fatalf("任务 %d 不存在", id)
	}
	return *r
}

// pollBalance 余额动账替身。
type pollBalance struct {
	mu  sync.Mutex
	ops []struct {
		userID  int
		amount  float64
		idemKey string
	}
	idemSeen map[string]bool
}

func newPollBalance() *pollBalance { return &pollBalance{idemSeen: map[string]bool{}} }

func (f *pollBalance) Hold(context.Context, int, float64, string) error { return nil }

func (f *pollBalance) Adjust(_ context.Context, userID int, amount float64, _ string, idemKey string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if idemKey != "" && f.idemSeen[idemKey] {
		return nil
	}
	if idemKey != "" {
		f.idemSeen[idemKey] = true
	}
	f.ops = append(f.ops, struct {
		userID  int
		amount  float64
		idemKey string
	}{userID, amount, idemKey})
	return nil
}

// pollSink 捕获 UsageRecord。
type pollSink struct {
	mu      sync.Mutex
	records []billing.UsageRecord
}

func (f *pollSink) Record(r billing.UsageRecord) {
	f.mu.Lock()
	f.records = append(f.records, r)
	f.mu.Unlock()
}

// pollErrSink 捕获失败留痕。
type pollErrSink struct {
	mu      sync.Mutex
	entries []errlog.Entry
}

func (f *pollErrSink) Record(e errlog.Entry) {
	f.mu.Lock()
	f.entries = append(f.entries, e)
	f.mu.Unlock()
}

func (f *pollErrSink) CountFailure(context.Context, int, string, string) {}

// pollLoader / pollPriceLoader 注册表与价目表替身。
type pollLoader struct{ snaps []registry.ChannelSnapshot }

func (f *pollLoader) LoadAllForRegistry(context.Context) ([]registry.ChannelSnapshot, error) {
	return f.snaps, nil
}

type pollPriceLoader struct{ prices map[string]pricing.Price }

func (f *pollPriceLoader) LoadAllPrices(context.Context) (map[string]pricing.Price, error) {
	return f.prices, nil
}

// fakePollAdaptor 轮询测试适配器：查询请求指向 httptest 上游，
// 响应体为 {"status","progress","seconds"} 直解。
type fakePollAdaptor struct{}

func (fakePollAdaptor) ParseSubmit(string, string, []byte) (*SubmitRequest, error) {
	return nil, nil
}

func (fakePollAdaptor) BuildSubmitRequest(context.Context, *Info, *SubmitRequest) (*http.Request, error) {
	return nil, nil
}

func (fakePollAdaptor) ParseSubmitResponse([]byte) (string, *Status, error) { return "", nil, nil }

func (fakePollAdaptor) BuildQueryRequest(ctx context.Context, info *Info, taskID string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, info.Channel.BaseURL+"/query/"+taskID, nil)
}

func (fakePollAdaptor) ParseQueryResponse(body []byte) (*Status, error) {
	var st Status
	if err := json.Unmarshal(body, &st); err != nil {
		return nil, err
	}
	st.Raw = body
	return &st, nil
}

func (fakePollAdaptor) RenderTask(*Task) []byte { return []byte(`{}`) }

// fakeBatchAdaptor 批量查询适配器（BatchQuerying）。
type fakeBatchAdaptor struct{ fakePollAdaptor }

func (fakeBatchAdaptor) BuildBatchQueryRequest(ctx context.Context, info *Info, ids []string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, info.Channel.BaseURL+"/batch", nil)
}

func (fakeBatchAdaptor) ParseBatchQueryResponse(body []byte) (map[string]*Status, error) {
	var m map[string]*Status
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (fakeBatchAdaptor) RenderTaskList([]*Task) []byte { return []byte(`{}`) }

func init() {
	Register("pollplat", func() Adaptor { return fakePollAdaptor{} })
	Register("pollbatch", func() Adaptor { return fakeBatchAdaptor{} })
}

// newTestPoller 组装轮询器（渠道 Type=pollplat/pollbatch）。
func newTestPoller(t *testing.T, snaps ...registry.ChannelSnapshot) (*Poller, *pollStore, *pollBalance, *pollSink, *pollErrSink) {
	t.Helper()
	reg := registry.New(&pollLoader{snaps: snaps}, nil)
	if err := reg.Reload(context.Background()); err != nil {
		t.Fatalf("注册表加载失败: %v", err)
	}
	cache := pricing.NewCache(&pollPriceLoader{prices: map[string]pricing.Price{
		"pv-model":   {VideoPerSecond: 0.1},
		"flat-model": {PerRequest: 0.5},
	}})
	if err := cache.Reload(context.Background()); err != nil {
		t.Fatalf("价目表加载失败: %v", err)
	}
	st := newPollStore()
	bal := newPollBalance()
	sink := &pollSink{}
	errSink := &pollErrSink{}
	p := NewPoller(Options{
		Registry: reg,
		Pricing:  cache,
		Sink:     sink,
		ErrLog:   errSink,
		Settings: pipeline.NewSettingsReader(nil),
		Store:    st,
		Balance:  bal,
	})
	p.sleep = func(context.Context, time.Duration) {}
	return p, st, bal, sink, errSink
}

func pollSnap(id int, chType, baseURL string) registry.ChannelSnapshot {
	return registry.ChannelSnapshot{
		ID: id, Name: fmt.Sprintf("pch-%d", id), Type: chType, BaseURL: baseURL,
		APIKeys: []string{"sk-poll"}, Status: registry.StatusEnabled, CostRatio: 1.0,
	}
}

// seedTask 预置一条在途任务（按秒模型，est 0.4 / hold 0.8 / rate 2）。
func seedTask(st *pollStore, channelID int, mutate ...func(*Task)) *Task {
	t := &Task{
		TaskID: "tk-1", Platform: "pollplat", Status: StatusInProgress,
		RequestModel: "pv-model", HoldAmount: 0.8, EstTotal: 0.4,
		RateMultiplier: 2.0, AccountRateMultiplier: 1.0, Seconds: 4,
		SubmitTime: time.Now(), UserID: 22, APIKeyID: 11, GroupID: 7, ChannelID: channelID,
	}
	for _, m := range mutate {
		m(t)
	}
	return st.seed(t)
}

// TestPollerSettlePerSecond 成功结算：上游实际 8s，final actual 1.6 > hold 0.8 → 补扣 0.8，
// usage_log 落账 SkipBalanceCharge 且按秒对账口径（calls=8 × 0.1）。
func TestPollerSettlePerSecond(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Status":"success","Progress":100,"Seconds":8}`))
	}))
	defer upstream.Close()

	p, st, bal, sink, _ := newTestPoller(t, pollSnap(1, "pollplat", upstream.URL))
	seedTask(st, 1)
	p.tick(context.Background())

	row := st.get(t, 1)
	if row.Status != StatusSuccess || !row.Settled || row.Seconds != 8 {
		t.Fatalf("row = %+v", row)
	}
	if len(bal.ops) != 1 || bal.ops[0].amount != -0.8 || bal.ops[0].idemKey != "task:settle:1" {
		t.Fatalf("动账 = %+v（应补扣 0.8）", bal.ops)
	}
	if len(sink.records) != 1 {
		t.Fatalf("usage records = %d", len(sink.records))
	}
	rec := sink.records[0]
	if !rec.SkipBalanceCharge || rec.Source != billing.SourceTask ||
		rec.Calls != 8 || rec.InputPrice != 0.1 ||
		rec.ActualCost != 1.6 || rec.TotalCost != 0.8 {
		t.Errorf("rec = %+v", rec)
	}
}

// TestPollerRefundOnFailure 上游报失败：全额退 hold，不落 usage_log。
func TestPollerRefundOnFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Status":"failure","FailReason":"content policy"}`))
	}))
	defer upstream.Close()

	p, st, bal, sink, errSink := newTestPoller(t, pollSnap(1, "pollplat", upstream.URL))
	seedTask(st, 1)
	p.tick(context.Background())

	row := st.get(t, 1)
	if row.Status != StatusFailure || row.FailReason != "content policy" {
		t.Fatalf("row = %+v", row)
	}
	if len(bal.ops) != 1 || bal.ops[0].amount != 0.8 || bal.ops[0].idemKey != "task:refund:1" {
		t.Fatalf("动账 = %+v（应全额退款）", bal.ops)
	}
	if len(sink.records) != 0 {
		t.Error("失败任务不应落 usage_log")
	}
	if len(errSink.entries) != 1 || errSink.entries[0].Phase != errlog.PhaseTaskFailed {
		t.Errorf("留痕 = %+v", errSink.entries)
	}
}

// TestPollerTimeoutSweep 超时清扫：超过 task_timeout_minutes 未终态 → 置失败退款，不再查上游。
func TestPollerTimeoutSweep(t *testing.T) {
	queried := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		queried = true
		_, _ = w.Write([]byte(`{"Status":"in_progress"}`))
	}))
	defer upstream.Close()

	p, st, bal, _, errSink := newTestPoller(t, pollSnap(1, "pollplat", upstream.URL))
	seedTask(st, 1, func(t *Task) { t.SubmitTime = time.Now().Add(-31 * time.Minute) })
	p.tick(context.Background())

	row := st.get(t, 1)
	if row.Status != StatusFailure {
		t.Fatalf("row = %+v", row)
	}
	if queried {
		t.Error("超时任务不应再查上游")
	}
	if len(bal.ops) != 1 || bal.ops[0].amount != 0.8 {
		t.Fatalf("动账 = %+v", bal.ops)
	}
	if len(errSink.entries) != 1 || errSink.entries[0].Phase != errlog.PhaseTaskTimeout {
		t.Errorf("留痕 = %+v", errSink.entries)
	}
}

// TestPollerChannelGone 渠道已删除：任务无法跟踪 → 置失败退款。
func TestPollerChannelGone(t *testing.T) {
	p, st, bal, _, _ := newTestPoller(t) // 注册表为空
	seedTask(st, 99)
	p.tick(context.Background())

	row := st.get(t, 1)
	if row.Status != StatusFailure {
		t.Fatalf("row = %+v", row)
	}
	if len(bal.ops) != 1 || bal.ops[0].amount != 0.8 {
		t.Fatalf("动账 = %+v", bal.ops)
	}
}

// TestPollerNoDoubleSettle 结算幂等：终态后重复 tick / 重复 refund 不再动账。
func TestPollerNoDoubleSettle(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Status":"success","Seconds":4}`))
	}))
	defer upstream.Close()

	p, st, bal, sink, _ := newTestPoller(t, pollSnap(1, "pollplat", upstream.URL))
	seeded := seedTask(st, 1)
	p.tick(context.Background())
	p.tick(context.Background()) // 第二轮：任务已终态，不再扫描

	// 直接重复调 refundFailure（模拟竞态重入）：MarkSettled CAS 已置位 → no-op。
	p.refundFailure(context.Background(), seeded, "dup", errlog.PhaseTaskFailed)

	// 4s 实际 = est → delta 0，不动账；usage 落一条。
	if len(bal.ops) != 0 {
		t.Errorf("动账 = %+v（delta=0 不应动账）", bal.ops)
	}
	if len(sink.records) != 1 {
		t.Errorf("usage records = %d, want 1", len(sink.records))
	}
}

// TestPollerBatchQuery 批量查询路径（BatchQuerying）：一次带全组 ID，逐个应用。
func TestPollerBatchQuery(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/batch" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"bt-1":{"Status":"success"},"bt-2":{"Status":"in_progress","Progress":50}}`))
	}))
	defer upstream.Close()

	p, st, bal, _, _ := newTestPoller(t, pollSnap(1, "pollbatch", upstream.URL))
	st.seed(&Task{
		TaskID: "bt-1", Platform: "pollbatch", Status: StatusInProgress,
		RequestModel: "flat-model", HoldAmount: 1.0, EstTotal: 0.5,
		RateMultiplier: 2.0, SubmitTime: time.Now(), UserID: 22, ChannelID: 1,
	})
	st.seed(&Task{
		TaskID: "bt-2", Platform: "pollbatch", Status: StatusSubmitted,
		RequestModel: "flat-model", HoldAmount: 1.0, EstTotal: 0.5,
		RateMultiplier: 2.0, SubmitTime: time.Now(), UserID: 22, ChannelID: 1,
	})
	p.tick(context.Background())

	if row := st.get(t, 1); row.Status != StatusSuccess || !row.Settled {
		t.Errorf("bt-1 = %+v", row)
	}
	if row := st.get(t, 2); row.Status != StatusInProgress || row.Progress != 50 {
		t.Errorf("bt-2 = %+v", row)
	}
	// flat-model 按次结算：final actual = 0.5×2 = 1.0 = hold → 无差额动账。
	if len(bal.ops) != 0 {
		t.Errorf("动账 = %+v", bal.ops)
	}
}

// TestPollerAuthFailedAutoBan 轮询 401：自动禁用渠道。
func TestPollerAuthFailedAutoBan(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	p, st, _, _, _ := newTestPoller(t, pollSnap(1, "pollplat", upstream.URL))
	seedTask(st, 1)
	p.tick(context.Background())

	snap, ok := p.registry.Snapshot(1)
	if !ok || snap.Status != registry.StatusDisabledAuto {
		t.Errorf("渠道状态 = %+v, want disabled_auto", snap)
	}
	// 任务保持在途（等渠道恢复或超时清扫）。
	if row := st.get(t, 1); IsTerminal(row.Status) {
		t.Errorf("任务不应被判终态: %+v", row)
	}
}
