package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/pkg/upstreamclient"
)

const (
	// The official Codex CLI uses this exact URL for its last-resort curated
	// plugin snapshot. It intentionally does not include an AirGate/API key or
	// an account-specific path. Keep the destination fixed so this public route
	// cannot become a caller-controlled SSRF proxy.
	codexCuratedPluginsExportUpstreamURL = "https://chatgpt.com/backend-api/plugins/export/curated"

	codexCuratedPluginsExportTimeout    = 30 * time.Second
	codexCuratedPluginsExportMaxBody    = 1 << 20
	codexCuratedPluginsExportHeaderMax  = 1024
	codexCuratedPluginsExportDefaultUA  = "codex_cli_rs"
	codexCuratedPluginsExportOriginator = "codex_cli_rs"
)

// codexCuratedPluginsExportProxy implements the one unauthenticated request
// made by the official startup-sync fallback. It is deliberately separate
// from Pipeline: the official request has no AirGate API key and the response
// is public snapshot metadata, not a model/account operation.
type codexCuratedPluginsExportProxy struct {
	client    *http.Client
	upstream  string
	maxBody   int64
	timeout   time.Duration
	allowHTTP bool // test-only; production constructor leaves this false.
}

func newCodexCuratedPluginsExportProxy() *codexCuratedPluginsExportProxy {
	return &codexCuratedPluginsExportProxy{
		client: &http.Client{
			Transport: upstreamclient.NewEnvironmentTransport(),
			Timeout:   codexCuratedPluginsExportTimeout,
			// A redirect would allow a compromised/misconfigured fixed upstream to
			// turn this public handler into a proxy for another host. The official
			// endpoint is expected to return metadata directly.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		upstream: codexCuratedPluginsExportUpstreamURL,
		maxBody:  codexCuratedPluginsExportMaxBody,
		timeout:  codexCuratedPluginsExportTimeout,
	}
}

// newCodexCuratedPluginsExportProxyForTest keeps tests independent from the
// public ChatGPT service while retaining the production validation boundary.
func newCodexCuratedPluginsExportProxyForTest(client *http.Client, upstream string) *codexCuratedPluginsExportProxy {
	proxy := newCodexCuratedPluginsExportProxy()
	if client != nil {
		proxy.client = client
	}
	if strings.TrimSpace(upstream) != "" {
		proxy.upstream = upstream
	}
	proxy.allowHTTP = true
	return proxy
}

// Handle is suitable for use as a Gin handler. Only GET is forwarded; all
// other methods are rejected explicitly so a known public path cannot be
// interpreted as a generic proxy operation.
func (p *codexCuratedPluginsExportProxy) Handle(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	if c.Request.Method != http.MethodGet {
		c.Header("Allow", http.MethodGet)
		writeCodexCuratedPluginsError(c, http.StatusMethodNotAllowed, "method_not_allowed", "curated plugin export only supports GET")
		return
	}
	if c.Request.URL != nil && c.Request.URL.RawQuery != "" {
		// The upstream URL is fixed and the official request has no query. Do
		// not let callers smuggle arbitrary query state into a cache key or make
		// future changes accidentally reflect user-controlled values upstream.
		writeCodexCuratedPluginsError(c, http.StatusBadRequest, "unsupported_query", "curated plugin export does not accept query parameters")
		return
	}
	if c.Request.ContentLength > 0 {
		writeCodexCuratedPluginsError(c, http.StatusBadRequest, "unexpected_body", "curated plugin export does not accept a request body")
		return
	}
	if p == nil || p.client == nil || strings.TrimSpace(p.upstream) == "" {
		writeCodexCuratedPluginsError(c, http.StatusBadGateway, "upstream_unavailable", "curated plugin export proxy is unavailable")
		return
	}
	if err := validateCodexCuratedPluginsUpstreamURL(p.upstream, p.allowHTTP); err != nil {
		slog.Error("codex_curated_plugins_upstream_invalid", "error", err)
		writeCodexCuratedPluginsError(c, http.StatusBadGateway, "upstream_unavailable", "curated plugin export proxy is unavailable")
		return
	}

	timeout := p.timeout
	if timeout <= 0 {
		timeout = codexCuratedPluginsExportTimeout
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	upstreamRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, p.upstream, nil)
	if err != nil {
		writeCodexCuratedPluginsError(c, http.StatusBadGateway, "upstream_unavailable", "curated plugin export proxy is unavailable")
		return
	}
	// Copy only the two identity headers sent by Codex's default_headers(). In
	// particular, never forward Authorization, cookies, API keys, or arbitrary
	// X-Forwarded-* values from the public caller.
	copyCodexStartupIdentityHeader(upstreamRequest.Header, c.GetHeader("Originator"), "Originator", codexCuratedPluginsExportOriginator)
	copyCodexStartupIdentityHeader(upstreamRequest.Header, c.GetHeader("User-Agent"), "User-Agent", codexCuratedPluginsExportDefaultUA)
	upstreamRequest.Header.Set("Accept", "application/json")

	response, err := p.client.Do(upstreamRequest)
	if err != nil {
		if c.Request.Context().Err() != nil {
			return
		}
		slog.Warn("codex_curated_plugins_upstream_request_failed", "error", err, "request_id", middlewareRequestID(c))
		writeCodexCuratedPluginsError(c, http.StatusBadGateway, "upstream_unavailable", "curated plugin export request failed")
		return
	}
	defer func() { _ = response.Body.Close() }()

	maxBody := p.maxBody
	if maxBody <= 0 {
		maxBody = codexCuratedPluginsExportMaxBody
	}
	if response.ContentLength > maxBody {
		writeCodexCuratedPluginsError(c, http.StatusBadGateway, "upstream_response_too_large", "curated plugin export response is too large")
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		writeCodexCuratedPluginsError(c, http.StatusBadGateway, "upstream_read_error", "curated plugin export response could not be read")
		return
	}
	if int64(len(body)) > maxBody {
		writeCodexCuratedPluginsError(c, http.StatusBadGateway, "upstream_response_too_large", "curated plugin export response is too large")
		return
	}

	copyCodexCuratedResponseHeaders(c.Writer.Header(), response.Header)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		// Preserve the upstream status/body for the CLI's fallback decision, but
		// keep the body bounded and strip stateful response headers above.
		if c.Writer.Header().Get("Content-Type") == "" {
			c.Writer.Header().Set("Content-Type", "application/json")
		}
		c.Data(response.StatusCode, c.Writer.Header().Get("Content-Type"), body)
		return
	}
	if err := validateCodexCuratedPluginsMetadata(body, p.allowHTTP); err != nil {
		slog.Warn("codex_curated_plugins_invalid_metadata", "error", err, "request_id", middlewareRequestID(c))
		writeCodexCuratedPluginsError(c, http.StatusBadGateway, "invalid_upstream_response", "curated plugin export metadata is invalid")
		return
	}
	if c.Writer.Header().Get("Content-Type") == "" {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
	// Metadata contains the provider-signed archive URL. Keep the JSON shape
	// byte-for-byte intact so current and future CLI fields remain compatible;
	// URL validation above prevents non-HTTPS/local targets from being emitted.
	c.Data(response.StatusCode, c.Writer.Header().Get("Content-Type"), body)
}

func validateCodexCuratedPluginsUpstreamURL(raw string, allowHTTP bool) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("upstream URL must be an absolute URL without credentials, query, or fragment")
	}
	if allowHTTP {
		if u.Scheme != "http" && u.Scheme != "https" {
			return errors.New("upstream URL must use http or https")
		}
	} else if u.Scheme != "https" {
		return errors.New("upstream URL must use https")
	}
	// Production traffic is pinned to ChatGPT's official host. The only
	// exception is the explicit test-only HTTP mode, where httptest servers
	// bind to loopback; accepting arbitrary remote hosts here would turn a
	// future configuration mistake into an SSRF primitive.
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host != "chatgpt.com" && !(allowHTTP && isLoopbackHost(host)) {
		return errors.New("upstream URL host is not the official ChatGPT host")
	}
	if !strings.EqualFold(strings.TrimRight(u.EscapedPath(), "/"), "/backend-api/plugins/export/curated") {
		return errors.New("upstream URL path is not the curated plugin export endpoint")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	parsed := net.ParseIP(host)
	return parsed != nil && parsed.IsLoopback()
}

