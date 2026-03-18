// Package config 提供配置管理（YAML 文件 + 环境变量覆盖）
package config

import (
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// DefaultPort 默认服务端口
const DefaultPort = 9517

// GetPort 获取服务端口（优先环境变量 PORT）
func GetPort() int {
	if v := os.Getenv("PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return DefaultPort
}

// Config 应用配置
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Redis    RedisConfig    `yaml:"redis"`
	JWT      JWTConfig      `yaml:"jwt"`
	Log      LogConfig      `yaml:"log"`
	Plugins  PluginsConfig  `yaml:"plugins"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level  string `yaml:"level"`  // debug/info/warn/error，默认 info
	Format string `yaml:"format"` // text/json，默认 text
}

// PluginsConfig 插件配置
type PluginsConfig struct {
	Dir string      `yaml:"dir"` // 插件二进制目录，默认 data/plugins
	Dev []DevPlugin `yaml:"dev"` // 开发模式：直接从源码加载的插件
}

// DevPlugin 开发模式插件
type DevPlugin struct {
	Name string `yaml:"name"` // 插件名称提示值（兼容字段，实际以插件 Info().ID 为准）
	Path string `yaml:"path"` // 源码目录路径（用 go run 启动）
}

// ServerConfig HTTP 服务器配置
type ServerConfig struct {
	Port   int    `yaml:"port"`
	Mode   string `yaml:"mode"`    // debug / release
	WebDir string `yaml:"web_dir"` // 前端静态文件目录，默认 "web/dist"
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"dbname"`
	SSLMode  string `yaml:"sslmode"`
}

// RedisConfig Redis 配置
type RedisConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
	TLS      bool   `yaml:"tls"`
}

// JWTConfig JWT 配置
type JWTConfig struct {
	Secret     string `yaml:"secret"`
	ExpireHour int    `yaml:"expire_hour"`
}

// DSN 返回 PostgreSQL 连接字符串
func (d DatabaseConfig) DSN() string {
	sslmode := d.SSLMode
	if sslmode == "" {
		sslmode = "disable"
	}
	return "host=" + d.Host +
		" port=" + strconv.Itoa(d.Port) +
		" user=" + d.User +
		" password=" + d.Password +
		" dbname=" + d.DBName +
		" sslmode=" + sslmode
}

// Load 从 YAML 文件加载配置，环境变量优先级高于配置文件
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		Server: ServerConfig{Port: DefaultPort, Mode: "release", WebDir: "web/dist"},
		JWT:    JWTConfig{ExpireHour: 24},
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	applyEnvOverrides(cfg)
	return cfg, nil
}

// applyEnvOverrides 用环境变量覆盖配置值
func applyEnvOverrides(cfg *Config) {
	// 服务器
	envInt("PORT", &cfg.Server.Port)
	envStr("GIN_MODE", &cfg.Server.Mode)
	envStr("WEB_DIR", &cfg.Server.WebDir)

	// 数据库
	envStr("DB_HOST", &cfg.Database.Host)
	envInt("DB_PORT", &cfg.Database.Port)
	envStr("DB_USER", &cfg.Database.User)
	envStr("DB_PASSWORD", &cfg.Database.Password)
	envStr("DB_NAME", &cfg.Database.DBName)
	envStr("DB_SSLMODE", &cfg.Database.SSLMode)

	// Redis
	envStr("REDIS_HOST", &cfg.Redis.Host)
	envInt("REDIS_PORT", &cfg.Redis.Port)
	envStr("REDIS_PASSWORD", &cfg.Redis.Password)
	envInt("REDIS_DB", &cfg.Redis.DB)

	// JWT
	envStr("JWT_SECRET", &cfg.JWT.Secret)
	envInt("JWT_EXPIRE_HOUR", &cfg.JWT.ExpireHour)

	// 日志
	envStr("LOG_LEVEL", &cfg.Log.Level)
	envStr("LOG_FORMAT", &cfg.Log.Format)

	// 插件
	envStr("PLUGINS_DIR", &cfg.Plugins.Dir)
}

// envStr 如果环境变量存在，覆盖目标字符串
func envStr(key string, dst *string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

// envInt 如果环境变量存在且为合法整数，覆盖目标整数
func envInt(key string, dst *int) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}

// ConfigPath 返回配置文件路径
func ConfigPath() string {
	if v := os.Getenv("CONFIG_PATH"); v != "" {
		return v
	}
	return "config.yaml"
}
