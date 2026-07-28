package probe

import (
	"context"
	"testing"
	"time"
)

type captureNotifier struct {
	events chan HealthEvent
}

func (n *captureNotifier) Notify(_ context.Context, event HealthEvent) error {
	n.events <- event
	return nil
}

func receiveHealthEvent(t *testing.T, events <-chan HealthEvent) HealthEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("等待健康状态通知超时")
		return HealthEvent{}
	}
}

func TestEngineNotifiesOnlyOnHealthChange(t *testing.T) {
	engine := New(nil, nil, nil, nil, nil)
	notifier := &captureNotifier{events: make(chan HealthEvent, 4)}
	engine.SetNotifier(notifier)

	engine.RecordFailure(7)
	engine.RecordFailure(7)
	select {
	case event := <-notifier.events:
		t.Fatalf("健康状态未变化时不应通知：%+v", event)
	case <-time.After(30 * time.Millisecond):
	}

	engine.RecordFailure(7)
	event := receiveHealthEvent(t, notifier.events)
	if event.KeyID != 7 || event.OldHealth != HealthHealthy || event.NewHealth != HealthDegraded {
		t.Fatalf("降级通知不正确：%+v", event)
	}
	if event.Failures != DefaultDegradeThreshold || event.Reason == "" {
		t.Fatalf("降级通知缺少计数或原因：%+v", event)
	}
}

func TestEngineAuthFailureNotifiesSuspended(t *testing.T) {
	engine := New(nil, nil, nil, nil, nil)
	notifier := &captureNotifier{events: make(chan HealthEvent, 1)}
	engine.SetNotifier(notifier)

	engine.RecordAuthFailure(9)
	event := receiveHealthEvent(t, notifier.events)
	if event.OldHealth != HealthHealthy || event.NewHealth != HealthSuspended {
		t.Fatalf("鉴权失败通知状态不正确：%+v", event)
	}
	if event.Failures != DefaultSuspendThreshold {
		t.Fatalf("鉴权失败计数 = %d，期望 %d", event.Failures, DefaultSuspendThreshold)
	}
}
