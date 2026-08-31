package account

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestBuildAccountHTTPTransportSupportsHTTPAndHTTPSProxy(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			transport, err := buildAccountHTTPTransport(scheme + "://user:pass@proxy.example:8443")
			if err != nil {
				t.Fatal(err)
			}
			proxyURL, err := transport.Proxy((&http.Request{URL: mustURL(t, "https://upstream.example")}).WithContext(context.Background()))
			if err != nil {
				t.Fatal(err)
			}
			if proxyURL == nil || proxyURL.Scheme != scheme || proxyURL.Host != "proxy.example:8443" {
				t.Fatalf("proxy URL = %v", proxyURL)
			}
			if proxyURL.User == nil {
				t.Fatal("proxy credentials were dropped")
			}
		})
	}
}

func TestBuildAccountHTTPTransportSupportsSOCKS5AndSOCKS5H(t *testing.T) {
	for _, scheme := range []string{"socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			transport, err := buildAccountHTTPTransport(scheme + "://user:pass@127.0.0.1:1080")
			if err != nil {
				t.Fatal(err)
			}
			if transport.Proxy != nil || transport.DialContext == nil {
				t.Fatalf("SOCKS transport not installed: proxy_set=%t dial_set=%t", transport.Proxy != nil, transport.DialContext != nil)
			}
		})
	}
}

func TestBuildAccountHTTPTransportRejectsUnknownProxyScheme(t *testing.T) {
	if _, err := buildAccountHTTPTransport("ftp://proxy.example:21"); err == nil {
		t.Fatal("unknown proxy scheme was accepted")
	}
	client := httpClient("ftp://proxy.example:21")
	if _, err := client.Get("https://upstream.example"); err == nil {
		t.Fatal("malformed proxy should fail closed")
	}
}
