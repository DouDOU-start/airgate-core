// Package pipeline 实现 relay 转发主循环：
// 余额/缺价预检 → user/key 并发闸门 → failover ≤3（渠道 Pick + RPM/并发闸门 +
// adaptor 直发 + outcome 判定）→ pricing 计费 → recorder 落账。
package pipeline

import (
	"net/http"

	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/pkg/upstreamclient"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"

	// openai_compatible / custom 适配器自注册（adaptor.Register）。
	_ "github.com/DouDOU-start/airgate-core/internal/relay/adaptor/openai"
)

// UsageSink 用量落账窄接口（*billing.Recorder 天然满足；测试注入 fake）。
type UsageSink interface {
	Record(record billing.UsageRecord)
}

// Options 管线装配依赖。
type Options struct {
	Registry    *registry.Registry
	Pricing     *pricing.Cache
	Concurrency *scheduler.ConcurrencyManager
	RPM         *scheduler.RPMCounter
	Calculator  *billing.Calculator
	Sink        UsageSink
	Settings    *SettingsReader
}

// Pipeline relay 转发管线。
type Pipeline struct {
	registry    *registry.Registry
	pricing     *pricing.Cache
	concurrency *scheduler.ConcurrencyManager
	rpm         *scheduler.RPMCounter
	calculator  *billing.Calculator
	sink        UsageSink
	settings    *SettingsReader
	// client 出口 HTTP 客户端：不设总超时（流式无总超时），仅设连接/TLS 层超时；
	// 非流式的总超时由调用方经 context 施加。重定向不跟随
	//（upstreamclient.NewClient 统一设 ErrUseLastResponse），
	// 3xx 原样进入 outcome 判定按 clientError 透传终止。
	client *http.Client
}

// New 创建转发管线。
func New(opts Options) *Pipeline {
	settings := opts.Settings
	if settings == nil {
		settings = NewSettingsReader(nil)
	}
	calculator := opts.Calculator
	if calculator == nil {
		calculator = billing.NewCalculator()
	}
	return &Pipeline{
		registry:    opts.Registry,
		pricing:     opts.Pricing,
		concurrency: opts.Concurrency,
		rpm:         opts.RPM,
		calculator:  calculator,
		sink:        opts.Sink,
		settings:    settings,
		client:      upstreamclient.NewClient(0),
	}
}
