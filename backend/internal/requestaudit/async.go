package requestaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const (
	defaultAsyncQueueSize       = 4096
	defaultAsyncWorkerCount     = 4
	defaultAsyncMaxPendingBytes = 256 << 20
	asyncWriteAttempts          = 3
)

// Options 控制请求审计异步工作池。
type Options struct {
	AsyncEnabled    bool
	QueueSize       int
	WorkerCount     int
	MaxPendingBytes int64
}

func normalizeOptions(options Options) Options {
	if options.QueueSize <= 0 {
		options.QueueSize = defaultAsyncQueueSize
	}
	if options.WorkerCount <= 0 {
		options.WorkerCount = defaultAsyncWorkerCount
	}
	if options.MaxPendingBytes <= 0 {
		options.MaxPendingBytes = defaultAsyncMaxPendingBytes
	}
	return options
}

type asyncJob struct {
	name          string
	retainedBytes int64
	run           func(context.Context) error
	after         func(error)
}

// AsyncStats 是审计异步工作池的实时状态。
type AsyncStats struct {
	Enabled             bool   `json:"enabled"`
	Closed              bool   `json:"closed"`
	WorkerCount         int    `json:"worker_count"`
	QueueDepth          int    `json:"queue_depth"`
	QueueCapacity       int    `json:"queue_capacity"`
	PendingJobs         int64  `json:"pending_jobs"`
	PendingBytes        int64  `json:"pending_bytes"`
	SynchronousFallback uint64 `json:"synchronous_fallback"`
	CompletedJobs       uint64 `json:"completed_jobs"`
	FailedJobs          uint64 `json:"failed_jobs"`
}

// StartBackground 启动有界审计工作池。重复调用是安全的。
func (s *Service) StartBackground() {
	if s == nil || s.db == nil || !s.options.AsyncEnabled || s.asyncClosed.Load() {
		return
	}
	s.ensureAsyncWorkers()
}

func (s *Service) ensureAsyncWorkers() {
	if s == nil || !s.options.AsyncEnabled || s.asyncClosed.Load() {
		return
	}
	s.asyncOnce.Do(func() {
		s.asyncQueue = make(chan asyncJob, s.options.QueueSize)
		s.asyncStop = make(chan struct{})
		s.asyncDone = make(chan struct{})

		var workers sync.WaitGroup
		workers.Add(s.options.WorkerCount)
		for range s.options.WorkerCount {
			go func() {
				defer workers.Done()
				s.runAsyncWorker()
			}()
		}
		go func() {
			workers.Wait()
			close(s.asyncDone)
		}()
	})
}

func (s *Service) runAsyncWorker() {
	for {
		select {
		case job := <-s.asyncQueue:
			s.executeQueuedJob(job)
		case <-s.asyncStop:
			for {
				select {
				case job := <-s.asyncQueue:
					s.executeQueuedJob(job)
				default:
					return
				}
			}
		}
	}
}

func (s *Service) executeQueuedJob(job asyncJob) {
	err := s.executeJob(job, asyncWriteAttempts)
	if job.after != nil {
		job.after(err)
	}
	s.asyncPending.Add(-1)
	if job.retainedBytes > 0 {
		s.asyncPendingBytes.Add(-job.retainedBytes)
	}
	if err != nil {
		failed := s.asyncFailed.Add(1)
		slog.Error("request_audit_async_write_failed",
			"job", job.name, "failed", failed, "error", err)
		return
	}
	s.asyncCompleted.Add(1)
}

func (s *Service) executeJob(job asyncJob, attempts int) error {
	if job.run == nil {
		return nil
	}
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		lastErr = job.run(ctx)
		cancel()
		if lastErr == nil {
			return nil
		}
		if attempt < attempts {
			time.Sleep(time.Duration(attempt*attempt) * 50 * time.Millisecond)
		}
	}
	return lastErr
}

func (s *Service) submitAsync(job asyncJob) {
	if s == nil {
		return
	}
	if !s.options.AsyncEnabled {
		s.executeSynchronously(job)
		return
	}
	s.ensureAsyncWorkers()
	s.asyncSubmitMu.RLock()
	if s.asyncClosed.Load() || s.asyncQueue == nil || !s.reservePendingBytes(job.retainedBytes) {
		s.asyncSubmitMu.RUnlock()
		s.executeSynchronously(job)
		return
	}
	s.asyncPending.Add(1)
	select {
	case s.asyncQueue <- job:
		s.asyncSubmitMu.RUnlock()
		return
	default:
		s.asyncPending.Add(-1)
		if job.retainedBytes > 0 {
			s.asyncPendingBytes.Add(-job.retainedBytes)
		}
		s.asyncSubmitMu.RUnlock()
		s.executeSynchronously(job)
	}
}

func (s *Service) reservePendingBytes(size int64) bool {
	if size <= 0 {
		return true
	}
	for {
		current := s.asyncPendingBytes.Load()
		if current+size > s.options.MaxPendingBytes {
			return false
		}
		if s.asyncPendingBytes.CompareAndSwap(current, current+size) {
			return true
		}
	}
}

