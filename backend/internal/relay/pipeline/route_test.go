package pipeline

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
)

type routeTestAccountLoader struct {
	accounts []accountreg.Snapshot
}

type routeTestFirstTokenSource struct {
	samples []AccountFirstTokenSample
	since   time.Time
	limit   int
}

func (s *routeTestFirstTokenSource) LoadRecentAccountFirstTokenSamples(
	_ context.Context,
	since time.Time,
	limit int,
) ([]AccountFirstTokenSample, error) {
	s.since = since
	s.limit = limit
	return append([]AccountFirstTokenSample(nil), s.samples...), nil
}

func BenchmarkPickRoute(b *testing.B) {
	for _, total := range []int{100, 1000} {
		b.Run(fmt.Sprintf("accounts_%d", total), func(b *testing.B) {
			snapshots := make([]accountreg.Snapshot, total)
			for i := range snapshots {
				snapshots[i] = accountreg.Snapshot{
					ID: i + 1, Priority: 50, Weight: 10, State: accountreg.StateActive,
					Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}},
				}
			}
			accounts := accountreg.New(routeTestAccountLoader{accounts: snapshots}, nil)
			if err := accounts.Reload(context.Background()); err != nil {
				b.Fatal(err)
			}
			p := &Pipeline{accounts: accounts}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, ok := p.pickRoute(7, "gpt-5", "openai", nil, nil, nil); !ok {
					b.Fatal("未找到可用路由")
				}
			}
		})
	}
}

func BenchmarkPickRouteLatencyAware(b *testing.B) {
	for _, total := range []int{5, 100} {
		b.Run(fmt.Sprintf("accounts_%d", total), func(b *testing.B) {
			snapshots := make([]accountreg.Snapshot, total)
			for i := range snapshots {
				snapshots[i] = accountreg.Snapshot{
					ID: i + 1, Priority: 50, Weight: 10, State: accountreg.StateActive,
					Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}},
				}
			}
			accounts := accountreg.New(routeTestAccountLoader{accounts: snapshots}, nil)
			if err := accounts.Reload(context.Background()); err != nil {
				b.Fatal(err)
			}
			p := &Pipeline{accounts: accounts}
			for i := range snapshots {
				for range accountLatencyMinSamples {
					p.recordAccountFirstToken(i+1, "gpt-5", int64(3_000+i*10))
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, ok := p.pickRoute(7, "gpt-5", "openai", nil, nil, nil); !ok {
					b.Fatal("未找到可用路由")
				}
			}
		})
	}
}

func BenchmarkBuildWeightedSchedule(b *testing.B) {
	candidates := make([]weightedRouteRef, 1_000)
	for i := range candidates {
		candidates[i] = weightedRouteRef{
			ref:    routeRef{kind: routeAccount, id: i + 1},
			weight: i%10 + 1,
		}
	}
	b.ReportAllocs()
	for range b.N {
		copyOfCandidates := append([]weightedRouteRef(nil), candidates...)
		if schedule := buildWeightedSchedule(copyOfCandidates); len(schedule) == 0 {
			b.Fatal("加权调度序列为空")
		}
	}
}

func (l routeTestAccountLoader) LoadAllForAccountRegistry(context.Context) ([]accountreg.Snapshot, error) {
	return l.accounts, nil
}

