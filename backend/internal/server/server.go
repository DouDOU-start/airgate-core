// Package server 提供 HTTP 服务器初始化和生命周期管理
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/config"
	"github.com/DouDOU-start/airgate-core/internal/plugin"
	"github.com/DouDOU-start/airgate-core/internal/ratelimit"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
)

// Server HTTP 服务器
type Server struct {
	cfg     *config.Config
	db      *ent.Client
	rdb     *redis.Client
	jwtMgr  *auth.JWTManager
	limiter *ratelimit.Limiter
	engine  *gin.Engine
	srv     *http.Server

	// 插件系统组件
	pluginMgr      *plugin.Manager
	forwarder      *plugin.Forwarder
	marketplace    *plugin.Marketplace
	dynamicRouter  *DynamicRouter
	extensionProxy *plugin.ExtensionProxy

	// 核心服务组件
	scheduler   *scheduler.Scheduler
	concurrency *scheduler.ConcurrencyManager
	priceMgr    *billing.PriceManager
	calculator  *billing.Calculator
	recorder    *billing.Recorder
}

// NewServer 创建 HTTP 服务器
func NewServer(cfg *config.Config, db *ent.Client, rdb *redis.Client) *Server {
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	jwtMgr := auth.NewJWTManager(cfg.JWT.Secret, cfg.JWT.ExpireHour)
	limiter := ratelimit.NewLimiter(rdb, ratelimit.DefaultConfig())

	// 核心服务组件
	sched := scheduler.NewScheduler(db, rdb)
	concurrency := scheduler.NewConcurrencyManager(rdb)
	priceMgr := billing.NewPriceManager()
	calculator := billing.NewCalculator()
	recorder := billing.NewRecorder(db, 0)

	// 插件系统组件
	pluginDir := cfg.Plugins.Dir
	if pluginDir == "" {
		pluginDir = "data/plugins"
	}
	pluginMgr := plugin.NewManager(pluginDir, cfg.Log.Level, priceMgr)
	forwarder := plugin.NewForwarder(db, pluginMgr, sched, concurrency, limiter, calculator, priceMgr, recorder)
	marketplace := plugin.NewMarketplace(pluginDir)
	dynamicRouter := NewDynamicRouter(forwarder)
	extensionProxy := plugin.NewExtensionProxy(pluginMgr)

	s := &Server{
		cfg:            cfg,
		db:             db,
		rdb:            rdb,
		jwtMgr:         jwtMgr,
		limiter:        limiter,
		engine:         gin.Default(),
		pluginMgr:      pluginMgr,
		forwarder:      forwarder,
		marketplace:    marketplace,
		dynamicRouter:  dynamicRouter,
		extensionProxy: extensionProxy,
		scheduler:      sched,
		concurrency:    concurrency,
		priceMgr:       priceMgr,
		calculator:     calculator,
		recorder:       recorder,
	}

	// 注册路由
	s.registerRoutes()

	s.srv = &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: s.engine,
	}

	return s
}

// Start 启动 HTTP 服务器（阻塞）
func (s *Server) Start() error {
	slog.Info("AirGate Core 服务器启动", "addr", s.srv.Addr)
	return s.srv.ListenAndServe()
}

// StartPlugins 启动异步记录器和插件系统
func (s *Server) StartPlugins(ctx context.Context) {
	// 启动使用量异步记录器
	s.recorder.Start()

	// 加载已编译的插件
	if err := s.pluginMgr.LoadAll(ctx); err != nil {
		slog.Error("加载插件失败（不影响核心服务）", "error", err)
	}

	// 加载开发模式插件（go run 源码）
	for _, dev := range s.cfg.Plugins.Dev {
		if err := s.pluginMgr.LoadDev(ctx, dev.Name, dev.Path); err != nil {
			slog.Error("加载开发插件失败", "name", dev.Name, "path", dev.Path, "error", err)
		}
	}
}

// Shutdown 优雅关闭服务器
func (s *Server) Shutdown(ctx context.Context) error {
	slog.Info("正在关闭服务器...")

	// 停止使用量记录器
	s.recorder.Stop()

	// 停止所有插件
	s.pluginMgr.StopAll(ctx)

	return s.srv.Shutdown(ctx)
}
