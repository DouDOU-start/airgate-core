// Package setup 提供安装向导逻辑
package setup

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/migrate"
	"github.com/DouDOU-start/airgate-core/internal/config"
	"github.com/DouDOU-start/airgate-core/internal/infra/store"
)

var installMu sync.Mutex

// EnvDBConfig 从环境变量解析数据库配置。
// 当 DB_HOST/DB_PORT/DB_USER/DB_PASSWORD/DB_NAME 全部存在时返回非 nil；
// 任意一项缺失返回 nil（用户需要走 wizard 手填）。
//
// 用途：docker compose 部署时 env 已经提供了完整连接信息，wizard 不应再问一遍。
func EnvDBConfig() *config.DatabaseConfig {
	host := os.Getenv("DB_HOST")
	user := os.Getenv("DB_USER")
	password := os.Getenv("DB_PASSWORD")
	dbname := os.Getenv("DB_NAME")
	portStr := os.Getenv("DB_PORT")
	if host == "" || user == "" || password == "" || dbname == "" || portStr == "" {
		return nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil
	}
	sslmode := os.Getenv("DB_SSLMODE")
	if sslmode == "" {
		sslmode = "disable"
	}
	return &config.DatabaseConfig{
		Host:     host,
		Port:     port,
		User:     user,
		Password: password,
		DBName:   dbname,
		SSLMode:  sslmode,
	}
}

// EnvRedisConfig 从环境变量解析 Redis 配置。
// 当 REDIS_HOST/REDIS_PORT/REDIS_PASSWORD 全部存在时返回非 nil；
// 没有密码或缺主机视为不可用，wizard 仍需手填。
func EnvRedisConfig() *config.RedisConfig {
	host := os.Getenv("REDIS_HOST")
	password := os.Getenv("REDIS_PASSWORD")
	portStr := os.Getenv("REDIS_PORT")
	if host == "" || password == "" || portStr == "" {
		return nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil
	}
	dbNum := 0
	if v := os.Getenv("REDIS_DB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			dbNum = n
		}
	}
	return &config.RedisConfig{
		Host:     host,
		Port:     port,
		Password: password,
		DB:       dbNum,
	}
}

// NeedsSetup 检查是否需要安装。
// 判断逻辑：config.yaml 不存在 → 需要安装；
// config.yaml 存在则尝试连接数据库，查询是否已有管理员账户。
//
// 当 config.yaml 已存在但 DB 暂不可达时（如服务器重启后 DB 尚未就绪），
// 会重试等待 DB 就绪，而非直接判定为"未安装"进入安装向导。
func NeedsSetup() bool {
	configPath := config.ConfigPath()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return true
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Warn("setup_config_load_failed", "stage", "config_load", sdk.LogFieldError, err)
		return true
	}

	dsn := cfg.Database.DSN()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		slog.Warn("db_open_failed", "stage", "needs_setup", "dsn", store.RedactDSN(dsn), sdk.LogFieldError, err)
		return true
	}
	defer func() { _ = db.Close() }()

	// config.yaml 存在说明曾经安装过，DB 不可达时重试等待而非直接判定未安装
	const maxRetries = 30
	const retryInterval = 2 * time.Second
	for attempt := 1; attempt <= maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := db.PingContext(ctx)
		cancel()
		if err == nil {
			break
		}
		if attempt == maxRetries {
			slog.Error("db_ping_failed_after_retries", "stage", "needs_setup",
				"attempts", maxRetries, sdk.LogFieldError, err)
			os.Exit(1)
		}
		slog.Warn("db_ping_retry", "stage", "needs_setup",
			"attempt", attempt, "max", maxRetries, sdk.LogFieldError, err)
		time.Sleep(retryInterval)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var count int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE role = 'admin'").Scan(&count)
	if err != nil {
		slog.Warn("setup_admin_query_failed", "stage", "needs_setup", sdk.LogFieldError, err)
		return true
	}

	if count == 0 {
		return true
	}
	slog.Info("setup_already_initialized")
	return false
}

// TestDBConnection 测试数据库连接。
//
// 如果目标库不存在（PostgreSQL 错误码 3D000），会自动连到 `postgres` 系统库
// 执行 CREATE DATABASE 后重试，对安装向导用户透明 —— 用户填了一个不存在的库名
// 意味着他期望工具帮他建库。
//
// 这个行为只在安装向导阶段触发（NeedsSetup() == true 时被调用），生产运行时
// 不会走到这里。如果连 `postgres` 系统库或 CREATE DATABASE 也失败（权限不足等），
// 返回拼接后的错误信息，用户可以看到具体原因。
func TestDBConnection(host string, port int, user, password, dbname, sslmode string) error {
	if sslmode == "" {
		sslmode = "disable"
	}
	if err := pingDatabase(host, port, user, password, dbname, sslmode); err != nil {
		if !isDatabaseNotExistError(err) {
			return err
		}
		slog.Info("setup_database_create_start", "dbname", dbname)
		if createErr := createDatabase(host, port, user, password, dbname, sslmode); createErr != nil {
			return fmt.Errorf("数据库 %q 不存在且自动创建失败: %w", dbname, createErr)
		}
		slog.Info("setup_database_created", "dbname", dbname)
		return pingDatabase(host, port, user, password, dbname, sslmode)
	}
	return nil
}

// pingDatabase 打开连接并 ping 一次，是 TestDBConnection 的底层 helper。
func pingDatabase(host string, port int, user, password, dbname, sslmode string) error {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		host, port, user, password, dbname, sslmode)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Warn("db_close_failed", "stage", "setup_ping", sdk.LogFieldError, err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}

