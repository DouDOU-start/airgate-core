package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// flakyReloadable 前 failures 次失败、之后成功的 reloadable。
type flakyReloadable struct {
	mu       sync.Mutex
	failures int
	calls    int
}

func (f *flakyReloadable) Reload(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failures {
		return errors.New("db down")
	}
	return nil
}

func (f *flakyReloadable) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// TestRetryReloadStopsOnSuccess 退避重试直至成功即停，不再多余调用。
func TestRetryReloadStopsOnSuccess(t *testing.T) {
	target := &flakyReloadable{failures: 2}
	done := make(chan struct{})
	go func() {
		retryReload(context.Background(), target, "test", time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retryReload 未在成功后退出")
	}
	if got := target.callCount(); got != 3 {
		t.Errorf("Reload 调用次数 = %d, want 3（2 败 1 成）", got)
	}
	// 成功退出后不再有新调用。
	time.Sleep(20 * time.Millisecond)
	if got := target.callCount(); got != 3 {
		t.Errorf("成功后仍在重试: 次数 = %d", got)
	}
}

// TestRetryReloadStopsOnCancel ctx 取消即停（服务关停不悬挂 goroutine）。
func TestRetryReloadStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	target := &flakyReloadable{failures: 1 << 30} // 永远失败
	done := make(chan struct{})
	go func() {
		retryReload(ctx, target, "test", time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retryReload 未在 ctx 取消后退出")
	}
}