func (s *Service) executeSynchronously(job asyncJob) {
	fallbacks := s.asyncFallbacks.Add(1)
	if fallbacks == 1 || fallbacks&(fallbacks-1) == 0 {
		slog.Warn("request_audit_async_fallback",
			"job", job.name, "fallbacks", fallbacks)
	}
	err := s.executeJob(job, 1)
	if job.after != nil {
		job.after(err)
	}
	if err != nil {
		failed := s.asyncFailed.Add(1)
		slog.Error("request_audit_sync_fallback_failed",
			"job", job.name, "failed", failed, "error", err)
		return
	}
	s.asyncCompleted.Add(1)
}

// Flush 等待当前已提交的审计任务全部完成。
func (s *Service) Flush(ctx context.Context) error {
	if s == nil || !s.options.AsyncEnabled {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		if s.asyncPending.Load() == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// CloseWithContext 停止接收异步任务，并在上下文期限内排空队列。
func (s *Service) CloseWithContext(ctx context.Context) error {
	if s == nil || !s.options.AsyncEnabled {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// 先通过 Once 完成通道初始化，避免与首次提交并发时遗漏工作池关闭。
	s.ensureAsyncWorkers()
	s.asyncSubmitMu.Lock()
	s.asyncClosed.Store(true)
	s.asyncClose.Do(func() { close(s.asyncStop) })
	s.asyncSubmitMu.Unlock()
	if s.asyncDone == nil {
		return nil
	}
	select {
	case <-s.asyncDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close 使用后台上下文排空并关闭异步工作池。
func (s *Service) Close() {
	_ = s.CloseWithContext(context.Background())
}

// AsyncStats 返回异步工作池状态。
func (s *Service) AsyncStats() AsyncStats {
	if s == nil {
		return AsyncStats{}
	}
	if s.options.AsyncEnabled && !s.asyncClosed.Load() {
		s.ensureAsyncWorkers()
	}
	s.asyncSubmitMu.RLock()
	defer s.asyncSubmitMu.RUnlock()
	stats := AsyncStats{
		Enabled:             s.options.AsyncEnabled,
		Closed:              s.asyncClosed.Load(),
		WorkerCount:         s.options.WorkerCount,
		PendingJobs:         s.asyncPending.Load(),
		PendingBytes:        s.asyncPendingBytes.Load(),
		SynchronousFallback: s.asyncFallbacks.Load(),
		CompletedJobs:       s.asyncCompleted.Load(),
		FailedJobs:          s.asyncFailed.Load(),
	}
	if s.asyncQueue != nil {
		stats.QueueDepth = len(s.asyncQueue)
		stats.QueueCapacity = cap(s.asyncQueue)
	} else {
		stats.QueueCapacity = s.options.QueueSize
	}
	return stats
}

func cloneRequestInput(in RequestInput) RequestInput {
	in.Headers = cloneHeader(in.Headers)
	if !in.BodyImmutable {
		in.Body = append([]byte(nil), in.Body...)
	}
	return in
}

func (s *Service) enrichRequest(ctx context.Context, id int, in RequestInput) error {
	headers, err := json.Marshal(cloneHeader(in.Headers))
	if err != nil {
		return fmt.Errorf("序列化入站请求头失败: %w", err)
	}
	headersEnc, err := s.seal(headers)
	if err != nil {
		return fmt.Errorf("加密入站请求头失败: %w", err)
	}
	bodyEnc, err := s.seal(redactBase64Images(in.Body))
	if err != nil {
		return fmt.Errorf("加密入站请求体失败: %w", err)
	}
	_, err = s.db.RequestAuditLog.UpdateOneID(id).
		SetInboundHeadersEnc(headersEnc).
		SetInboundBodyEnc(bodyEnc).
		SetInboundBodyBytes(int64(len(in.Body))).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("补写入站请求审计载荷失败: %w", err)
	}
	return nil
}

func (s *Service) enrichAttempt(ctx context.Context, id int, upstreamURL string, headers http.Header, body []byte) error {
	headerJSON, err := json.Marshal(headers)
	if err != nil {
		return fmt.Errorf("序列化上游请求头失败: %w", err)
	}
	urlEnc, err := s.seal([]byte(upstreamURL))
	if err != nil {
		return fmt.Errorf("加密上游 URL 失败: %w", err)
	}
	headersEnc, err := s.seal(headerJSON)
	if err != nil {
		return fmt.Errorf("加密上游请求头失败: %w", err)
	}
	bodyEnc, err := s.seal(redactBase64Images(body))
	if err != nil {
		return fmt.Errorf("加密上游请求体失败: %w", err)
	}
	_, err = s.db.RequestAuditAttempt.UpdateOneID(id).
		SetUpstreamURLEnc(urlEnc).
		SetForwardHeadersEnc(headersEnc).
		SetForwardBodyEnc(bodyEnc).
		SetForwardBodyBytes(int64(len(body))).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("补写上游请求审计载荷失败: %w", err)
	}
	return nil
}
