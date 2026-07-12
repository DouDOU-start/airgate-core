package provider

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// randomNonce 生成 n 个字符的十六进制随机字符串
func randomNonce(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())[:n]
	}
	return hex.EncodeToString(b)[:n]
}

// truncate 截断字符串到最多 n 个字符，超出部分以 "..." 结尾
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
