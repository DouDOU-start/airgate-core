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
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/DouDOU-start/airgate-core/internal/requestaudit"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"

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
	// AccountFirstTokenSource 提供近期账号×模型首字样本，用于服务重启后的调度预热。
	AccountFirstTokenSource AccountFirstTokenSource
	// ChannelFirstTokenSource 提供近期渠道密钥×模型首字样本，用于服务重启后的调度预热。
	ChannelFirstTokenSource ChannelFirstTokenSource
	// CPA 账号路径转发器（生产环境注入 *cpa.Bridge；nil 时账号候选不执行）。
	CPA AccountForwarder
	// ProviderTransport executes selected-account requests. When nil, CPA is
	// wrapped with transport.CPAAdapter, preserving the historical data path.
	ProviderTransport providertransport.ProviderTransport
	// CodexTransportPolicy supplies the plugin-wide Codex routing mode. It is
	// separate from account credentials. The production plugin manager exposes
	// policy only from a currently running executor, so disabling or failing to
	// start the plugin returns routing to auto.
	CodexTransportPolicy providertransport.CodexTransportPolicy
	// RelayHook 外部请求改写与本次请求路由扩展点（nil 时完全保持原路径）。
	RelayHook relayhook.Hook
	// RequestAudit 完整请求审计（nil 时关闭）。
	RequestAudit *requestaudit.Service
	// RemoteControlTokens stores upstream Remote Control enrollments. The
	// store is shared with server authentication middleware.
	RemoteControlTokens *middleware.RemoteControlTokenStore
}

// AccountForwarder is retained as a source-compatible alias for callers that
// inject CPA/test forwarders. New provider implementations should depend on
// transport.ProviderTransport directly; CPAAdapter bridges this legacy shape.
type AccountForwarder = providertransport.LegacyForwarder

// Pipeline relay 转发管线。
type Pipeline struct {
	registry                *registry.Registry
	pricing                 *pricing.Cache
	concurrency             *scheduler.ConcurrencyManager
	rpm                     *scheduler.RPMCounter
	calculator              *billing.Calculator
	sink                    UsageSink
	errSink                 ErrSink
	settings                *SettingsReader
	moderation              ModerationChecker
	healthTracker           HealthTracker
	accounts                *accountreg.Registry
	accountFirstTokenSource AccountFirstTokenSource
	channelFirstTokenSource ChannelFirstTokenSource
	cpa                     AccountForwarder
	providerTransport       providertransport.ProviderTransport
	codexTransportPolicy    providertransport.CodexTransportPolicy
	relayHook               relayhook.Hook
	requestAudit            *requestaudit.Service
	codexFileUploads        *codexFileUploadStore
	codexPluginUploads      *codexPluginUploadStore
	remoteControlTokens     *middleware.RemoteControlTokenStore
	accountDirectTransport  http.RoundTripper
	accountTransportMu      sync.Mutex
	accountTransports       sync.Map
	accountTransportCount   atomic.Int64
	// accountInflight 记录本实例各账号正在执行的真实上游 attempt。
	// 新会话调度用它做无网络往返的轻量负载感知；跨实例仍由 Redis 并发闸门兜底。
	accountInflight sync.Map
	// accountFirstToken 保存账号×模型近期成功首字的本实例 EWMA。
	// 用于同优先级新会话选择和粘性会话自适应重平衡；不形成失败冷却，
	// 也不跨越配置优先级或绕过配置权重。
	accountFirstToken sync.Map
	// channelInflight 记录本实例各渠道密钥正在执行的真实上游 attempt。
	channelInflight sync.Map
	// channelFirstToken 保存渠道密钥×模型近期首字 EWMA 与慢失败等级。
	channelFirstToken sync.Map
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
	transport := opts.ProviderTransport
	if transport == nil {
		transport = providertransport.NewCPAAdapter(opts.CPA)
	}
	policy := opts.CodexTransportPolicy
	if policy == nil {
		policy, _ = transport.(providertransport.CodexTransportPolicy)
	}
	remoteControlTokens := opts.RemoteControlTokens
	if remoteControlTokens == nil {
		remoteControlTokens = middleware.NewRemoteControlTokenStore()
	}
	return &Pipeline{
		registry:                opts.Registry,
		pricing:                 opts.Pricing,
		concurrency:             opts.Concurrency,
		rpm:                     opts.RPM,
		calculator:              calculator,
		sink:                    opts.Sink,
		errSink:                 opts.ErrLog,
		settings:                settings,
		moderation:              opts.Moderation,
		healthTracker:           opts.HealthTracker,
		accounts:                opts.Accounts,
		accountFirstTokenSource: opts.AccountFirstTokenSource,
		channelFirstTokenSource: opts.ChannelFirstTokenSource,
		cpa:                     opts.CPA,
		providerTransport:       transport,
		codexTransportPolicy:    policy,
		relayHook:               opts.RelayHook,
		requestAudit:            opts.RequestAudit,
		codexFileUploads:        newCodexFileUploadStore(),
		codexPluginUploads:      newCodexPluginUploadStore(),
		remoteControlTokens:     remoteControlTokens,
		accountDirectTransport:  upstreamclient.NewEnvironmentTransport(),
		client:                  upstreamclient.NewClient(0),
	}
}
