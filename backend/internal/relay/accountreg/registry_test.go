package accountreg

import (
	"context"
	"testing"
	"time"
)

type registryLoaderStub struct {
	items []Snapshot
}

func (s registryLoaderStub) LoadAllForAccountRegistry(context.Context) ([]Snapshot, error) {
	return s.items, nil
}

type statePersistCall struct {
	state string
}

type registryPersisterStub struct {
	calls chan statePersistCall
}

func (s registryPersisterStub) PersistAccountState(_ context.Context, _ int, state string, _ *time.Time, _ string) error {
	s.calls <- statePersistCall{state: state}
	return nil
}

func TestModelsForGroup(t *testing.T) {
	registry := New(registryLoaderStub{items: []Snapshot{
		{
			ID: 1, State: StateActive, Platform: "xai",
			Models:   map[string]struct{}{"grok-3": {}, "grok-4": {}},
			GroupIDs: map[int]struct{}{7: {}},
		},
		{
			ID: 2, State: StateRateLimited, Platform: "xai",
			Models:   map[string]struct{}{"grok-3": {}, "grok-rate-limited-only": {}},
			GroupIDs: map[int]struct{}{7: {}},
		},
		{
			ID: 3, State: StateDisabled, Platform: "xai",
			Models:   map[string]struct{}{"grok-disabled": {}},
			GroupIDs: map[int]struct{}{7: {}},
		},
		{
			ID: 4, State: StateActive, Platform: "claude",
			Models:   map[string]struct{}{"claude-sonnet": {}},
			GroupIDs: map[int]struct{}{8: {}},
		},
		{
			ID: 5, State: StateActive, Platform: "xai",
			Models:   map[string]struct{}{"unbound-model": {}},
			GroupIDs: map[int]struct{}{},
		},
		{
			ID: 6, State: StateActive, Platform: "xai",
			Models:   nil, // 空模型不进目录
			GroupIDs: map[int]struct{}{7: {}},
		},
	}}, nil)
	if err := registry.Reload(context.Background()); err != nil {
		t.Fatalf("加载注册表失败: %v", err)
	}

	got7 := registry.ModelsForGroup(7)
	want7 := []string{"grok-3", "grok-4", "grok-rate-limited-only"}
	if len(got7) != len(want7) {
		t.Fatalf("group 7 models = %v, want %v", got7, want7)
	}
	for i := range want7 {
		if got7[i] != want7[i] {
			t.Fatalf("group 7 models = %v, want %v", got7, want7)
		}
	}

	got8 := registry.ModelsForGroup(8)
	if len(got8) != 1 || got8[0] != "claude-sonnet" {
		t.Fatalf("group 8 models = %v, want [claude-sonnet]", got8)
	}

	if got := registry.ModelsForGroup(999); len(got) != 0 {
		t.Fatalf("empty group models = %v, want []", got)
	}

	var nilReg *Registry
	if got := nilReg.ModelsForGroup(7); got != nil {
		t.Fatalf("nil registry = %v, want nil", got)
	}
}

func TestMarkActiveDoesNotPersistUnchangedState(t *testing.T) {
	persister := registryPersisterStub{calls: make(chan statePersistCall, 1)}
	registry := New(registryLoaderStub{items: []Snapshot{{ID: 1, State: StateActive}}}, persister)
	if err := registry.Reload(context.Background()); err != nil {
		t.Fatalf("加载注册表失败: %v", err)
	}

	registry.MarkActive(1)

	select {
	case call := <-persister.calls:
		t.Fatalf("未变化的 active 状态不应落库：%+v", call)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestStatePersistenceRejectsOlderVersion(t *testing.T) {
	persister := registryPersisterStub{calls: make(chan statePersistCall, 2)}
	registry := New(registryLoaderStub{}, persister)

	registry.persistAsync(1, StateDisabled, nil, "失效", 2)
	registry.persistAsync(1, StateActive, nil, "", 1)

	select {
	case call := <-persister.calls:
		if call.state != StateDisabled {
			t.Fatalf("落库了旧状态：%s", call.state)
		}
	case <-time.After(time.Second):
		t.Fatal("等待状态落库超时")
	}
	select {
	case call := <-persister.calls:
		t.Fatalf("旧版本不应再次落库：%+v", call)
	case <-time.After(30 * time.Millisecond):
	}
}
