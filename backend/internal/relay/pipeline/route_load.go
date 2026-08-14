package pipeline

import (
	"context"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const (
	accountLoadProbeLimit       = 8
	accountLatencyMinSamples    = 3
	accountLatencyFreshDuration = 15 * time.Minute
	accountLatencyEWMAWeight    = 8
	accountLatencySwitchRatio   = 0.95
	accountLatencyWarmupMaxRows = 20_000
)

// AccountFirstTokenSample 是账号×模型一次成功流的历史首字样本。
type AccountFirstTokenSample struct {
	AccountID    int
	Model        string
	FirstTokenMs int64
	CreatedAt    time.Time
}

// AccountFirstTokenSource 加载近期成功首字样本。
// 实现方应优先返回时间窗内最新的有限行，避免启动预热扫描完整使用记录表。
type AccountFirstTokenSource interface {
	LoadRecentAccountFirstTokenSamples(ctx context.Context, since time.Time, limit int) ([]AccountFirstTokenSample, error)
}

type accountLatencyKey struct {
	accountID int
	model     string
}

type accountLatencyState struct {
	ewmaMs    atomic.Int64
	samples   atomic.Uint64
	updatedAt atomic.Int64
}

type accountLatencySeed struct {
	ewmaMs    int64
	samples   uint64
	updatedAt int64
}

// accountInflightCounter 返回账号的本实例在途计数器。
// 账号集合规模有明确上限，计数器保留复用可避免热路径反复分配与删除竞态。
func (p *Pipeline) accountInflightCounter(accountID int) *atomic.Int64 {
	if p == nil || accountID <= 0 {
		return nil
	}
	if value, ok := p.accountInflight.Load(accountID); ok {
		return value.(*atomic.Int64)
	}
	value, _ := p.accountInflight.LoadOrStore(accountID, &atomic.Int64{})
	return value.(*atomic.Int64)
}

func (p *Pipeline) accountInflightCount(accountID int) int64 {
	if p == nil || accountID <= 0 {
		return 0
	}
	value, ok := p.accountInflight.Load(accountID)
	if !ok {
		return 0
	}
	return max(value.(*atomic.Int64).Load(), 0)
}

// trackAccountAttempt 在真实 CPA attempt 生命周期内维护本实例账号在途量。
func (p *Pipeline) trackAccountAttempt(accountID int) func() {
	counter := p.accountInflightCounter(accountID)
	if counter == nil {
		return func() {}
	}
	counter.Add(1)
	var released atomic.Bool
	return func() {
		if !released.CompareAndSwap(false, true) {
			return
		}
		// 正常路径严格成对；CAS 兜底避免未来调用方误释放导致负数污染调度。
		for {
			current := counter.Load()
			if current <= 0 || counter.CompareAndSwap(current, current-1) {
				return
			}
		}
	}
}

// recordAccountFirstToken 记录成功完成流的账号×模型首字 EWMA。
// 不记录失败和中断，因此不会形成短时冷却；异常账号仍由现有 failover 处理。
func (p *Pipeline) recordAccountFirstToken(accountID int, model string, firstTokenMs int64) {
	p.recordAccountFirstTokenAt(accountID, model, firstTokenMs, time.Now())
}

func (p *Pipeline) recordAccountFirstTokenAt(accountID int, model string, firstTokenMs int64, recordedAt time.Time) {
	model = strings.TrimSpace(model)
	if p == nil || accountID <= 0 || model == "" || firstTokenMs <= 0 || recordedAt.IsZero() {
		return
	}
	key := accountLatencyKey{accountID: accountID, model: model}
	value, _ := p.accountFirstToken.LoadOrStore(key, &accountLatencyState{})
	state := value.(*accountLatencyState)
	for {
		current := state.ewmaMs.Load()
		next := firstTokenMs
		if current > 0 {
			next = (current*(accountLatencyEWMAWeight-1) + firstTokenMs) / accountLatencyEWMAWeight
		}
		if state.ewmaMs.CompareAndSwap(current, next) {
			break
		}
	}
	state.samples.Add(1)
	storeLatestUnixNano(&state.updatedAt, recordedAt.UnixNano())
}

func storeLatestUnixNano(target *atomic.Int64, candidate int64) {
	for {
		current := target.Load()
		if candidate <= current || target.CompareAndSwap(current, candidate) {
			return
		}
	}
}

// WarmAccountFirstTokens 从近期成功记录恢复本实例 EWMA，消除服务重启后的冷启动探索期。
// 已被实时请求写入的 key 不覆盖，避免预热查询较慢时旧样本倒灌到新状态。
func (p *Pipeline) WarmAccountFirstTokens(ctx context.Context) (int, error) {
	if p == nil || p.accountFirstTokenSource == nil {
		return 0, nil
	}
	now := time.Now()
	since := now.Add(-accountLatencyFreshDuration)
	samples, err := p.accountFirstTokenSource.LoadRecentAccountFirstTokenSamples(ctx, since, accountLatencyWarmupMaxRows)
	if err != nil {
		return 0, err
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].CreatedAt.Before(samples[j].CreatedAt) })

	seeds := make(map[accountLatencyKey]accountLatencySeed, len(samples))
	for _, sample := range samples {
		model := strings.TrimSpace(sample.Model)
		if sample.AccountID <= 0 || model == "" || sample.FirstTokenMs <= 0 || sample.CreatedAt.Before(since) || sample.CreatedAt.After(now) {
			continue
		}
		key := accountLatencyKey{accountID: sample.AccountID, model: model}
		seed := seeds[key]
		next := sample.FirstTokenMs
		if seed.ewmaMs > 0 {
			next = (seed.ewmaMs*(accountLatencyEWMAWeight-1) + sample.FirstTokenMs) / accountLatencyEWMAWeight
		}
		seed.ewmaMs = next
		seed.samples++
		seed.updatedAt = max(seed.updatedAt, sample.CreatedAt.UnixNano())
		seeds[key] = seed
	}

	warmed := 0
	for key, seed := range seeds {
		state := &accountLatencyState{}
		state.ewmaMs.Store(seed.ewmaMs)
		state.samples.Store(seed.samples)
		state.updatedAt.Store(seed.updatedAt)
		if _, loaded := p.accountFirstToken.LoadOrStore(key, state); !loaded {
			warmed++
		}
	}
	return warmed, nil
}

