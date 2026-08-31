package pluginruntime

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"

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
	case bool:
		return strconv.FormatBool(typed), nil
	case int:
		return strconv.Itoa(typed), nil
	case int8:
		return strconv.FormatInt(int64(typed), 10), nil
	case int16:
		return strconv.FormatInt(int64(typed), 10), nil
	case int32:
		return strconv.FormatInt(int64(typed), 10), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case uint:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint64:
		return strconv.FormatUint(typed, 10), nil
	case float32:
		return formatConfigFloat(float64(typed)), nil
	case float64:
		return formatConfigFloat(typed), nil
	case json.Number:
		return encodeConfigJSONNumber(typed)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
}

func encodeConfigJSONNumber(value json.Number) (string, error) {
	if value == "" {
		return "", nil
	}
	if i, err := value.Int64(); err == nil {
		return strconv.FormatInt(i, 10), nil
	}
	f, err := value.Float64()
	if err != nil {
		return "", fmt.Errorf("编码数字配置失败: %w", err)
	}
	return formatConfigFloat(f), nil
}

// formatConfigFloat 把浮点标量格式化成插件可解析的十进制字符串。
// fmt.Sprint(float64) 对 >= 1e6 的整数会输出科学计数法（如 8.388608e+06），
// 导致插件 strconv.Atoi 失败。
func formatConfigFloat(value float64) string {
	if n, ok := wholeNumberInt64(value); ok {
		return strconv.FormatInt(n, 10)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func wholeNumberInt64(value float64) (int64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) {
		return 0, false
	}
	if value < math.MinInt64 || value > math.MaxInt64 {
		return 0, false
	}
	return int64(value), true
}
