package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/pkg/upstreamclient"
)

const (
	// Agent Identity JWKS is a public key-discovery endpoint.  It is deliberately
	// a fixed upstream rather than a caller-selected URL: the endpoint must stay
	// unauthenticated, but it must never become an open SSRF proxy.
	codexAgentIdentityJWKSUpstreamURL = "https://chatgpt.com/backend-api/wham/agent-identities/jwks"
	codexAgentIdentityJWKSTimeout     = 10 * time.Second
	codexAgentIdentityJWKSMaxBody     = 1 << 20
	codexAgentIdentityJWKSHeaderMax   = 1024
	codexAgentIdentityJWKSDefaultUA   = "codex_cli_rs"
	codexAgentIdentityJWKSOriginator  = "codex_cli_rs"
)

// codexAgentIdentityJWKSProxy implements the unauthenticated GET used by the
// official Codex client before it has an API-key/account request context.  It
// forwards only the public JWKS document and a small, harmless identity header
// projection; credentials, cookies, and arbitrary caller headers never cross
// the boundary.
type codexAgentIdentityJWKSProxy struct {
	client    *http.Client
	upstream  string
	maxBody   int64
	timeout   time.Duration
	allowHTTP bool // test-only; production constructor leaves this false.
}

func newCodexAgentIdentityJWKSProxy() *codexAgentIdentityJWKSProxy {
	return &codexAgentIdentityJWKSProxy{
		client: &http.Client{
			Transport: upstreamclient.NewEnvironmentTransport(),
			Timeout:   codexAgentIdentityJWKSTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		upstream: codexAgentIdentityJWKSUpstreamURL,
		maxBody:  codexAgentIdentityJWKSMaxBody,
		timeout:  codexAgentIdentityJWKSTimeout,
	}
}

// newCodexAgentIdentityJWKSProxyForTest keeps tests independent of the public
// ChatGPT service while retaining the production validation boundary.
func newCodexAgentIdentityJWKSProxyForTest(client *http.Client, upstream string) *codexAgentIdentityJWKSProxy {
	proxy := newCodexAgentIdentityJWKSProxy()
	if client != nil {
		proxy.client = client
	}
	if strings.TrimSpace(upstream) != "" {
		proxy.upstream = upstream
	}
	proxy.allowHTTP = true
	return proxy
}

func (p *codexAgentIdentityJWKSProxy) Handle(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	if c.Request.Method != http.MethodGet {
		c.Header("Allow", http.MethodGet)
		writeCodexAgentIdentityJWKSError(c, http.StatusMethodNotAllowed, "method_not_allowed", "agent identity JWKS only supports GET")
		return
	}
	if c.Request.URL != nil && c.Request.URL.RawQuery != "" {
		writeCodexAgentIdentityJWKSError(c, http.StatusBadRequest, "unsupported_query", "agent identity JWKS does not accept query parameters")
		return
	}
	// A chunked request body is represented by ContentLength == -1, so checking
	// only positive lengths would silently accept and discard it. JWKS discovery
	// never has a request payload: reject every declared, unknown-length, or
	// transfer-encoded body before constructing the credential-free upstream
	// request.
	if c.Request.ContentLength != 0 || len(c.Request.TransferEncoding) != 0 {
		writeCodexAgentIdentityJWKSError(c, http.StatusBadRequest, "unexpected_body", "agent identity JWKS does not accept a request body")
		return
	}
	if p == nil || p.client == nil || strings.TrimSpace(p.upstream) == "" || validateCodexAgentIdentityJWKSUpstreamURL(p.upstream, p.allowHTTP) != nil {
		writeCodexAgentIdentityJWKSError(c, http.StatusBadGateway, "upstream_unavailable", "agent identity JWKS proxy is unavailable")
		return
	}

	timeout := p.timeout
	if timeout <= 0 {
		timeout = codexAgentIdentityJWKSTimeout
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	upstreamRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, p.upstream, nil)
	if err != nil {
		writeCodexAgentIdentityJWKSError(c, http.StatusBadGateway, "upstream_unavailable", "agent identity JWKS proxy is unavailable")
		return
	}
	copyCodexAgentIdentityJWKSHeader(upstreamRequest.Header, c.GetHeader("Originator"), "Originator", codexAgentIdentityJWKSOriginator)
	copyCodexAgentIdentityJWKSHeader(upstreamRequest.Header, c.GetHeader("User-Agent"), "User-Agent", codexAgentIdentityJWKSDefaultUA)
	upstreamRequest.Header.Set("Accept", "application/json")

	response, err := p.client.Do(upstreamRequest)
	if err != nil {
		if c.Request.Context().Err() != nil {
			return
		}
		slog.Warn("codex_agent_identity_jwks_upstream_request_failed", "error", err, "request_id", middlewareRequestID(c))
		writeCodexAgentIdentityJWKSError(c, http.StatusBadGateway, "upstream_unavailable", "agent identity JWKS request failed")
		return
	}
	defer func() { _ = response.Body.Close() }()

	maxBody := p.maxBody
	if maxBody <= 0 {
		maxBody = codexAgentIdentityJWKSMaxBody
	}
	if response.ContentLength > maxBody {
		writeCodexAgentIdentityJWKSError(c, http.StatusBadGateway, "upstream_response_too_large", "agent identity JWKS response is too large")
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		writeCodexAgentIdentityJWKSError(c, http.StatusBadGateway, "upstream_read_error", "agent identity JWKS response could not be read")
		return
	}
	if int64(len(body)) > maxBody {
		writeCodexAgentIdentityJWKSError(c, http.StatusBadGateway, "upstream_response_too_large", "agent identity JWKS response is too large")
		return
	}

	copyCodexAgentIdentityJWKSResponseHeaders(c.Writer.Header(), response.Header)
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		if err := validateCodexAgentIdentityJWKSBody(body); err != nil {
			slog.Warn("codex_agent_identity_jwks_invalid_response", "error", err, "request_id", middlewareRequestID(c))
			writeCodexAgentIdentityJWKSError(c, http.StatusBadGateway, "invalid_upstream_response", "agent identity JWKS response is invalid")
			return
		}
	}
	contentType := c.Writer.Header().Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	// Preserve upstream status and bytes.  The official client performs its own
	// error-for-status handling, while successful responses must remain a valid
	// JWK Set with all future key fields untouched.
	c.Data(response.StatusCode, contentType, body)
}

