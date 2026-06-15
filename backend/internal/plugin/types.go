package plugin

import (
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/routing"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// forwardState 一次转发请求在 Core 内的上下文。
// 跨 failover attempt 稳定的字段（body / model / keyInfo / plugin）+ 每次 attempt 会被覆盖的字段（account / requestID）。
type forwardState struct {
	startedAt   time.Time
	requestPath string
	requestID   string

	body  []byte
	model string
	// schedulingModels 是调度层使用的模型候选。协议翻译入口里，客户端传入的
	// model 可能不是上游真实模型，例如 OpenAI 插件的 /v1/messages 会把
	// claude-* 映射到 GPT 模型后再调用上游。
	schedulingModels []string
	schedulingModel  string
	stream           bool
	realtime         bool
	sessionID        string

	// 推理强度档位快照。
	reasoningEffort string
	accountReq      scheduler.AccountRequirements

	// 缓存的 image tool payload，避免 forwarder 热路径上重复反序列化 body
	imageToolPayloadValid bool
	imageToolPayload      imageToolPayload

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
	Model           string
	Stream          bool
	SessionID       string
	ReasoningEffort string // 推理强度档位

	// 缓存 image tool payload 解析结果，避免 requestNeedsImage / accountRequirementsForRequest 重复反序列化 body
	imageToolPayloadValid bool
	imageToolPayload      imageToolPayload
}

// requestFields 一次性 Unmarshal 的 JSON 字段结构。
type requestFields struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Metadata struct {
		UserID string `json:"user_id"`
	} `json:"metadata"`
	ReasoningEffort string `json:"reasoning_effort"`
	Reasoning       *struct {
		Effort string `json:"effort"`
	} `json:"reasoning"`
	OutputConfig *struct {
		Effort string `json:"effort"`
	} `json:"output_config"`
	Thinking *struct{} `json:"thinking"`
}

func (s *forwardState) schedulingModelCandidates() []string {
	if s == nil {
		return nil
	}
	if len(s.schedulingModels) > 0 {
		return s.schedulingModels
	}
	if s.model == "" {
		return nil
	}
	return []string{s.model}
}

func (s *forwardState) modelForScheduling() string {
	if s == nil {
		return ""
	}
	if s.schedulingModel != "" {
		return s.schedulingModel
	}
	if len(s.schedulingModels) > 0 {
		return s.schedulingModels[0]
	}
	return s.model
}
