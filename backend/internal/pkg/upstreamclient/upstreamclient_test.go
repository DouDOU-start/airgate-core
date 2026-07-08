package upstreamclient

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	cases := []struct {
		name     string
		proxyURL string
		timeout  time.Duration
		wantErr  bool
	}{
		{"直连", "", 0, false},
		{"http 代理", "http://127.0.0.1:8080", 15 * time.Second, false},
		{"socks5 代理", "socks5://u:p@127.0.0.1:1080", 0, false},
		{"非法协议", "ftp://127.0.0.1:21", 0, true},
		{"非法 URL", "://bad", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewClient(tc.proxyURL, tc.timeout)
			if tc.wantErr {
				if err == nil {
					t.Fatal("期望报错")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewClient err = %v", err)
			}
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

func TestBuildTransportProxyModes(t *testing.T) {
	t.Run("http 代理设置 Proxy 函数", func(t *testing.T) {
		transport, err := BuildTransport("http://user:pass@127.0.0.1:8080")
		if err != nil {
			t.Fatalf("BuildTransport err = %v", err)
		}
		if transport.Proxy == nil {
			t.Fatal("http 代理应设置 Proxy")
		}
		req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
		u, err := transport.Proxy(req)
		if err != nil || u.String() != "http://user:pass@127.0.0.1:8080" {
			t.Errorf("Proxy URL = %v, err = %v", u, err)
		}
	})
	t.Run("直连不设置 Proxy", func(t *testing.T) {
		transport, err := BuildTransport("")
		if err != nil {
			t.Fatalf("BuildTransport err = %v", err)
		}
		if transport.Proxy != nil {
			t.Error("直连不应设置 Proxy")
		}
	})
}
