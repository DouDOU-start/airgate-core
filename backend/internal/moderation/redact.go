package moderation

import (
	"regexp"
	"strings"
)

// secretPatterns 入库前的输入摘录脱敏：URL、键值形态凭据、Bearer、JWT、
// 前缀密钥、长 hex、长随机串、base64、UUID。
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>，。；、]+`),
	regexp.MustCompile(`(?i)\b((?:api[_-]?key|apikey|access[_-]?token|refresh[_-]?token|id[_-]?token|session[_-]?token|token|session|cookie|set[_-]?cookie|authorization|bearer|password|passwd|pwd|secret|client[_-]?secret|private[_-]?key)\s*[:=]\s*)(["']?)[^"'\s,;，。；、]{6,}`),
	regexp.MustCompile(`(?i)\b(Bearer\s+)[A-Za-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`),
	regexp.MustCompile(`(?i)\b(?:sk|sk-proj|sk-ant|sess|rk|pk|ak|api|key|token|secret)[_-][A-Za-z0-9._~+/=-]{12,}\b`),
	regexp.MustCompile(`\b[0-9a-fA-F]{32,}\b`),
	regexp.MustCompile(`\b[A-Za-z0-9_-]{48,}\b`),
	regexp.MustCompile(`\b[A-Za-z0-9+/]{48,}={0,2}\b`),
	regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`),
}

func redactSecrets(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	out := text
	for idx, pattern := range secretPatterns {
		switch idx {
		case 1:
			out = pattern.ReplaceAllString(out, `${1}${2}[已脱敏]`)
		case 2:
			out = pattern.ReplaceAllString(out, `${1}[已脱敏]`)
		default:
			out = pattern.ReplaceAllString(out, `[已脱敏]`)
		}
	}
	return out
}

// Excerpt 脱敏 + 截断为入库摘录。
func Excerpt(text string) string {
	return trimRunes(redactSecrets(text), maxExcerptRunes)
}
