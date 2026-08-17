package account

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshUsage合并同账号并发请求(t *testing.T) {
	const (
		secret    = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
		callers   = 16
		accountID = 7
	)

	repo := &oauthRefreshRepo{}
	service := NewService(repo, secret)
	encrypted, _, err := service.prepareCredentials(map[string]string{"access_token": "访问令牌"})
	if err != nil {
		t.Fatalf("加密测试凭证失败: %v", err)
	}
	repo.item = Account{
		ID:             accountID,
		Name:           "Grok OAuth",
		Platform:       "xai",
		Type:           TypeOAuth,
		CredentialsEnc: encrypted,
	}

	var fetchCalls atomic.Int32
	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	service.usageFetcher = func(ctx context.Context, _ string, _ string, _ map[string]string, _ string) (UsageSnapshot, error) {
		if fetchCalls.Add(1) == 1 {
			close(fetchStarted)
		}
		select {
		case <-releaseFetch:
			return UsageSnapshot{
				CapturedAt: time.Now().UTC(),
				Windows:    []UsageWindow{{Key: "weekly", UsedPercent: 20}},
			}, nil
		case <-ctx.Done():
			return UsageSnapshot{}, ctx.Err()
		}
	}

	start := make(chan struct{})
	ready := &sync.WaitGroup{}
	ready.Add(callers)
	errorsCh := make(chan error, callers)
	for range callers {
		go func() {
			ready.Done()
			<-start
			_, _, refreshErr := service.RefreshUsage(context.Background(), accountID)
			errorsCh <- refreshErr
		}()
	}
	ready.Wait()
	close(start)
	<-fetchStarted
	time.Sleep(20 * time.Millisecond)
	close(releaseFetch)

	for range callers {
		if refreshErr := <-errorsCh; refreshErr != nil {
			t.Fatalf("并发刷新失败: %v", refreshErr)
		}
	}
	if got := fetchCalls.Load(); got != 1 {
		t.Fatalf("同账号并发刷新调用上游 %d 次，期望 1 次", got)
	}

	if _, _, err := service.RefreshUsage(context.Background(), accountID); err != nil {
		t.Fatalf("后续独立刷新失败: %v", err)
	}
	if got := fetchCalls.Load(); got != 2 {
		t.Fatalf("singleflight 不应缓存已完成结果，上游调用次数 = %d，期望 2", got)
	}
}