// isDatabaseNotExistError 检测 lib/pq 返回的"目标库不存在"错误。
// PostgreSQL 错误码 3D000 = invalid_catalog_name。
func isDatabaseNotExistError(err error) bool {
	if err == nil {
		return false
	}
	if pqErr, ok := err.(*pq.Error); ok {
		return pqErr.Code == "3D000"
	}
	// lib/pq 在某些路径下也可能返回非 *pq.Error 的字符串错误
	return strings.Contains(err.Error(), "does not exist")
}

// createDatabase 连到 PostgreSQL 系统库 `postgres` 执行 CREATE DATABASE。
// 通过 quoteIdentifier 防止用户在数据库名里塞 SQL 注入（虽然安装向导通常没人这么干，
// 但 CREATE DATABASE 不支持参数占位符，必须自己拼字符串，所以这一步是必须的）。
func createDatabase(host string, port int, user, password, dbname, sslmode string) error {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=postgres sslmode=%s",
		host, port, user, password, sslmode)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("连接 postgres 系统库失败: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Warn("db_close_failed", "stage", "setup_create_db", sdk.LogFieldError, err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping postgres 系统库失败: %w", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE DATABASE "+quoteIdentifier(dbname)); err != nil {
		return err
	}
	return nil
}

// quoteIdentifier 把 PostgreSQL 标识符用双引号包起来，并把内部的双引号转义为两个双引号。
// 这是 PostgreSQL 标准的标识符引用方式，参考 lib/pq 的 QuoteIdentifier。
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// TestRedisConnection 测试 Redis 连接
func TestRedisConnection(host string, port int, password string, db int) error {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", host, port),
		Password: password,
		DB:       db,
	})
	defer func() {
		if err := rdb.Close(); err != nil {
			slog.Warn("redis_close_failed", "stage", "setup_test", sdk.LogFieldError, err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return rdb.Ping(ctx).Err()
}

// InstallParams 安装参数
type InstallParams struct {
	DB    config.DatabaseConfig
	Redis config.RedisConfig
	Admin struct {
		Email    string
		Password string
	}
}

// Install 执行安装
func Install(params InstallParams) error {
	installMu.Lock()
	defer installMu.Unlock()

	if !NeedsSetup() {
		return fmt.Errorf("系统已安装")
	}

	slog.Info("setup_initialization_start")

	// 1. 测试数据库连接
	if err := TestDBConnection(params.DB.Host, params.DB.Port, params.DB.User, params.DB.Password, params.DB.DBName, params.DB.SSLMode); err != nil {
		slog.Error("setup_failed", "stage", "test_db_connection", sdk.LogFieldError, err)
		return fmt.Errorf("数据库连接失败: %w", err)
	}

	// 2. 连接数据库，运行 Ent 迁移
	dsn := params.DB.DSN()
	drv, err := entsql.Open(dialect.Postgres, dsn)
	if err != nil {
		slog.Error("setup_failed", "stage", "open_database", "dsn", store.RedactDSN(dsn), sdk.LogFieldError, err)
		return fmt.Errorf("打开数据库失败: %w", err)
	}
	client := ent.NewClient(ent.Driver(drv), store.EntSlogLogger())
	defer func() {
		if err := client.Close(); err != nil {
			slog.Warn("db_close_failed", "stage", "setup_install", sdk.LogFieldError, err)
		}
	}()

	slog.Info("setup_migration_start")
	if err := client.Schema.Create(context.Background(),
		migrate.WithDropIndex(false),
		migrate.WithDropColumn(false),
	); err != nil {
		slog.Error("setup_failed", "stage", "schema_migration", sdk.LogFieldError, err)
		return fmt.Errorf("数据库迁移失败: %w", err)
	}
	slog.Info("setup_migration_done")

	// 3. 创建管理员账户
	hash, err := bcrypt.GenerateFromPassword([]byte(params.Admin.Password), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("setup_failed", "stage", "password_hash", sdk.LogFieldError, err)
		return fmt.Errorf("密码加密失败: %w", err)
	}
	_, err = client.User.Create().
		SetEmail(params.Admin.Email).
		SetPasswordHash(string(hash)).
		SetRole("admin").
		SetStatus("active").
		Save(context.Background())
	if err != nil {
		slog.Error("setup_failed", "stage", "create_admin", "admin_email", store.EmailHash(params.Admin.Email), sdk.LogFieldError, err)
		return fmt.Errorf("创建管理员失败: %w", err)
	}
	slog.Info("setup_admin_created", "admin_email", store.EmailHash(params.Admin.Email))

	// 4. 写入配置文件
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: config.GetPort(), Mode: "release"},
		Database: params.DB,
		Redis:    params.Redis,
		JWT:      config.JWTConfig{Secret: generateSecret(), ExpireHour: 24},
	}
	cfgData, err := yaml.Marshal(cfg)
	if err != nil {
		slog.Error("setup_failed", "stage", "marshal_config", sdk.LogFieldError, err)
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	if err := os.WriteFile(config.ConfigPath(), cfgData, 0644); err != nil {
		slog.Error("setup_failed", "stage", "write_config", sdk.LogFieldError, err)
		return fmt.Errorf("写入配置文件失败: %w", err)
	}
	slog.Info("setup_defaults_seeded", "config_path", config.ConfigPath())

	slog.Info("setup_initialization_done")
	return nil
}

func generateSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// 极端情况下的回退
		return "airgate-default-secret-change-me"
	}
	return hex.EncodeToString(b)
}
