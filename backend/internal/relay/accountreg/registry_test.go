package accountreg

import (
	"context"
	"sync"
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

func TestCandidateListsOnlyExplicitlyAllowRateLimitedAccounts(t *testing.T) {
	until := time.Now().Add(time.Hour)
	registry := New(registryLoaderStub{items: []Snapshot{
		{ID: 1, State: StateActive, Platform: "codex", Type: "oauth", Models: map[string]struct{}{"gpt-test": {}}, GroupIDs: map[int]struct{}{7: {}}},
		{ID: 2, State: StateRateLimited, StateUntil: &until, Platform: "codex", Type: "oauth", Models: map[string]struct{}{"gpt-test": {}}, GroupIDs: map[int]struct{}{7: {}}},
		{ID: 3, State: StateDisabled, Platform: "codex", Type: "oauth", Models: map[string]struct{}{"gpt-test": {}}, GroupIDs: map[int]struct{}{7: {}}},
		{ID: 4, State: StateRateLimited, StateUntil: &until, Platform: "codex", Type: "oauth", Models: map[string]struct{}{"gpt-test": {}}, GroupIDs: map[int]struct{}{8: {}}},
		{ID: 5, State: StateRateLimited, StateUntil: &until, Platform: "codex", Type: "oauth", Models: map[string]struct{}{"other": {}}, GroupIDs: map[int]struct{}{7: {}}},
		{ID: 6, State: StateRateLimited, StateUntil: &until, Platform: "xai", Type: "oauth", Models: map[string]struct{}{"gpt-test": {}}, GroupIDs: map[int]struct{}{7: {}}},
		{ID: 7, State: StateRateLimited, StateUntil: &until, Platform: "codex", Type: "api_key", Models: map[string]struct{}{"gpt-test": {}}, GroupIDs: map[int]struct{}{7: {}}},
	}}, nil)
	if err := registry.Reload(context.Background()); err != nil {
		t.Fatalf("加载注册表失败: %v", err)
	}

	if got := snapshotIDs(registry.ListCandidates(7, "gpt-test", nil)); !sameIntSet(got, []int{1}) {
		t.Fatalf("普通候选 = %v，期望 [1]", got)
	}
	if got := snapshotIDs(registry.ListRelayHookCandidates(7, "gpt-test")); !sameIntSet(got, []int{1, 2, 6, 7}) {
		t.Fatalf("Hook 可见候选 = %v，期望 [1 2 6 7]", got)
	}
	if got := snapshotIDs(registry.ListCandidatesAllowRateLimited(7, "gpt-test", nil, []int{2, 3, 4, 5, 6, 7})); !sameIntSet(got, []int{1, 2}) {
		t.Fatalf("显式放行候选 = %v，期望 [1 2]", got)
	}
	if got := snapshotIDs(registry.ListCandidatesAllowRateLimited(7, "gpt-test", []int{2}, []int{2})); !sameIntSet(got, []int{1}) {
		t.Fatalf("排除账号后的候选 = %v，期望 [1]", got)
	}
}

func snapshotIDs(items []*Snapshot) []int {
	result := make([]int, 0, len(items))
	for _, item := range items {
		result = append(result, item.ID)
	}
	return result
}

func sameIntSet(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	values := make(map[int]int, len(left))
	for _, value := range left {
		values[value]++
	}
	for _, value := range right {
		values[value]--
		if values[value] < 0 {
			return false
		}
	}
	return true
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

func TestBeginRateLimitProbeAllowsOnlyOneConcurrentClaim(t *testing.T) {
	until := time.Now().Add(time.Hour)
	registry := newRateLimitProbeRegistry(t, StateRateLimited, &until)

	const workers = 32
	start := make(chan struct{})
	decisions := make([]RateLimitProbeDecision, workers)
	leases := make([]RateLimitProbeLease, workers)
	var wg sync.WaitGroup
	for i := range decisions {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			decisions[index], leases[index] = registry.BeginRateLimitProbe(1)
		}(i)
	}
	close(start)
	wg.Wait()

	acquired := 0
	blocked := 0
	var acquiredLease RateLimitProbeLease
	for index, decision := range decisions {
		switch decision {
		case RateLimitProbeAcquired:
			acquired++
			acquiredLease = leases[index]
		case RateLimitProbeBlocked:
			blocked++
		default:
			t.Fatalf("限流账号返回了意外探测决策：%v", decision)
		}
	}
	if acquired != 1 || blocked != workers-1 {
		t.Fatalf("探测决策 acquired/blocked = %d/%d，期望 1/%d", acquired, blocked, workers-1)
	}

	registry.CancelRateLimitProbe(1, acquiredLease)
	got, nextLease := registry.BeginRateLimitProbe(1)
	if got != RateLimitProbeAcquired {
		t.Fatalf("取消探测后决策 = %v，期望 Acquired", got)
	}
	registry.CancelRateLimitProbe(1, nextLease)
}

func TestBeginRateLimitProbeDecisionsByState(t *testing.T) {
	registry := New(registryLoaderStub{items: []Snapshot{
		{ID: 1, State: StateActive},
		{ID: 2, State: StateDegraded},
		{ID: 3, State: StateDisabled},
	}}, nil)
	if err := registry.Reload(context.Background()); err != nil {
		t.Fatalf("加载注册表失败: %v", err)
	}

	for _, test := range []struct {
		accountID int
		want      RateLimitProbeDecision
	}{
		{accountID: 1, want: RateLimitProbeNotNeeded},
		{accountID: 2, want: RateLimitProbeNotNeeded},
		{accountID: 3, want: RateLimitProbeBlocked},
		{accountID: 999, want: RateLimitProbeBlocked},
	} {
		if got, _ := registry.BeginRateLimitProbe(test.accountID); got != test.want {
			t.Errorf("账号 %d 探测决策 = %v，期望 %v", test.accountID, got, test.want)
		}
	}
}

func TestRateLimitProbeGateFiltersEveryCandidateList(t *testing.T) {
	// 冷却已到期的 rate_limited 账号原本可进入普通候选；claim 后三个入口都必须隐藏。
	until := time.Now().Add(-time.Second)
	registry := newRateLimitProbeRegistry(t, StateRateLimited, &until)
	if got := snapshotIDs(registry.ListCandidates(7, "gpt-test", nil)); !sameIntSet(got, []int{1}) {
		t.Fatalf("claim 前普通候选 = %v，期望 [1]", got)
	}
	lease := mustAcquireRateLimitProbe(t, registry, 1)

	assertRateLimitProbeHidden(t, registry)
	registry.CancelRateLimitProbe(1, lease)
	if got := snapshotIDs(registry.ListRelayHookCandidates(7, "gpt-test")); !sameIntSet(got, []int{1}) {
		t.Fatalf("取消后 Hook 候选 = %v，期望 [1]", got)
	}
}

func TestRateLimitProbeFailureBlocksWithExponentialBackoff(t *testing.T) {
	until := time.Now().Add(5 * time.Second)
	registry := newRateLimitProbeRegistry(t, StateRateLimited, &until)
	lease := mustAcquireRateLimitProbe(t, registry, 1)

	started := time.Now()
	blockUntil := registry.MarkRateLimitProbeFailed(1, lease, time.Second, "超额请求仍返回 429")
	if blockUntil.Before(started.Add(rateLimitProbeInitialBackoff - time.Second)) {
		t.Fatalf("首次失败退避不足 30 秒：%s", blockUntil.Sub(started))
	}
	if blockUntil.After(started.Add(rateLimitProbeInitialBackoff + time.Second)) {
		t.Fatalf("首次失败退避异常过长：%s", blockUntil.Sub(started))
	}
	snap, ok := registry.Snapshot(1)
	if !ok || snap.State != StateRateLimited || snap.rateLimitProbeInFlight || snap.rateLimitProbeFailures != 1 {
		t.Fatalf("失败后运行时状态异常：%+v", snap)
	}
	if snap.StateUntil == nil || !snap.StateUntil.Equal(blockUntil) || !snap.rateLimitProbeBlockUntil.Equal(blockUntil) {
		t.Fatalf("失败截止时间未统一：state_until=%v block_until=%v 返回=%v", snap.StateUntil, snap.rateLimitProbeBlockUntil, blockUntil)
	}
	if snap.ErrorMsg != "超额请求仍返回 429" {
		t.Fatalf("失败原因 = %q", snap.ErrorMsg)
	}
	if got, _ := registry.BeginRateLimitProbe(1); got != RateLimitProbeBlocked {
		t.Fatalf("退避中探测决策 = %v，期望 Blocked", got)
	}
	assertRateLimitProbeHidden(t, registry)

	// 同一轮普通 MarkRateLimited 不得清除失败门闩或缩短截止时间。
	registry.MarkRateLimited(1, time.Now().Add(time.Second), "再次返回 429")
	afterMark, _ := registry.Snapshot(1)
	if afterMark.rateLimitProbeFailures != 1 || !afterMark.rateLimitProbeBlockUntil.Equal(blockUntil) {
		t.Fatalf("同轮 MarkRateLimited 清除了探测门闩：%+v", afterMark)
	}

	backoffCases := []struct {
		failures int
		want     time.Duration
	}{
		{1, 30 * time.Second},
		{2, time.Minute},
		{3, 2 * time.Minute},
		{4, 4 * time.Minute},
		{5, 8 * time.Minute},
		{6, 15 * time.Minute},
		{20, 15 * time.Minute},
	}
	for _, test := range backoffCases {
		if got := rateLimitProbeBackoff(test.failures); got != test.want {
			t.Errorf("失败 %d 次退避 = %s，期望 %s", test.failures, got, test.want)
		}
	}
}

func TestRateLimitProbeFailureHonorsRetryAfterAndCurrentStateUntil(t *testing.T) {
	t.Run("Retry-After 更晚", func(t *testing.T) {
		until := time.Now().Add(5 * time.Second)
		registry := newRateLimitProbeRegistry(t, StateRateLimited, &until)
		lease := mustAcquireRateLimitProbe(t, registry, 1)
		started := time.Now()
		blockUntil := registry.MarkRateLimitProbeFailed(1, lease, 2*time.Minute, "429")
		if blockUntil.Before(started.Add(2*time.Minute - time.Second)) {
			t.Fatalf("未遵守 Retry-After：%s", blockUntil.Sub(started))
		}
	})

	t.Run("当前 StateUntil 更晚", func(t *testing.T) {
		until := time.Now().Add(3 * time.Minute)
		registry := newRateLimitProbeRegistry(t, StateRateLimited, &until)
		lease := mustAcquireRateLimitProbe(t, registry, 1)
		blockUntil := registry.MarkRateLimitProbeFailed(1, lease, time.Second, "429")
		if !blockUntil.Equal(until) {
			t.Fatalf("blockUntil = %v，期望保留 StateUntil %v", blockUntil, until)
		}
	})
}

func TestRateLimitProbeSucceededRestoresActive(t *testing.T) {
	until := time.Now().Add(time.Hour)
	registry := newRateLimitProbeRegistry(t, StateRateLimited, &until)
	lease := mustAcquireRateLimitProbe(t, registry, 1)

	registry.MarkRateLimitProbeSucceeded(1, lease)
	snap, ok := registry.Snapshot(1)
	if !ok || snap.State != StateActive || snap.StateUntil != nil || snap.ErrorMsg != "" || !snap.rateLimitProbeClean() {
		t.Fatalf("探测成功后状态异常：%+v", snap)
	}
	if got, _ := registry.BeginRateLimitProbe(1); got != RateLimitProbeNotNeeded {
		t.Fatalf("恢复后探测决策 = %v，期望 NotNeeded", got)
	}
	if got := snapshotIDs(registry.ListCandidates(7, "gpt-test", nil)); !sameIntSet(got, []int{1}) {
		t.Fatalf("恢复后普通候选 = %v，期望 [1]", got)
	}
}

func TestMarkActiveIfNotRateLimitedDoesNotOverrideNewRateLimit(t *testing.T) {
	registry := newRateLimitProbeRegistry(t, StateActive, nil)
	until := time.Now().Add(time.Minute)
	registry.MarkRateLimited(1, until, "并发请求返回 429")

	// 模拟在 MarkRateLimited 之前取得 active 快照、之后才完成的旧请求。
	registry.MarkActiveIfNotRateLimited(1)
	snap, _ := registry.Snapshot(1)
	if snap.State != StateRateLimited || snap.StateUntil == nil || !snap.StateUntil.Equal(until) {
		t.Fatalf("旧成功覆盖了新限流：%+v", snap)
	}
	lease := mustAcquireRateLimitProbe(t, registry, 1)
	registry.CancelRateLimitProbe(1, lease)

	registry.MarkDisabled(1, "管理员禁用")
	registry.MarkActiveIfNotRateLimited(1)
	snap, _ = registry.Snapshot(1)
	if snap.State != StateDisabled {
		t.Fatalf("普通成功恢复了 disabled 账号：%+v", snap)
	}
}

func TestNewRateLimitCycleCanProbeAgain(t *testing.T) {
	registry := newRateLimitProbeRegistry(t, StateActive, nil)
	registry.MarkRateLimited(1, time.Now().Add(time.Minute), "第一轮 429")
	firstLease := mustAcquireRateLimitProbe(t, registry, 1)
	registry.MarkRateLimitProbeSucceeded(1, firstLease)

	registry.MarkRateLimited(1, time.Now().Add(time.Minute), "第二轮 429")
	mustAcquireRateLimitProbe(t, registry, 1)
}

func TestStaleRateLimitProbeLeaseCannotSettleNewProbe(t *testing.T) {
	registry := newRateLimitProbeRegistry(t, StateRateLimited, cloneTimePointer(time.Now().Add(time.Minute)))
	oldLease := mustAcquireRateLimitProbe(t, registry, 1)

	// 模拟管理员恢复后账号再次进入限流，并由另一个请求取得新租约。
	registry.MarkActive(1)
	registry.MarkRateLimited(1, time.Now().Add(time.Minute), "新一轮 429")
	newLease := mustAcquireRateLimitProbe(t, registry, 1)
	if oldLease == newLease {
		t.Fatal("不同代际不应复用同一探测租约")
	}

	registry.MarkRateLimitProbeSucceeded(1, oldLease)
	if got := registry.MarkRateLimitProbeFailed(1, oldLease, 0, "旧探测失败"); !got.IsZero() {
		t.Fatalf("旧租约不应写入失败冷却：%v", got)
	}
	registry.CancelRateLimitProbe(1, oldLease)

	snap, _ := registry.Snapshot(1)
	if snap.State != StateRateLimited || !snap.rateLimitProbeInFlight || snap.rateLimitProbeLease != newLease || snap.rateLimitProbeFailures != 0 {
		t.Fatalf("旧租约误结算了新探测：%+v", snap)
	}
	registry.MarkRateLimitProbeSucceeded(1, newLease)
	snap, _ = registry.Snapshot(1)
	if snap.State != StateActive || !snap.rateLimitProbeClean() {
		t.Fatalf("新租约未能正常恢复账号：%+v", snap)
	}
}

func TestReloadPreservesRateLimitProbeGate(t *testing.T) {
	until := time.Now().Add(5 * time.Second)
	loader := &registryLoaderStub{items: []Snapshot{rateLimitProbeSnapshot(StateRateLimited, &until)}}
	registry := New(loader, nil)
	if err := registry.Reload(context.Background()); err != nil {
		t.Fatalf("加载注册表失败: %v", err)
	}
	lease := mustAcquireRateLimitProbe(t, registry, 1)
	blockUntil := registry.MarkRateLimitProbeFailed(1, lease, time.Second, "仍然 429")

	if err := registry.Reload(context.Background()); err != nil {
		t.Fatalf("重载注册表失败: %v", err)
	}
	snap, _ := registry.Snapshot(1)
	if snap.rateLimitProbeFailures != 1 || !snap.rateLimitProbeBlockUntil.Equal(blockUntil) {
		t.Fatalf("Reload 未保留探测门闩：%+v", snap)
	}
	if got, _ := registry.BeginRateLimitProbe(1); got != RateLimitProbeBlocked {
		t.Fatalf("Reload 后探测决策 = %v，期望 Blocked", got)
	}

	loader.items[0].State = StateActive
	loader.items[0].StateUntil = nil
	if err := registry.Reload(context.Background()); err != nil {
		t.Fatalf("恢复 active 后重载失败: %v", err)
	}
	snap, _ = registry.Snapshot(1)
	if snap.State != StateActive || !snap.rateLimitProbeClean() {
		t.Fatalf("离开限流后未清门闩：%+v", snap)
	}
}

func TestReloadPreservesStateChangedDuringLoad(t *testing.T) {
	loader := newBlockingRegistryLoader(rateLimitProbeSnapshot(StateActive, nil))
	registry := New(loader, nil)
	if err := registry.Reload(context.Background()); err != nil {
		t.Fatalf("初次加载注册表失败: %v", err)
	}

	started, release := loader.arm(rateLimitProbeSnapshot(StateActive, nil))
	done := make(chan error, 1)
	go func() { done <- registry.Reload(context.Background()) }()
	<-started

	until := time.Now().Add(time.Minute)
	registry.MarkRateLimited(1, until, "加载期间返回 429")
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("并发重载失败: %v", err)
	}

	snap, _ := registry.Snapshot(1)
	if snap.State != StateRateLimited || snap.StateUntil == nil || !snap.StateUntil.Equal(until) || snap.ErrorMsg != "加载期间返回 429" {
		t.Fatalf("Reload 用旧快照覆盖了并发新状态：%+v", snap)
	}
}

