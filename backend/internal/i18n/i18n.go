// Package i18n 提供国际化支持
//
// 翻译文件 internal/i18n/locales/{zh,en}.json 通过 //go:embed 嵌入二进制
// （见 embed.go），调用方使用 LoadEmbedded() 在启动时加载，无需关心运行
// 目录中是否存在 locales/ 目录。
package i18n

import (
	"sync"
)

var (
	translations = map[string]map[string]string{}
	mu           sync.RWMutex
	defaultLang  = "zh"
)

// T 获取翻译文本
func T(lang, key string) string {
	mu.RLock()
	defer mu.RUnlock()
	if msgs, ok := translations[lang]; ok {
		if val, ok := msgs[key]; ok {
			return val
		}
	}
	// 回退到默认语言
	if msgs, ok := translations[defaultLang]; ok {
		if val, ok := msgs[key]; ok {
			return val
		}
	}
	return key
}
