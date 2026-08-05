package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"

	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/bootstrap"
	"github.com/DouDOU-start/airgate-core/internal/bootstrap/priceseed"
	"github.com/DouDOU-start/airgate-core/internal/config"
	"github.com/DouDOU-start/airgate-core/internal/infra/store"
	"github.com/DouDOU-start/airgate-core/internal/server"
	"github.com/DouDOU-start/airgate-core/internal/version"
)

func main() {
	// CLI flags ------------------------------------------------------------
	// 仅声明少量必要 flag，避免 cobra 之类的额外依赖；其余配置项继续走
	// 配置文件 + 环境变量两条腿。
	var (
		showVersion bool
		configPath  string
	)
	flag.BoolVar(&showVersion, "version", false, "打印版本号并退出")
	flag.StringVar(&configPath, "config", "", "配置文件路径，默认为环境变量 CONFIG_PATH 或 ./config.yaml")
	flag.Parse()

	if showVersion {
		fmt.Printf("airgate-core %s %s/%s\n", version.Version, runtime.GOOS, runtime.GOARCH)
		return
	}

	// 如果 --config 提供了路径，把它写回环境变量，让后续 config.ConfigPath() 看到
	if configPath != "" {
		_ = os.Setenv("CONFIG_PATH", configPath)
	}

	// 默认初始化日志（配置加载前先用默认值）
	logx.InitLogger("core", "info", "text")
	slog.Info("AirGate Core 启动中...", "version", version.Version)

	// 加载配置：docker compose 用镜像内置 config.yaml + 环境变量覆盖，
	// make dev 用本地 config.yaml（由 config.yaml.example 复制）。
	// 数据库/Redis 连接信息由部署侧提供，不再有页面安装向导。
	cfgPath := config.ConfigPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("config_load_failed", "path", cfgPath, "error", err)
		os.Exit(1)
	}

	// 用配置值重新初始化日志（应用配置文件中的 level/format）
	logx.InitLogger("core", cfg.Log.Level, cfg.Log.Format)
	slog.Info("config_loaded", "path", cfgPath, "log_level", cfg.Log.Level, "log_format", cfg.Log.Format)

	// 启动正常服务
	startMainServer(cfg)
}

// startMainServer 启动主服务器
func startMainServer(cfg *config.Config) {
	bootStart := time.Now()

	// 初始化数据库连接（Ent Client）
	dsn := cfg.Database.DSN()
	drv, err := sql.Open(dialect.Postgres, dsn)
	if err != nil {
		slog.Error("db_open_failed", "dsn", store.RedactDSN(dsn), logx.LogFieldError, err)
		os.Exit(1)
	}
	slog.Info("db_connected",
		"driver", "postgres",
		"host", cfg.Database.Host,
		"port", cfg.Database.Port,
		"db", cfg.Database.DBName,
		"dsn", store.RedactDSN(dsn))

	// 配置连接池：不限制时 Go 会无限开连接，高并发下 Postgres "too many clients already"
	maxOpen := cfg.Database.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 50
	}
	maxIdle := cfg.Database.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 25
	}
	lifeMin := cfg.Database.ConnMaxLifetimeMinutes
	if lifeMin <= 0 {
		lifeMin = 30
	}
	drv.DB().SetMaxOpenConns(maxOpen)
	drv.DB().SetMaxIdleConns(maxIdle)
	drv.DB().SetConnMaxLifetime(time.Duration(lifeMin) * time.Minute)
	slog.Info("db_pool_configured",
		"max_open", maxOpen, "max_idle", maxIdle, "lifetime_min", lifeMin)

	const dbPingMaxRetries = 30
	const dbPingRetryInterval = 2 * time.Second
	for attempt := 1; attempt <= dbPingMaxRetries; attempt++ {
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := drv.DB().PingContext(pingCtx)
		pingCancel()
		if err == nil {
			break
		}
		if attempt == dbPingMaxRetries {
			slog.Error("db_ping_failed_after_retries",
				"attempts", dbPingMaxRetries, logx.LogFieldError, err)
			os.Exit(1)
		}
		slog.Warn("db_ping_retry",
			"attempt", attempt, "max", dbPingMaxRetries, logx.LogFieldError, err)
		time.Sleep(dbPingRetryInterval)
	}

	// 注入 slog 桥接，让 ent 内部 debug/error 日志走结构化通道
	db := ent.NewClient(ent.Driver(drv), store.EntSlogLogger())
	slog.Info("ent_client_initialized",
		"driver", "postgres",
		"max_open_conns", maxOpen,
		"max_idle_conns", maxIdle,
		"lifetime_min", lifeMin)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Warn("db_close_failed", logx.LogFieldError, err)
		}
	}()

	// 结构迁移：ent 非破坏性建表建列 + 存量库定点修复（见 bootstrap.Migrate）。
	if err := bootstrap.Migrate(context.Background(), db, drv.DB(), cfg.APIKeySecret()); err != nil {
		slog.Error("db_migration_failed", logx.LogFieldError, err)
		os.Exit(1)
	}

	// 导入默认模型价目表种子（insert-if-absent，不覆盖已有条目；失败只 Warn 不阻塞）
	priceseed.Load(context.Background(), store.NewModelPriceStore(db), config.ConfigPath())

	// 初始化 Redis（PoolSize <= 0 时 go-redis 用默认值 10 × CPU 核数）
	redisOpts := &redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		PoolSize: cfg.Redis.PoolSize,
	}
	if cfg.Redis.TLS {
		// ServerName 留空由 crypto/tls 从 Addr 推导（托管 Redis 常见 TLS 形态）。
		redisOpts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	rdb := redis.NewClient(redisOpts)
	const redisPingMaxRetries = 30
	const redisPingRetryInterval = 2 * time.Second
	for attempt := 1; attempt <= redisPingMaxRetries; attempt++ {
		redisCtx, redisCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := rdb.Ping(redisCtx).Err()
		redisCancel()
		if err == nil {
			break
		}
		if attempt == redisPingMaxRetries {
			slog.Error("redis_ping_failed_after_retries",
				"host", cfg.Redis.Host,
				"port", cfg.Redis.Port,
				"attempts", redisPingMaxRetries,
				logx.LogFieldError, err)
			os.Exit(1)
		}
		slog.Warn("redis_ping_retry",
			"host", cfg.Redis.Host,
			"port", cfg.Redis.Port,
			"attempt", attempt, "max", redisPingMaxRetries,
			logx.LogFieldError, err)
		time.Sleep(redisPingRetryInterval)
	}
	slog.Info("redis_connected",
		"host", cfg.Redis.Host,
		"port", cfg.Redis.Port,
		"db", cfg.Redis.DB)
	defer func() {
		if err := rdb.Close(); err != nil {
			slog.Warn("redis_close_failed", logx.LogFieldError, err)
		}
	}()

	slog.Info("bootstrap_completed", "duration_ms", time.Since(bootStart).Milliseconds())

	// 创建并启动 HTTP 服务器
	srv := server.NewServer(cfg, db, rdb)

	// 启动后台组件（计费/留痕记录器 + 注册表与价目表装载 + 支付后台循环，非阻塞）
	srv.StartBackground(context.Background())

	// 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.Start(); err != nil {
			slog.Error("服务器退出", "error", err)
		}
	}()

	<-quit
	slog.Info("收到关闭信号，开始优雅关闭...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("服务器关闭失败", "error", err)
	}
	slog.Info("服务器已关闭")
}
