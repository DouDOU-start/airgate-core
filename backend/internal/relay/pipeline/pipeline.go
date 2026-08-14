// Package pipeline 实现 relay 转发主循环：
// 余额/缺价预检 → user/key 并发闸门 → failover ≤3（渠道 Pick + RPM/并发闸门 +
// adaptor 直发 + outcome 判定）→ pricing 计费 → recorder 落账。
package pipeline

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/errlog"
	"github.com/DouDOU-start/airgate-core/internal/pkg/upstreamclient"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/relay/relayhook"
	"github.com/DouDOU-start/airgate-core/internal/requestaudit"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"

	// 各协议适配器自注册（adaptor.Register）。
	_ "github.com/DouDOU-start/airgate-core/internal/relay/adaptor/anthropic"
	_ "github.com/DouDOU-start/airgate-core/internal/relay/adaptor/gemini"
	_ "github.com/DouDOU-start/airgate-core/internal/relay/adaptor/openai"
)

// UsageSink 用量落账窄接口（*billing.Recorder 天然满足；测试注入 fake）。
type UsageSink interface {
	Record(record billing.UsageRecord)
}

// ErrSink 上游请求日志投递窄接口（*errlog.Recorder 天然满足；nil 安全）。
// Record 落失败留痕（异步、允许丢）；CountFailure 渠道×verdict / phase 分钟桶
// 计数（错误率事实源，恒计数）。
type ErrSink interface {
	Record(e errlog.Entry)
	CountFailure(ctx context.Context, channelID int, verdict, phase string)
}

// HealthTracker 健康信号接收窄接口（*probe.Engine 实现；nil 安全）。
type HealthTracker interface {
	RecordSuccess(keyID int)
	RecordFailure(keyID int)
	RecordAuthFailure(keyID int)
}

// Options 管线装配依赖。
type Options struct {
	Registry    *registry.Registry
	Pricing     *pricing.Cache
	Concurrency *scheduler.ConcurrencyManager
	RPM         *scheduler.RPMCounter
	Calculator  *billing.Calculator
	Sink        UsageSink
	ErrLog      ErrSink
	Settings    *SettingsReader
	// Moderation 内容审核引擎（风控中心；nil 时全部放行）。
	Moderation ModerationChecker
	// HealthTracker 健康信号接收器（探针引擎；nil 时静默跳过）。
	HealthTracker HealthTracker
	// Accounts 账号注册表（nil 时仅渠道路径）。
	Accounts *accountreg.Registry
	// CPA 账号路径转发器（生产环境注入 *cpa.Bridge；nil 时账号候选不执行）。
	CPA AccountForwarder
	// RelayHook 外部请求改写与本次请求路由扩展点（nil 时完全保持原路径）。
	RelayHook relayhook.Hook
	// RequestAudit 完整请求审计（nil 时关闭）。
	RequestAudit *requestaudit.Service
}

// Pipeline relay 转发管线。
type Pipeline struct {
	registry               *registry.Registry
	pricing                *pricing.Cache
	concurrency            *scheduler.ConcurrencyManager
	rpm                    *scheduler.RPMCounter
	calculator             *billing.Calculator
	sink                   UsageSink
	errSink                ErrSink
	settings               *SettingsReader
	moderation             ModerationChecker
	healthTracker          HealthTracker
	accounts               *accountreg.Registry
	cpa                    AccountForwarder
	relayHook              relayhook.Hook
	requestAudit           *requestaudit.Service
	accountDirectTransport http.RoundTripper
	accountTransportMu     sync.Mutex
	accountTransports      sync.Map
	accountTransportCount  atomic.Int64
	// accountInflight 记录本实例各账号正在执行的真实上游 attempt。
	// 新会话调度用它做无网络往返的轻量负载感知；跨实例仍由 Redis 并发闸门兜底。
	accountInflight   sync.Map
	routeMu           sync.Mutex
	routeWeights      map[routeBalanceKey]routeBalanceState
	routePicks        uint64
	routeCatalogMu    sync.Mutex
	routeCatalogs     sync.Map
	routeCatalogCount atomic.Int64
	// sessionAffinity 粘性会话绑定缓存（见 session_affinity.go）。
	sessionAffinity sessionAffinityCache
	// client 出口 HTTP 客户端：不设总超时（流式无总超时），仅设连接/TLS 层超时；
	// 非流式的总超时由调用方经 context 施加。重定向不跟随
	//（upstreamclient.NewClient 统一设 ErrUseLastResponse），
	// 3xx 原样进入 outcome 判定按 clientError 语义重建终止。
	client *http.Client
	// queueWaiters 当前处于排队退避（渠道容量满等待重竞争）的请求数：
	// 每个排队请求整段持有请求体与 user/key 并发槽，须有全局上限泄压（见 forward）。
	queueWaiters atomic.Int64
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
		registry:               opts.Registry,
		pricing:                opts.Pricing,
		concurrency:            opts.Concurrency,
		rpm:                    opts.RPM,
		calculator:             calculator,
		sink:                   opts.Sink,
		errSink:                opts.ErrLog,
		settings:               settings,
		moderation:             opts.Moderation,
		healthTracker:          opts.HealthTracker,
		accounts:               opts.Accounts,
		cpa:                    opts.CPA,
		relayHook:              opts.RelayHook,
		requestAudit:           opts.RequestAudit,
		accountDirectTransport: upstreamclient.NewEnvironmentTransport(),
		client:                 upstreamclient.NewClient(0),
	}
}
