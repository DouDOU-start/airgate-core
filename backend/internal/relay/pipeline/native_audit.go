package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/DouDOU-start/airgate-core/internal/requestaudit"
)

type providerAuditContextKey struct{}

func withProviderAuditSink(ctx context.Context, sink providertransport.UpstreamAuditSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, providerAuditContextKey{}, sink)
}

func providerAuditSinkFromContext(ctx context.Context) providertransport.UpstreamAuditSink {
	if ctx == nil {
		return nil
	}
	sink, _ := ctx.Value(providerAuditContextKey{}).(providertransport.UpstreamAuditSink)
	return sink
}

type nativeAuditSink struct {
	handle *requestaudit.Handle
	target requestaudit.Target
}

func newNativeAuditSink(handle *requestaudit.Handle, target requestaudit.Target) providertransport.UpstreamAuditSink {
	if handle == nil {
		return nil
	}
	return &nativeAuditSink{handle: handle, target: target}
}

func (s *nativeAuditSink) BeginUpstreamAttempt(ctx context.Context, in providertransport.UpstreamAuditRequest) (providertransport.UpstreamAuditAttempt, error) {
	if s == nil || s.handle == nil {
		return nil, nil
	}
	method := strings.TrimSpace(in.Method)
	if method == "" {
		method = http.MethodPost
	}
	// Native executor audit events are emitted immediately before the real
	// provider request.  Remote Control requests are control-plane messages and
	// may contain pairing codes, bearer tokens, or installation metadata.  Keep
	// the provider body untouched while passing a redacted copy to the audit
	// store; this is the final persistence boundary for out-of-process native
	// transports.
	auditURL := sanitizeNativeAuditURL(in.URL)
	auditBody := redactNativeAuditBody(auditURL, in.Body)
	req, err := http.NewRequestWithContext(ctx, method, auditURL, bytes.NewReader(auditBody))
	if err != nil {
		return nil, err
	}
	req.Header = cloneNativeAuditHeaders(in.Headers)
	attempt, err := s.handle.BeginAttemptFast(ctx, s.target, req)
	if err != nil {
		return nil, err
	}
	return &nativeAuditAttempt{attempt: attempt, started: time.Now()}, nil
}

type nativeAuditAttempt struct {
	attempt *requestaudit.AttemptHandle
	started time.Time
	once    sync.Once
}

func (a *nativeAuditAttempt) FinishUpstreamAttempt(in providertransport.UpstreamAuditResult) {
	if a == nil || a.attempt == nil {
		return
	}
	a.once.Do(func() {
		latency := time.Duration(in.LatencyMs) * time.Millisecond
		if latency <= 0 {
			latency = time.Since(a.started)
		}
		verdict := nativeAuditVerdict(in.StatusCode, in.NetworkError)
		reason := ""
		if in.ErrorCode != "" {
			reason = in.ErrorCode
		}
		a.attempt.Finish(requestaudit.AttemptFinish{
			StatusCode: in.StatusCode, Verdict: verdict, Reason: reason,
			RetryAfter: parseNativeRetryAfter(in.RetryAfter), Latency: latency,
			FirstTokenMs: in.FirstTokenMs, ResponseStarted: in.ResponseStarted,
			StreamCompleted: in.StreamCompleted,
		})
	})
}

func nativeAuditVerdict(status int, networkError bool) string {
	if networkError {
		return "networkError"
	}
	switch {
	case status >= 200 && status < 300:
		return "success"
	case status == http.StatusTooManyRequests:
		return "rateLimited"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "authFailed"
	case status >= 500:
		return "transient"
	default:
		return "clientError"
	}
}

func parseNativeRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := time.ParseDuration(value + "s"); err == nil {
		return seconds
	}
	if when, err := http.ParseTime(value); err == nil {
		if remaining := time.Until(when); remaining > 0 {
			return remaining
		}
	}
	return 0
}

