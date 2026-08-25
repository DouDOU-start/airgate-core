package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// Context Key 常量
const CtxKeyLang = "lang"

// I18n 国际化中间件
// 从 Accept-Language 头检测语言，设置到 Context
func I18n() gin.HandlerFunc {
	return func(c *gin.Context) {
		lang := detectLanguage(c.GetHeader("Accept-Language"))
		c.Set(CtxKeyLang, lang)
		c.Next()
	}
}

// detectLanguage 从 Accept-Language 头检测语言
// 支持的语言：zh（默认）、en
func detectLanguage(header string) string {
	if header == "" {
		return "zh"
	}
	// 简单解析，取第一个语言标签
	header = strings.ToLower(header)
	// 按逗号分割多个语言偏好
	parts := strings.Split(header, ",")
	for _, part := range parts {
		// 去除权重 (q=xxx)
		lang := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if strings.HasPrefix(lang, "en") {
			return "en"
		}
		if strings.HasPrefix(lang, "zh") {
			return "zh"
		}
	}
	return "zh"
}
