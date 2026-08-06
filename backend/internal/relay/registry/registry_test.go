package registry

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeLoader 返回固定快照集合。
type fakeLoader struct {
	snaps []ChannelKeySnapshot
	err   error
}

func (f *fakeLoader) LoadAllForRegistry(context.Context) ([]ChannelKeySnapshot, error) {
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
	errMsg string
}

func newFakePersister() *fakePersister {
	return &fakePersister{done: make(chan struct{}, 16)}
}

func (f *fakePersister) PersistState(_ context.Context, id int, status string, errMsg string) error {
	f.mu.Lock()
	f.calls = append(f.calls, persistCall{id: id, status: status, errMsg: errMsg})
	f.mu.Unlock()
	f.done <- struct{}{}
	return nil
}

func (f *fakePersister) PersistCredentialState(_ context.Context, id int, status string, errMsg string) error {
	f.mu.Lock()
	f.calls = append(f.calls, persistCall{id: id, status: status, errMsg: errMsg})
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

// snap 构造测试 key 端点快照的便捷函数（KeyID 与 ChannelID 同值，便于断言）。
// 默认绑定分组 0（与用例里未显式指定的 groupID 零值对应），
// 需要测试"未绑定分组"语义时显式传 GroupIDs: map[int]struct{}{}。
func snap(id int, mutate ...func(*ChannelKeySnapshot)) ChannelKeySnapshot {
	s := ChannelKeySnapshot{
		KeyID:       id,
		ChannelID:   id,
		ChannelName: "ch",
		Type:        "openai_compatible",
		BaseURL:     "https://upstream.example",
		APIKey:      "sk-a",
		Models:      map[string]struct{}{"gpt-4o": {}},
		Priority:    50,
		Weight:      10,
		Status:      StatusEnabled,
		GroupIDs:    map[int]struct{}{0: {}},
	}
	for _, m := range mutate {
		m(&s)
	}
	return s
}

func newTestRegistry(t *testing.T, persister Persister, snaps ...ChannelKeySnapshot) *Registry {
	t.Helper()
	r := New(&fakeLoader{snaps: snaps}, persister)
	if err := r.Reload(context.Background()); err != nil {
		t.Fatalf("Reload 失败: %v", err)
	}
	return r
}

// TestMarkCredentialAutoDisabledStopsAllProtocols 验证 401 凭证级禁用会同时移除
// 同一 API Key 的全部协议端点，而不是只打掉当前协议。
func TestMarkCredentialAutoDisabledStopsAllProtocols(t *testing.T) {
	persister := newFakePersister()
	r := newTestRegistry(t, persister,
		snap(1, func(s *ChannelKeySnapshot) {
			s.CredentialID = 99
			s.CredentialStatus = StatusEnabled
		}),
		snap(2, func(s *ChannelKeySnapshot) {
			s.CredentialID = 99
			s.CredentialStatus = StatusEnabled
			s.Type = "anthropic"
		}),
	)

	r.MarkCredentialAutoDisabled(1, "HTTP 401")
	call := persister.waitOne(t)
	if call.id != 99 || call.status != StatusDisabledAuto || call.errMsg != "HTTP 401" {
		t.Fatalf("凭证状态落库 = %+v", call)
	}
	for _, keyID := range []int{1, 2} {
		snapshot, ok := r.Snapshot(keyID)
		if !ok || snapshot.CredentialStatus != StatusDisabledAuto {
			t.Fatalf("端点 %d 未继承凭证禁用状态: %+v", keyID, snapshot)
		}
	}
	if _, err := r.Pick(0, "gpt-4o", ProtocolOpenAI, nil); !errors.Is(err, ErrNoAvailableChannel) {
		t.Fatalf("OpenAI 协议仍可调度: %v", err)
	}
	if _, err := r.Pick(0, "gpt-4o", ProtocolAnthropic, nil); !errors.Is(err, ErrNoAvailableChannel) {
		t.Fatalf("Anthropic 协议仍可调度: %v", err)
	}
}

func TestChannelKeySnapshotEffectiveCostRatio(t *testing.T) {
	tests := []struct {
		name string
		snap *ChannelKeySnapshot
		want float64
	}{
		{name: "开关开启时探测结果优先", snap: &ChannelKeySnapshot{CostRatio: 0.5, UpstreamRate: 0.8, UseUpstreamRateForCost: true}, want: 0.8},
		{name: "开关关闭时忽略探测结果", snap: &ChannelKeySnapshot{CostRatio: 0.5, UpstreamRate: 0.8}, want: 0.5},
		{name: "开关开启但无探测结果时回退配置", snap: &ChannelKeySnapshot{CostRatio: 0.5, UseUpstreamRateForCost: true}, want: 0.5},
		{name: "无探测结果回退配置", snap: &ChannelKeySnapshot{CostRatio: 0.5}, want: 0.5},
		{name: "倍率均无效回退一倍", snap: &ChannelKeySnapshot{}, want: 1},
		{name: "空快照回退一倍", snap: nil, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.snap.EffectiveCostRatio(); got != tt.want {
				t.Fatalf("EffectiveCostRatio() = %v, want %v", got, tt.want)
			}
		})
	}
}
func TestRegistryPick(t *testing.T) {
	cases := []struct {
		name    string
		snaps   []ChannelKeySnapshot
		groupID int
		model   string
		// protocol 入口协议；用例未显式指定时按 openai 执行（见循环内缺省）。
		protocol string
		exclude  []int
		randN    int // randFn 固定返回值
		wantID   int
		wantErr  error
	}{
		{
			name:    "无任何 key",
			snaps:   nil,
			model:   "gpt-4o",
			wantErr: ErrNoAvailableChannel,
		},
		{
			name:    "模型不命中",
			snaps:   []ChannelKeySnapshot{snap(1)},
			model:   "claude-3-opus",
			wantErr: ErrNoAvailableChannel,
		},
		{
			name: "手动禁用与自动禁用均不可调度",
			snaps: []ChannelKeySnapshot{
				snap(1, func(s *ChannelKeySnapshot) { s.Status = StatusDisabledManual }),
				snap(2, func(s *ChannelKeySnapshot) { s.Status = StatusDisabledAuto }),
			},
			model:   "gpt-4o",
			wantErr: ErrNoAvailableChannel,
		},
		{
			name: "空分组 key 不会被任何分组调度到",
			snaps: []ChannelKeySnapshot{
				snap(1, func(s *ChannelKeySnapshot) { s.GroupIDs = map[int]struct{}{} }),
			},
			groupID: 99,
			model:   "gpt-4o",
			wantErr: ErrNoAvailableChannel,
		},
		{
			name: "分组不命中被过滤",
			snaps: []ChannelKeySnapshot{
				snap(1, func(s *ChannelKeySnapshot) { s.GroupIDs = map[int]struct{}{7: {}} }),
			},
			groupID: 8,
			model:   "gpt-4o",
			wantErr: ErrNoAvailableChannel,
		},
		{
			name: "分组命中可选",
			snaps: []ChannelKeySnapshot{
				snap(1, func(s *ChannelKeySnapshot) { s.GroupIDs = map[int]struct{}{7: {}} }),
			},
			groupID: 7,
			model:   "gpt-4o",
			wantID:  1,
		},
		{
			name: "exclude 按 keyID 硬排除",
			snaps: []ChannelKeySnapshot{
				snap(1),
				snap(2),
			},
			model:   "gpt-4o",
			exclude: []int{1},
			wantID:  2,
		},
		{
			name: "最高优先级档优先",
			snaps: []ChannelKeySnapshot{
				snap(1, func(s *ChannelKeySnapshot) { s.Priority = 10 }),
				snap(2, func(s *ChannelKeySnapshot) { s.Priority = 90 }),
				snap(3, func(s *ChannelKeySnapshot) { s.Priority = 50 }),
			},
			model:  "gpt-4o",
			wantID: 2,
		},
		{
			name: "档内 weight+10 加权随机：随机值落在第一个候选区间",
			snaps: []ChannelKeySnapshot{
				snap(1, func(s *ChannelKeySnapshot) { s.Weight = 0 }),  // 区间 [0,10)
				snap(2, func(s *ChannelKeySnapshot) { s.Weight = 90 }), // 区间 [10,110)
			},
			model:  "gpt-4o",
			randN:  9,
			wantID: 1,
		},
		{
			name: "档内 weight+10 加权随机：随机值落在第二个候选区间",
			snaps: []ChannelKeySnapshot{
				snap(1, func(s *ChannelKeySnapshot) { s.Weight = 0 }),
				snap(2, func(s *ChannelKeySnapshot) { s.Weight = 90 }),
			},
			model:  "gpt-4o",
			randN:  10,
			wantID: 2,
		},
		// ===== 协议维度过滤（纯透传：同模型跨协议 key 不串台） =====
		{
			name: "openai 协议只命中 openai_compatible key（同模型 anthropic key 被过滤）",
			snaps: []ChannelKeySnapshot{
				snap(1, func(s *ChannelKeySnapshot) { s.Type = "anthropic"; s.Priority = 99 }),
				snap(2), // openai_compatible
			},
			model:    "gpt-4o",
			protocol: ProtocolOpenAI,
			wantID:   2,
		},
		{
			name: "anthropic 协议只命中 anthropic key（同模型 openai key 被过滤）",
			snaps: []ChannelKeySnapshot{
				snap(1, func(s *ChannelKeySnapshot) { s.Priority = 99 }), // openai_compatible
				snap(2, func(s *ChannelKeySnapshot) { s.Type = "anthropic" }),
			},
			model:    "gpt-4o",
			protocol: ProtocolAnthropic,
			wantID:   2,
		},
		{
			name: "gemini 协议只命中 gemini key",
			snaps: []ChannelKeySnapshot{
				snap(1, func(s *ChannelKeySnapshot) { s.Type = "anthropic" }),
				snap(2, func(s *ChannelKeySnapshot) { s.Type = "gemini" }),
				snap(3), // openai_compatible
			},
			model:    "gpt-4o",
			protocol: ProtocolGemini,
			wantID:   2,
		},
		{
			name: "协议无匹配 key 类型时无可用 key",
			snaps: []ChannelKeySnapshot{
				snap(1), // openai_compatible
			},
			model:    "gpt-4o",
			protocol: ProtocolAnthropic,
			wantErr:  ErrNoAvailableChannel,
		},
		// ===== key 级故障隔离：同渠道不同 key 独立调度 =====
		{
			name: "同渠道一把 key 自动禁用不连累另一把",
			snaps: []ChannelKeySnapshot{
				snap(1, func(s *ChannelKeySnapshot) { s.ChannelID = 100; s.Status = StatusDisabledAuto; s.Priority = 99 }),
				snap(2, func(s *ChannelKeySnapshot) { s.ChannelID = 100 }),
			},
			model:  "gpt-4o",
			wantID: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRegistry(t, nil, tc.snaps...)
			// 固定随机源；Pick 档内按 KeyID 排序，随机值区间可精确断言。
			r.randFn = func(int) int { return tc.randN }

			protocol := tc.protocol
			if protocol == "" {
				protocol = ProtocolOpenAI
			}
			got, err := r.Pick(tc.groupID, tc.model, protocol, tc.exclude)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("期望错误 %v，实际 %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Pick 失败: %v", err)
			}
			if got.KeyID != tc.wantID {
				t.Fatalf("期望选中 key %d，实际 %d", tc.wantID, got.KeyID)
			}
		})
	}
}

