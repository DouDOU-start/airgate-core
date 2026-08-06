package pluginruntime

import (
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
)

// loadPluginConfig 读取插件目录旁的 YAML 配置。顶层标量按字符串传递，数组和对象
// 编码为 JSON，使独立插件可以通过 map[string]string 读取结构化规则。
func loadPluginConfig(path, logLevel string) (map[string]string, error) {
	result := map[string]string{protocol.ConfigKeyLogLevel: logLevel}
	if path == "" {
		return result, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, fmt.Errorf("读取插件配置失败: %w", err)
	}

	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("解析插件配置失败: %w", err)
	}
	for key, value := range raw {
		encoded, err := encodeConfigValue(value)
		if err != nil {
			return nil, fmt.Errorf("编码插件配置项 %s 失败: %w", key, err)
		}
		result[key] = encoded
	}
	return result, nil
}

func encodeConfigValue(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case bool, int, int64, uint64, float64:
		return fmt.Sprint(typed), nil
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
}
