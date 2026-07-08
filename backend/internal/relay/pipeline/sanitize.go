package pipeline

import "strings"

// errorMsgMaxLen 自动禁用原因落库 error_msg 的长度上限（字节）。
const errorMsgMaxLen = 300

// sanitizeKeyLeak 把文本中出现的渠道明文 API Key 精确替换为掩码（sk-***+尾 4 位）。
//
// 上游 401 错误体可能回显 Authorization 凭证（部分 OpenAI 兼容中转整段回显），
// 该文本会进入服务器日志、channel.error_msg 落库与管理端响应，
// 违反「渠道 api_keys 明文永不出现在任何 API 响应」红线——出口前统一脱敏。
// 仅做精确 key 字符串替换，不改其他内容。
func sanitizeKeyLeak(s string, apiKeys []string) string {
	for _, key := range apiKeys {
		if key == "" || !strings.Contains(s, key) {
			continue
		}
		s = strings.ReplaceAll(s, key, maskAPIKey(key))
	}
	return s
}

// maskAPIKey 密钥掩码：sk-*** + 尾 4 位；短于 4 位时不保留尾部。
func maskAPIKey(key string) string {
	if len(key) <= 4 {
		return "sk-***"
	}
	return "sk-***" + key[len(key)-4:]
}

// truncateErrorMsg 截断落库 error_msg（≤300 字节，按 UTF-8 字符边界回退）。
func truncateErrorMsg(s string) string {
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