// TestRegistryPickUnknownProtocol 未知协议（含空串）不再有任何缺省兼容：
// 无可路由 key 类型，直接返回 ErrNoAvailableChannel。
func TestRegistryPickUnknownProtocol(t *testing.T) {
	r := newTestRegistry(t, nil, snap(1))
	for _, protocol := range []string{"", "unknown"} {
		if _, err := r.Pick(0, "gpt-4o", protocol, nil); !errors.Is(err, ErrNoAvailableChannel) {
			t.Errorf("protocol=%q 期望 ErrNoAvailableChannel，实际 %v", protocol, err)
		}
	}
}

// TestRegistryPickWeighted 对加权随机做全区间扫描：
// 遍历全部随机值，验证各候选被选中的次数恰好等于其有效权重（weight+10）。
func TestRegistryPickWeighted(t *testing.T) {
	r := newTestRegistry(t, nil,
		snap(1, func(s *ChannelKeySnapshot) { s.Weight = 0 }),  // 有效权重 10
		snap(2, func(s *ChannelKeySnapshot) { s.Weight = 90 }), // 有效权重 100
	)

	counts := map[int]int{}
	for n := 0; n < 110; n++ {
		r.randFn = func(int) int { return n }
		got, err := r.Pick(0, "gpt-4o", ProtocolOpenAI, nil)
		if err != nil {
			t.Fatalf("Pick 失败: %v", err)
		}
		counts[got.KeyID]++
	}
	if counts[1] != 10 || counts[2] != 100 {
		t.Fatalf("加权分布不符：期望 {1:10, 2:100}，实际 %v", counts)
	}
}

