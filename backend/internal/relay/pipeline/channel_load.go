package pipeline

import (
	"context"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const (
	channelLoadProbeLimit          = 8
	channelLatencyMinSamples       = 5
	channelLatencyFreshDuration    = 15 * time.Minute
	channelLatencyEWMAWeight       = 8
	channelLatencySwitchRatio      = 0.9
	channelLatencyWarmupMaxRows    = 20_000
	channelSlowFailureThreshold    = 8 * time.Second
	channelSlowFailureDecay        = 3 * time.Minute
	channelSlowFailureMaxLevel     = 3
	channelSlowFailureFactorLevel1 = 0.5
	channelSlowFailureFactorLevel2 = 0.25
	channelSlowFailureFactorLevel3 = 0.1
)

// ChannelLatencyConfig 是通用渠道真实 TTFT 与慢失败调度参数。
type ChannelLatencyConfig struct {
	Enabled              bool
	MinSamples           int
	FreshDuration        time.Duration
	EWMAWeight           int
	ProbeLimit           int
	SwitchRatio          float64
	SlowFailureThreshold time.Duration
	SlowFailureDecay     time.Duration
}

func defaultChannelLatencyConfig() ChannelLatencyConfig {
	return ChannelLatencyConfig{
		Enabled:              true,
		MinSamples:           channelLatencyMinSamples,
		FreshDuration:        channelLatencyFreshDuration,
		EWMAWeight:           channelLatencyEWMAWeight,
		ProbeLimit:           channelLoadProbeLimit,
		SwitchRatio:          channelLatencySwitchRatio,
		SlowFailureThreshold: channelSlowFailureThreshold,
		SlowFailureDecay:     channelSlowFailureDecay,
	}
}

func normalizeChannelLatencyConfig(config ChannelLatencyConfig) ChannelLatencyConfig {
	if config.MinSamples <= 0 {
		config.MinSamples = channelLatencyMinSamples
	}
	if config.FreshDuration <= 0 {
		config.FreshDuration = channelLatencyFreshDuration
	}
	if config.EWMAWeight < 2 {
		config.EWMAWeight = channelLatencyEWMAWeight
	}
	if config.ProbeLimit <= 0 {
		config.ProbeLimit = channelLoadProbeLimit
	}
	if config.SwitchRatio <= 0 || config.SwitchRatio > 1 {
		config.SwitchRatio = channelLatencySwitchRatio
	}
	if config.SlowFailureThreshold <= 0 {
		config.SlowFailureThreshold = channelSlowFailureThreshold
	}
	if config.SlowFailureDecay <= 0 {
		config.SlowFailureDecay = channelSlowFailureDecay
	}
	return config
}

// ChannelFirstTokenSample 是渠道密钥×模型一次成功流的历史首字样本。
type ChannelFirstTokenSample struct {
	ChannelKeyID int
	Model        string
	FirstTokenMs int64
	CreatedAt    time.Time
}

// ChannelFirstTokenSource 加载近期成功渠道首字样本。
// 实现方应优先返回时间窗内最新的有限行，避免启动预热扫描完整使用记录表。
type ChannelFirstTokenSource interface {
	LoadRecentChannelFirstTokenSamples(ctx context.Context, since time.Time, limit int) ([]ChannelFirstTokenSample, error)
}

type channelLatencyKey struct {
	channelKeyID int
	model        string
}

type channelLatencyState struct {
	ewmaMs    atomic.Int64
	samples   atomic.Uint64
	updatedAt atomic.Int64
	// slowFailure 低两位保存等级，高位保存最后一次状态更新时间的 Unix 毫秒。
	slowFailure atomic.Uint64
}

type channelLatencySeed struct {
	ewmaMs    int64
	samples   uint64
	updatedAt time.Time
}

type channelPerformanceStats struct {
	latencyMs     int64
	hasLatency    bool
	failureLevel  int
	failureFactor float64
}

// channelInflightCounter 返回渠道密钥的本实例在途计数器。
func (p *Pipeline) channelInflightCounter(channelKeyID int) *atomic.Int64 {
	if p == nil || channelKeyID <= 0 {
		return nil
	}
	if value, ok := p.channelInflight.Load(channelKeyID); ok {
		return value.(*atomic.Int64)
	}
	value, _ := p.channelInflight.LoadOrStore(channelKeyID, &atomic.Int64{})
	return value.(*atomic.Int64)
}

func (p *Pipeline) channelInflightCount(channelKeyID int) int64 {
	if p == nil || channelKeyID <= 0 {
		return 0
	}
	value, ok := p.channelInflight.Load(channelKeyID)
	if !ok {
		return 0
	}
	return max(value.(*atomic.Int64).Load(), 0)
}

// trackChannelAttempt 在真实渠道 attempt 生命周期内维护本实例在途量。
func (p *Pipeline) trackChannelAttempt(channelKeyID int) func() {
	counter := p.channelInflightCounter(channelKeyID)
	if counter == nil {
		return func() {}
	}
	counter.Add(1)
	var released atomic.Bool
	return func() {
		if !released.CompareAndSwap(false, true) {
			return
		}
		for {
			current := counter.Load()
			if current <= 0 || counter.CompareAndSwap(current, current-1) {
				return
			}
		}
	}
}

func (p *Pipeline) channelLatencyState(channelKeyID int, model string) (*channelLatencyState, bool) {
	model = strings.TrimSpace(model)
	if p == nil || channelKeyID <= 0 || model == "" {
		return nil, false
	}
	key := channelLatencyKey{channelKeyID: channelKeyID, model: model}
	value, _ := p.channelFirstToken.LoadOrStore(key, &channelLatencyState{})
	return value.(*channelLatencyState), true
}

// recordChannelFirstToken 记录成功完成流的单次渠道 attempt 首字 EWMA。
// 首字达到慢失败阈值时仍更新延迟，但不会抵消已有慢失败惩罚。
func (p *Pipeline) recordChannelFirstToken(channelKeyID int, model string, firstTokenMs int64) {
	p.recordChannelFirstTokenWithConfig(channelKeyID, model, firstTokenMs, time.Now(), defaultChannelLatencyConfig())
}

func (p *Pipeline) recordChannelFirstTokenWithConfig(
	channelKeyID int,
	model string,
	firstTokenMs int64,
	recordedAt time.Time,
	config ChannelLatencyConfig,
) {
	if !config.Enabled || firstTokenMs <= 0 || recordedAt.IsZero() {
		return
	}
	state, ok := p.channelLatencyState(channelKeyID, model)
	if !ok {
		return
	}
	for {
		current := state.ewmaMs.Load()
		next := firstTokenMs
		if current > 0 {
			next = (current*(int64(config.EWMAWeight)-1) + firstTokenMs) / int64(config.EWMAWeight)
		}
		if state.ewmaMs.CompareAndSwap(current, next) {
			break
		}
	}
	state.samples.Add(1)
	storeLatestUnixNano(&state.updatedAt, recordedAt.UnixNano())
	if firstTokenMs < config.SlowFailureThreshold.Milliseconds() {
		recoverChannelSlowFailure(state, recordedAt, config)
	}
}

// recordChannelFastSuccess 让没有首字样本的快速成功请求参与慢失败恢复。
// 非流式响应不与流式 TTFT 混合，只恢复失败等级。
func (p *Pipeline) recordChannelFastSuccess(channelKeyID int, model string, attemptLatencyMs int64) {
	p.recordChannelFastSuccessWithConfig(channelKeyID, model, attemptLatencyMs, defaultChannelLatencyConfig())
}

func (p *Pipeline) recordChannelFastSuccessWithConfig(
	channelKeyID int,
	model string,
	attemptLatencyMs int64,
	config ChannelLatencyConfig,
) {
	if !config.Enabled || attemptLatencyMs <= 0 || attemptLatencyMs >= config.SlowFailureThreshold.Milliseconds() {
		return
	}
	state, ok := p.channelLatencyState(channelKeyID, model)
	if !ok {
		return
	}
	now := time.Now()
	recoverChannelSlowFailure(state, now, config)
}

func recoverChannelSlowFailure(state *channelLatencyState, now time.Time, config ChannelLatencyConfig) {
	for {
		current := state.slowFailure.Load()
		level := effectiveChannelSlowFailureLevel(current, now, config)
		if level <= 0 {
			if current == 0 || state.slowFailure.CompareAndSwap(current, 0) {
				return
			}
			continue
		}
		level--
		next := uint64(0)
		if level > 0 {
			next = encodeChannelSlowFailure(level, now)
		}
		if state.slowFailure.CompareAndSwap(current, next) {
			return
		}
	}
}

// recordChannelSlowFailure 记录首字前等待过久后发生的限流、瞬态或网络失败。
func (p *Pipeline) recordChannelSlowFailure(channelKeyID int, model string, attemptLatencyMs int64) {
	p.recordChannelSlowFailureWithConfig(channelKeyID, model, attemptLatencyMs, defaultChannelLatencyConfig())
}

func (p *Pipeline) recordChannelSlowFailureWithConfig(
	channelKeyID int,
	model string,
	attemptLatencyMs int64,
	config ChannelLatencyConfig,
) {
	if !config.Enabled || attemptLatencyMs <= 0 || attemptLatencyMs < config.SlowFailureThreshold.Milliseconds() {
		return
	}
	state, ok := p.channelLatencyState(channelKeyID, model)
	if !ok {
		return
	}
	now := time.Now()
	for {
		current := state.slowFailure.Load()
		level := min(effectiveChannelSlowFailureLevel(current, now, config)+1, channelSlowFailureMaxLevel)
		if state.slowFailure.CompareAndSwap(current, encodeChannelSlowFailure(level, now)) {
			return
		}
	}
}

func encodeChannelSlowFailure(level int, updatedAt time.Time) uint64 {
	if level <= 0 || updatedAt.IsZero() {
		return 0
	}
	return uint64(updatedAt.UnixMilli())<<2 | uint64(min(level, channelSlowFailureMaxLevel))
}

func effectiveChannelSlowFailureLevel(encoded uint64, now time.Time, config ChannelLatencyConfig) int {
	level := int(encoded & 0b11)
	if level <= 0 || encoded>>2 == 0 {
		return 0
	}
	updatedAtMs := int64(encoded >> 2)
	elapsedMs := now.UnixMilli() - updatedAtMs
	decayMs := max(config.SlowFailureDecay.Milliseconds(), int64(1))
	if elapsedMs <= 0 {
		return level
	}
	return max(level-int(elapsedMs/decayMs), 0)
}

func channelSlowFailureFactor(level int) float64 {
	switch {
	case level >= 3:
		return channelSlowFailureFactorLevel3
	case level == 2:
		return channelSlowFailureFactorLevel2
	case level == 1:
		return channelSlowFailureFactorLevel1
	default:
		return 1
	}
}

func (p *Pipeline) recentChannelPerformance(channelKeyID int, model string, now time.Time) channelPerformanceStats {
	return p.recentChannelPerformanceWithConfig(channelKeyID, model, now, defaultChannelLatencyConfig())
}

func (p *Pipeline) recentChannelPerformanceWithConfig(
	channelKeyID int,
	model string,
	now time.Time,
	config ChannelLatencyConfig,
) channelPerformanceStats {
	if !config.Enabled || p == nil || channelKeyID <= 0 || strings.TrimSpace(model) == "" {
		return channelPerformanceStats{failureFactor: 1}
	}
	value, ok := p.channelFirstToken.Load(channelLatencyKey{channelKeyID: channelKeyID, model: model})
	if !ok {
		return channelPerformanceStats{failureFactor: 1}
	}
	state := value.(*channelLatencyState)
	failureLevel := effectiveChannelSlowFailureLevel(state.slowFailure.Load(), now, config)
	stats := channelPerformanceStats{
		failureLevel:  failureLevel,
		failureFactor: channelSlowFailureFactor(failureLevel),
	}
	samples := state.samples.Load()
	latencyMs := state.ewmaMs.Load()
	updatedAt := state.updatedAt.Load()
	if samples >= uint64(config.MinSamples) && latencyMs > 0 && updatedAt > 0 &&
		now.Sub(time.Unix(0, updatedAt)) <= config.FreshDuration {
		stats.latencyMs = latencyMs
		stats.hasLatency = true
	}
	return stats
}

// preferChannelCandidate 在两个同优先级渠道之间比较预计首字完成时间。
// 有有效样本时使用 (在途+1)×EWMA/(权重×慢失败因子)；冷启动时只在存在
// 在途或慢失败惩罚时调整静态加权初选，保留目录调度的探索能力。
func (p *Pipeline) preferChannelCandidateWithConfig(
	current, candidate routeTarget,
	model string,
	now time.Time,
	config ChannelLatencyConfig,
) routeTarget {
	if !config.Enabled {
		return current
	}
	if current.kind != routeChannel || candidate.kind != routeChannel ||
		current.channel == nil || candidate.channel == nil || current.priority != candidate.priority {
		return current
	}
	currentLoad := p.channelInflightCount(current.channel.KeyID)
	candidateLoad := p.channelInflightCount(candidate.channel.KeyID)
	currentStats := p.recentChannelPerformanceWithConfig(current.channel.KeyID, model, now, config)
	candidateStats := p.recentChannelPerformanceWithConfig(candidate.channel.KeyID, model, now, config)
	currentCapacity := float64(effectiveRouteWeight(current.weight)) * currentStats.failureFactor
	candidateCapacity := float64(effectiveRouteWeight(candidate.weight)) * candidateStats.failureFactor

	if currentStats.hasLatency && candidateStats.hasLatency {
		currentScore := float64(currentLoad+1) * float64(currentStats.latencyMs) / currentCapacity
		candidateScore := float64(candidateLoad+1) * float64(candidateStats.latencyMs) / candidateCapacity
		if candidateScore < currentScore*config.SwitchRatio {
			return candidate
		}
		return current
	}
	if currentLoad == 0 && candidateLoad == 0 && currentStats.failureLevel == 0 && candidateStats.failureLevel == 0 {
		return current
	}
	currentScore := float64(currentLoad+1) / currentCapacity
	candidateScore := float64(candidateLoad+1) / candidateCapacity
	if candidateScore < currentScore*config.SwitchRatio {
		return candidate
	}
	return current
}

// WarmChannelFirstTokens 从近期成功记录恢复本实例渠道首字 EWMA。
func (p *Pipeline) WarmChannelFirstTokens(ctx context.Context) (int, error) {
	if p == nil || p.channelFirstTokenSource == nil {
		return 0, nil
	}
	config := defaultChannelLatencyConfig()
	if p.settings != nil {
		config = p.settings.Get(ctx).ChannelLatency
	}
	if !config.Enabled {
		return 0, nil
	}
	now := time.Now()
	since := now.Add(-config.FreshDuration)
	samples, err := p.channelFirstTokenSource.LoadRecentChannelFirstTokenSamples(ctx, since, channelLatencyWarmupMaxRows)
	if err != nil {
		return 0, err
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].CreatedAt.Before(samples[j].CreatedAt) })

	seeds := make(map[channelLatencyKey]channelLatencySeed, len(samples))
	for _, sample := range samples {
		model := strings.TrimSpace(sample.Model)
		if sample.ChannelKeyID <= 0 || model == "" || sample.FirstTokenMs <= 0 ||
			sample.CreatedAt.Before(since) || sample.CreatedAt.After(now) {
			continue
		}
		key := channelLatencyKey{channelKeyID: sample.ChannelKeyID, model: model}
		seed := seeds[key]
		if seed.ewmaMs <= 0 {
			seed.ewmaMs = sample.FirstTokenMs
		} else {
			seed.ewmaMs = (seed.ewmaMs*(int64(config.EWMAWeight)-1) + sample.FirstTokenMs) / int64(config.EWMAWeight)
		}
		seed.samples++
		if sample.CreatedAt.After(seed.updatedAt) {
			seed.updatedAt = sample.CreatedAt
		}
		seeds[key] = seed
	}

	warmed := 0
	for key, seed := range seeds {
		state := &channelLatencyState{}
		state.ewmaMs.Store(seed.ewmaMs)
		state.samples.Store(seed.samples)
		state.updatedAt.Store(seed.updatedAt.UnixNano())
		if _, loaded := p.channelFirstToken.LoadOrStore(key, state); !loaded {
			warmed++
		}
	}
	return warmed, nil
}
