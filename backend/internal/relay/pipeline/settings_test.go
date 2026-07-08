package pipeline

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// fakeLister 返回固定设置项。
type fakeLister struct {
	items []Setting
	err   error
	calls int
	// lastCtxErr / lastCtxHasDeadline 记录 List 执行时刻的 ctx 状态
	//（断言刷新用独立后台 ctx，不能在 List 返回后再查——刷新方会 cancel）。
	lastCtxErr         error
	lastCtxHasDeadline bool
}

func (f *fakeLister) List(ctx context.Context, _ string) ([]Setting, error) {
	f.calls++
	f.lastCtxErr = ctx.Err()
	_, f.lastCtxHasDeadline = ctx.Deadline()
	return f.items, f.err
}

func TestSettingsReader(t *testing.T) {
	cases := []struct {
		name   string
		lister SettingsLister
		want   GatewaySettings
	}{
		{
			name:   "nil lister 恒返回默认",
			lister: nil,
			want:   defaultGatewaySettings(),
		},
		{
			name:   "读失败用默认兜底",
			lister: &fakeLister{err: errors.New("db down")},
			want:   defaultGatewaySettings(),
		},
		{
			name: "两键全解析",
			lister: &fakeLister{items: []Setting{
				{Key: "channel_auto_ban_enabled", Value: "false"},
				{Key: "channel_ban_keywords", Value: `["Custom KEYWORD"," another "]`},
			}},
			want: GatewaySettings{
				AutoBanEnabled: false,
				BanKeywords:    []string{"custom keyword", "another"}, // 统一小写 + 去首尾空白
			},
		},
		{
			name: "关键词 JSON 非法保留默认表",
			lister: &fakeLister{items: []Setting{
				{Key: "channel_ban_keywords", Value: `not-json`},
			}},
			want: defaultGatewaySettings(),
		},
		{
			name: "空值项忽略",
			lister: &fakeLister{items: []Setting{
				{Key: "channel_auto_ban_enabled", Value: "  "},
			}},
			want: defaultGatewaySettings(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewSettingsReader(tc.lister)
			got := r.Get(context.Background())
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Get = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestSettingsReaderTTLCache TTL 内不重复打后端。
func TestSettingsReaderTTLCache(t *testing.T) {
	lister := &fakeLister{items: []Setting{{Key: "channel_auto_ban_enabled", Value: "false"}}}
	r := NewSettingsReader(lister)

	for i := 0; i < 5; i++ {
		if got := r.Get(context.Background()); got.AutoBanEnabled {
			t.Fatal("AutoBanEnabled 应为 false")
		}
	}
	if lister.calls != 1 {
		t.Errorf("TTL 内 List 调用次数 = %d, want 1", lister.calls)
	}
}

// expire 强制缓存过期（测试用）。
func (r *SettingsReader) expire() {
	r.mu.Lock()
	r.expiresAt = time.Time{}
	r.mu.Unlock()
}

// TestSettingsReaderServeStale 刷新失败时沿用上一次成功加载的值（serve-stale），
// 不得用默认值覆盖管理员配置（缓存中毒会把 auto_ban 关闭窗口变回默认开启）。
func TestSettingsReaderServeStale(t *testing.T) {
	lister := &fakeLister{items: []Setting{
		{Key: "channel_auto_ban_enabled", Value: "false"},
	}}
	r := NewSettingsReader(lister)

	first := r.Get(context.Background())
	if first.AutoBanEnabled {
		t.Fatalf("首次加载异常: %+v", first)
	}

	// 后端故障 + 缓存过期：应返回旧值而不是默认值。
	lister.err = errors.New("db down")
	r.expire()
	got := r.Get(context.Background())
	if got.AutoBanEnabled {
		t.Error("刷新失败后 AutoBanEnabled 被默认值覆盖（缓存中毒）")
	}

	// serve-stale 续短 TTL：随后立即 Get 不应再打后端（未到期）。
	callsAfterFailure := lister.calls
	if got := r.Get(context.Background()); got.AutoBanEnabled {
		t.Error("serve-stale 窗口内仍应返回旧值")
	}
	if lister.calls != callsAfterFailure {
		t.Errorf("serve-stale 窗口内不应重复打后端: calls %d → %d", callsAfterFailure, lister.calls)
	}

	// 后端恢复 + 过期：恢复正常刷新。
	lister.err = nil
	lister.items = []Setting{{Key: "channel_auto_ban_enabled", Value: "true"}}
	r.expire()
	if got := r.Get(context.Background()); !got.AutoBanEnabled {
		t.Error("后端恢复后应读到新值")
	}
}

// TestSettingsReaderNeverLoadedFallsBackToDefaults 从未成功加载过时才回退默认值。
func TestSettingsReaderNeverLoadedFallsBackToDefaults(t *testing.T) {
	lister := &fakeLister{err: errors.New("db down")}
	r := NewSettingsReader(lister)
	if got := r.Get(context.Background()); !reflect.DeepEqual(got, defaultGatewaySettings()) {
		t.Errorf("Get = %+v, want 默认值", got)
	}
}

// TestSettingsReaderRefreshUsesBackgroundCtx 刷新不使用触发请求的 ctx：
// 客户端断连（ctx 已取消）不得把 context.Canceled 放大为刷新失败。
func TestSettingsReaderRefreshUsesBackgroundCtx(t *testing.T) {
	lister := &fakeLister{items: []Setting{{Key: "channel_auto_ban_enabled", Value: "false"}}}
	r := NewSettingsReader(lister)

	canceled, cancel := context.WithCancel(context.Background())
	cancel() // 模拟客户端已断连的请求 ctx

	got := r.Get(canceled)
	if got.AutoBanEnabled {
		t.Fatalf("断连 ctx 触发的刷新应正常加载: %+v", got)
	}
	if lister.calls != 1 {
		t.Fatal("List 未被调用")
	}
	if lister.lastCtxErr != nil {
		t.Errorf("刷新 ctx 不应继承请求 ctx 的取消状态: %v", lister.lastCtxErr)
	}
	if !lister.lastCtxHasDeadline {
		t.Error("刷新 ctx 应带独立超时")
	}
}

// TestSettingsReaderSingleFlight 刷新进行中其他请求直接用现值，不排队等 DB。
func TestSettingsReaderSingleFlight(t *testing.T) {
	lister := &fakeLister{items: []Setting{{Key: "channel_auto_ban_enabled", Value: "false"}}}
	r := NewSettingsReader(lister)
	if got := r.Get(context.Background()); got.AutoBanEnabled {
		t.Fatal("首次加载失败")
	}

	// 模拟另一 goroutine 正在刷新：过期但 refreshing=true。
	r.mu.Lock()
	r.expiresAt = time.Time{}
	r.refreshing = true
	r.mu.Unlock()

	callsBefore := lister.calls
	if got := r.Get(context.Background()); got.AutoBanEnabled {
		t.Error("刷新进行中应返回现有缓存值")
	}
	if lister.calls != callsBefore {
		t.Errorf("刷新进行中不应再触发 List: calls %d → %d", callsBefore, lister.calls)
	}
}
