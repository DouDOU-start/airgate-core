package probe

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Engine 健康探针引擎：后台定时探测 + 余额同步 + 转发管线健康信号接收。
//
// 转发管线（forward.go）经 HealthTracker 接口喂健康信号，Engine 驱动状态机
// 并同步更新内存注册表 + 异步落库。后台 goroutine 负责：
//   - 探针调度：对 probe_enabled 且处于 suspended/recovering 的 key 定期发探测请求
//   - 余额同步：对 balance_check_enabled 且余额过期的 key 定期刷新余额
type Engine struct {
	store        Store
	tester       Tester
	balStore     BalanceStore
	balSyncer    BalanceSyncer
	registry     RegistryMutator
	bilProber    BillingProber
	bilStore     BillingProbeStore

	// mu 保护 states 内存映射（比 store 查 DB 快得多，转发热路径用）。
	mu     sync.RWMutex
	states map[int]*keyState
}

// keyState 内存中的 key 健康状态。
type keyState struct {
	health    HealthStatus
	failures  int
	successes int
}

// New 创建探针引擎。所有依赖均为可选（nil 安全），缺失时对应功能静默禁用。
func New(store Store, tester Tester, balStore BalanceStore, balSyncer BalanceSyncer, registry RegistryMutator) *Engine {
	return &Engine{
		store:     store,
		tester:    tester,
		balStore:  balStore,
		balSyncer: balSyncer,
		registry:  registry,
		states:    make(map[int]*keyState),
	}
}

// SetBillingProbe 注入上游倍率探测依赖（可选，nil 则倍率探测禁用）。
func (e *Engine) SetBillingProbe(prober BillingProber, store BillingProbeStore) {
	e.bilProber = prober
	e.bilStore = store
}

// LoadStates 从 DB 加载全量健康状态到内存（启动时调用一次）。
func (e *Engine) LoadStates(ctx context.Context) {
	if e.store == nil {
		return
	}
	targets, err := e.store.ListProbeTargets(ctx)
	if err != nil {
		slog.Warn("probe_load_states_failed", "error", err)
		return
	}
	e.mu.Lock()
	for _, t := range targets {
		e.states[t.KeyID] = &keyState{
			health:    t.HealthStatus,
			failures:  t.ConsecutiveFailures,
			successes: t.ConsecutiveSuccesses,
		}
	}
	e.mu.Unlock()
}

// StartBackground 拉起探针调度、余额同步和倍率探测后台 goroutine。
func (e *Engine) StartBackground(ctx context.Context) {
	go e.probeLoop(ctx)
	go e.balanceSyncLoop(ctx)
	go e.billingProbeLoop(ctx)
}

// RecordSuccess 转发成功时调用：驱动状态机并同步更新。
func (e *Engine) RecordSuccess(keyID int) {
	e.mu.Lock()
	st := e.getOrCreate(keyID)
	if st.health == HealthHealthy && st.failures == 0 {
		e.mu.Unlock()
		return
	}
	tr := OnSuccess(st.health, st.failures, st.successes)
	st.health = tr.NewHealth
	st.failures = tr.Failures
	st.successes = tr.Successes
	e.mu.Unlock()

	e.applyAction(keyID, tr)
}

// RecordFailure 瞬态失败（5xx/网络/限流）时调用。
func (e *Engine) RecordFailure(keyID int) {
	e.mu.Lock()
	st := e.getOrCreate(keyID)
	tr := OnFailure(st.health, st.failures, st.successes)
	st.health = tr.NewHealth
	st.failures = tr.Failures
	st.successes = tr.Successes
	e.mu.Unlock()

	e.applyAction(keyID, tr)
}

// RecordAuthFailure 鉴权失败（401/403）时调用：直接暂停。
func (e *Engine) RecordAuthFailure(keyID int) {
	e.mu.Lock()
	st := e.getOrCreate(keyID)
	tr := OnAuthFailure(st.health)
	st.health = tr.NewHealth
	st.failures = tr.Failures
	st.successes = tr.Successes
	e.mu.Unlock()

	e.applyAction(keyID, tr)
}

// getOrCreate 内存中获取或初始化 key 状态（调用方持锁）。
func (e *Engine) getOrCreate(keyID int) *keyState {
	st, ok := e.states[keyID]
	if !ok {
		st = &keyState{health: HealthHealthy}
		e.states[keyID] = st
	}
	return st
}

// applyAction 将状态机输出的动作应用到注册表（内存）和持久层（异步）。
func (e *Engine) applyAction(keyID int, tr Transition) {
	switch tr.Action {
	case ActionNone:
		return
	case ActionSuspend:
		if e.registry != nil {
			e.registry.MarkAutoDisabled(keyID, "health probe: suspended")
			e.registry.UpdateHealth(keyID, string(HealthSuspended))
		}
	case ActionRecover:
		if e.registry != nil {
			e.registry.MarkRecovered(keyID)
			e.registry.UpdateHealth(keyID, string(HealthHealthy))
		}
	case ActionUpdateHealth:
		if e.registry != nil {
			e.registry.UpdateHealth(keyID, string(tr.NewHealth))
		}
	}

	// 异步落库
	if e.store != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := e.store.UpdateHealthState(ctx, keyID, string(tr.NewHealth), tr.Failures, tr.Successes); err != nil {
				slog.Error("probe_persist_health_failed", "channel_key_id", keyID, "error", err)
			}
		}()
	}
}