func (p *Pipeline) recentAccountFirstToken(accountID int, model string, now time.Time) (int64, bool) {
	if p == nil || accountID <= 0 || strings.TrimSpace(model) == "" {
		return 0, false
	}
	value, ok := p.accountFirstToken.Load(accountLatencyKey{accountID: accountID, model: model})
	if !ok {
		return 0, false
	}
	state := value.(*accountLatencyState)
	if state.samples.Load() < accountLatencyMinSamples {
		return 0, false
	}
	updatedAt := state.updatedAt.Load()
	if updatedAt <= 0 || now.Sub(time.Unix(0, updatedAt)) > accountLatencyFreshDuration {
		return 0, false
	}
	latency := state.ewmaMs.Load()
	return latency, latency > 0
}

// preferAccountCandidate 在两个同优先级账号之间比较预计首字完成时间。
// 有近期样本时使用 (在途+1)×EWMA/权重；样本不足时回退到加权最小在途。
// 5% 切换迟滞避免相近账号因微小抖动频繁互抢，同时保留加权轮询的探索流量。
func (p *Pipeline) preferAccountCandidate(current, candidate routeTarget, model string, now time.Time) routeTarget {
	if current.kind != routeAccount || candidate.kind != routeAccount ||
		current.account == nil || candidate.account == nil {
		return current
	}
	currentLoad := p.accountInflightCount(current.account.ID)
	candidateLoad := p.accountInflightCount(candidate.account.ID)
	currentWeight := float64(effectiveRouteWeight(current.weight))
	candidateWeight := float64(effectiveRouteWeight(candidate.weight))
	currentLatency, currentOK := p.recentAccountFirstToken(current.account.ID, model, now)
	candidateLatency, candidateOK := p.recentAccountFirstToken(candidate.account.ID, model, now)
	if currentOK && candidateOK {
		currentScore := float64(currentLoad+1) * float64(currentLatency) / currentWeight
		candidateScore := float64(candidateLoad+1) * float64(candidateLatency) / candidateWeight
		if candidateScore < currentScore*accountLatencySwitchRatio {
			return candidate
		}
		return current
	}
	if currentLoad == 0 {
		return current
	}
	currentScore := float64(currentLoad+1) / currentWeight
	candidateScore := float64(candidateLoad+1) / candidateWeight
	if candidateScore < currentScore {
		return candidate
	}
	return current
}
