package registry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeLoader 返回固定快照集合。
type fakeLoader struct {
	snaps []ChannelSnapshot
	err   error
}

func (f *fakeLoader) LoadAllForRegistry(context.Context) ([]ChannelSnapshot, error) {
	return f.snaps, f.err
}

// fakePersister 记录落库调用，供异步断言。
type fakePersister struct {
	mu    sync.Mutex
	calls []persistCall
	done  chan struct{}
}

type persistCall struct {
	id     int
	status string
	until  *time.Time
	errMsg string
}

func newFakePersister() *fakePersister {
	return &fakePersister{done: make(chan struct{}, 16)}
}

func (f *fakePersister) PersistState(_ context.Context, id int, status string, until *time.Time, errMsg string) error {
	f.mu.Lock()
	f.calls = append(f.calls, persistCall{id: id, status: status, until: until, errMsg: errMsg})
	f.mu.Unlock()
	f.done <- struct{}{}
	return nil
}

func (f *fakePersister) waitOne(t *testing.T) persistCall {
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

// snap 构造测试快照的便捷函数。
func snap(id int, mutate ...func(*ChannelSnapshot)) ChannelSnapshot {
	s := ChannelSnapshot{
		ID:       id,
		Name:     "ch",
		Type:     "openai_compatible",
		BaseURL:  "https://upstream.example",
		APIKeys:  []string{"sk-a", "sk-b"},
		Models:   map[string]struct{}{"gpt-4o": {}},
		Priority: 50,
		Weight:   10,
		Status:   StatusEnabled,
		GroupIDs: map[int]struct{}{},
	}
	for _, m := range mutate {
		m(&s)
	}
	return s
}

func newTestRegistry(t *testing.T, persister Persister, snaps ...ChannelSnapshot) *Registry {
	t.Helper()
	r := New(&fakeLoader{snaps: snaps}, persister)
	if err := r.Reload(context.Background()); err != nil {
		t.Fatalf("Reload 失败: %v", err)
	}
	return r
}

func TestRegistryPick(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(time.Minute)

	cases := []struct {
		name    string
		snaps   []ChannelSnapshot
		groupID int
		model   string
		exclude []int
		randN   int // randFn 固定返回值
		wantID  int
		wantErr error
	}{
		{
			name:    "无任何渠道",
			snaps:   nil,
			model:   "gpt-4o",
			wantErr: ErrNoAvailableChannel,
		},
		{
			name:    "模型不命中",
			snaps:   []ChannelSnapshot{snap(1)},
			model:   "claude-3-opus",
			wantErr: ErrNoAvailableChannel,
		},
		{
			name: "手动禁用与自动禁用均不可调度",
			snaps: []ChannelSnapshot{
				snap(1, func(s *ChannelSnapshot) { s.Status = StatusDisabledManual }),
				snap(2, func(s *ChannelSnapshot) { s.Status = StatusDisabledAuto }),
			},
			model:   "gpt-4o",
			wantErr: ErrNoAvailableChannel,
		},
		{
			name: "冷却未到期不可调度",
			snaps: []ChannelSnapshot{
				snap(1, func(s *ChannelSnapshot) { s.StatusUntil = &future }),
			},
			model:   "gpt-4o",
			wantErr: ErrNoAvailableChannel,
		},
		{
			name: "冷却已过期恢复可选",
			snaps: []ChannelSnapshot{
				snap(1, func(s *ChannelSnapshot) { s.StatusUntil = &past }),
			},
			model:  "gpt-4o",
			wantID: 1,
		},
		{
			name: "公共渠道（空分组）对任意分组可用",
			snaps: []ChannelSnapshot{
				snap(1),
			},
			groupID: 99,
			model:   "gpt-4o",
			wantID:  1,
		},
		{
			name: "分组不命中被过滤",
			snaps: []ChannelSnapshot{
				snap(1, func(s *ChannelSnapshot) { s.GroupIDs = map[int]struct{}{7: {}} }),
			},
			groupID: 8,
			model:   "gpt-4o",
			wantErr: ErrNoAvailableChannel,
		},
		{
			name: "分组命中可选",
			snaps: []ChannelSnapshot{
				snap(1, func(s *ChannelSnapshot) { s.GroupIDs = map[int]struct{}{7: {}} }),
			},
			groupID: 7,
			model:   "gpt-4o",
			wantID:  1,
		},
		{
			name: "exclude 硬排除",
			snaps: []ChannelSnapshot{
				snap(1),
				snap(2),
			},
			model:   "gpt-4o",
			exclude: []int{1},
			wantID:  2,
		},
		{
			name: "最高优先级档优先",
			snaps: []ChannelSnapshot{
				snap(1, func(s *ChannelSnapshot) { s.Priority = 10 }),
				snap(2, func(s *ChannelSnapshot) { s.Priority = 90 }),
				snap(3, func(s *ChannelSnapshot) { s.Priority = 50 }),
			},
			model:  "gpt-4o",
			wantID: 2,
		},
		{
			name: "档内 weight+10 加权随机：随机值落在第一个候选区间",
			snaps: []ChannelSnapshot{
				snap(1, func(s *ChannelSnapshot) { s.Weight = 0 }),  // 区间 [0,10)
				snap(2, func(s *ChannelSnapshot) { s.Weight = 90 }), // 区间 [10,110)
			},
			model:  "gpt-4o",
			randN:  9,
			wantID: 1,
		},
		{
			name: "档内 weight+10 加权随机：随机值落在第二个候选区间",
			snaps: []ChannelSnapshot{
				snap(1, func(s *ChannelSnapshot) { s.Weight = 0 }),
				snap(2, func(s *ChannelSnapshot) { s.Weight = 90 }),
			},
			model:  "gpt-4o",
			randN:  10,
			wantID: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRegistry(t, nil, tc.snaps...)
			// 固定随机源；Pick 档内按 ID 排序，随机值区间可精确断言。
			r.randFn = func(int) int { return tc.randN }

			got, err := r.Pick(tc.groupID, tc.model, tc.exclude)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("期望错误 %v，实际 %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Pick 失败: %v", err)
			}
			if got.ID != tc.wantID {
				t.Fatalf("期望选中渠道 %d，实际 %d", tc.wantID, got.ID)
			}
		})
	}
}