func TestReloadAppliesLoadedStateAfterProbeClaimWasReturned(t *testing.T) {
	initialUntil := time.Now().Add(time.Minute)
	loader := newBlockingRegistryLoader(rateLimitProbeSnapshot(StateRateLimited, &initialUntil))
	registry := New(loader, nil)
	if err := registry.Reload(context.Background()); err != nil {
		t.Fatalf("初次加载注册表失败: %v", err)
	}

	started, release := loader.arm(rateLimitProbeSnapshot(StateDisabled, nil))
	done := make(chan error, 1)
	go func() { done <- registry.Reload(context.Background()) }()
	<-started

	lease := mustAcquireRateLimitProbe(t, registry, 1)
	registry.CancelRateLimitProbe(1, lease)
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("并发重载失败: %v", err)
	}

	snap, _ := registry.Snapshot(1)
	if snap.State != StateDisabled || !snap.rateLimitProbeClean() {
		t.Fatalf("已归还租约不应吞掉加载到的 disabled 状态：%+v", snap)
	}
}

func TestReloadExtendsProbeGateToLaterLoadedStateUntil(t *testing.T) {
	initialUntil := time.Now().Add(5 * time.Second)
	loader := &registryLoaderStub{items: []Snapshot{rateLimitProbeSnapshot(StateRateLimited, &initialUntil)}}
	registry := New(loader, nil)
	if err := registry.Reload(context.Background()); err != nil {
		t.Fatalf("加载注册表失败: %v", err)
	}
	lease := mustAcquireRateLimitProbe(t, registry, 1)
	registry.MarkRateLimitProbeFailed(1, lease, time.Second, "仍然 429")

	laterUntil := time.Now().Add(10 * time.Minute)
	loader.items[0].StateUntil = &laterUntil
	if err := registry.Reload(context.Background()); err != nil {
		t.Fatalf("重载注册表失败: %v", err)
	}
	snap, _ := registry.Snapshot(1)
	if snap.StateUntil == nil || !snap.StateUntil.Equal(laterUntil) || !snap.rateLimitProbeBlockUntil.Equal(laterUntil) {
		t.Fatalf("Reload 未合并更晚的限流截止时间：%+v", snap)
	}
	if got, _ := registry.BeginRateLimitProbe(1); got != RateLimitProbeBlocked {
		t.Fatalf("更晚截止时间内探测决策 = %v，期望 Blocked", got)
	}
}

