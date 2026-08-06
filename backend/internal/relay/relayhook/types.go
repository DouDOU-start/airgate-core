// Package relayhook 定义 Relay Pipeline 与外部 Hook 插件之间的稳定窄契约。
package relayhook

import (
	"context"
	"encoding/json"
)

const (
	// VersionV1 当前 Relay Hook JSON 协议版本。
	VersionV1 = "v1"
	// BeforeDispatchPath 是 Core 通过插件内部 gRPC 通用请求接口调用的逻辑路径。
	BeforeDispatchPath = "/relay-hook/v1/before-dispatch"
	// FallbackCore 表示有序账号不可用后回到 Core 原有混合调度。
	FallbackCore = "core"
)

// Hook 是 Pipeline 使用的插件扩展点。实现必须把故障返回为 error；Pipeline 对所有
// error、超时和非法决策均采用 fail-open，继续执行原有请求与调度。
type Hook interface {
	BeforeDispatch(ctx context.Context, req Request) (Decision, error)
}

// Request 是发送给外部插件的版本化请求。Body 是客户端 JSON 请求体；Candidates
// 只含调度元数据，绝不包含账号凭证、API Key、代理地址或上游地址。
type Request struct {
	Version    string          `json:"version"`
	RequestID  string          `json:"request_id,omitempty"`
	UserID     int             `json:"user_id"`
	APIKeyID   int             `json:"api_key_id"`
	GroupID    int             `json:"group_id"`
	Client     string          `json:"client,omitempty"`
	Endpoint   string          `json:"endpoint"`
	Protocol   string          `json:"protocol"`
	Model      string          `json:"model"`
	Stream     bool            `json:"stream"`
	Body       json.RawMessage `json:"body"`
	Candidates []Candidate     `json:"candidates,omitempty"`
}

// Candidate 是对插件可见的脱敏候选摘要。
type Candidate struct {
	Kind           string `json:"kind"`
	ID             int    `json:"id"`
	Name           string `json:"name,omitempty"`
	Platform       string `json:"platform,omitempty"`
	Type           string `json:"type,omitempty"`
	Priority       int    `json:"priority"`
	Weight         int    `json:"weight"`
	MaxConcurrency int    `json:"max_concurrency"`
	MaxRPM         int    `json:"max_rpm"`
	State          string `json:"state,omitempty"`
}

// Decision 是插件返回的本次请求决策。RequestBody 为完整替换体；Route 仅能指定
// 当前请求可见的账号 ID，Core 会再次过滤并保留所有限流、并发与状态机检查。
type Decision struct {
	Version     string          `json:"version"`
	RequestBody json.RawMessage `json:"request_body,omitempty"`
	Route       *RoutePlan      `json:"route,omitempty"`
}

// RoutePlan 定义账号严格尝试顺序。当前只接受 FallbackCore。
type RoutePlan struct {
	AccountIDs []int  `json:"account_ids,omitempty"`
	Fallback   string `json:"fallback"`
}
