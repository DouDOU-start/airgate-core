package moderation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type fakeSource struct {
	enabled bool
	cfg     *Config
}

func (s *fakeSource) Runtime(context.Context) (bool, *Config, error) {
	return s.enabled, s.cfg.Clone(), nil
}

type fakeLogStore struct {
	mu      sync.Mutex
	entries []LogEntry
	count   int
}

func (s *fakeLogStore) Create(_ context.Context, e LogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
	return nil
}

func (s *fakeLogStore) CountFlaggedSince(context.Context, int, time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count, nil
}

func (s *fakeLogStore) Cleanup(context.Context, time.Time, time.Time) (CleanupResult, error) {
	return CleanupResult{FinishedAt: time.Now()}, nil
}

func (s *fakeLogStore) all() []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]LogEntry(nil), s.entries...)
}

type fakeHashCache struct {
	mu  sync.Mutex
	set map[string]struct{}
}

func newFakeHashCache() *fakeHashCache { return &fakeHashCache{set: map[string]struct{}{}} }

func (c *fakeHashCache) Record(_ context.Context, h string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.set[h] = struct{}{}
	return nil
}

func (c *fakeHashCache) Has(_ context.Context, h string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.set[h]
	return ok, nil
}

func (c *fakeHashCache) Delete(_ context.Context, h string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.set, h)
	return true, nil
}

func (c *fakeHashCache) Clear(context.Context) (int64, error) { return 0, nil }
func (c *fakeHashCache) Count(context.Context) (int64, error) { return 0, nil }

type fakeBanner struct {
	mu     sync.Mutex
	banned []int
}

func (b *fakeBanner) DisableUser(_ context.Context, userID int) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.banned = append(b.banned, userID)
	return true, nil
}

func newTestEngine(src *fakeSource, logs *fakeLogStore, hashes HashCache, banner UserBanner) *Engine {
	e := NewEngine(src, logs, hashes, banner)
	e.snapshotTTL = time.Nanosecond // 每次 Check 取最新 fake 配置
	return e
}

