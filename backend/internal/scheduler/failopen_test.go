package scheduler

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestFailOpenLogGateThrottles fail-open 日志限频：同一窗口内并发调用
// 只有一个 goroutine 拿到输出权，窗口过期后重新允许输出。
func TestFailOpenLogGateThrottles(t *testing.T) {
	var g failOpenLogGate

	// 并发抢占：恰好一个 goroutine 允许输出
	const workers = 32
	var wg sync.WaitGroup
	var allowed atomic.Int32
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g.allow() {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := allowed.Load(); n != 1 {
		t.Fatalf("同一窗口内允许输出次数 = %d, want 1", n)
	}

	// 窗口内继续调用被抑制
	if g.allow() {
		t.Fatal("窗口内二次调用应被抑制")
	}

	// 时间戳拨回窗口之前，模拟窗口过期后重新允许
	g.lastLogNano.Store(time.Now().Add(-failOpenLogInterval - time.Second).UnixNano())
	if !g.allow() {
		t.Fatal("窗口过期后应重新允许输出")
	}
}