func cloneNativeAuditHeaders(in http.Header) http.Header {
	out := make(http.Header)
	for key, values := range in {
		lower := strings.ToLower(key)
		if isNativeAuditSecretHeader(lower) {
			continue
		}
		for _, value := range values {
			out.Add(key, value)
		}
	}
	return out
}

// sanitizeNativeAuditURL removes credential-shaped query values and userinfo
// from the URL persisted in a native audit attempt.  The URL used by the
// actual provider request is never passed through this helper.
func sanitizeNativeAuditURL(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return raw
	}
	query := u.Query()
	for key := range query {
		if isNativeAuditSecretQueryKey(key) {
			query.Set(key, "[redacted]")
		}
	}
	u.RawQuery = query.Encode()
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	u.User = nil
	return u.String()
}

// redactNativeAuditBody returns a persistence-only copy of a native request
// body.  Remote Control payloads are JSON-RPC/control-plane data rather than
// model input; redact known credential and device-pairing fields recursively.
// If a future client sends a malformed/non-JSON body, fail closed with a
// marker instead of storing opaque bytes that may contain a secret.
func redactNativeAuditBody(rawURL string, body []byte) []byte {
	if len(body) == 0 || !isNativeRemoteControlURL(rawURL) {
		return append([]byte(nil), body...)
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return []byte(`[redacted remote-control body]`)
	}
	redactNativeAuditValue(value)
	redacted, err := json.Marshal(value)
	if err != nil {
		return []byte(`[redacted remote-control body]`)
	}
	return redacted
}

func redactNativeAuditValue(value any) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if isNativeAuditSensitiveField(key) {
				current[key] = "[redacted]"
				continue
			}
			redactNativeAuditValue(child)
		}
	case []any:
		for _, child := range current {
			redactNativeAuditValue(child)
		}
	}
}

func isNativeRemoteControlURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return false
	}
	path := strings.ToLower(strings.TrimRight(u.Path, "/"))
	return strings.Contains(path, "/remote/control/") || strings.HasSuffix(path, "/remote/control")
}

func isNativeAuditSecretQueryKey(key string) bool {
	normalized := normalizeNativeAuditKey(key)
	if normalized == "" {
		return false
	}
	return strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "credential") ||
		strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "authorization") ||
		normalized == "apikey" || normalized == "pairingcode" || normalized == "manualcode"
}

func isNativeAuditSecretHeader(key string) bool {
	normalized := normalizeNativeAuditKey(key)
	if normalized == "" {
		return true
	}
	switch normalized {
	case "host", "contentlength", "authorization", "proxyauthorization", "cookie", "cookie2", "setcookie", "apikey", "xapikey", "xopenaapikey":
		return true
	}
	return strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "credential") ||
		strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "authorization") ||
		strings.Contains(normalized, "pairingcode") || strings.Contains(normalized, "manualcode") ||
		strings.HasSuffix(normalized, "accountid") ||
		strings.HasSuffix(normalized, "serverid") || strings.HasSuffix(normalized, "environmentid") ||
		strings.HasSuffix(normalized, "installationid") || strings.HasSuffix(normalized, "servername") ||
		strings.HasSuffix(normalized, "hostname") || strings.HasSuffix(normalized, "hostdevicekind") ||
		strings.HasSuffix(normalized, "subscribecursor")
}

func isNativeAuditSensitiveField(key string) bool {
	normalized := normalizeNativeAuditKey(key)
	if normalized == "" {
		return false
	}
	if isNativeAuditSecretQueryKey(normalized) {
		return true
	}
	// Device/server metadata is not an authentication credential, but it is
	// still private account/environment state and is unnecessary for request
	// diagnosis. Redact it on Remote Control payloads as well.
	switch normalized {
	case "accountid", "serverid", "environmentid", "installationid", "servername", "hostname", "name", "protocolversion", "hostdevicekind", "subscribecursor":
		return true
	default:
		return false
	}
}

func normalizeNativeAuditKey(key string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(key)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
