package outcome

import (
	"net/url"
	"regexp"
	"strings"
)

// errorMsgMaxLen 自动禁用原因落库 error_msg 的长度上限（字节）。
const errorMsgMaxLen = 300

// upstreamMask 上游身份占位符（面向用户的脱敏替换目标）。
const upstreamMask = "[upstream]"

// upstreamURLRe / upstreamIPRe 通用上游身份形态：任意 http(s) URL、裸 IPv4[:port]。
// 用于抹掉上游错误里可能回显的主机/地址（如 "dial tcp 1.2.3.4:443" / "https://api.x.com/..."）。
var (
	upstreamURLRe = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>)]+`)
	upstreamIPRe  = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?\b`)
)

// SanitizeKeyLeak 把文本中出现的渠道明文 API Key 精确替换为掩码（sk-***+尾 4 位）。
//
// 上游 401 错误体可能回显 Authorization 凭证（部分 OpenAI 兼容中转整段回显），
// 该文本会进入服务器日志、channel.error_msg 落库与管理端响应，
// 违反「渠道 api_keys 明文永不出现在任何 API 响应」红线——出口前统一脱敏。
// 仅做精确 key 字符串替换，不改其他内容。
func SanitizeKeyLeak(s string, apiKeys []string) string {
	for _, key := range apiKeys {
		if key == "" || !strings.Contains(s, key) {
			continue
		}
		s = strings.ReplaceAll(s, key, maskAPIKey(key))
	}
	return s
}

// SanitizeUpstreamLeak 面向用户（含调度失败）的出口脱敏：在 SanitizeKeyLeak 基础上，
// 进一步抹掉可能暴露上游真实渠道身份的部分——渠道 base_url 及其主机、任意 http(s) URL、
// 裸 IP[:port]——统一替换为中性占位，确保用户无法从响应/错误里看到实际上游渠道。
//
// 仅用于用户可见的响应文本；管理端留痕（upstream log / channel error_msg）不经此处，
// 管理员需要看到渠道拓扑用于排障。
func SanitizeUpstreamLeak(s string, apiKeys []string, baseURL string) string {
	s = SanitizeKeyLeak(s, apiKeys)
	// 先整体抹掉任意 http(s) URL（含 base_url + 路径），避免只替换 base_url 主体后残留路径。
	s = upstreamURLRe.ReplaceAllString(s, upstreamMask)
	// 再替换本渠道 base_url 主体与其主机名（覆盖无 scheme 直接出现主机名的情况）。
	if baseURL != "" {
		s = strings.ReplaceAll(s, baseURL, upstreamMask)
		if u, err := url.Parse(baseURL); err == nil {
			if u.Host != "" {
				s = strings.ReplaceAll(s, u.Host, upstreamMask)
			}
			if h := u.Hostname(); h != "" && h != u.Host {
				s = strings.ReplaceAll(s, h, upstreamMask)
			}
		}
	}
	// 兜底：抹掉裸 IP[:port]（如 "dial tcp 1.2.3.4:443"）。
	s = upstreamIPRe.ReplaceAllString(s, upstreamMask)
	return s
}

// maskAPIKey 密钥掩码：sk-*** + 尾 4 位；短于 4 位时不保留尾部。
func maskAPIKey(key string) string {
	if len(key) <= 4 {
		return "sk-***"
	}
	return "sk-***" + key[len(key)-4:]
}

// TruncateErrorMsg 截断落库 error_msg（≤300 字节，按 UTF-8 字符边界回退）。
func TruncateErrorMsg(s string) string {
	if len(s) <= errorMsgMaxLen {
		return s
	}
	cut := errorMsgMaxLen
	// 避免切在多字节字符中间：回退到 UTF-8 起始字节。
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut]
}

// KeyHint 渠道密钥尾 4 位提示（明文永不落库）。
// 口径与 maskAPIKey 一致：长度 >4 保留尾 4 位。
func KeyHint(key string) string {
	if len(key) <= 4 {
		return "…"
	}
	return "…" + key[len(key)-4:]
}
