package plugin

import (
	"context"
	"log/slog"
	"time"

	sdkgrpc "github.com/DouDOU-start/airgate-sdk/runtimego/grpc"
)

// minBackgroundInterval 兜底最小间隔，避免插件声明 0 / 极小间隔时把 Core 打爆。
const minBackgroundInterval = 30 * time.Second

// taskRunTimeout 单次任务调用的硬超时；防止插件 handler 卡死把 goroutine 永远阻塞。
const taskRunTimeout = 5 * time.Minute

// startExtensionBackgroundTasks 查询 extension 插件声明的后台任务，并为每个任务
// 起一个独立的 goroutine + ticker 周期触发。所有 goroutine 共享一个 cancelable
// context，stopPlugin 时统一取消。
//
// 设计要点：
//   - gRPC 边界上 Handler 函数无法序列化（参见 ExtensionGRPCClient.BackgroundTasks
//     的注释），因此 Core 这边只拿到任务名 + 间隔，真正执行通过 RunBackgroundTask
//     RPC 反向调用插件进程内的 handler 表（见 ExtensionGRPCServer）。
//   - 启动后立即跑一次（不等第一个 tick），让重启服务后立刻清理积压的过期数据。
//   - 单次任务执行用独立 timeout，不用 ticker 的循环 ctx，避免一次慢调用阻塞下一轮。
func (m *Manager) startExtensionBackgroundTasks(inst *PluginInstance) {
	if inst == nil || inst.Extension == nil {
		return
	}
	tasks := inst.Extension.BackgroundTasks()
	if len(tasks) == 0 {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	inst.stopBackground = cancel

	for _, t := range tasks {
		interval := t.Interval
		if interval < minBackgroundInterval {
			slog.Warn("插件后台任务间隔过小，已抬升到最小间隔",
				"plugin", inst.Name, "task", t.Name,
				"declared", t.Interval, "applied", minBackgroundInterval)
			interval = minBackgroundInterval
		}
		go m.runBackgroundTaskLoop(ctx, inst.Name, inst.Extension, t.Name, interval)
		slog.Info("已启动插件后台任务", "plugin", inst.Name, "task", t.Name, "interval", interval)
	}
}

func (m *Manager) runBackgroundTaskLoop(ctx context.Context, pluginName string, ext *sdkgrpc.ExtensionGRPCClient, taskName string, interval time.Duration) {
	// 启动后立即执行一次，避免重启后等待整个 interval 才清理。
	m.runBackgroundTaskOnce(ctx, pluginName, ext, taskName)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.runBackgroundTaskOnce(ctx, pluginName, ext, taskName)
		}
	}
}

func (m *Manager) runBackgroundTaskOnce(parent context.Context, pluginName string, ext *sdkgrpc.ExtensionGRPCClient, taskName string) {
	// parent 已 Done 时直接放弃，不要再发 RPC（插件即将停止）。
	if err := parent.Err(); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, taskRunTimeout)
	defer cancel()
	if err := ext.RunBackgroundTask(ctx, taskName); err != nil {
		slog.Warn("插件后台任务执行失败",
			"plugin", pluginName, "task", taskName, "error", err)
	}
}