func TestRegistryMarkAutoDisabledAndRecovered(t *testing.T) {
	persister := newFakePersister()
	r := newTestRegistry(t, persister, snap(1))

	r.MarkAutoDisabled(1, "invalid_api_key")
	if _, err := r.Pick(0, "gpt-4o", ProtocolOpenAI, nil); !errors.Is(err, ErrNoAvailableChannel) {
		t.Fatalf("自动禁用后 key 仍被选中，err=%v", err)
	}
	call := persister.waitOne(t)
	if call.status != StatusDisabledAuto || call.errMsg != "invalid_api_key" {
		t.Fatalf("自动禁用落库参数不符: %+v", call)
	}

	r.MarkRecovered(1)
	if got, err := r.Pick(0, "gpt-4o", ProtocolOpenAI, nil); err != nil || got.KeyID != 1 {
		t.Fatalf("恢复后应可选中 key 1，got=%v err=%v", got, err)
	}
	call = persister.waitOne(t)
	if call.status != StatusEnabled || call.errMsg != "" {
		t.Fatalf("恢复落库参数不符: %+v", call)
	}
}

func TestRegistryMarkRecoveredSkipsManualDisabled(t *testing.T) {
	persister := newFakePersister()
	r := newTestRegistry(t, persister,
		snap(1, func(s *ChannelKeySnapshot) { s.Status = StatusDisabledManual }),
	)

	// 手动禁用 key 不被 MarkRecovered 恢复，也不应触发落库。
	r.MarkRecovered(1)
	if _, err := r.Pick(0, "gpt-4o", ProtocolOpenAI, nil); !errors.Is(err, ErrNoAvailableChannel) {
		t.Fatalf("手动禁用 key 被错误恢复，err=%v", err)
	}
	select {
	case <-persister.done:
		t.Fatal("手动禁用 key 不应触发状态落库")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRegistryReloadReplacesKeys(t *testing.T) {
	loader := &fakeLoader{snaps: []ChannelKeySnapshot{snap(1), snap(2)}}
	r := New(loader, nil)
	if err := r.Reload(context.Background()); err != nil {
		t.Fatalf("Reload 失败: %v", err)
	}
	if _, ok := r.Snapshot(1); !ok {
		t.Fatal("key 1 应存在")
	}

	// 重载后整体替换：key 1 被删除、key 3 新增。
	loader.snaps = []ChannelKeySnapshot{snap(3)}
	if err := r.Reload(context.Background()); err != nil {
		t.Fatalf("Reload 失败: %v", err)
	}
	if _, ok := r.Snapshot(1); ok {
		t.Fatal("重载后 key 1 应被移除")
	}
	if _, ok := r.Snapshot(3); !ok {
		t.Fatal("重载后 key 3 应存在")
	}
}

// TestRegistryAnyKeyForChannel 存量任务无 key_id 时回退：取渠道任一 enabled key。
func TestRegistryAnyKeyForChannel(t *testing.T) {
	r := newTestRegistry(t, nil,
		snap(1, func(s *ChannelKeySnapshot) { s.ChannelID = 100; s.Status = StatusDisabledManual }),
		snap(2, func(s *ChannelKeySnapshot) { s.ChannelID = 100 }),
	)
	got, ok := r.AnyKeyForChannel(100)
	if !ok || got.KeyID != 2 {
		t.Fatalf("期望回退到 enabled key 2，got=%v ok=%v", got, ok)
	}
	if _, ok := r.AnyKeyForChannel(404); ok {
		t.Fatal("不存在渠道应返回 false")
	}
}

// TestRegistryModelEntriesForGroup 分组过滤与状态语义：
// 绑定分组 key 只对本组可见、未绑定分组 key 对任何分组均不可见、停用 key 不进目录。
func TestRegistryModelEntriesForGroup(t *testing.T) {
	r := newTestRegistry(t, nil,
		snap(1, func(s *ChannelKeySnapshot) {
			s.Models = map[string]struct{}{"gpt-4o": {}, "gpt-4o-mini": {}}
			s.GroupIDs = map[int]struct{}{7: {}}
		}),
		snap(2, func(s *ChannelKeySnapshot) {
			s.Models = map[string]struct{}{"claude-x": {}}
			s.GroupIDs = map[int]struct{}{8: {}}
		}),
		snap(3, func(s *ChannelKeySnapshot) {
			s.Models = map[string]struct{}{"disabled-model": {}}
			s.GroupIDs = map[int]struct{}{7: {}}
			s.Status = StatusDisabledManual
		}),
		snap(4, func(s *ChannelKeySnapshot) {
			s.Models = map[string]struct{}{"unbound-model": {}}
			s.GroupIDs = map[int]struct{}{}
		}),
	)

	cases := []struct {
		name    string
		groupID int
		want    []string
	}{
		{
			name:    "分组 7：仅本组绑定 key 的模型（停用 key 不计入）",
			groupID: 7,
			want:    []string{"gpt-4o", "gpt-4o-mini"},
		},
		{
			name:    "分组 8：仅本组绑定 key 的模型",
			groupID: 8,
			want:    []string{"claude-x"},
		},
		{
			name:    "未绑定 key 对任何分组均不可见",
			groupID: 999,
			want:    []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries := r.ModelEntriesForGroup(tc.groupID)
			got := make([]string, 0, len(entries))
			for _, e := range entries {
				got = append(got, e.Name)
				// 测试快照 key 均为 openai_compatible 类型：协议集合恒为 [openai]。
				if !slices.Equal(e.Protocols, []string{ProtocolOpenAI}) {
					t.Fatalf("模型 %s 协议 = %v, 期望 [openai]", e.Name, e.Protocols)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("ModelEntriesForGroup 模型名 = %v, 期望 %v", got, tc.want)
			}
		})
	}
}

// countingLoader 可切换失败/成功并统计加载次数的 Loader。
type countingLoader struct {
	mu    sync.Mutex
	snaps []ChannelKeySnapshot
	err   error
	loads int
}

func (c *countingLoader) LoadAllForRegistry(context.Context) ([]ChannelKeySnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loads++
	if c.err != nil {
		return nil, c.err
	}
	return c.snaps, nil
}

func (c *countingLoader) set(snaps []ChannelKeySnapshot, err error) {
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

	// DB 仍不可用：Pick 惰性尝试一次后仍无可用 key。
	if _, err := r.Pick(0, "gpt-4o", ProtocolOpenAI, nil); !errors.Is(err, ErrNoAvailableChannel) {
		t.Fatalf("Pick err = %v, want ErrNoAvailableChannel", err)
	}
	if loader.loadCount() != 2 { // 启动一次 + 惰性一次
		t.Fatalf("加载次数 = %d, want 2", loader.loadCount())
	}

	// 节流窗口内不重复打 DB。
	if _, err := r.Pick(0, "gpt-4o", ProtocolOpenAI, nil); !errors.Is(err, ErrNoAvailableChannel) {
		t.Fatalf("Pick err = %v", err)
	}
	if loader.loadCount() != 2 {
		t.Fatalf("节流窗口内加载次数 = %d, want 2", loader.loadCount())
	}

	// DB 恢复：Pick 惰性重载成功，无需任何管理员写操作。
	loader.set([]ChannelKeySnapshot{snap(1)}, nil)
	r.resetLazyThrottle()
	k, err := r.Pick(0, "gpt-4o", ProtocolOpenAI, nil)
	if err != nil || k.KeyID != 1 {
		t.Fatalf("Pick = (%+v, %v), want key 1", k, err)
	}

	// 已加载成功：后续 Pick 不再触发惰性重载。
	r.resetLazyThrottle()
	if _, err := r.Pick(0, "gpt-4o", ProtocolOpenAI, nil); err != nil {
		t.Fatalf("Pick err = %v", err)
	}
	if loader.loadCount() != 3 {
		t.Errorf("加载成功后仍触发惰性重载: 次数 = %d, want 3", loader.loadCount())
	}
}

func TestModelEntriesForGroupAggregatesProtocols(t *testing.T) {
	r := newTestRegistry(t, nil,
		// claude-sonnet 同时由 anthropic 原生 key 与 openai 兼容聚合 key 供给
		snap(1, func(s *ChannelKeySnapshot) {
			s.Type = "anthropic"
			s.Models = map[string]struct{}{"claude-sonnet": {}}
		}),
		snap(2, func(s *ChannelKeySnapshot) {
			s.Models = map[string]struct{}{"claude-sonnet": {}, "gpt-4o": {}}
		}),
		snap(3, func(s *ChannelKeySnapshot) {
			s.Type = "gemini"
			s.Models = map[string]struct{}{"gemini-2.5-pro": {}}
		}),
		// 停用 key 不进目录
		snap(4, func(s *ChannelKeySnapshot) {
			s.Status = StatusDisabledManual
			s.Models = map[string]struct{}{"disabled-model": {}}
		}),
	)

	entries := r.ModelEntriesForGroup(0)
	got := map[string][]string{}
	for _, e := range entries {
		got[e.Name] = e.Protocols
	}

	want := map[string][]string{
		"claude-sonnet":  {"anthropic", "openai"},
		"gpt-4o":         {"openai"},
		"gemini-2.5-pro": {"gemini"},
	}
	if len(got) != len(want) {
		t.Fatalf("模型目录条数 = %d, 期望 %d（%v）", len(got), len(want), got)
	}
	for name, protos := range want {
		if !slices.Equal(got[name], protos) {
			t.Fatalf("模型 %s 协议 = %v, 期望 %v", name, got[name], protos)
		}
	}
}
