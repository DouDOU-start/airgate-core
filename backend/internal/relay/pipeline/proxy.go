package pipeline

import (
	"net/http"
	"sync"

	"github.com/DouDOU-start/airgate-core/internal/pkg/upstreamclient"
)

// clientPool 出口 HTTP 客户端池：按 ProxyURL 缓存复用（连接池按代理隔离）。
//
// 客户端不设总超时（流式无总超时），仅设连接/TLS 层超时；
// 非流式的 5min 总超时由调用方经 context 施加。
// 重定向不跟随（upstreamclient.NewClient 统一设 ErrUseLastResponse），
// 3xx 原样进入 outcome 判定按 clientError 透传终止。
type clientPool struct {
	clients sync.Map // proxyURL string → *http.Client
}

func newClientPool() *clientPool {
	return &clientPool{}
}

// Get 返回指定出口代理的 HTTP 客户端；proxyURL 空串表示直连。
// 支持 http/https proxy 与 socks5（golang.org/x/net/proxy）。
func (p *clientPool) Get(proxyURL string) (*http.Client, error) {
	if cached, ok := p.clients.Load(proxyURL); ok {
		return cached.(*http.Client), nil
	}

	client, err := upstreamclient.NewClient(proxyURL, 0)
	if err != nil {
		return nil, err
	}
	actual, _ := p.clients.LoadOrStore(proxyURL, client)
	return actual.(*http.Client), nil
}