// TestRegistryPickWeighted 对加权随机做全区间扫描：
// 遍历全部随机值，验证各候选被选中的次数恰好等于其有效权重（weight+10）。
func TestRegistryPickWeighted(t *testing.T) {
	r := newTestRegistry(t, nil,
		snap(1, func(s *ChannelSnapshot) { s.Weight = 0 }),  // 有效权重 10
		snap(2, func(s *ChannelSnapshot) { s.Weight = 90 }), // 有效权重 100
	)

	counts := map[int]int{}
	for n := 0; n < 110; n++ {
		r.randFn = func(int) int { return n }
		got, err := r.Pick(0, "gpt-4o", nil)
		if err != nil {
			t.Fatalf("Pick 失败: %v", err)
		}
		counts[got.ID]++
	}
	if counts[1] != 10 || counts[2] != 100 {
		t.Fatalf("加权分布不符：期望 {1:10, 2:100}，实际 %v", counts)
	}
}

func TestRegistryNextKey(t *testing.T) {
	r := newTestRegistry(t, nil,
		snap(1, func(s *ChannelSnapshot) { s.APIKeys = []string{"k1", "k2", "k3"} }),
		snap(2, func(s *ChannelSnapshot) { s.APIKeys = nil }),
	)

	// 渠道内轮询：依次返回 k1 k2 k3 k1。
	want := []string{"k1", "k2", "k3", "k1"}
	for i, w := range want {
		if got := r.NextKey(1); got != w {
			t.Fatalf("第 %d 次 NextKey 期望 %q，实际 %q", i+1, w, got)
		}
	}

	if got := r.NextKey(2); got != "" {
		t.Fatalf("无密钥渠道期望空串，实际 %q", got)
	}
	if got := r.NextKey(404); got != "" {
		t.Fatalf("不存在渠道期望空串，实际 %q", got)
	}
}

