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