// probeLoop 探针调度主循环。启动时立即执行一轮，之后每 60 秒一轮。
func (e *Engine) probeLoop(ctx context.Context) {
	if e.store == nil || e.tester == nil {
		return
	}

	e.runProbes(ctx)

	ticker := time.NewTicker(DefaultProbeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.runProbes(ctx)
		}
	}
}

// runProbes 一轮探针执行。
func (e *Engine) runProbes(ctx context.Context) {
	targets, err := e.store.ListProbeTargets(ctx)
	if err != nil {
		slog.Warn("probe_list_targets_failed", "error", err)
		return
	}

	now := time.Now()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5) // 最多 5 并发探测

	for _, t := range targets {
		if t.HealthStatus != HealthSuspended && t.HealthStatus != HealthRecovering {
			continue
		}
		if t.LastProbeAt != nil && now.Sub(*t.LastProbeAt) < DefaultProbeKeyInterval {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(target KeyHealthState) {
			defer func() { <-sem; wg.Done() }()

			probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			err := e.tester.TestKey(probeCtx, target.KeyID)

			// 更新探测时间
			if e.store != nil {
				_ = e.store.UpdateProbeTime(context.Background(), target.KeyID, time.Now())
			}

			if err != nil {
				slog.Info("probe_failed", "channel_key_id", target.KeyID, "error", err)
				e.RecordFailure(target.KeyID)
			} else {
				slog.Info("probe_succeeded", "channel_key_id", target.KeyID)

				// 探针成功 → 驱动状态机
				e.mu.Lock()
				st := e.getOrCreate(target.KeyID)
				tr := OnSuccess(st.health, st.failures, st.successes)
				st.health = tr.NewHealth
				st.failures = tr.Failures
				st.successes = tr.Successes
				e.mu.Unlock()

				e.applyAction(target.KeyID, tr)
			}
		}(t)
	}
	wg.Wait()
}

// balanceSyncLoop 余额同步主循环。启动时立即执行一轮，之后每 5 分钟一轮。
func (e *Engine) balanceSyncLoop(ctx context.Context) {
	if e.balStore == nil || e.balSyncer == nil {
		return
	}

	e.runBalanceSync(ctx)

	ticker := time.NewTicker(DefaultBalanceSyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.runBalanceSync(ctx)
		}
	}
}

// billingProbeLoop 上游倍率探测主循环。启动时立即执行一轮，之后每 30 分钟一轮。
func (e *Engine) billingProbeLoop(ctx context.Context) {
	if e.bilProber == nil || e.bilStore == nil {
		return
	}

	e.runBillingProbes(ctx)

	ticker := time.NewTicker(DefaultBillingProbeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.runBillingProbes(ctx)
		}
	}
}

// runBillingProbes 一轮上游倍率探测。
func (e *Engine) runBillingProbes(ctx context.Context) {
	targets, err := e.bilStore.ListBillingProbeTargets(ctx)
	if err != nil {
		slog.Warn("billing_probe_list_targets_failed", "error", err)
		return
	}
	if len(targets) == 0 {
		return
	}

	slog.Info("billing_probe_start", "count", len(targets))

	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)

	for _, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(target BillingProbeTarget) {
			defer func() { <-sem; wg.Done() }()

			probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			result, err := e.bilProber.ProbeBilling(probeCtx, target.KeyID)
			if err != nil {
				slog.Warn("billing_probe_failed", "channel_key_id", target.KeyID, "error", err)
				return
			}

			now := time.Now()
			if err := e.bilStore.UpdateUpstreamRate(ctx, target.KeyID, result.RateMultiplier, now); err != nil {
				slog.Error("billing_probe_persist_failed", "channel_key_id", target.KeyID, "error", err)
			}

			slog.Info("billing_probe_ok", "channel_key_id", target.KeyID, "rate", result.RateMultiplier)
		}(t)
	}
	wg.Wait()
}

// runBalanceSync 一轮余额同步。
func (e *Engine) runBalanceSync(ctx context.Context) {
	staleBefore := time.Now().Add(-DefaultBalanceStaleDur)
	keyIDs, err := e.balStore.ListBalanceSyncTargets(ctx, staleBefore)
	if err != nil {
		slog.Warn("balance_sync_list_targets_failed", "error", err)
		return
	}
	if len(keyIDs) == 0 {
		return
	}

	slog.Info("balance_sync_start", "count", len(keyIDs))

	var wg sync.WaitGroup
	sem := make(chan struct{}, 3) // 余额同步并发上限

	for _, keyID := range keyIDs {
		wg.Add(1)
		sem <- struct{}{}
		go func(id int) {
			defer func() { <-sem; wg.Done() }()
			syncCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := e.balSyncer.SyncBalance(syncCtx, id); err != nil {
				slog.Warn("balance_sync_failed", "channel_key_id", id, "error", err)
			}
		}(keyID)
	}
	wg.Wait()
}
