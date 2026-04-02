package plugin

import (
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	sdk "github.com/DouDOU-start/airgate-sdk"
)

// forwardState 保存一次转发请求在 Core 内的上下文状态。
type forwardState struct {
	startedAt   time.Time
	requestPath string
	requestID   string

	body      []byte
	model     string
	stream    bool
	sessionID string

	keyInfo *auth.APIKeyInfo
	plugin  *PluginInstance
	account *ent.Account
}

// forwardExecution 保存插件执行结果，避免在主流程中散落多个返回值。
type forwardExecution struct {
	result   *sdk.ForwardResult
	err      error
	duration time.Duration
}

type parsedForwardRequest struct {
	Model     string
	Stream    bool
	SessionID string
}
