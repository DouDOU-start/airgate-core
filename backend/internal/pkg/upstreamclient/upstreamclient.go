// Package upstreamclient 提供出口（上游方向）HTTP 客户端构建：
// 统一连接/TLS 层超时并禁用重定向跟随。
//
// relay 管线（pipeline/tester）与渠道 fetch-models 共用此构建逻辑，
// 保证所有对上游的请求走同一套重定向语义。
package upstreamclient

import (
	"net"
	"net/http"
	"time"
)

// NewClient 构建出口 HTTP 客户端；timeout <= 0 表示无总超时
// （流式转发无总超时，非流式总超时由调用方经 context 施加）。
//
// 重定向一律不跟随（http.ErrUseLastResponse）：3xx 原样返回调用方，
// 防止对上游静默重复 POST（307/308 自动重放）、POST→GET 降级（301/302/303）
// 与跨域重定向剥掉 Authorization 头引发的 401 误判自动禁用。
func NewClient(timeout time.Duration) *http.Client {
	client := &http.Client{
		Transport: buildTransport(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if timeout > 0 {
		client.Timeout = timeout
	}
	return client
}

// buildTransport 构建出口 Transport（连接/TLS 层超时，无总超时）。
func buildTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   15 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	return &http.Transport{
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		// 响应头超时：本超时只为清除"握手后永不回头"的挂死连接，
		// 只约束到响应头到达为止，不影响流式 body 的长时间读取。
		// 非流式长生成（chat / 图像生成）首包可达数分钟，取太紧会把合法慢响应
		// 误判为网络错误触发 failover（重复打多个渠道、重复计费），故取宽松上限；
		// 死主机由 Dial 侧超时兜底。
		ResponseHeaderTimeout: 10 * time.Minute,
		ForceAttemptHTTP2:     true,
		// 空闲连接上限按高并发网关口径取值：流量高度集中在少数上游 host，
		// PerHost 低于峰值并发时超出的连接用完即弃，每请求重付 TCP+TLS 握手
		// （延迟暴涨 + TIME_WAIT/临时端口堆积）。空闲连接只占少量内存，放大无代价。
		MaxIdleConns:          2048,
		MaxIdleConnsPerHost:   512,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
