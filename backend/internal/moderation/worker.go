package moderation

import (
	"context"
	"log/slog"
	"time"
)

// asyncTask 异步队列的两类任务：log 非空为「纯记录」（pre_block 阻断后的
// 落库+副作用转异步）；否则为 observe 审核任务（调外部 API，恒不阻断）。
type asyncTask struct {
	input      CheckRequest
	content    Input
	inputHash  string
	log        *LogEntry
	config     *Config
	recordHash bool
	sideFx     bool
	enqueuedAt time.Time
}

func (e *Engine) enqueueAudit(in CheckRequest, content Input, hashText string) {
	e.enqueue(asyncTask{input: in, content: content, inputHash: hashText, enqueuedAt: time.Now()})
}

func (e *Engine) enqueueRecord(cfg *Config, entry LogEntry, recordHash, sideFx bool) {
	e.enqueue(asyncTask{
		input:      CheckRequest{UserID: entry.UserID, Endpoint: entry.Endpoint},
		inputHash:  entry.InputHash,
		log:        &entry,
		config:     cfg.Clone(),
		recordHash: recordHash,
		sideFx:     sideFx,
		enqueuedAt: time.Now(),
	})
}

func (e *Engine) enqueue(task asyncTask) {
	queueSize := defaultQueueSize
	if snap := e.snapshot.Load(); snap != nil && snap.config.QueueSize > 0 {
		queueSize = snap.config.QueueSize
	}
	if len(e.asyncQueue) >= queueSize {
		slog.Warn("moderation.async_queue_full", "user_id", task.input.UserID, "queue_size", queueSize)
		e.asyncDropped.Add(1)
		return
	}
	select {
	case e.asyncQueue <- task:
		e.asyncEnqueued.Add(1)
	default:
		e.asyncDropped.Add(1)
	}
}

// StartBackground 拉起异步 worker 池与日志 TTL 清理循环，随 ctx 取消优雅退出。
// worker 按最大规格常驻，id >= 配置 WorkerCount 的 worker 空转让出（动态缩容）。
func (e *Engine) StartBackground(ctx context.Context) {
	if e == nil || e.src == nil || e.logs == nil {
		return
	}
	for i := 0; i < maxWorkerCount; i++ {
		go e.worker(ctx, i)
	}
	go e.cleanupLoop(ctx)
}

func (e *Engine) worker(ctx context.Context, id int) {
	for {
		if ctx.Err() != nil {
			return
		}
		snap, err := e.loadSnapshot(ctx)
		if err != nil || id >= snap.config.WorkerCount {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		cfg := snap.config
		task, ok := e.dequeue(ctx, time.Second)
		if !ok {
			continue
		}
		e.runTask(ctx, cfg, task)
	}
}

func (e *Engine) runTask(ctx context.Context, cfg *Config, task asyncTask) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("moderation.worker_panic", "recover", r)
		}
	}()
	taskCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), maxTimeoutMS*time.Millisecond+10*time.Second)
	defer cancel()
	if task.log != nil {
		e.asyncActive.Add(1)
		defer e.asyncActive.Add(-1)
		task.log.QueueDelayMS = time.Since(task.enqueuedAt).Milliseconds()
		taskCfg := task.config
		if taskCfg == nil {
			taskCfg = cfg
		}
		e.persistLog(taskCtx, taskCfg, *task.log, task.recordHash, task.sideFx)
		e.asyncProcessed.Add(1)
		return
	}
	// observe 审核任务：执行前按最新配置复查开关与作用域（入队到出队之间配置可能已变）。
	if !cfg.Enabled || cfg.Mode == ModeOff || len(cfg.APIKeys) == 0 {
		return
	}
	if !cfg.includesGroup(task.input.GroupID) || !cfg.includesModel(task.input.Model) {
		return
	}
	e.asyncActive.Add(1)
	defer e.asyncActive.Add(-1)
	queueDelay := time.Since(task.enqueuedAt).Milliseconds()
	if queueDelay <= 0 {
		queueDelay = 1
	}
	_ = e.checkSync(taskCtx, task.input, cfg, task.content, task.inputHash, queueDelay, false)
	e.asyncProcessed.Add(1)
}

func (e *Engine) dequeue(ctx context.Context, idleWait time.Duration) (asyncTask, bool) {
	var zero asyncTask
	timer := time.NewTimer(idleWait)
	defer timer.Stop()
	select {
	case task, ok := <-e.asyncQueue:
		return task, ok
	case <-ctx.Done():
		return zero, false
	case <-timer.C:
		return zero, false
	}
}
