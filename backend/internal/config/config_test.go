package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGetPortAndHostUseEnvironmentWithFallback(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("HOST", "")
	if got := GetPort(); got != DefaultPort {
		t.Fatalf("默认端口 = %d，期望 %d", got, DefaultPort)
	}
	if got := GetHost(); got != DefaultHost {
		t.Fatalf("默认主机 = %q，期望 %q", got, DefaultHost)
	}

	t.Setenv("PORT", "18080")
	t.Setenv("HOST", "127.0.0.1")
	if got := GetPort(); got != 18080 {
		t.Fatalf("环境端口 = %d，期望 18080", got)
	}
	if got := GetHost(); got != "127.0.0.1" {
		t.Fatalf("环境主机 = %q，期望 127.0.0.1", got)
	}

	t.Setenv("PORT", "bad")
	if got := GetPort(); got != DefaultPort {
		t.Fatalf("非法端口应回退默认值，得到 %d", got)
	}
}

func TestLoadAppliesDefaultsAndEnvironmentOverrides(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte(`
server:
  port: 9000
  mode: debug
database:
  host: db.local
  port: 5432
  user: app
  password: secret
  dbname: airgate
redis:
  host: redis.local
  port: 6379
jwt:
  secret: yaml-secret
  expire_hour: 12
log:
  level: info
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("写入临时配置失败: %v", err)
	}
	t.Setenv("HOST", "127.0.0.1")
	t.Setenv("PORT", "18080")
	t.Setenv("DB_HOST", "db.env")
	t.Setenv("DB_PORT", "15432")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("PLUGINS_ENABLED", "true")
	t.Setenv("PLUGINS_DIR", "/tmp/relay-hooks")
	t.Setenv("PLUGINS_HOOK_TIMEOUT_MS", "75")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	if cfg.Server.Host != "127.0.0.1" || cfg.Server.Port != 18080 {
		t.Fatalf("服务器配置未被环境变量覆盖: %+v", cfg.Server)
	}
	if cfg.Database.Host != "db.env" || cfg.Database.Port != 15432 {
		t.Fatalf("数据库配置未被环境变量覆盖: %+v", cfg.Database)
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("日志配置未被环境变量覆盖: log=%+v", cfg.Log)
	}
	if !cfg.Plugins.Enabled || cfg.Plugins.Dir != "/tmp/relay-hooks" || cfg.Plugins.HookTimeoutMS != 75 {
		t.Fatalf("插件配置未被环境变量覆盖: plugins=%+v", cfg.Plugins)
	}
}

func TestLoadDefaultsPluginsToDisabled(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := Load(filepath.Join(t.TempDir(), "不存在.yaml"))
	if err != nil {
		t.Fatalf("无配置文件加载失败: %v", err)
	}
	if cfg.Plugins.Enabled {
		t.Fatal("插件进程默认不应开启")
	}
	if cfg.Plugins.Dir != "data/plugins" || cfg.Plugins.HookTimeoutMS != 500 {
		t.Fatalf("插件默认配置异常: %+v", cfg.Plugins)
	}
}

func Test仓库YAML示例语法有效(t *testing.T) {
	paths := []string{
		filepath.Join("..", "..", "config.yaml.example"),
		filepath.Join("..", "..", "..", "deploy", "config.docker.yaml"),
		filepath.Join("..", "..", "..", "deploy", "docker-compose.yml"),
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取 YAML 示例 %s 失败：%v", path, err)
		}
		var document any
		if err := yaml.Unmarshal(data, &document); err != nil {
			t.Fatalf("YAML 示例 %s 语法无效：%v", path, err)
		}
	}
}

func TestDatabaseDSNDefaultsSSLMode(t *testing.T) {
	dsn := DatabaseConfig{
		Host:     "db",
		Port:     5432,
		User:     "user",
		Password: "pass",
		DBName:   "airgate",
	}.DSN()

	want := "host=db port=5432 user=user password=pass dbname=airgate sslmode=disable"
	if dsn != want {
		t.Fatalf("DSN = %q，期望 %q", dsn, want)
	}
}

func TestAPIKeySecretValidatesConfiguredSecret(t *testing.T) {
	valid := "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff-extra"
	cfg := &Config{Security: SecurityConfig{APIKeySecret: valid}}
	if got := cfg.APIKeySecret(); got != valid {
		t.Fatalf("有效密钥 = %q，期望使用配置值", got)
	}

	cfg.Security.APIKeySecret = "不是有效 hex"
	if got := cfg.APIKeySecret(); got == cfg.Security.APIKeySecret {
		t.Fatalf("非法密钥不应被采用")
	}
}

func TestConfigPathUsesEnvironment(t *testing.T) {
	t.Setenv("CONFIG_PATH", "/tmp/airgate.yaml")
	if got := ConfigPath(); got != "/tmp/airgate.yaml" {
		t.Fatalf("配置路径 = %q，期望环境变量值", got)
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"HOST", "PORT", "GIN_MODE",
		"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME", "DB_SSLMODE",
		"REDIS_HOST", "REDIS_PORT", "REDIS_PASSWORD", "REDIS_DB",
		"JWT_SECRET", "JWT_EXPIRE_HOUR",
		"LOG_LEVEL", "LOG_FORMAT",
		"API_KEY_SECRET",
		"PLUGINS_ENABLED", "PLUGINS_DIR", "PLUGINS_HOOK_TIMEOUT_MS",
	}
	for _, key := range keys {
		t.Setenv(key, "")
	}
}
