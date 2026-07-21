// Package server 提供 HTTP 服务器初始化和生命周期管理
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/bootstrap"
	"github.com/DouDOU-start/airgate-core/internal/config"
	"github.com/DouDOU-start/airgate-core/internal/errlog"
	"github.com/DouDOU-start/airgate-core/internal/relay/pipeline"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/relay/task"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"

	// 任务平台适配器自注册（task.Register；同步协议适配器由 pipeline 包内注册）。
	_ "github.com/DouDOU-start/airgate-core/internal/relay/task/openaivideo"
	_ "github.com/DouDOU-start/airgate-core/internal/relay/task/suno"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

// Server HTTP 服务器
type Server struct {
	cfg    *config.Config
	db     *ent.Client
	rdb    *redis.Client
	jwtMgr *auth.JWTManager
	engine *gin.Engine
	srv    *http.Server

	// 核心服务组件
	concurrency     *scheduler.ConcurrencyManager
	recorder        *billing.Recorder
	errRecorder     *errlog.Recorder
	handlers        *bootstrap.HTTPHandlers
	channelRegistry *registry.Registry
	pricingCache    *pricing.Cache
	relay           *pipeline.Pipeline
	taskFlow        *task.Flow
	taskPoller      *task.Poller

	// 中间件组件（需 Shutdown 时释放）
	ipRateLimiter *middleware.IPRateLimiter
	// oauthRateLimiter /oauth/token 端点的 IP 限流器（防 secret 爆破）。
	oauthRateLimiter *middleware.IPRateLimiter
	// ccUsageRateLimiter /v1/usage（cc-switch 兼容端点）的 IP 限流器（防刷）。
	ccUsageRateLimiter *middleware.IPRateLimiter
	// modelMarketRateLimiter /api/v1/model-market（模型广场公开端点）的 IP 限流器（防刷）。
	modelMarketRateLimiter *middleware.IPRateLimiter

	backgroundCancel context.CancelFunc
}

// NewServer 创建 HTTP 服务器
func NewServer(cfg *config.Config, db *ent.Client, rdb *redis.Client) *Server {
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	jwtMgr := auth.NewJWTManager(cfg.JWT.Secret, cfg.JWT.ExpireHour)

	// 核心服务组件
	concurrency := scheduler.NewConcurrencyManager(rdb)
	recorder := billing.NewRecorder(db, 0)
	errRecorder := errlog.NewRecorder(db, rdb)

	s := &Server{
		cfg:    cfg,
		db:     db,
		rdb:    rdb,
		jwtMgr: jwtMgr,
		// gin.New 不挂默认 Logger/Recovery，由我们的中间件接管以便接入结构化日志
		engine:      gin.New(),
		concurrency: concurrency,
		recorder:    recorder,
		errRecorder: errRecorder,
	}

	s.handlers = bootstrap.NewHTTPHandlers(bootstrap.HTTPDependencies{
		Config:      cfg,
		DB:          db,
		Redis:       rdb,
		JWTMgr:      jwtMgr,
		Concurrency: concurrency,
	})

	// 转发消费扣费后触发余额预警检查：billing 不依赖 app，经 hook 注入 user 服务。
	// 每个用户异步检查（checkBalanceAlert 内部有 notified 幂等，降破发一次、回升重置）。
	userSvc := s.handlers.UserService
	recorder.SetBalanceChargedHook(func(userIDs []int) {
		for _, id := range userIDs {
			go userSvc.CheckBalanceAlert(context.Background(), id)
		}
	})

	// 渠道注册表与价目表缓存：
	// channel service 充当注册表的 Loader/Persister（解密 api_keys、状态落库），
	// 注册表反向作为 channel service 的 Reloader（写操作成功后全量重载）；
	// modelprice service 同理充当 pricing 缓存的 Loader，缓存作为其写后失效器。
	s.channelRegistry = registry.New(s.handlers.ChannelService, s.handlers.ChannelService)
	s.handlers.ChannelService.SetReloader(s.channelRegistry)
	s.pricingCache = pricing.NewCache(s.handlers.ModelPriceService)
	s.handlers.ModelPriceService.SetInvalidator(s.pricingCache)

	// relay 转发管线：注册表调度 + 渠道 RPM/并发闸门 + 计费落账；
	// 渠道测试器走同一 adaptor 链路（server 层适配器负责解密与快照构造）。
	settingsReader := pipeline.NewSettingsReader(gatewaySettingsSource{s.handlers.SettingsService})
	rpmCounter := scheduler.NewRPMCounter(rdb)
	s.relay = pipeline.New(pipeline.Options{
		Registry:    s.channelRegistry,
		Pricing:     s.pricingCache,
		Concurrency: concurrency,
		RPM:         rpmCounter,
		Calculator:  billing.NewCalculator(),
		Sink:        recorder,
		ErrLog:      errRecorder,
		Settings:    settingsReader,
		Moderation:  s.handlers.ModerationEngine,
	})

	// 异步任务子系统（视频/音乐）：与同步管线同源组件 + task 持久化 + 余额动账适配器。
	taskOpts := task.Options{
		Registry:    s.channelRegistry,
		Pricing:     s.pricingCache,
		Concurrency: concurrency,
		RPM:         rpmCounter,
		Calculator:  billing.NewCalculator(),
		Sink:        recorder,
		ErrLog:      errRecorder,
		Settings:    settingsReader,
		Store:       s.handlers.TaskStore,
		Balance:     taskBalanceAdapter{svc: s.handlers.UserService},
		Moderation:  s.handlers.ModerationEngine,
	}
	s.taskFlow = task.NewFlow(taskOpts)
	s.taskPoller = task.NewPoller(taskOpts)
	s.handlers.ChannelService.SetTester(&channelTester{pipe: s.relay, secret: cfg.APIKeySecret()})
	// 渠道失败计数读取（渠道页监控列，读 errlog 分钟桶）。
	s.handlers.UpstreamLogService.SetFailureCounter(errRecorder)

	// 注册路由
	s.registerRoutes()

	s.srv = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler: s.engine,
	}

	return s
}