func TestRegistryMarkCooldown(t *testing.T) {
	persister := newFakePersister()
	r := newTestRegistry(t, persister, snap(1))

	until := time.Now().Add(time.Minute)
	r.MarkCooldown(1, until)

	// 内存即时生效：冷却期间不可被 Pick。
	if _, err := r.Pick(0, "gpt-4o", nil); !errors.Is(err, ErrNoAvailableChannel) {
		t.Fatalf("冷却中渠道仍被选中，err=%v", err)
	}

	call := persister.waitOne(t)
	if call.id != 1 || call.status != StatusEnabled || call.until == nil || !call.until.Equal(until) {
		t.Fatalf("落库参数不符: %+v", call)
	}
}

func TestRegistryMarkAutoDisabledAndRecovered(t *testing.T) {
	persister := newFakePersister()
	r := newTestRegistry(t, persister, snap(1))

	r.MarkAutoDisabled(1, "invalid_api_key")
	if _, err := r.Pick(0, "gpt-4o", nil); !errors.Is(err, ErrNoAvailableChannel) {
		t.Fatalf("自动禁用后渠道仍被选中，err=%v", err)
	}
	call := persister.waitOne(t)
	if call.status != StatusDisabledAuto || call.errMsg != "invalid_api_key" {
		t.Fatalf("自动禁用落库参数不符: %+v", call)
	}

	r.MarkRecovered(1)
	if got, err := r.Pick(0, "gpt-4o", nil); err != nil || got.ID != 1 {
		t.Fatalf("恢复后应可选中渠道 1，got=%v err=%v", got, err)
	}
	call = persister.waitOne(t)
	if call.status != StatusEnabled || call.errMsg != "" {
		t.Fatalf("恢复落库参数不符: %+v", call)
	}
}

