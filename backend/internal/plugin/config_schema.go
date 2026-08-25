package plugin

// PluginConfigSchema 保存插件声明的富配置结构。
//
// ConfigSchema 仍保留 SDK v1 的扁平字段，供现有插件兼容使用；该结构用于
// 无损承载支持 widget、动态数据源和结构化默认值的新协议配置。
type PluginConfigSchema struct {
	Version string
	Fields  []PluginConfigField
}

// PluginConfigField 是管理端动态配置表单使用的通用字段声明。
type PluginConfigField struct {
	Key         string
	FallbackKey string
	Label       string
	Type        string
	Widget      string
	DataSource  string
	Required    bool
	Default     any
	Description string
	Placeholder string
	Filter      map[string]string
}

// Clone 返回一份可安全传递给上层的深拷贝。
func (schema *PluginConfigSchema) Clone() *PluginConfigSchema {
	if schema == nil {
		return nil
	}
	cloned := &PluginConfigSchema{
		Version: schema.Version,
		Fields:  make([]PluginConfigField, 0, len(schema.Fields)),
	}
	for _, field := range schema.Fields {
		field.Filter = cloneConfigStringMap(field.Filter)
		field.Default = cloneConfigDefault(field.Default)
		cloned.Fields = append(cloned.Fields, field)
	}
	return cloned
}

func cloneConfigStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneConfigDefault(value any) any {
	switch typed := value.(type) {
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = cloneConfigDefault(item)
		}
		return result
	case []string:
		return append([]string(nil), typed...)
	case []int:
		return append([]int(nil), typed...)
	case []int64:
		return append([]int64(nil), typed...)
	case []float64:
		return append([]float64(nil), typed...)
	case []bool:
		return append([]bool(nil), typed...)
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = cloneConfigDefault(item)
		}
		return result
	case map[string]string:
		return cloneConfigStringMap(typed)
	default:
		return value
	}
}
