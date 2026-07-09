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
	Tag             string                 `yaml:"tag"` // 标签名（家族归类，可空）
	Input           float64                `yaml:"input"`
	Output          float64                `yaml:"output"`
	CachedInput     float64                `yaml:"cached_input"`
	CacheCreation   float64                `yaml:"cache_creation"`    // 缓存写入 5m 档
	CacheCreation1h float64                `yaml:"cache_creation_1h"` // 缓存写入 1h 档
	PerRequest      float64                `yaml:"per_request"`
	PricingExtra    map[string]interface{} `yaml:"pricing_extra"`
}

// SeedItem 单条种子：价格输入 + 标签名（可空；导入时按名称 find-or-create）。
type SeedItem struct {
	appmodelprice.CreateInput
	TagName string
}

// Parse 解析种子 YAML 为 SeedItem 列表。
// 空 model 名条目被跳过（视为无效行）。
func Parse(data []byte) ([]SeedItem, error) {
	var file seedFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("解析种子 YAML 失败: %w", err)
	}

	items := make([]SeedItem, 0, len(file.Models))
	for _, m := range file.Models {
		name := strings.TrimSpace(m.Model)
		if name == "" {
			continue
		}
		items = append(items, SeedItem{
			CreateInput: appmodelprice.CreateInput{
				Model:                name,
				InputPrice:           m.Input,
				OutputPrice:          m.Output,
				CachedInputPrice:     m.CachedInput,
				CacheCreationPrice:   m.CacheCreation,
				CacheCreation1hPrice: m.CacheCreation1h,
				PerRequestPrice:      m.PerRequest,
				PricingExtra:         m.PricingExtra,
			},
			TagName: strings.TrimSpace(m.Tag),
		})
	}
	return items, nil
}
