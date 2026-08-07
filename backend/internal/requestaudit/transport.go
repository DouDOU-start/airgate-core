package requestaudit

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RoundTripper 在 CPA executor 注入 OAuth Token 后、真正触网前保存最终请求。
// 审计写入失败时直接返回错误，保证不会出现“已发包但无记录”。
type RoundTripper struct {
	base    http.RoundTripper
	request *Handle
	target  Target
}

// NewRoundTripper 创建审计网络层包装器。
func NewRoundTripper(base http.RoundTripper, request *Handle, target Target) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &RoundTripper{base: base, request: request, target: target}
}

// RoundTrip 实现 http.RoundTripper。
func (t *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	attempt, err := t.request.BeginAttempt(req.Context(), t.target, req)
	if err != nil {
		return nil, fmt.Errorf("%w，已阻止发包: %v", ErrWrite, err)
	}
	started := time.Now()
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		attempt.Finish(AttemptFinish{
			Verdict: "networkError", Reason: err.Error(), Latency: time.Since(started),
			StreamCompleted: false,
		})
		return nil, err
	}
	statusVerdict := verdictForStatus(resp.StatusCode)
	retryAfter := retryAfterOf(resp.Header)
	if resp.Body == nil {
		attempt.Finish(AttemptFinish{
			StatusCode: resp.StatusCode, Verdict: statusVerdict, RetryAfter: retryAfter,
			Latency: time.Since(started), ResponseStarted: true, StreamCompleted: true,
		})
		return resp, nil
	}
	resp.Body = &observedBody{
		ReadCloser: resp.Body,
		started:    started,
		finish: func(firstTokenMs int64, complete bool, reason string) {
			attempt.Finish(AttemptFinish{
				StatusCode: resp.StatusCode, Verdict: statusVerdict, Reason: reason,
				RetryAfter: retryAfter, Latency: time.Since(started), FirstTokenMs: firstTokenMs,
				ResponseStarted: true, StreamCompleted: complete,
			})
		},
	}
	return resp, nil
}

type observedBody struct {
	io.ReadCloser
	started      time.Time
	firstTokenMs int64
	finish       func(int64, bool, string)
	once         sync.Once
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 && b.firstTokenMs == 0 {
		b.firstTokenMs = time.Since(b.started).Milliseconds()
	}
	if err != nil {
		if err == io.EOF {
			b.done(true, "")
		} else {
			b.done(false, err.Error())
		}
	}
	return n, err
}

func (b *observedBody) Close() error {
	err := b.ReadCloser.Close()
	if err != nil {
		b.done(false, err.Error())
	} else {
		b.done(false, "响应体在 EOF 前关闭")
	}
	return err
}

func (b *observedBody) done(complete bool, reason string) {
	b.once.Do(func() { b.finish(b.firstTokenMs, complete, reason) })
}

func verdictForStatus(status int) string {
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

func retryAfterOf(headers http.Header) time.Duration {
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		return max(time.Until(when), 0)
	}
	return 0
}
