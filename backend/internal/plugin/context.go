package plugin

import (
	"fmt"
	"log/slog"
	"strconv"
	"time"

	sdk "github.com/DouDOU-start/airgate-sdk"
)

// corePluginContext 核心侧的 PluginContext 实现
// 用于 Init() 调用时传递配置给插件
type corePluginContext struct {
	config sdk.PluginConfig
	logger *slog.Logger
}

// newCorePluginContext 从 DB 中的 config map 创建 PluginContext
func newCorePluginContext(config map[string]interface{}, pluginName string) *corePluginContext {
	// 将 map[string]interface{} 转换为 map[string]string
	strConfig := make(map[string]string, len(config))
	for k, v := range config {
		strConfig[k] = fmt.Sprintf("%v", v)
	}

	return &corePluginContext{
		config: &coreMapConfig{data: strConfig},
		logger: slog.Default().With("plugin", pluginName),
	}
}

func (c *corePluginContext) Logger() *slog.Logger {
	return c.logger
}

func (c *corePluginContext) Config() sdk.PluginConfig {
	return c.config
}

// coreMapConfig 基于 map 的配置实现
type coreMapConfig struct {
	data map[string]string
}

func (c *coreMapConfig) GetString(key string) string {
	return c.data[key]
}

func (c *coreMapConfig) GetInt(key string) int {
	v, _ := strconv.Atoi(c.data[key])
	return v
}

func (c *coreMapConfig) GetBool(key string) bool {
	v, _ := strconv.ParseBool(c.data[key])
	return v
}

func (c *coreMapConfig) GetFloat64(key string) float64 {
	v, _ := strconv.ParseFloat(c.data[key], 64)
	return v
}

func (c *coreMapConfig) GetDuration(key string) time.Duration {
	v, _ := time.ParseDuration(c.data[key])
	return v
}

func (c *coreMapConfig) GetAll() map[string]string {
	return c.data
}
