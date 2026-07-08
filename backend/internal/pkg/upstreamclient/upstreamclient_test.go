package upstreamclient

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	cases := []struct {
		name    string
		timeout time.Duration
	}{
		{"无总超时", 0},
		{"带总超时", 15 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient(tc.timeout)
			if client.Timeout != tc.timeout {
				t.Errorf("Timeout = %v, want %v", client.Timeout, tc.timeout)
			}
			// 重定向策略：一律返回 ErrUseLastResponse（3xx 原样返回不跟随）。
			if client.CheckRedirect == nil {
				t.Fatal("CheckRedirect 未设置：出口 client 会静默跟随重定向")
			}
			if err := client.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
				t.Errorf("CheckRedirect = %v, want ErrUseLastResponse", err)
			}
		})
	}
}
