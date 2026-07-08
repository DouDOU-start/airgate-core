package priceseed

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	appmodelprice "github.com/DouDOU-start/airgate-core/internal/app/modelprice"
)

// seedFile 是种子 YAML 的顶层结构。
type seedFile struct {
	Models []seedModel `yaml:"models"`
}

// seedModel 是单个模型的种子价格。字段单位见 YAML 头部注释。
type seedModel struct {
	Model           string                 `yaml:"model"`
	Input           float64                `yaml:"input"`
	Output          float64                `yaml:"output"`
	CachedInput     float64                `yaml:"cached_input"`
	CacheCreation   float64                `yaml:"cache_creation"`    // 缓存写入 5m 档
	CacheCreation1h float64                `yaml:"cache_creation_1h"` // 缓存写入 1h 档
	PerRequest      float64                `yaml:"per_request"`
	PricingExtra    map[string]interface{} `yaml:"pricing_extra"`
}

// Parse 解析种子 YAML 为 modelprice 的 CreateInput 列表。
// 空 model 名条目被跳过（视为无效行）。
func Parse(data []byte) ([]appmodelprice.CreateInput, error) {
	var file seedFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("解析种子 YAML 失败: %w", err)
	}

	items := make([]appmodelprice.CreateInput, 0, len(file.Models))
	for _, m := range file.Models {
		name := strings.TrimSpace(m.Model)
		if name == "" {
			continue
		}
		items = append(items, appmodelprice.CreateInput{
			Model:                name,
			InputPrice:           m.Input,
			OutputPrice:          m.Output,
			CachedInputPrice:     m.CachedInput,
			CacheCreationPrice:   m.CacheCreation,
			CacheCreation1hPrice: m.CacheCreation1h,
			PerRequestPrice:      m.PerRequest,
			PricingExtra:         m.PricingExtra,
		})
	}
	return items, nil
}
