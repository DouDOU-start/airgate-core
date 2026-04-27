package plugin

import (
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/routing"
	sdk "github.com/DouDOU-start/airgate-sdk"
)

// forwardState 一次转发请求在 Core 内的上下文。
// 跨 failover attempt 稳定的字段（body / model / keyInfo / plugin）+ 每次 attempt 会被覆盖的字段（account / requestID）。
type forwardState struct {
	startedAt   time.Time
	requestPath string
	requestID   string

	body      []byte
	model     string
	stream    bool
	realtime  bool
	sessionID string

	requestedPlatform string
	selectedRoute     routing.Candidate

	keyInfo *auth.APIKeyInfo
	plugin  *PluginInstance
	account *ent.Account
}

// forwardExecution 一次 plugin.Forward 调用的结果。
// err 仅表示"插件自身崩了"；业务判决全在 outcome.Kind。
type forwardExecution struct {
	outcome  sdk.ForwardOutcome
	err      error
	duration time.Duration
}

// parsedRequest 从 JSON body 提取的请求元信息。
type parsedRequest struct {
	Model     string
	Stream    bool
	SessionID string
}

// requestFields 一次性 Unmarshal 的 JSON 字段结构。
type requestFields struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Metadata struct {
		UserID string `json:"user_id"`
	} `json:"metadata"`
}
