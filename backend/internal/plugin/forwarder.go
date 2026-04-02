package plugin

import (
	"errors"
	"io"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/ratelimit"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
)

// openAIError 返回 OpenAI 兼容的错误格式，确保 Claude Code 等客户端能正确识别。
func openAIError(c *gin.Context, status int, errType, code, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
			"code":    code,
		},
	})
}

// Forwarder 请求转发器。
// 完整流程：认证 → 限流 → 余额预检 → 调度 → 并发控制 → 转发 → 计费 → 记录。
type Forwarder struct {
	db          *ent.Client
	manager     *Manager
	scheduler   *scheduler.Scheduler
	concurrency *scheduler.ConcurrencyManager
	limiter     *ratelimit.Limiter
	calculator  *billing.Calculator
	recorder    *billing.Recorder
}

// shouldPenalizeForwardError 判断转发失败是否应该计入账号失败次数。
// 像 WebSocket EOF / 正常关闭这类连接中断通常属于瞬时链路问题，不应导致账号被自动停用。
func shouldPenalizeForwardError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return false
	}

	msg := strings.ToLower(err.Error())
	ignored := []string{
		"websocket 连接失败: eof",
		"读取 websocket 消息失败: eof",
		"读取上游消息: eof",
		"读取客户端消息: eof",
		"websocket: close 1000",
		"websocket: close 1001",
	}
	for _, needle := range ignored {
		if strings.Contains(msg, needle) {
			return false
		}
	}
	return true
}

// NewForwarder 创建转发器。
func NewForwarder(
	db *ent.Client,
	manager *Manager,
	sched *scheduler.Scheduler,
	concurrency *scheduler.ConcurrencyManager,
	limiter *ratelimit.Limiter,
	calculator *billing.Calculator,
	recorder *billing.Recorder,
) *Forwarder {
	return &Forwarder{
		db:          db,
		manager:     manager,
		scheduler:   sched,
		concurrency: concurrency,
		limiter:     limiter,
		calculator:  calculator,
		recorder:    recorder,
	}
}

// Forward 转发请求到对应插件。
func (f *Forwarder) Forward(c *gin.Context) {
	state, ok := f.buildForwardState(c)
	if !ok {
		return
	}

	if !f.ensureForwardAllowed(c, state) {
		return
	}

	if !f.selectForwardAccount(c, state) {
		return
	}

	cleanup, ok := f.prepareForwardExecution(c, state)
	if !ok {
		return
	}
	defer cleanup()

	execution := f.executeForward(c, state)
	f.finishForward(c, state, execution)
}
