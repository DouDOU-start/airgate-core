package account

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	xproxy "golang.org/x/net/proxy"
)

// httpClient is shared by OAuth, session import, usage probes, and account
// connectivity tests.  Keep proxy construction in one place so every
// credential path supports the same proxy schemes and authentication rules.
// A malformed proxy is represented by a failing RoundTripper instead of
// silently falling back to a direct connection (which could leak credentials).
func httpClient(proxyURL string) *http.Client {
	client := &http.Client{Timeout: 60 * time.Second}
	transport, err := buildAccountHTTPTransport(proxyURL)
	if err != nil {
		client.Transport = failingRoundTripper{err: err}
		return client
	}
	client.Transport = transport
	return client
}

func buildAccountHTTPTransport(proxyURL string) (*http.Transport, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if ok {
		base = base.Clone()
	} else {
		base = &http.Transport{}
	}

	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return base, nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("invalid proxy_url")
	}
	if u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("invalid proxy_url")
	}
	// A proxy URL is an endpoint, not a request target. Normalize a harmless
	// root path away so equivalent values produce the same transport behavior.
	u.Path = ""
	u.RawPath = ""

	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		// net/http supports both HTTP and TLS-wrapped HTTP proxy URLs and
		// handles Proxy-Authorization from URL userinfo.
		base.Proxy = http.ProxyURL(u)
		return base, nil
	case "socks", "socks5h":
		u.Scheme = "socks5h"
		fallthrough
	case "socks5", "socks5-tcp":
		// x/net/proxy's SOCKS implementation sends domain names to the proxy
		// when the target is not an IP, which gives socks5h semantics and is
		// also safe for the usual socks5 configuration used by AirGate.
		var auth *xproxy.Auth
		if u.User != nil {
			password, _ := u.User.Password()
			auth = &xproxy.Auth{User: u.User.Username(), Password: password}
		}
		dialer, err := xproxy.SOCKS5("tcp", u.Host, auth, xproxy.Direct)
		if err != nil {
			return nil, err
		}
		base.Proxy = nil
		base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			if contextDialer, ok := dialer.(xproxy.ContextDialer); ok {
				return contextDialer.DialContext(ctx, network, address)
			}
			// Older x/net versions expose only Dial.  Preserve compatibility;
			// request cancellation is still observed by the HTTP transport after
			// the connection is established.
			return dialer.Dial(network, address)
		}
		return base, nil
	default:
		return nil, errors.New("unsupported proxy scheme: " + u.Scheme)
	}
}

type failingRoundTripper struct{ err error }

func (t failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	if t.err != nil {
		return nil, t.err
	}
	return nil, errors.New("http transport unavailable")
}
