package moderation

import (
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		mustGone  []string
		mustExist []string
	}{
		{"URL", "访问 https://example.com/path?k=v 获取", []string{"example.com"}, []string{"访问"}},
		{"键值凭据", "api_key: sk-abcdef123456789 泄露", []string{"sk-abcdef123456789"}, []string{"api_key"}},
		{"Bearer", "header 是 Bearer abcdefghijklmnop", []string{"abcdefghijklmnop"}, []string{"Bearer"}},
		{"JWT", "token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9Pl", []string{"eyJhbGci"}, nil},
		{"前缀密钥", "用 sk-proj_abcdefghijklmnopqrst 调用", []string{"abcdefghijklmnopqrst"}, nil},
		{"长hex", "哈希 0123456789abcdef0123456789abcdef 记下", []string{"0123456789abcdef"}, nil},
		{"UUID", "id 是 123e4567-e89b-12d3-a456-426614174000", []string{"123e4567"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := redactSecrets(tt.text)
			for _, gone := range tt.mustGone {
				if strings.Contains(out, gone) {
					t.Fatalf("敏感串未脱敏: %q in %q", gone, out)
				}
			}
			for _, keep := range tt.mustExist {
				if !strings.Contains(out, keep) {
					t.Fatalf("普通文本被误删: %q not in %q", keep, out)
				}
			}
			if !strings.Contains(out, "[已脱敏]") {
				t.Fatalf("未出现脱敏占位: %q", out)
			}
		})
	}
}

func TestExcerptTruncates(t *testing.T) {
	long := strings.Repeat("测", 500)
	out := Excerpt(long)
	if len([]rune(out)) != maxExcerptRunes {
		t.Fatalf("截断失败: %d runes", len([]rune(out)))
	}
	if Excerpt("") != "" {
		t.Fatal("空文本应返回空")
	}
}