// Start 启动 HTTP 服务器（阻塞）
func (s *Server) Start() error {
	slog.Info("server_listening", "host", s.cfg.Server.Host, "port", s.cfg.Server.Port, "addr", s.srv.Addr)
	if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server_listen_failed", "addr", s.srv.Addr, "error", err)
		return err
	}
	return nil
}

// StartBackground 启动后台组件：使用量异步记录器与各类后台装载/清理循环。
func (s *Server) StartBackground(ctx context.Context) {
	// 启动使用量异步记录器与上游请求日志记录器（含 TTL 清理）
	s.recorder.Start()
	s.errRecorder.Start()

	backgroundCtx, cancel := context.WithCancel(ctx)
	s.backgroundCancel = cancel

	// 渠道注册表初次加载与价目表预热；失败不阻塞启动：
	// 注册表起后台指数退避重试（成功即停，Pick 另有惰性兜底），
	// 价目表由 Get 惰性重载兜底。
	if err := s.channelRegistry.Reload(ctx); err != nil {
		slog.Warn("channel_registry_initial_load_failed", "error", err)
		go retryReload(backgroundCtx, s.channelRegistry, "channel_registry", time.Second)
	}
	if err := s.pricingCache.Reload(ctx); err != nil {
		slog.Warn("model_price_cache_warmup_failed", "error", err)
	}

	// 支付服务商装载（失败不阻塞启动：admin 保存配置时会再次 Reload）+ 订单过期清理。
	if err := s.handlers.PaymentService.ReloadProviders(ctx); err != nil {
		slog.Warn("payment_providers_initial_load_failed", "error", err)
	}
	go s.handlers.PaymentService.StartExpireLoop(backgroundCtx)

	// 异步任务轮询器：扫未完成任务 → 查上游 → 终态结算/退款。
	go s.taskPoller.Run(backgroundCtx)

	// 风控审核引擎：observe 异步 worker 池 + 审核日志 TTL 清理。
	s.handlers.ModerationEngine.StartBackground(backgroundCtx)
}

// reloadable 后台重试所需的窄接口（registry.Registry 实现；便于测试注入）。
type reloadable interface {
	Reload(ctx context.Context) error
}

// retryReloadMaxDelay 后台重载重试的退避上限。
const retryReloadMaxDelay = 30 * time.Second

// retryReload 指数退避重试 Reload（initialDelay 起、30s 封顶），成功或 ctx 取消即停。
func retryReload(ctx context.Context, target reloadable, name string, initialDelay time.Duration) {
	delay := initialDelay
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if err := target.Reload(ctx); err != nil {
			slog.Warn("background_reload_retry_failed", "target", name, "delay", delay.String(), "error", err)
			delay = min(delay*2, retryReloadMaxDelay)
			continue
		}
		slog.Info("background_reload_retry_succeeded", "target", name)
		return
	}
}

// Shutdown 优雅关闭服务器
func (s *Server) Shutdown(ctx context.Context) error {
	slog.Info("正在关闭服务器...")

	if s.backgroundCancel != nil {
		s.backgroundCancel()
	}

	// 停止 IP 限流器后台清理
	if s.ipRateLimiter != nil {
		s.ipRateLimiter.Stop()
	}
	if s.oauthRateLimiter != nil {
		s.oauthRateLimiter.Stop()
	}
	if s.ccUsageRateLimiter != nil {
		s.ccUsageRateLimiter.Stop()
	}
	if s.modelMarketRateLimiter != nil {
		s.modelMarketRateLimiter.Stop()
	}

	// 先排空 HTTP 在途请求，再停两个 recorder：在途请求收尾时仍会调 Record，
	// 先停 recorder 会把关停窗口内的计费/留痕全部丢弃。
	err := s.srv.Shutdown(ctx)
	s.recorder.Stop()
	s.errRecorder.Stop()
	return err
}
