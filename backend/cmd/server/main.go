package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"

	sdk "github.com/DouDOU-start/airgate-sdk"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/migrate"
	"github.com/DouDOU-start/airgate-core/internal/config"
	"github.com/DouDOU-start/airgate-core/internal/i18n"
	"github.com/DouDOU-start/airgate-core/internal/server"
	"github.com/DouDOU-start/airgate-core/internal/setup"
)

func main() {
	// 默认初始化日志（配置加载前先用默认值）
	sdk.InitLogger("core", "info", "text")
	slog.Info("AirGate Core 启动中...")

	// 加载国际化
	_ = i18n.Load("locales")

	// 检查是否需要安装
	if setup.NeedsSetup() {
		slog.Info("系统未安装，启动安装向导...")
		startSetupServer()
		// 安装完成后继续往下执行，启动正常服务
		slog.Info("安装完成，启动主服务...")
	}

	// 加载配置
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		slog.Error("加载配置失败", "error", err)
		os.Exit(1)
	}

	// 用配置值重新初始化日志（应用配置文件中的 level/format）
	sdk.InitLogger("core", cfg.Log.Level, cfg.Log.Format)

	// 启动正常服务
	startMainServer(cfg)
}

// startSetupServer 启动安装向导服务器，安装完成后自动关闭
func startSetupServer() {
	r := gin.Default()

	// 用于通知安装完成
	done := make(chan struct{})
	setup.RegisterRoutesWithCallback(r, func() {
		close(done)
	})

	// 静态文件服务（前端）
	webDir := os.Getenv("WEB_DIR")
	if webDir == "" {
		webDir = "web/dist"
	}
	indexHTML := filepath.Join(webDir, "index.html")
	r.Static("/assets", filepath.Join(webDir, "assets"))
	r.StaticFile("/", indexHTML)
	r.NoRoute(func(c *gin.Context) {
		c.File(indexHTML)
	})

	port := config.GetPort()
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: r}

	slog.Info("安装向导服务器启动", "port", port)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("安装向导启动失败", "error", err)
			os.Exit(1)
		}
	}()

	// 等待安装完成
	<-done
	slog.Info("安装完成，关闭安装向导服务器...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// startMainServer 启动主服务器
func startMainServer(cfg *config.Config) {
	// 初始化数据库连接（Ent Client）
	drv, err := sql.Open(dialect.Postgres, cfg.Database.DSN())
	if err != nil {
		slog.Error("打开数据库失败", "error", err)
		os.Exit(1)
	}
	db := ent.NewClient(ent.Driver(drv))
	defer func() {
		if err := db.Close(); err != nil {
			slog.Warn("关闭数据库连接失败", "error", err)
		}
	}()

	// 启动时执行非破坏性迁移，补齐缺失表和字段，避免升级后因 schema 落后导致接口报错。
	if err := db.Schema.Create(context.Background(), migrate.WithDropIndex(false), migrate.WithDropColumn(false)); err != nil {
		slog.Error("执行数据库迁移失败", "error", err)
		os.Exit(1)
	}

	// 初始化 Redis
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	defer func() {
		if err := rdb.Close(); err != nil {
			slog.Warn("关闭 Redis 连接失败", "error", err)
		}
	}()

	// 创建并启动 HTTP 服务器
	srv := server.NewServer(cfg, db, rdb)

	// 启动插件系统（非阻塞，失败不影响核心服务）
	srv.StartPlugins(context.Background())

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