func validateCodexCuratedPluginsMetadata(body []byte, allowHTTP bool) error {
	var metadata struct {
		DownloadURL string `json:"download_url"`
	}
	if err := json.Unmarshal(body, &metadata); err != nil {
		return fmt.Errorf("metadata is not valid JSON: %w", err)
	}
	if strings.TrimSpace(metadata.DownloadURL) == "" {
		return errors.New("metadata does not contain download_url")
	}
	u, err := url.Parse(strings.TrimSpace(metadata.DownloadURL))
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.Fragment != "" || !utf8.ValidString(metadata.DownloadURL) {
		return errors.New("download_url must be an absolute URL without credentials or fragment")
	}
	// Even in the test-only upstream mode, the archive URL emitted to a CLI
	// must remain HTTPS. `allowHTTP` exists solely to let tests use an HTTP
	// httptest origin for the metadata endpoint itself.
	if u.Scheme != "https" {
		return errors.New("download_url must use https")
	}
	return nil
}

func copyCodexStartupIdentityHeader(dst http.Header, raw, name, fallback string) {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = fallback
	}
	if len(value) > codexCuratedPluginsExportHeaderMax || !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n") {
		value = fallback
	}
	dst.Set(name, value)
}

func copyCodexCuratedResponseHeaders(dst, src http.Header) {
	for _, name := range []string{
		"Content-Type", "Cache-Control", "ETag", "Last-Modified", "Expires",
		"Retry-After", "Vary", "X-Request-ID",
	} {
		values := src.Values(name)
		for _, value := range values {
			if len(value) > codexCuratedPluginsExportHeaderMax || strings.ContainsAny(value, "\r\n") {
				continue
			}
			dst.Add(name, value)
		}
	}
	// A signed archive URL should not be cached by shared intermediaries longer
	// than the upstream lease. Respect an explicit upstream policy; otherwise
	// make the safe default explicit.
	if dst.Get("Cache-Control") == "" {
		dst.Set("Cache-Control", "no-store")
	}
}

func writeCodexCuratedPluginsError(c *gin.Context, status int, code, message string) {
	if c == nil {
		return
	}
	c.AbortWithStatusJSON(status, gin.H{
		"error": gin.H{
			"type":    "invalid_request_error",
			"code":    code,
			"message": message,
		},
	})
}

// Tiny indirection keeps this file independent from middleware's concrete
// request-id implementation while still avoiding accidental credential logs.
func middlewareRequestID(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	return c.GetHeader("X-Request-ID")
}