func TestRegistryMarkRecoveredSkipsManualDisabled(t *testing.T) {
	persister := newFakePersister()
	r := newTestRegistry(t, persister,
		snap(1, func(s *ChannelSnapshot) { s.Status = StatusDisabledManual }),
	)

	// 手动禁用渠道不被 MarkRecovered 恢复，也不应触发落库。
	r.MarkRecovered(1)
	if _, err := r.Pick(0, "gpt-4o", nil); !errors.Is(err, ErrNoAvailableChannel) {
		t.Fatalf("手动禁用渠道被错误恢复，err=%v", err)
	}
	select {
	case <-persister.done:
		t.Fatal("手动禁用渠道不应触发状态落库")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRegistryReloadKeepsCounters(t *testing.T) {
	loader := &fakeLoader{snaps: []ChannelSnapshot{
		snap(1, func(s *ChannelSnapshot) { s.APIKeys = []string{"k1", "k2"} }),
	}}
	r := New(loader, nil)
	if err := r.Reload(context.Background()); err != nil {
		t.Fatalf("Reload 失败: %v", err)
	}

	_ = r.NextKey(1) // k1
	// 重载后计数器保留，轮询继续推进而非重置。
	if err := r.Reload(context.Background()); err != nil {
		t.Fatalf("Reload 失败: %v", err)
	}
	if got := r.NextKey(1); got != "k2" {
		t.Fatalf("重载后期望继续轮询到 k2，实际 %q", got)
	}

	// 渠道被删除后计数器清理，NextKey 返回空串。
	loader.snaps = nil
	if err := r.Reload(context.Background()); err != nil {
		t.Fatalf("Reload 失败: %v", err)
	}
	if got := r.NextKey(1); got != "" {
		t.Fatalf("渠道删除后期望空串，实际 %q", got)
	}
}

func TestRegistryModelsForGroup(t *testing.T) {
	future := time.Now().Add(time.Minute)
	r := newTestRegistry(t, nil,
		snap(1, func(s *ChannelSnapshot) {
			s.Models = map[string]struct{}{"gpt-4o": {}, "gpt-4o-mini": {}}
		}),
		snap(2, func(s *ChannelSnapshot) {
			s.Models = map[string]struct{}{"claude-x": {}}
			s.GroupIDs = map[int]struct{}{7: {}}
		}),
		snap(3, func(s *ChannelSnapshot) {
			s.Models = map[string]struct{}{"disabled-model": {}}
			s.Status = StatusDisabledManual
		}),
		snap(4, func(s *ChannelSnapshot) {
			// 冷却是瞬态状态：模型目录仍然可见。
			s.Models = map[string]struct{}{"cooldown-model": {}}
			s.StatusUntil = &future
		}),
	)

	cases := []struct {
		name    string
		groupID int
		want    []string
	}{
		{
			name:    "绑定分组：公共渠道 + 本组渠道并集，字典序",
			groupID: 7,
			want:    []string{"claude-x", "cooldown-model", "gpt-4o", "gpt-4o-mini"},
		},
		{
			name:    "其他分组：仅公共渠道",
			groupID: 8,
			want:    []string{"cooldown-model", "gpt-4o", "gpt-4o-mini"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := r.ModelsForGroup(tc.groupID)
			if len(got) != len(tc.want) {
				t.Fatalf("ModelsForGroup = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("ModelsForGroup = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// countingLoader 可切换失败/成功并统计加载次数的 Loader。
type countingLoader struct {
	mu    sync.Mutex
	snaps []ChannelSnapshot
	err   error
	loads int
}

func (c *countingLoader) LoadAllForRegistry(context.Context) ([]ChannelSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loads++
	if c.err != nil {
		return nil, c.err
	}
	return c.snaps, nil
}

func (c *countingLoader) set(snaps []ChannelSnapshot, err error) {
	c.mu.Lock()
	c.snaps, c.err = snaps, err
	c.mu.Unlock()
}

func (c *countingLoader) loadCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loads
}

// resetLazyThrottle 清除惰性重载节流（测试用，绕开 lazyReloadInterval 等待）。
func (r *Registry) resetLazyThrottle() {
	r.lazyMu.Lock()
	r.lastLazyReload = time.Time{}
	r.lazyMu.Unlock()
}

// TestRegistryPickLazyReload 注册表从未成功加载过（启动 Reload 失败）时，
// Pick 惰性触发重载自愈；加载成功后不再触发。
func TestRegistryPickLazyReload(t *testing.T) {
	loader := &countingLoader{err: errors.New("db down")}
	r := New(loader, nil)

	// 启动加载失败（模拟 server 启动时 DB 瞬断）。
	if err := r.Reload(context.Background()); err == nil {
		t.Fatal("Reload 应失败")
	}

	// DB 仍不可用：Pick 惰性尝试一次后仍无可用渠道。
	if _, err := r.Pick(0, "gpt-4o", nil); !errors.Is(err, ErrNoAvailableChannel) {
		t.Fatalf("Pick err = %v, want ErrNoAvailableChannel", err)
	}
	if loader.loadCount() != 2 { // 启动一次 + 惰性一次
		t.Fatalf("加载次数 = %d, want 2", loader.loadCount())
	}

	// 节流窗口内不重复打 DB。
	if _, err := r.Pick(0, "gpt-4o", nil); !errors.Is(err, ErrNoAvailableChannel) {
		t.Fatalf("Pick err = %v", err)
	}
	if loader.loadCount() != 2 {
		t.Fatalf("节流窗口内加载次数 = %d, want 2", loader.loadCount())
	}

	// DB 恢复：Pick 惰性重载成功，无需任何管理员写操作。
	loader.set([]ChannelSnapshot{snap(1)}, nil)
	r.resetLazyThrottle()
	ch, err := r.Pick(0, "gpt-4o", nil)
	if err != nil || ch.ID != 1 {
		t.Fatalf("Pick = (%+v, %v), want 渠道 1", ch, err)
	}

	// 已加载成功：后续 Pick 不再触发惰性重载。
	r.resetLazyThrottle()
	if _, err := r.Pick(0, "gpt-4o", nil); err != nil {
		t.Fatalf("Pick err = %v", err)
	}
	if loader.loadCount() != 3 {
		t.Errorf("加载成功后仍触发惰性重载: 次数 = %d, want 3", loader.loadCount())
	}
}