func newRateLimitProbeRegistry(t *testing.T, state string, until *time.Time) *Registry {
	t.Helper()
	registry := New(registryLoaderStub{items: []Snapshot{rateLimitProbeSnapshot(state, until)}}, nil)
	if err := registry.Reload(context.Background()); err != nil {
		t.Fatalf("加载注册表失败: %v", err)
	}
	return registry
}

func mustAcquireRateLimitProbe(t *testing.T, registry *Registry, accountID int) RateLimitProbeLease {
	t.Helper()
	decision, lease := registry.BeginRateLimitProbe(accountID)
	if decision != RateLimitProbeAcquired || lease == 0 {
		t.Fatalf("账号 %d 探测决策/租约 = %v/%d，期望 Acquired/非零", accountID, decision, lease)
	}
	return lease
}

func cloneTimePointer(value time.Time) *time.Time {
	return &value
}

type blockingRegistryLoader struct {
	mu      sync.Mutex
	items   []Snapshot
	block   bool
	started chan struct{}
	release chan struct{}
}

func newBlockingRegistryLoader(items ...Snapshot) *blockingRegistryLoader {
	return &blockingRegistryLoader{items: items}
}

func (l *blockingRegistryLoader) arm(items ...Snapshot) (<-chan struct{}, chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = items
	l.block = true
	l.started = make(chan struct{})
	l.release = make(chan struct{})
	return l.started, l.release
}