func validateCodexAgentIdentityJWKSUpstreamURL(raw string, allowHTTP bool) error {
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
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if allowHTTP {
		if host != "chatgpt.com" && host != "chatgpt-staging.com" && host != "api.chatgpt-staging.com" && !isLoopbackHost(host) {
			return errors.New("upstream URL host is not an approved ChatGPT host")
		}
	} else if host != "chatgpt.com" {
		return errors.New("upstream URL host is not the official ChatGPT host")
	}
	if !strings.EqualFold(strings.TrimRight(u.EscapedPath(), "/"), "/backend-api/wham/agent-identities/jwks") &&
		!strings.EqualFold(strings.TrimRight(u.EscapedPath(), "/"), "/agent-identities/jwks") {
		return errors.New("upstream URL path is not an Agent Identity JWKS endpoint")
	}
	return nil
}

func validateCodexAgentIdentityJWKSBody(body []byte) error {
	var envelope struct {
		Keys json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return err
	}
	if len(envelope.Keys) == 0 || string(envelope.Keys) == "null" {
		return errors.New("JWKS body does not contain keys")
	}
	var keys []json.RawMessage
	if err := json.Unmarshal(envelope.Keys, &keys); err != nil {
		return errors.New("JWKS keys is not an array")
	}
	for _, key := range keys {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(key, &object); err != nil || object == nil {
			return errors.New("JWKS key is not an object")
		}
	}
	return nil
}

func copyCodexAgentIdentityJWKSHeader(dst http.Header, raw, name, fallback string) {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = fallback
	}
	if len(value) > codexAgentIdentityJWKSHeaderMax || !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n") {
		value = fallback
	}
	dst.Set(name, value)
}

func copyCodexAgentIdentityJWKSResponseHeaders(dst, src http.Header) {
	for _, name := range []string{
		"Content-Type", "Cache-Control", "ETag", "Last-Modified", "Expires",
		"Retry-After", "Vary", "X-Request-ID",
	} {
		for _, value := range src.Values(name) {
			if len(value) > codexAgentIdentityJWKSHeaderMax || strings.ContainsAny(value, "\r\n") {
				continue
			}
			dst.Add(name, value)
		}
	}
	if dst.Get("Cache-Control") == "" {
		dst.Set("Cache-Control", "no-store")
	}
}

func writeCodexAgentIdentityJWKSError(c *gin.Context, status int, code, message string) {
	if c == nil {
		return
	}
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{
		"type":    "invalid_request_error",
		"code":    code,
		"message": message,
	}})
}
