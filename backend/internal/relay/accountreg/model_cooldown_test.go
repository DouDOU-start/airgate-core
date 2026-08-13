package accountreg

import (
	"context"
	"testing"
	"time"
)

func newModelCooldownRegistry(t *testing.T) *Registry {
	t.Helper()
	registry := New(registryLoaderStub{items: []Snapshot{
		{
			ID: 1, State: StateActive, Platform: "codex",
			Models:   map[string]struct{}{"gpt-5.5": {}, "gpt-5.4": {}},
			GroupIDs: map[int]struct{}{7: {}},
		},
	}}, nil)
	if err := registry.Reload(context.Background()); err != nil {
		t.Fatalf("加载注册表失败: %v", err)
	}
	return registry
}

func TestModelRateLimitIsolatesSiblingModels(t *testing.T) {
	registry := newModelCooldownRegistry(t)
	now := time.Now()

	registry.MarkModelRateLimited(1, "gpt-5.5", time.Minute)

	if _, ok := registry.RouteCandidate(1, 7, "gpt-5.5", now); ok {
		t.Fatal("冷却中的模型不应可调度")
	}
	if _, ok := registry.RouteCandidate(1, 7, "gpt-5.4", now); !ok {
		t.Fatal("同账号其余模型不应受限流影响")
	}
	if got := registry.ListCandidates(7, "gpt-5.5", nil); len(got) != 0 {
		t.Fatalf("ListCandidates 应过滤冷却模型，got %d", len(got))
	}
	if got := registry.ListCandidates(7, "gpt-5.4", nil); len(got) != 1 {
		t.Fatalf("兄弟模型候选应保留，got %d", len(got))
	}
}

func TestModelRateLimitExpiryAndClear(t *testing.T) {
	registry := newModelCooldownRegistry(t)

	until := registry.MarkModelRateLimited(1, "gpt-5.5", 50*time.Millisecond)
	if !until.After(time.Now()) {
		t.Fatal("冷却截止时间应在未来")
	}
	// 到期自动可用（惰性判定，无需任何恢复动作）
	if registry.isModelRateLimited(1, "gpt-5.5", until.Add(time.Millisecond)) {
		t.Fatal("冷却到期后应自动可用")
	}
	// 成功清除后立即可用
	registry.MarkModelRateLimited(1, "gpt-5.5", time.Minute)
	registry.ClearModelRateLimited(1, "gpt-5.5")
	if registry.isModelRateLimited(1, "gpt-5.5", time.Now()) {
		t.Fatal("成功清除后应立即可用")
	}
}

func TestModelRateLimitBackoffEscalatesOncePerWindow(t *testing.T) {
	registry := newModelCooldownRegistry(t)
	registry.randFn = func(int) int { return 0 } // 去抖动，便于断言

	first := registry.MarkModelRateLimited(1, "gpt-5.5", 0)
	// 同窗口并发失败：不升级退避，截止时间不变
	second := registry.MarkModelRateLimited(1, "gpt-5.5", 0)
	if !second.Equal(first) {
		t.Fatalf("同窗口并发失败应复用当前窗口: first=%v second=%v", first, second)
	}
	entry := registry.modelCooldown.entries[modelCooldownKey{accountID: 1, model: "gpt-5.5"}]
	if entry.backoffLevel != 1 {
		t.Fatalf("同窗口只应升一级退避, got %d", entry.backoffLevel)
	}
}

func TestAllModelsRateLimited(t *testing.T) {
	registry := newModelCooldownRegistry(t)
	now := time.Now()

	registry.MarkModelRateLimited(1, "gpt-5.5", time.Minute)
	if registry.AllModelsRateLimited(1, now) {
		t.Fatal("仅一个模型冷却时不应判定全冷")
	}
	registry.MarkModelRateLimited(1, "gpt-5.4", time.Minute)
	if !registry.AllModelsRateLimited(1, now) {
		t.Fatal("全部模型冷却时应判定全冷")
	}
	registry.ClearModelRateLimited(1, "gpt-5.4")
	if registry.AllModelsRateLimited(1, now) {
		t.Fatal("任一模型恢复后不应再判定全冷")
	}
}
