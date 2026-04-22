package scheduler

import (
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
)

// TestRouteCache_HitMiss 基础命中 / 未命中行为。
func TestRouteCache_HitMiss(t *testing.T) {
	c := newRouteCache(100 * time.Millisecond)

	if _, _, ok := c.Get(1, "openai"); ok {
		t.Fatalf("空缓存不应命中")
	}

	accounts := []*ent.Account{{ID: 10}, {ID: 20}}
	routing := map[string][]int64{"gpt-4o": {10}}
	c.Set(1, "openai", accounts, routing)

	got, r, ok := c.Get(1, "openai")
	if !ok {
		t.Fatalf("写入后应命中")
	}
	if len(got) != 2 || got[0].ID != 10 || got[1].ID != 20 {
		t.Errorf("命中的账号列表不符预期: %+v", got)
	}
	if r["gpt-4o"][0] != 10 {
		t.Errorf("routing 未正确缓存: %+v", r)
	}
}

// TestRouteCache_Expiry TTL 过期后要返回 miss，避免把陈旧数据喂给调度器。
func TestRouteCache_Expiry(t *testing.T) {
	c := newRouteCache(20 * time.Millisecond)
	c.Set(1, "openai", []*ent.Account{{ID: 1}}, nil)

	time.Sleep(40 * time.Millisecond)
	if _, _, ok := c.Get(1, "openai"); ok {
		t.Fatalf("超过 TTL 应返回 miss")
	}
}

// TestRouteCache_InvalidateGroup 清指定 group 的所有 platform；不影响其它 group。
func TestRouteCache_InvalidateGroup(t *testing.T) {
	c := newRouteCache(1 * time.Second)
	c.Set(1, "openai", []*ent.Account{{ID: 1}}, nil)
	c.Set(1, "claude", []*ent.Account{{ID: 2}}, nil)
	c.Set(2, "openai", []*ent.Account{{ID: 3}}, nil)

	c.InvalidateGroup(1)

	if _, _, ok := c.Get(1, "openai"); ok {
		t.Errorf("group=1 openai 应被清除")
	}
	if _, _, ok := c.Get(1, "claude"); ok {
		t.Errorf("group=1 claude 应被清除")
	}
	if _, _, ok := c.Get(2, "openai"); !ok {
		t.Errorf("group=2 不应受影响")
	}
}

// TestRouteCache_InvalidateAll 全量清空（状态机关键转移时触发）。
func TestRouteCache_InvalidateAll(t *testing.T) {
	c := newRouteCache(1 * time.Second)
	c.Set(1, "openai", []*ent.Account{{ID: 1}}, nil)
	c.Set(2, "openai", []*ent.Account{{ID: 2}}, nil)

	c.InvalidateAll()

	if _, _, ok := c.Get(1, "openai"); ok {
		t.Errorf("InvalidateAll 后 group=1 应 miss")
	}
	if _, _, ok := c.Get(2, "openai"); ok {
		t.Errorf("InvalidateAll 后 group=2 应 miss")
	}
}

// TestRouteCache_NilSafe 零值 / nil 接收者不能 panic。
func TestRouteCache_NilSafe(t *testing.T) {
	var c *routeCache
	if _, _, ok := c.Get(1, "openai"); ok {
		t.Errorf("nil 缓存不应命中")
	}
	c.Set(1, "openai", nil, nil) // 不应 panic
	c.InvalidateGroup(1)         // 不应 panic
	c.InvalidateAll()            // 不应 panic
}

// TestApplyModelRouting_PassThrough routing 为空时原样返回。
func TestApplyModelRouting_PassThrough(t *testing.T) {
	accounts := []*ent.Account{{ID: 1}, {ID: 2}}
	got := applyModelRouting(accounts, nil, "gpt-4o")
	if len(got) != 2 {
		t.Errorf("routing 为 nil 时应原样返回，got=%+v", got)
	}

	got = applyModelRouting(accounts, map[string][]int64{}, "gpt-4o")
	if len(got) != 2 {
		t.Errorf("routing 为空 map 时应原样返回，got=%+v", got)
	}
}

// TestApplyModelRouting_Filter 命中 routing 时按 ID 过滤。
func TestApplyModelRouting_Filter(t *testing.T) {
	accounts := []*ent.Account{{ID: 1}, {ID: 2}, {ID: 3}}
	routing := map[string][]int64{"gpt-4o": {1, 3}}

	got := applyModelRouting(accounts, routing, "gpt-4o")
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Errorf("按 routing 过滤失败: %+v", got)
	}

	// 未命中 model：原样返回（上游规则）
	got = applyModelRouting(accounts, routing, "gpt-5.4")
	if len(got) != 3 {
		t.Errorf("未命中 model 应返回全部: %+v", got)
	}
}

// TestApplyModelRouting_NoMutation 过滤时不能修改原 slice（缓存共享底层数组）。
func TestApplyModelRouting_NoMutation(t *testing.T) {
	accounts := []*ent.Account{{ID: 1}, {ID: 2}, {ID: 3}}
	routing := map[string][]int64{"gpt-4o": {1}}

	_ = applyModelRouting(accounts, routing, "gpt-4o")

	// 原 slice 必须保持不变
	if len(accounts) != 3 || accounts[0].ID != 1 || accounts[1].ID != 2 || accounts[2].ID != 3 {
		t.Errorf("原 slice 被修改: %+v", accounts)
	}
}
