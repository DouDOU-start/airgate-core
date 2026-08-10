package pipeline

import (
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"

	"github.com/DouDOU-start/airgate-core/internal/pkg/upstreamclient"
)

const maxAccountTransports = 1024

type accountTransportEntry struct {
	transport http.RoundTripper
	err       error
}

// accountAuditTransport 为每个代理配置返回稳定的 Transport，
// 让带审计的账号流量能够复用 TCP/TLS 连接。
func (p *Pipeline) accountAuditTransport(rawProxyURL string) (http.RoundTripper, error) {
	proxyURL := strings.TrimSpace(rawProxyURL)
	if proxyURL == "" {
		if p.accountDirectTransport != nil {
			return p.accountDirectTransport, nil
		}
		return upstreamclient.NewEnvironmentTransport(), nil
	}
	if cached, ok := p.accountTransports.Load(proxyURL); ok {
		entry := cached.(accountTransportEntry)
		return entry.transport, entry.err
	}

	p.accountTransportMu.Lock()
	defer p.accountTransportMu.Unlock()
	if cached, ok := p.accountTransports.Load(proxyURL); ok {
		entry := cached.(accountTransportEntry)
		return entry.transport, entry.err
	}

	transport, _, err := proxyutil.BuildHTTPTransport(proxyURL)
	var baseTransport http.RoundTripper
	if transport != nil {
		upstreamclient.TuneTransport(transport)
		baseTransport = transport
	} else if err == nil {
		baseTransport = p.accountDirectTransport
		if baseTransport == nil {
			baseTransport = upstreamclient.NewEnvironmentTransport()
		}
	}
	entry := accountTransportEntry{transport: baseTransport, err: err}
	if p.accountTransportCount.Load() >= maxAccountTransports {
		p.accountTransports.Range(func(_, value any) bool {
			if cachedTransport, ok := value.(accountTransportEntry).transport.(*http.Transport); ok {
				cachedTransport.CloseIdleConnections()
			}
			return true
		})
		p.accountTransports.Clear()
		p.accountTransportCount.Store(0)
	}
	p.accountTransports.Store(proxyURL, entry)
	p.accountTransportCount.Add(1)
	return entry.transport, entry.err
}
