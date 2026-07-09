package scheduler

import (
	"sync/atomic"
	"time"
)

// failOpenLogInterval 同类 fail-open WARN 日志的最小输出间隔。
// Redis 故障时 fail-open 放行发生在每次请求的每道闸门上（频率 = QPS × 闸门数），
// 不限频会形成日志风暴，反而淹没故障根因；每 30 秒一条足以留痕告警。
const failOpenLogInterval = 30 * time.Second

// failOpenLogGate 按"日志类别"维护上次输出时间戳（unix 纳秒）。
// allow 用 CompareAndSwap 保证并发下同一窗口内只有一个 goroutine 拿到输出权。
type failOpenLogGate struct {
	lastLogNano atomic.Int64
}

// allow 判断当前是否允许输出一条该类别的日志（每 failOpenLogInterval 最多一条）。
func (g *failOpenLogGate) allow() bool {
	now := time.Now().UnixNano()
	prev := g.lastLogNano.Load()
	if now-prev < int64(failOpenLogInterval) {
		return false
	}
	return g.lastLogNano.CompareAndSwap(prev, now)
}

// 并发闸门 / RPM 限流各自独立限频，互不挤占输出窗口。
var (
	concurrencyFailOpenLog failOpenLogGate
	rpmFailOpenLog         failOpenLogGate
)
