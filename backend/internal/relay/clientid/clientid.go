// Package clientid 从请求头识别客户端类型（Claude Code / Codex）。
// 识别结果经 gin context 传递，供转发管线分组限制检查使用。
package clientid

import (
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// 客户端类型常量，与 Group.allowed_clients 值域一致。
const (
	ClaudeCode = "claude_code"
	Codex      = "codex"
)

const ctxKey = "x-client-type"

var claudeCodeUA = regexp.MustCompile(`(?i)^claude-cli/\d+\.\d+`)

// Detect 从请求头识别客户端类型并存入 gin context。
// 识别顺序：Claude Code → Codex → 空（普通客户端）。
func Detect(c *gin.Context) {
	ua := c.GetHeader("User-Agent")

	if claudeCodeUA.MatchString(ua) {
		c.Set(ctxKey, ClaudeCode)
		return
	}

	if isCodex(ua, c) {
		c.Set(ctxKey, Codex)
		return
	}
}

// Get 从 gin context 取客户端类型，空串表示普通客户端。
func Get(c *gin.Context) string {
	v, _ := c.Get(ctxKey)
	s, _ := v.(string)
	return s
}

// isCodex 检测 Codex 客户端。
// Codex CLI 的 UA 形如 "codex/0.1.0" 或 "openai-codex-..."，
// 且真实 Codex 请求会携带 x-codex-* 前缀的引擎指纹头。
func isCodex(ua string, c *gin.Context) bool {
	uaLower := strings.ToLower(ua)
	if strings.HasPrefix(uaLower, "codex/") || strings.Contains(uaLower, "codex-cli") {
		return true
	}

	for key := range c.Request.Header {
		if strings.HasPrefix(strings.ToLower(key), "x-codex-") {
			return true
		}
	}
	return false
}

// Matches 检查当前客户端是否匹配 allowed 列表中的任一类型。
// allowed 为空时视为不限制，返回 true。
func Matches(clientType string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if a == clientType {
			return true
		}
	}
	return false
}