func TestPickRouteUsesHighestPriorityAsHardTier(t *testing.T) {
	accounts := accountreg.New(routeTestAccountLoader{accounts: []accountreg.Snapshot{
		{ID: 1, Priority: 100, Weight: 1, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
		{ID: 2, Priority: 10, Weight: 1_000, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
	}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{accounts: accounts}
	for range 20 {
		selected, ok := p.pickRoute(7, "gpt-5", "openai", nil, nil, nil)
		if !ok || selected.account == nil || selected.account.ID != 1 {
			t.Fatalf("低优先级候选越过了硬优先级档：%+v", selected)
		}
	}
}

func TestIndexedRouteUsesConfiguredWeight(t *testing.T) {
	accounts := accountreg.New(routeTestAccountLoader{accounts: []accountreg.Snapshot{
		{ID: 1, Priority: 50, Weight: 8, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
		{ID: 2, Priority: 50, Weight: 2, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
	}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{accounts: accounts}
	counts := map[int]int{}
	for range 100 {
		selected, ok := p.pickRoute(7, "gpt-5", "openai", nil, nil, nil)
		if !ok || selected.account == nil {
			t.Fatal("未找到可用路由")
		}
		counts[selected.account.ID]++
	}
	if counts[1] != 80 || counts[2] != 20 {
		t.Fatalf("加权分布 = %v，期望 map[1:80 2:20]", counts)
	}
}

func TestIndexedRoutePrefersLessLoadedAccount(t *testing.T) {
	accounts := accountreg.New(routeTestAccountLoader{accounts: []accountreg.Snapshot{
		{ID: 1, Priority: 50, Weight: 10, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
		{ID: 2, Priority: 50, Weight: 10, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
	}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{accounts: accounts}
	releases := []func(){p.trackAccountAttempt(1), p.trackAccountAttempt(1)}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()

	selected, ok := p.pickRoute(7, "gpt-5", "openai", nil, nil, nil)
	if !ok || selected.account == nil || selected.account.ID != 2 {
		t.Fatalf("未优先选择低在途账号：%+v", selected)
	}
}

func TestIndexedRoutePrefersRecentLowFirstTokenAccount(t *testing.T) {
	accounts := accountreg.New(routeTestAccountLoader{accounts: []accountreg.Snapshot{
		{ID: 1, Priority: 50, Weight: 10, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
		{ID: 2, Priority: 50, Weight: 10, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
	}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{accounts: accounts}
	for range accountLatencyMinSamples {
		p.recordAccountFirstToken(1, "gpt-5", 8_000)
		p.recordAccountFirstToken(2, "gpt-5", 3_000)
	}

	selected, ok := p.pickRoute(7, "gpt-5", "openai", nil, nil, nil)
	if !ok || selected.account == nil || selected.account.ID != 2 {
		t.Fatalf("未优先选择近期首字更低账号：%+v", selected)
	}
}

func TestWarmAccountFirstTokens启动后立即优选快速账号(t *testing.T) {
	now := time.Now()
	source := &routeTestFirstTokenSource{samples: []AccountFirstTokenSample{
		{AccountID: 1, Model: "gpt-5", FirstTokenMs: 8_000, CreatedAt: now.Add(-6 * time.Minute)},
		{AccountID: 2, Model: "gpt-5", FirstTokenMs: 3_000, CreatedAt: now.Add(-5 * time.Minute)},
		{AccountID: 1, Model: "gpt-5", FirstTokenMs: 8_000, CreatedAt: now.Add(-4 * time.Minute)},
		{AccountID: 2, Model: "gpt-5", FirstTokenMs: 3_000, CreatedAt: now.Add(-3 * time.Minute)},
		{AccountID: 1, Model: "gpt-5", FirstTokenMs: 8_000, CreatedAt: now.Add(-2 * time.Minute)},
		{AccountID: 2, Model: "gpt-5", FirstTokenMs: 3_000, CreatedAt: now.Add(-time.Minute)},
	}}
	accounts := accountreg.New(routeTestAccountLoader{accounts: []accountreg.Snapshot{
		{ID: 1, Priority: 50, Weight: 10, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
		{ID: 2, Priority: 50, Weight: 10, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
	}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{accounts: accounts, accountFirstTokenSource: source}
	warmed, err := p.WarmAccountFirstTokens(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if warmed != 2 || source.limit != accountLatencyWarmupMaxRows {
		t.Fatalf("预热结果 keys=%d limit=%d，期望 2/%d", warmed, source.limit, accountLatencyWarmupMaxRows)
	}
	if age := time.Since(source.since); age < accountLatencyFreshDuration || age > accountLatencyFreshDuration+time.Second {
		t.Fatalf("预热时间窗异常：%v", age)
	}

	selected, ok := p.pickRoute(7, "gpt-5", "openai", nil, nil, nil)
	if !ok || selected.account == nil || selected.account.ID != 2 {
		t.Fatalf("预热后未立即优选快速账号：%+v", selected)
	}
}

func TestWarmAccountFirstTokens样本不足不启用延迟评分(t *testing.T) {
	now := time.Now()
	source := &routeTestFirstTokenSource{samples: []AccountFirstTokenSample{
		{AccountID: 7, Model: "gpt-5", FirstTokenMs: 2_000, CreatedAt: now.Add(-2 * time.Minute)},
		{AccountID: 7, Model: "gpt-5", FirstTokenMs: 1_800, CreatedAt: now.Add(-time.Minute)},
	}}
	p := &Pipeline{accountFirstTokenSource: source}
	if _, err := p.WarmAccountFirstTokens(context.Background()); err != nil {
		t.Fatal(err)
	}
	if latency, ok := p.recentAccountFirstToken(7, "gpt-5", time.Now()); ok {
		t.Fatalf("不足 %d 个样本不应启用延迟评分，实际 %dms", accountLatencyMinSamples, latency)
	}
}

func TestWarmAccountFirstTokens忽略非法和越界样本(t *testing.T) {
	now := time.Now()
	source := &routeTestFirstTokenSource{samples: []AccountFirstTokenSample{
		{AccountID: 1, Model: "gpt-5", FirstTokenMs: 2_000, CreatedAt: now.Add(-time.Minute)},
		{AccountID: 2, Model: "gpt-5", FirstTokenMs: 2_000, CreatedAt: now.Add(-accountLatencyFreshDuration - time.Minute)},
		{AccountID: 3, Model: "gpt-5", FirstTokenMs: 2_000, CreatedAt: now.Add(time.Minute)},
		{AccountID: 0, Model: "gpt-5", FirstTokenMs: 2_000, CreatedAt: now.Add(-time.Minute)},
		{AccountID: 4, Model: " ", FirstTokenMs: 2_000, CreatedAt: now.Add(-time.Minute)},
		{AccountID: 5, Model: "gpt-5", FirstTokenMs: 0, CreatedAt: now.Add(-time.Minute)},
	}}
	p := &Pipeline{accountFirstTokenSource: source}
	warmed, err := p.WarmAccountFirstTokens(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if warmed != 1 {
		t.Fatalf("只有一个合法 key 应被预热，实际 %d", warmed)
	}
	for _, accountID := range []int{2, 3, 4, 5} {
		if _, ok := p.accountFirstToken.Load(accountLatencyKey{accountID: accountID, model: "gpt-5"}); ok {
			t.Fatalf("非法账号样本不应进入预热状态：%d", accountID)
		}
	}
}

func TestWarmAccountFirstTokens不覆盖实时状态(t *testing.T) {
	now := time.Now()
	source := &routeTestFirstTokenSource{samples: []AccountFirstTokenSample{
		{AccountID: 1, Model: "gpt-5", FirstTokenMs: 9_000, CreatedAt: now.Add(-3 * time.Minute)},
		{AccountID: 1, Model: "gpt-5", FirstTokenMs: 9_000, CreatedAt: now.Add(-2 * time.Minute)},
		{AccountID: 1, Model: "gpt-5", FirstTokenMs: 9_000, CreatedAt: now.Add(-time.Minute)},
	}}
	p := &Pipeline{accountFirstTokenSource: source}
	for range accountLatencyMinSamples {
		p.recordAccountFirstToken(1, "gpt-5", 1_000)
	}
	warmed, err := p.WarmAccountFirstTokens(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if warmed != 0 {
		t.Fatalf("已有实时状态不应被历史状态覆盖，实际新增 %d 个 key", warmed)
	}
	latency, ok := p.recentAccountFirstToken(1, "gpt-5", time.Now())
	if !ok || latency != 1_000 {
		t.Fatalf("实时 EWMA 被预热污染：latency=%d ok=%v", latency, ok)
	}
}

func TestIndexedRouteBalancesLatencyAndInflight(t *testing.T) {
	accounts := accountreg.New(routeTestAccountLoader{accounts: []accountreg.Snapshot{
		{ID: 1, Priority: 50, Weight: 10, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
		{ID: 2, Priority: 50, Weight: 10, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
	}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{accounts: accounts}
	for range accountLatencyMinSamples {
		p.recordAccountFirstToken(1, "gpt-5", 3_000)
		p.recordAccountFirstToken(2, "gpt-5", 8_000)
	}
	releases := []func(){p.trackAccountAttempt(1), p.trackAccountAttempt(1)}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()

	selected, ok := p.pickRoute(7, "gpt-5", "openai", nil, nil, nil)
	if !ok || selected.account == nil || selected.account.ID != 2 {
		t.Fatalf("快速账号在途较高时应选择预计完成更早的空闲账号：%+v", selected)
	}
}

func TestTrackAccountAttemptReleasesLoad(t *testing.T) {
	p := &Pipeline{}
	release := p.trackAccountAttempt(9)
	if got := p.accountInflightCount(9); got != 1 {
		t.Fatalf("在途计数 = %d，期望 1", got)
	}
	release()
	if got := p.accountInflightCount(9); got != 0 {
		t.Fatalf("释放后在途计数 = %d，期望 0", got)
	}
	// 重复释放也不得把调度计数污染成负数。
	release()
	if got := p.accountInflightCount(9); got != 0 {
		t.Fatalf("重复释放后在途计数 = %d，期望 0", got)
	}
}

func TestIndexedRouteFallsBackWhenTopTierExcluded(t *testing.T) {
	accounts := accountreg.New(routeTestAccountLoader{accounts: []accountreg.Snapshot{
		{ID: 1, Priority: 100, Weight: 1, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
		{ID: 2, Priority: 10, Weight: 1, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
	}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{accounts: accounts}
	selected, ok := p.pickRoute(7, "gpt-5", "openai", nil, []int{1}, nil)
	if !ok || selected.account == nil || selected.account.ID != 2 {
		t.Fatalf("兜底路由 = %+v，期望账号 2", selected)
	}
}

func TestIndexedRouteInvalidatesAfterAccountStateChange(t *testing.T) {
	accounts := accountreg.New(routeTestAccountLoader{accounts: []accountreg.Snapshot{
		{ID: 1, Priority: 100, Weight: 1, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
		{ID: 2, Priority: 10, Weight: 1, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
	}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{accounts: accounts}
	assertAccount := func(want int) {
		t.Helper()
		selected, ok := p.pickRoute(7, "gpt-5", "openai", nil, nil, nil)
		if !ok || selected.account == nil || selected.account.ID != want {
			t.Fatalf("路由 = %+v，期望账号 %d", selected, want)
		}
	}
	assertAccount(1)
	accounts.MarkDisabled(1, "测试")
	assertAccount(2)
	accounts.MarkActive(1)
	assertAccount(1)
}

func TestIndexedRouteConcurrentSelection(t *testing.T) {
	accounts := accountreg.New(routeTestAccountLoader{accounts: []accountreg.Snapshot{
		{ID: 1, Priority: 50, Weight: 3, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
		{ID: 2, Priority: 50, Weight: 1, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-5": {}}, GroupIDs: map[int]struct{}{7: {}}},
	}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{accounts: accounts}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1_000 {
				selected, ok := p.pickRoute(7, "gpt-5", "openai", nil, nil, nil)
				if !ok || selected.account == nil || (selected.account.ID != 1 && selected.account.ID != 2) {
					t.Errorf("无效路由：%+v", selected)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestSmoothWeightedRouteUsesConfiguredRatio(t *testing.T) {
	p := &Pipeline{}
	tier := []routeTarget{
		{kind: routeAccount, weight: 8, account: &accountreg.Snapshot{ID: 1}},
		{kind: routeAccount, weight: 2, account: &accountreg.Snapshot{ID: 2}},
	}
	counts := map[int]int{}
	for range 100 {
		selected := p.pickSmoothWeighted(7, "gpt-5", "openai", tier)
		counts[selected.account.ID]++
	}
	if counts[1] != 80 || counts[2] != 20 {
		t.Fatalf("加权分布 = %v，期望 map[1:80 2:20]", counts)
	}
}

func TestSmoothWeightedRouteIsIndependentPerModel(t *testing.T) {
	p := &Pipeline{}
	tier := []routeTarget{
		{kind: routeAccount, weight: 3, account: &accountreg.Snapshot{ID: 1}},
		{kind: routeAccount, weight: 1, account: &accountreg.Snapshot{ID: 2}},
	}
	for range 3 {
		_ = p.pickSmoothWeighted(7, "model-a", "openai", tier)
	}
	first := p.pickSmoothWeighted(7, "model-b", "openai", tier)
	if first.account.ID != 1 {
		t.Fatalf("新模型应使用独立调度序列，实际得到账号 %d", first.account.ID)
	}
}

func TestEffectiveRouteWeightUsesExactValue(t *testing.T) {
	if got := effectiveRouteWeight(20); got != 20 {
		t.Fatalf("权重 20 被转换为 %d", got)
	}
	if got := effectiveRouteWeight(0); got != 1 {
		t.Fatalf("零权重兜底值 = %d，期望 1", got)
	}
}
