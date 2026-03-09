// Package config 提供配置管理（YAML 文件 + 环境变量）
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
	Plugins  PluginsConfig  `yaml:"plugins"`
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
	Port int    `yaml:"port"`
	Mode string `yaml:"mode"` // debug / release
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
		" port=" + itoa(d.Port) +
		" user=" + d.User +
		" password=" + d.Password +
		" dbname=" + d.DBName +
		" sslmode=" + sslmode
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// Load 从 YAML 文件加载配置
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		Server: ServerConfig{Port: DefaultPort, Mode: "release"},
		JWT:    JWTConfig{ExpireHour: 24},
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	// 环境变量覆盖
	if v := os.Getenv("PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = p
		}
	}
	return cfg, nil
}

// ConfigPath 返回配置文件路径
func ConfigPath() string {
	if v := os.Getenv("CONFIG_PATH"); v != "" {
		return v
	}
	return "config.yaml"
}