func chatBody(text string) []byte {
	return []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"` + text + `"}]}`)
}

func baseCheckRequest(text string) CheckRequest {
	return CheckRequest{
		RequestID: "req-1",
		UserID:    7,
		UserEmail: "u@example.com",
		APIKeyID:  3,
		GroupID:   1,
		Endpoint:  "/v1/chat/completions",
		Protocol:  ProtocolOpenAIChat,
		Model:     "gpt-4o",
		Body:      chatBody(text),
	}
}

// drainQueue 处理完引擎异步队列中的任务（测试里不起后台 worker，手动排空）。
func drainQueue(t *testing.T, e *Engine, cfg *Config) {
	t.Helper()
	for {
		task, ok := e.dequeue(context.Background(), 10*time.Millisecond)
		if !ok {
			return
		}
		e.runTask(context.Background(), cfg, task)
	}
}

func TestCheckShortCircuits(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModePreBlock
	cfg.BlockedKeywords = []string{"炸弹"}
	cfg.KeywordBlockingMode = KeywordModeKeywordOnly

	tests := []struct {
		name   string
		mutate func(src *fakeSource, in *CheckRequest)
	}{
		{"总开关关", func(src *fakeSource, in *CheckRequest) { src.enabled = false }},
		{"enabled 关", func(src *fakeSource, in *CheckRequest) { src.cfg.Enabled = false }},
		{"mode off", func(src *fakeSource, in *CheckRequest) { src.cfg.Mode = ModeOff }},
		{"分组不在范围", func(src *fakeSource, in *CheckRequest) {
			src.cfg.AllGroups = false
			src.cfg.GroupIDs = []int{99}
		}},
		{"模型被排除", func(src *fakeSource, in *CheckRequest) {
			src.cfg.ModelFilter = ModelFilter{Type: ModelFilterExclude, Models: []string{"gpt-4o"}}
		}},
		{"空输入", func(src *fakeSource, in *CheckRequest) { in.Body = []byte(`{"messages":[]}`) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &fakeSource{enabled: true, cfg: cfg.Clone()}
			logs := &fakeLogStore{}
			e := newTestEngine(src, logs, newFakeHashCache(), &fakeBanner{})
			in := baseCheckRequest("含炸弹的输入")
			tt.mutate(src, &in)
			d := e.Check(context.Background(), in)
			if !d.Allowed {
				t.Fatalf("应放行, got %+v", d)
			}
		})
	}
}

func TestCheckKeywordBlock(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModePreBlock
	cfg.BlockedKeywords = []string{"炸弹"}
	cfg.KeywordBlockingMode = KeywordModeKeywordOnly

	src := &fakeSource{enabled: true, cfg: cfg}
	logs := &fakeLogStore{}
	banner := &fakeBanner{}
	e := newTestEngine(src, logs, newFakeHashCache(), banner)

	d := e.Check(context.Background(), baseCheckRequest("如何制造炸弹"))
	if d.Allowed || d.Action != ActionKeywordBlock || d.StatusCode != http.StatusForbidden {
		t.Fatalf("decision = %+v", d)
	}
	drainQueue(t, e, cfg)
	entries := logs.all()
	if len(entries) != 1 {
		t.Fatalf("日志条数 = %d", len(entries))
	}
	entry := entries[0]
	if entry.MatchedKeyword != "炸弹" || !entry.Flagged || entry.Action != ActionKeywordBlock {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.ViolationCount != 1 {
		t.Fatalf("violation = %d", entry.ViolationCount)
	}

	// keyword_only 不打外部 API：无 key 也应能拦截；未命中直接放行。
	d = e.Check(context.Background(), baseCheckRequest("普通问题"))
	if !d.Allowed {
		t.Fatalf("未命中应放行: %+v", d)
	}
}

func TestCheckHashBlockSkipsBanCount(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModePreBlock
	cfg.PreHashCheckEnabled = true
	cfg.KeywordBlockingMode = KeywordModeAPIOnly

	src := &fakeSource{enabled: true, cfg: cfg}
	logs := &fakeLogStore{}
	banner := &fakeBanner{}
	hashes := newFakeHashCache()
	e := newTestEngine(src, logs, hashes, banner)

	in := baseCheckRequest("重复的坏内容")
	content := ExtractInput(in.Protocol, "", in.Body)
	_ = hashes.Record(context.Background(), content.Hash())

	d := e.Check(context.Background(), in)
	if d.Allowed || d.Action != ActionHashBlock || d.InputHash == "" {
		t.Fatalf("decision = %+v", d)
	}
	drainQueue(t, e, cfg)
	entries := logs.all()
	if len(entries) != 1 || entries[0].Action != ActionHashBlock {
		t.Fatalf("entries = %+v", entries)
	}
	// hash_block 不触发封号副作用。
	if len(banner.banned) != 0 || entries[0].ViolationCount != 0 {
		t.Fatalf("hash_block 不应计封号: banned=%v violation=%d", banner.banned, entries[0].ViolationCount)
	}
}

func TestCheckPreBlockAPIFlowAndAutoBan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"flagged":true,"category_scores":{"hate":0.99}}]}`))
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModePreBlock
	cfg.BaseURL = srv.URL
	cfg.APIKeys = []string{"k"}
	cfg.KeywordBlockingMode = KeywordModeAPIOnly
	cfg.AutoBanEnabled = true
	cfg.BanThreshold = 3

	src := &fakeSource{enabled: true, cfg: cfg}
	logs := &fakeLogStore{count: 2} // 历史已有 2 次 → 本次第 3 次触发封禁
	banner := &fakeBanner{}
	hashes := newFakeHashCache()
	e := newTestEngine(src, logs, hashes, banner)

	d := e.Check(context.Background(), baseCheckRequest("辱骂内容"))
	if d.Allowed || d.Action != ActionBlock || d.HighestCategory != "hate" {
		t.Fatalf("decision = %+v", d)
	}
	drainQueue(t, e, cfg)
	entries := logs.all()
	if len(entries) != 1 {
		t.Fatalf("entries = %d", len(entries))
	}
	if entries[0].ViolationCount != 3 || !entries[0].AutoBanned {
		t.Fatalf("entry = %+v", entries[0])
	}
	if len(banner.banned) != 1 || banner.banned[0] != 7 {
		t.Fatalf("banned = %v", banner.banned)
	}
	// 命中后 hash 已入缓存 → 同内容再次提交走 hash_block（不再打 API）。
	src.cfg.PreHashCheckEnabled = true
	e.InvalidateSnapshot() // 快照 serve-stale，改配置后需显式失效
	d = e.Check(context.Background(), baseCheckRequest("辱骂内容"))
	if d.Action != ActionHashBlock {
		t.Fatalf("应 hash_block: %+v", d)
	}
}

func TestCheckObserveNeverBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"flagged":true,"category_scores":{"sexual":0.99}}]}`))
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeObserve
	cfg.BaseURL = srv.URL
	cfg.APIKeys = []string{"k"}

	src := &fakeSource{enabled: true, cfg: cfg}
	logs := &fakeLogStore{}
	banner := &fakeBanner{}
	e := newTestEngine(src, logs, newFakeHashCache(), banner)

	d := e.Check(context.Background(), baseCheckRequest("命中内容"))
	if !d.Allowed {
		t.Fatalf("observe 不应阻断: %+v", d)
	}
	drainQueue(t, e, cfg)
	entries := logs.all()
	if len(entries) != 1 || !entries[0].Flagged || entries[0].Action != ActionAllow {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].QueueDelayMS <= 0 {
		t.Fatalf("observe 日志应带排队时延: %+v", entries[0])
	}
	// observe 命中不触发封禁副作用。
	if len(banner.banned) != 0 {
		t.Fatalf("observe 不应封禁: %v", banner.banned)
	}
}

func TestCheckSampleRateZeroSkipsAPI(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModePreBlock
	cfg.SampleRate = 0
	cfg.APIKeys = []string{"k"}
	cfg.KeywordBlockingMode = KeywordModeAPIOnly
	cfg.BaseURL = "http://127.0.0.1:1" // 若走到 API 会失败，验证根本不触网

	src := &fakeSource{enabled: true, cfg: cfg}
	e := newTestEngine(src, &fakeLogStore{}, newFakeHashCache(), &fakeBanner{})
	d := e.Check(context.Background(), baseCheckRequest("任意内容"))
	if !d.Allowed {
		t.Fatalf("采样为 0 应放行: %+v", d)
	}
}

func TestNilEngineAllows(t *testing.T) {
	var e *Engine
	d := e.Check(context.Background(), baseCheckRequest("x"))
	if !d.Allowed {
		t.Fatal("nil engine 应放行")
	}
}
