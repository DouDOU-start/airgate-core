package outcome

import (
	"strings"
	"testing"
)

func TestSanitizeKeyLeak(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		apiKeys []string
		want    string
	}{
		{
			name:    "上游回显完整 key 被掩码",
			input:   `{"error":{"message":"Invalid token: sk-abcdef1234567890"}}`,
			apiKeys: []string{"sk-abcdef1234567890"},
			want:    `{"error":{"message":"Invalid token: sk-***7890"}}`,
		},
		{
			name:    "多个 key 全部替换",
			input:   "k1=sk-aaaa1111 k2=sk-bbbb2222 k1又出现 sk-aaaa1111",
			apiKeys: []string{"sk-aaaa1111", "sk-bbbb2222"},
			want:    "k1=sk-***1111 k2=sk-***2222 k1又出现 sk-***1111",
		},
		{
			name:    "无 key 出现原样返回",
			input:   `{"error":{"message":"rate limited"}}`,
			apiKeys: []string{"sk-abcdef1234567890"},
			want:    `{"error":{"message":"rate limited"}}`,
		},
		{
			name:    "空 key 忽略",
			input:   "hello",
			apiKeys: []string{""},
			want:    "hello",
		},
		{
			name:    "极短 key 全掩码不留尾部",
			input:   "key=abcd end",
			apiKeys: []string{"abcd"},
			want:    "key=sk-*** end",
		},
		{
			name:    "无 key 列表原样返回",
			input:   "anything",
			apiKeys: nil,
			want:    "anything",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeKeyLeak(tc.input, tc.apiKeys); got != tc.want {
				t.Errorf("SanitizeKeyLeak = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTruncateErrorMsg(t *testing.T) {
	t.Run("短文本不截断", func(t *testing.T) {
		if got := TruncateErrorMsg("short"); got != "short" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("超长截断到上限", func(t *testing.T) {
		long := strings.Repeat("a", 500)
		got := TruncateErrorMsg(long)
		if len(got) != errorMsgMaxLen {
			t.Errorf("len = %d, want %d", len(got), errorMsgMaxLen)
		}
	})
	t.Run("多字节字符不被切断", func(t *testing.T) {
		long := strings.Repeat("错", 200) // 600 字节
		got := TruncateErrorMsg(long)
		if len(got) > errorMsgMaxLen {
			t.Errorf("len = %d, want <= %d", len(got), errorMsgMaxLen)
		}
		for _, r := range got {
			if r != '错' {
				t.Fatalf("截断破坏了 UTF-8 边界: %q", got[len(got)-6:])
			}
		}
	})
}

func TestSanitizeUpstreamLeak(t *testing.T) {
	const baseURL = "https://api.reseller.example.com/v1"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "抹掉 base_url 全串",
			in:   "failed to reach " + baseURL + "/chat/completions",
			want: "failed to reach [upstream]",
		},
		{
			name: "抹掉裸主机名（无 scheme）",
			in:   "dial tcp: lookup api.reseller.example.com: no such host",
			want: "dial tcp: lookup [upstream]: no such host",
		},
		{
			name: "抹掉裸 IP:port",
			in:   "dial tcp 203.0.113.9:443: connect: connection refused",
			want: "dial tcp [upstream]: connect: connection refused",
		},
		{
			name: "抹掉任意上游 URL",
			in:   "invalid url (POST https://cdn.other-host.net/api)",
			want: "invalid url (POST [upstream])",
		},
		{
			name: "同时抹掉密钥与主机",
			in:   "unauthorized key sk-abcdefgh1234 at api.reseller.example.com",
			want: "unauthorized key sk-***1234 at [upstream]",
		},
		{
			name: "普通业务错误保持不变",
			in:   "model not found",
			want: "model not found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeUpstreamLeak(tc.in, []string{"sk-abcdefgh1234"}, baseURL)
			if got != tc.want {
				t.Errorf("SanitizeUpstreamLeak(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
