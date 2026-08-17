package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/relay/relayhook"
)

type routeTestChannelFirstTokenSource struct {
	samples []ChannelFirstTokenSample
	since   time.Time
	limit   int
}

func TestDefaultChannelLatencyConfig生产默认值(t *testing.T) {
	got := defaultChannelLatencyConfig()
	if !got.Enabled || got.MinSamples != 5 || got.FreshDuration != 15*time.Minute ||
		got.EWMAWeight != 8 || got.ProbeLimit != 8 || got.SwitchRatio != 0.9 ||
		got.SlowFailureThreshold != 8*time.Second || got.SlowFailureDecay != 3*time.Minute {
		t.Fatalf("渠道性能调度默认值异常：%+v", got)
	}
}

func (s *routeTestChannelFirstTokenSource) LoadRecentChannelFirstTokenSamples(
	_ context.Context,
	since time.Time,
	limit int,
) ([]ChannelFirstTokenSample, error) {
	s.since = since
	s.limit = limit
	return append([]ChannelFirstTokenSample(nil), s.samples...), nil
}

func recordChannelSamples(p *Pipeline, channelKeyID int, model string, firstTokenMs int64) {
	for range channelLatencyMinSamples {
		p.recordChannelFirstToken(channelKeyID, model, firstTokenMs)
	}
}

func TestIndexedRoute优选同级低首字渠道(t *testing.T) {
	env := newTestEnv(t,
		testSnap(1, "http://channel-1.invalid"),
		testSnap(2, "http://channel-2.invalid"),
	)
	recordChannelSamples(env.pipe, 1, testModel, 20_000)
	recordChannelSamples(env.pipe, 2, testModel, 5_000)

	selected, ok := env.pipe.pickRoute(7, testModel, registry.ProtocolOpenAI, nil, nil, nil)
	if !ok || selected.channel == nil || selected.channel.KeyID != 2 {
		t.Fatalf("调度结果 = %+v，期望低首字渠道 2", selected)
	}
}

func TestIndexedRoute渠道首字不跨优先级(t *testing.T) {
	env := newTestEnv(t,
		testSnap(1, "http://channel-1.invalid", func(s *registry.ChannelKeySnapshot) { s.Priority = 100 }),
		testSnap(2, "http://channel-2.invalid", func(s *registry.ChannelKeySnapshot) { s.Priority = 10 }),
	)
	recordChannelSamples(env.pipe, 1, testModel, 20_000)
	recordChannelSamples(env.pipe, 2, testModel, 1_000)

	selected, ok := env.pipe.pickRoute(7, testModel, registry.ProtocolOpenAI, nil, nil, nil)
	if !ok || selected.channel == nil || selected.channel.KeyID != 1 {
		t.Fatalf("调度结果 = %+v，TTFT 不应让低优先级渠道抢占", selected)
	}
}

func TestRoutePlan回退沿用通用渠道首字评分(t *testing.T) {
	env := newTestEnv(t,
		testSnap(1, "http://channel-1.invalid"),
		testSnap(2, "http://channel-2.invalid"),
	)
	recordChannelSamples(env.pipe, 1, testModel, 20_000)
	recordChannelSamples(env.pipe, 2, testModel, 5_000)
	plan := &relayhook.RoutePlan{AccountIDs: []int{999}, Fallback: relayhook.FallbackCore}

	selected, ok := env.pipe.pickRoute(7, testModel, registry.ProtocolOpenAI, nil, nil, plan)
	if !ok || selected.channel == nil || selected.channel.KeyID != 2 {
		t.Fatalf("RoutePlan 回退调度 = %+v，期望低首字渠道 2", selected)
	}
}

func TestChannelSlowFailure降权并逐步恢复(t *testing.T) {
	env := newTestEnv(t,
		testSnap(1, "http://channel-1.invalid"),
		testSnap(2, "http://channel-2.invalid"),
	)
	for range channelSlowFailureMaxLevel {
		env.pipe.recordChannelSlowFailure(1, testModel, channelSlowFailureThreshold.Milliseconds())
	}
	stats := env.pipe.recentChannelPerformance(1, testModel, time.Now())
	if stats.failureLevel != 3 || stats.failureFactor != channelSlowFailureFactorLevel3 {
		t.Fatalf("慢失败状态 = %+v，期望三级重惩罚", stats)
	}

	selected, ok := env.pipe.pickRoute(7, testModel, registry.ProtocolOpenAI, nil, nil, nil)
	if !ok || selected.channel == nil || selected.channel.KeyID != 2 {
		t.Fatalf("慢失败降权后调度 = %+v，期望渠道 2", selected)
	}

	for wantLevel := 2; wantLevel >= 0; wantLevel-- {
		env.pipe.recordChannelFastSuccess(1, testModel, 100)
		stats = env.pipe.recentChannelPerformance(1, testModel, time.Now())
		if stats.failureLevel != wantLevel {
			t.Fatalf("快速成功恢复后等级 = %d，期望 %d", stats.failureLevel, wantLevel)
		}
	}
}

