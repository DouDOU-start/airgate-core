package streamlife

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDetachAfterStart开始前跟随客户端取消(t *testing.T) {
	clientCtx, clientCancel := context.WithCancel(context.Background())
	upstreamCtx, _, cancel := DetachAfterStart(clientCtx, time.Second)
	defer cancel()

	clientCancel()
	select {
	case <-upstreamCtx.Done():
		if !errors.Is(upstreamCtx.Err(), context.Canceled) {
			t.Fatalf("上游错误 = %v，期望 context.Canceled", upstreamCtx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("开始响应前，客户端取消应立即传递给上游")
	}
}

func TestDetachAfterStart开始后忽略客户端取消(t *testing.T) {
	clientCtx, clientCancel := context.WithCancel(context.Background())
	upstreamCtx, markStarted, cancel := DetachAfterStart(clientCtx, time.Second)
	defer cancel()

	markStarted()
	clientCancel()
	select {
	case <-upstreamCtx.Done():
		t.Fatalf("开始响应后不应被客户端取消：%v", upstreamCtx.Err())
	case <-time.After(30 * time.Millisecond):
	}
}

func TestDetachAfterStart达到硬超时后取消(t *testing.T) {
	upstreamCtx, markStarted, cancel := DetachAfterStart(context.Background(), 20*time.Millisecond)
	defer cancel()
	markStarted()

	select {
	case <-upstreamCtx.Done():
		if !errors.Is(upstreamCtx.Err(), context.DeadlineExceeded) {
			t.Fatalf("上游错误 = %v，期望 context.DeadlineExceeded", upstreamCtx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("达到硬超时后应取消上游")
	}
}
