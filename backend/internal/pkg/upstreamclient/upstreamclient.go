// Package upstreamclient 提供出口（上游方向）HTTP 客户端构建：
// 按渠道出口代理（http/https/socks5）构建 Transport，并统一禁用重定向跟随。
//
// relay 管线（pipeline/tester）与渠道 fetch-models 共用此构建逻辑，
// 保证所有对上游的请求走同一套代理与重定向语义。
package upstreamclient

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	xproxy "golang.org/x/net/proxy"
)

// NewClient 构建出口 HTTP 客户端；proxyURL 空串表示直连，timeout <= 0 表示无总超时
// （流式转发无总超时，非流式总超时由调用方经 context 施加）。
//
// 重定向一律不跟随（http.ErrUseLastResponse）：3xx 原样返回调用方，
// 防止对上游静默重复 POST（307/308 自动重放）、POST→GET 降级（301/302/303）
// 与跨域重定向剥掉 Authorization 头引发的 401 误判自动禁用。
func NewClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	transport, err := BuildTransport(proxyURL)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if timeout > 0 {
		client.Timeout = timeout
	}
	return client, nil
}

// BuildTransport 构建按代理隔离的 Transport（连接/TLS 层超时，无总超时）。
// 支持 http/https proxy 与 socks5（golang.org/x/net/proxy）。
func BuildTransport(proxyURL string) (*http.Transport, error) {
	dialer := &net.Dialer{
		Timeout:   15 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if proxyURL == "" {
		return transport, nil
	}

	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("解析代理地址失败: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
		transport.Proxy = http.ProxyURL(u)
	case "socks5", "socks5h":
		socksDialer, err := xproxy.FromURL(u, dialer)
		if err != nil {
			return nil, fmt.Errorf("构建 socks5 代理失败: %w", err)
		}
		ctxDialer, ok := socksDialer.(xproxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("socks5 代理不支持 context 拨号")
		}
		transport.DialContext = ctxDialer.DialContext
	default:
		return nil, fmt.Errorf("不支持的代理协议: %s", u.Scheme)
	}
	return transport, nil
}