func TestChannelSlowFailure按时间衰减(t *testing.T) {
	p := &Pipeline{}
	p.recordChannelSlowFailure(1, testModel, channelSlowFailureThreshold.Milliseconds())
	state, ok := p.channelLatencyState(1, testModel)
	if !ok {
		t.Fatal("未创建渠道性能状态")
	}
	state.slowFailure.Store(encodeChannelSlowFailure(1, time.Now().Add(-channelSlowFailureDecay-time.Second)))

	stats := p.recentChannelPerformance(1, testModel, time.Now())
	if stats.failureLevel != 0 || stats.failureFactor != 1 {
		t.Fatalf("衰减后状态 = %+v，期望惩罚清除", stats)
	}
}

func TestTrackChannelAttempt释放在途量(t *testing.T) {
	p := &Pipeline{}
	release := p.trackChannelAttempt(9)
	if got := p.channelInflightCount(9); got != 1 {
		t.Fatalf("在途计数 = %d，期望 1", got)
	}
	release()
	release()
	if got := p.channelInflightCount(9); got != 0 {
		t.Fatalf("重复释放后在途计数 = %d，期望 0", got)
	}
}

func TestWarmChannelFirstTokens启动后立即生效(t *testing.T) {
	now := time.Now()
	source := &routeTestChannelFirstTokenSource{samples: []ChannelFirstTokenSample{
		{ChannelKeyID: 1, Model: testModel, FirstTokenMs: 8_000, CreatedAt: now.Add(-10 * time.Minute)},
		{ChannelKeyID: 2, Model: testModel, FirstTokenMs: 3_000, CreatedAt: now.Add(-9 * time.Minute)},
		{ChannelKeyID: 1, Model: testModel, FirstTokenMs: 8_000, CreatedAt: now.Add(-8 * time.Minute)},
		{ChannelKeyID: 2, Model: testModel, FirstTokenMs: 3_000, CreatedAt: now.Add(-7 * time.Minute)},
		{ChannelKeyID: 1, Model: testModel, FirstTokenMs: 8_000, CreatedAt: now.Add(-6 * time.Minute)},
		{ChannelKeyID: 2, Model: testModel, FirstTokenMs: 3_000, CreatedAt: now.Add(-5 * time.Minute)},
		{ChannelKeyID: 1, Model: testModel, FirstTokenMs: 8_000, CreatedAt: now.Add(-4 * time.Minute)},
		{ChannelKeyID: 2, Model: testModel, FirstTokenMs: 3_000, CreatedAt: now.Add(-3 * time.Minute)},
		{ChannelKeyID: 1, Model: testModel, FirstTokenMs: 8_000, CreatedAt: now.Add(-2 * time.Minute)},
		{ChannelKeyID: 2, Model: testModel, FirstTokenMs: 3_000, CreatedAt: now.Add(-time.Minute)},
	}}
	p := &Pipeline{channelFirstTokenSource: source}
	warmed, err := p.WarmChannelFirstTokens(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if warmed != 2 || source.limit != channelLatencyWarmupMaxRows || source.since.IsZero() {
		t.Fatalf("预热结果异常：warmed=%d limit=%d since=%v", warmed, source.limit, source.since)
	}
	first := p.recentChannelPerformance(1, testModel, time.Now())
	second := p.recentChannelPerformance(2, testModel, time.Now())
	if !first.hasLatency || !second.hasLatency || first.latencyMs <= second.latencyMs {
		t.Fatalf("预热延迟异常：渠道1=%+v 渠道2=%+v", first, second)
	}
}

func TestChannelLatency配置可关闭动态状态(t *testing.T) {
	config := defaultChannelLatencyConfig()
	config.Enabled = false
	p := &Pipeline{}
	p.recordChannelFirstTokenWithConfig(1, testModel, 3_000, time.Now(), config)
	p.recordChannelSlowFailureWithConfig(1, testModel, channelSlowFailureThreshold.Milliseconds(), config)

	stats := p.recentChannelPerformanceWithConfig(1, testModel, time.Now(), config)
	if stats.hasLatency || stats.failureLevel != 0 || stats.failureFactor != 1 {
		t.Fatalf("关闭后仍产生动态状态：%+v", stats)
	}
	if _, exists := p.channelFirstToken.Load(channelLatencyKey{channelKeyID: 1, model: testModel}); exists {
		t.Fatal("关闭后不应写入渠道性能状态")
	}
}

func TestChannelLatency运行时开关立即控制候选比较(t *testing.T) {
	p := &Pipeline{}
	recordChannelSamples(p, 1, testModel, 20_000)
	recordChannelSamples(p, 2, testModel, 2_000)
	currentChannel := testSnap(1, "http://channel-1.invalid")
	candidateChannel := testSnap(2, "http://channel-2.invalid")
	current := routeTarget{
		kind: routeChannel, priority: 100, weight: 1,
		channel: &currentChannel,
	}
	candidate := routeTarget{
		kind: routeChannel, priority: 100, weight: 1,
		channel: &candidateChannel,
	}

	enabled := defaultChannelLatencyConfig()
	if got := p.preferChannelCandidateWithConfig(current, candidate, testModel, time.Now(), enabled); got.channel.KeyID != 2 {
		t.Fatalf("开启后候选渠道 = %d，期望 2", got.channel.KeyID)
	}
	enabled.Enabled = false
	if got := p.preferChannelCandidateWithConfig(current, candidate, testModel, time.Now(), enabled); got.channel.KeyID != 1 {
		t.Fatalf("关闭后候选渠道 = %d，期望保持静态初选 1", got.channel.KeyID)
	}
}