func (l *blockingRegistryLoader) LoadAllForAccountRegistry(ctx context.Context) ([]Snapshot, error) {
	l.mu.Lock()
	items := append([]Snapshot(nil), l.items...)
	block := l.block
	started := l.started
	release := l.release
	if block {
		l.block = false
	}
	l.mu.Unlock()

	if block {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return items, nil
}

func rateLimitProbeSnapshot(state string, until *time.Time) Snapshot {
	return Snapshot{
		ID: 1, State: state, StateUntil: until, Platform: "codex", Type: "oauth",
		Models: map[string]struct{}{"gpt-test": {}}, GroupIDs: map[int]struct{}{7: {}},
	}
}

func assertRateLimitProbeHidden(t *testing.T, registry *Registry) {
	t.Helper()
	if got := snapshotIDs(registry.ListCandidates(7, "gpt-test", nil)); len(got) != 0 {
		t.Errorf("普通候选仍包含门闩账号：%v", got)
	}
	if got := snapshotIDs(registry.ListRelayHookCandidates(7, "gpt-test")); len(got) != 0 {
		t.Errorf("Hook 候选仍包含门闩账号：%v", got)
	}
	if got := snapshotIDs(registry.ListCandidatesAllowRateLimited(7, "gpt-test", nil, []int{1})); len(got) != 0 {
		t.Errorf("显式 allow 仍绕过门闩：%v", got)
	}
}
