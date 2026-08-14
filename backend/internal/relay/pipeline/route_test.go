package pipeline

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
)

type routeTestAccountLoader struct {
	accounts []accountreg.Snapshot
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
