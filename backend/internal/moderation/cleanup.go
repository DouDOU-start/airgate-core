package moderation

import (
	"context"
	"log/slog"
	"time"
)

// cleanupLoop 日志 TTL 清理：启动 5 分钟后首跑，此后每 24h 一次。
// 命中/未命中双保留期取当前配置值。
func (e *Engine) cleanupLoop(ctx context.Context) {
	timer := time.NewTimer(cleanupDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			e.runCleanupOnce(ctx)
			timer.Reset(cleanupInterval)
		}
	}
}

func (e *Engine) runCleanupOnce(ctx context.Context) {
	cleanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	snap, err := e.loadSnapshot(cleanCtx)
	if err != nil {
		slog.Warn("moderation.cleanup_load_config_failed", "error", err)
		return
	}
	cfg := snap.config
	now := time.Now()
	result, err := e.logs.Cleanup(cleanCtx,
		now.AddDate(0, 0, -cfg.HitRetentionDays),
		now.AddDate(0, 0, -cfg.NonHitRetentionDays))
	if err != nil {
		slog.Warn("moderation.cleanup_failed", "error", err)
		return
	}
	e.lastCleanupUnix.Store(result.FinishedAt.Unix())
	e.lastCleanupDeletedHit.Store(result.DeletedHit)
	e.lastCleanupDeletedNonHit.Store(result.DeletedNonHit)
}
