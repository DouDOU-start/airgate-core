// Package streamlife 管理流式上游请求的取消生命周期。
package streamlife

import (
	"context"
	"sync"
	"time"
)

const (
	// MaxDuration 流开始后的最长后台排空时间。
	// 客户端断开后网关会继续读取上游以捕获最终 usage，但不得无限占用连接与并发槽。
	MaxDuration = 30 * time.Minute
)

// DetachAfterStart 创建流式上游上下文：
//
//   - markStarted 调用前，客户端取消会同步取消上游；
//   - markStarted 调用后，客户端取消不再影响上游，网关可继续排空流并捕获 usage；
//   - 无论是否开始，达到 maxDuration 都会强制取消。
//
// cancel 必须由调用方 defer 调用，以及时释放定时器资源。
func DetachAfterStart(clientCtx context.Context, maxDuration time.Duration) (
	upstreamCtx context.Context,
	markStarted func(),
	cancel context.CancelFunc,
) {
	if clientCtx == nil {
		clientCtx = context.Background()
	}
	if maxDuration <= 0 {
		maxDuration = MaxDuration
	}

	// 保留请求上下文中的值，但移除客户端断连带来的取消与 deadline；
	// 上游生命周期由下方的显式取消桥和硬超时共同控制。
	base := context.WithoutCancel(clientCtx)
	upstreamCtx, cancel = context.WithTimeout(base, maxDuration)

	var (
		mu      sync.Mutex
		started bool
	)
	markStarted = func() {
		mu.Lock()
		started = true
		mu.Unlock()
	}

	go func() {
		select {
		case <-clientCtx.Done():
			mu.Lock()
			if !started {
				cancel()
			}
			mu.Unlock()
		case <-upstreamCtx.Done():
		}
	}()

	return upstreamCtx, markStarted, cancel
}
