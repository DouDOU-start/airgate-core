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

// RoundTripper 在 CPA executor 注入 OAuth Token 后、真正触网前保存最终请求骨架。
// 骨架写入失败时直接返回错误；密文载荷和收尾状态由有界工作池异步补写。
type RoundTripper struct {
	base     http.RoundTripper
	request  *Handle
	target   Target
	latestMu sync.RWMutex
	latest   *observedAttempt
}

// NewRoundTripper 创建审计网络层包装器。
func NewRoundTripper(base http.RoundTripper, request *Handle, target Target) *RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &RoundTripper{base: base, request: request, target: target}
}

// MarkLatestStreamCompleted 把最新一次真实发包标记为协议级完整流。
//
// 部分上游 executor 收到 response.completed、response.incomplete 或 message_stop
// 后会立即结束读取并关闭 HTTP Body，不再等待传输层 EOF。此时 Close 本身是正常
// 收尾，不能被审计误判为上游流中断。真实读错误仍由 observedBody 原样保留。
func (t *RoundTripper) MarkLatestStreamCompleted() {
	if t == nil {
		return
	}
	t.latestMu.RLock()
	attempt := t.latest
	t.latestMu.RUnlock()
	if attempt != nil {
		attempt.markStreamCompleted()
	}
}

// RoundTrip 实现 http.RoundTripper。
func (t *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	attempt, err := t.request.BeginAttemptFast(req.Context(), t.target, req)
	if err != nil {
		return nil, fmt.Errorf("%w，已阻止发包: %v", ErrWrite, err)
	}
	observed := &observedAttempt{handle: attempt}
	t.latestMu.Lock()
	t.latest = observed
	t.latestMu.Unlock()
	started := time.Now()
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		observed.finish(AttemptFinish{
			Verdict: "networkError", Reason: err.Error(), Latency: time.Since(started),
			StreamCompleted: false,
		})
		return nil, err
	}
	statusVerdict := verdictForStatus(resp.StatusCode)
	retryAfter := retryAfterOf(resp.Header)
	if resp.Body == nil {
		observed.finish(AttemptFinish{
			StatusCode: resp.StatusCode, Verdict: statusVerdict, RetryAfter: retryAfter,
			Latency: time.Since(started), ResponseStarted: true, StreamCompleted: true,
		})
		return resp, nil
	}
	resp.Body = &observedBody{
		ReadCloser: resp.Body,
		started:    started,
		finish: func(firstTokenMs int64, complete bool, reason string) {
			observed.finish(AttemptFinish{
				StatusCode: resp.StatusCode, Verdict: statusVerdict, Reason: reason,
				RetryAfter: retryAfter, Latency: time.Since(started), FirstTokenMs: firstTokenMs,
				ResponseStarted: true, StreamCompleted: complete,
			})
		},
	}
	return resp, nil
}

const responseBodyClosedBeforeEOFReason = "响应体在 EOF 前关闭"

// observedAttempt 合并传输层观测与上层协议终态。
// MarkLatestStreamCompleted 与 Body.Close 的先后顺序都可能发生，故两侧统一在此
// 串行合并，再利用 AttemptHandle 的 revision 机制覆盖异步落库结果。
type observedAttempt struct {
	handle *AttemptHandle

	mu              sync.Mutex
	latest          AttemptFinish
	hasLatest       bool
	streamCompleted bool
}

func (a *observedAttempt) finish(in AttemptFinish) {
	if a == nil || a.handle == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.streamCompleted {
		in.StreamCompleted = true
		if in.Reason == responseBodyClosedBeforeEOFReason {
			in.Reason = ""
		}
	}
	a.latest = in
	a.hasLatest = true
	// 必须在合并锁内提交，避免协议终态覆盖刚写入后，较早的 Close 结果
	// 才进入 AttemptHandle.Finish，反向把最终状态覆盖回未完成。
	a.handle.Finish(in)
}

func (a *observedAttempt) markStreamCompleted() {
	if a == nil || a.handle == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.streamCompleted = true
	if !a.hasLatest {
		return
	}
	in := a.latest
	in.StreamCompleted = true
	if in.Reason == responseBodyClosedBeforeEOFReason {
		in.Reason = ""
	}
	a.latest = in
	a.handle.Finish(in)
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
		b.done(false, responseBodyClosedBeforeEOFReason)
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
