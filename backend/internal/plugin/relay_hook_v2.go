package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"

	"github.com/DouDOU-start/airgate-core/internal/plugin/hookv2"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

const (
	defaultRelayHookV2Timeout     = 500 * time.Millisecond
	maxRelayHookV2BodyBytes       = 32 << 20
	relayHookV2FailureLimit       = 3
	relayHookV2OpenDuration       = 30 * time.Second
	relayHookV2StopTimeout        = 3 * time.Second
	relayHookV2AccountTestTimeout = 2 * time.Second

	relayHookV2Version            = "v1"
	relayHookV2BeforeDispatchPath = "/relay-hook/v1/before-dispatch"
	relayHookV2AccountTestPath    = "/account-test-transform/v1/request"
)

type relayHookV2Client interface {
	Info() hookv2.PluginInfo
	Init(context.Context, map[string]string) error
	Start(context.Context) error
	Stop(context.Context) error
	Handle(context.Context, hookv2.Request) (hookv2.Response, error)
}

// relayHookV2Plugin 保存单个旧版 Hook 客户端的进程内保护状态。连续失败会短暂
// 熔断；到期后只允许一个半开探测。所有错误均由调用链 fail-open 处理。
type relayHookV2Plugin struct {
	name   string
	client relayHookV2Client

	mu                  sync.Mutex
	calls               sync.WaitGroup
	stopping            bool
	consecutiveFailures int
	circuitUntil        time.Time
	halfOpenProbe       bool
}

func newRelayHookV2Plugin(name string, client relayHookV2Client) *relayHookV2Plugin {
	return &relayHookV2Plugin{name: name, client: client}
}

func (p *relayHookV2Plugin) acquireCall(now time.Time) bool {
	if p == nil || p.client == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopping {
		return false
	}
	if !p.circuitUntil.IsZero() {
		if now.Before(p.circuitUntil) || p.halfOpenProbe {
			return false
		}
		p.halfOpenProbe = true
	}
	p.calls.Add(1)
	return true
}

func (p *relayHookV2Plugin) invoke(ctx context.Context, request hookv2.Request) (response hookv2.Response, called bool, err error) {
	if !p.acquireCall(time.Now()) {
		return hookv2.Response{}, false, nil
	}
	called = true
	defer p.calls.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			response = hookv2.Response{}
			err = fmt.Errorf("Relay Hook v2 插件调用发生 panic")
		}
	}()
	response, err = p.client.Handle(ctx, request)
	return response, true, err
}

func (p *relayHookV2Plugin) recordSuccess() {
	p.mu.Lock()
	p.consecutiveFailures = 0
	p.circuitUntil = time.Time{}
	p.halfOpenProbe = false
	p.mu.Unlock()
}

func (p *relayHookV2Plugin) recordFailure(now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.halfOpenProbe || !p.circuitUntil.IsZero() {
		p.consecutiveFailures = 0
		p.halfOpenProbe = false
		p.circuitUntil = now.Add(relayHookV2OpenDuration)
		return true
	}
	p.consecutiveFailures++
	if p.consecutiveFailures < relayHookV2FailureLimit {
		return false
	}
	p.consecutiveFailures = 0
	p.circuitUntil = now.Add(relayHookV2OpenDuration)
	return true
}

func (p *relayHookV2Plugin) stop(ctx context.Context) error {
	if p == nil || p.client == nil {
		return nil
	}
	p.mu.Lock()
	p.stopping = true
	p.mu.Unlock()

	waitDone := make(chan struct{})
	go func() {
		p.calls.Wait()
		close(waitDone)
	}()
	var waitErr error
	select {
	case <-waitDone:
	case <-ctx.Done():
		waitErr = ctx.Err()
	}
	return errors.Join(waitErr, p.client.Stop(ctx))
}

type relayHookV2ChainItem struct {
	name     string
	priority int32
	hook     *relayHookV2Plugin
}

func (m *Manager) relayHookV2Chain() []relayHookV2ChainItem {
	return m.relayHookV2ChainFor(hookv2.CapabilityRelayHookV1)
}

func (m *Manager) relayHookV2ChainFor(capability string) []relayHookV2ChainItem {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	items := make([]relayHookV2ChainItem, 0)
	for _, instance := range m.instances {
		if instance == nil || instance.RelayHookV2 == nil || !containsString(instance.Capabilities, capability) {
			continue
		}
		items = append(items, relayHookV2ChainItem{
			name:     instance.Name,
			priority: instance.Priority,
			hook:     instance.RelayHookV2,
		})
	}
	m.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool {
		if items[i].priority != items[j].priority {
			return items[i].priority < items[j].priority
		}
		return items[i].name < items[j].name
	})
	return items
}

type relayHookV2Request struct {
	Version    string                 `json:"version"`
	RequestID  string                 `json:"request_id,omitempty"`
	UserID     int                    `json:"user_id"`
	APIKeyID   int                    `json:"api_key_id"`
	GroupID    int                    `json:"group_id"`
	Client     string                 `json:"client,omitempty"`
	Endpoint   string                 `json:"endpoint"`
	Protocol   string                 `json:"protocol"`
	Model      string                 `json:"model"`
	Stream     bool                   `json:"stream"`
	Body       json.RawMessage        `json:"body"`
	Candidates []relayHookV2Candidate `json:"candidates,omitempty"`
}

type relayHookV2Candidate struct {
	Kind     string `json:"kind"`
	ID       int    `json:"id"`
	Platform string `json:"platform,omitempty"`
	Type     string `json:"type,omitempty"`
	Priority int    `json:"priority"`
	State    string `json:"state,omitempty"`
}

// relayHookV2Decision 保留旧契约中曾出现过的路由字段，只用于识别并记录忽略。
// Core 永远不会把它们接入账号调度，也不会据此放行 rate_limited 账号。
type relayHookV2Decision struct {
	Version                    string          `json:"version"`
	RequestBody                json.RawMessage `json:"request_body,omitempty"`
	Route                      json.RawMessage `json:"route,omitempty"`
	AccountIDs                 []int           `json:"account_ids,omitempty"`
	AllowRateLimitedAccountIDs []int           `json:"allow_rate_limited_account_ids,omitempty"`
	Fallback                   string          `json:"fallback,omitempty"`
}

func (d relayHookV2Decision) hasIgnoredRouting() bool {
	trimmedRoute := strings.TrimSpace(string(d.Route))
	return (trimmedRoute != "" && trimmedRoute != "null") ||
		len(d.AccountIDs) > 0 ||
		len(d.AllowRateLimitedAccountIDs) > 0 ||
		strings.TrimSpace(d.Fallback) != ""
}

// applyRelayHookV2 在账号选择前调用全部旧版通用 Hook。它只接受完整 JSON body
// 替换，且 model/stream 必须保持不变；插件失败、超时、崩溃或非法响应一律保留
// 当前已确认的 body，绝不改变路由和账号数据库状态。
func (f *Forwarder) applyRelayHookV2(c *gin.Context, state *forwardState) {
	if f == nil || f.manager == nil || c == nil || state == nil || state.keyInfo == nil {
		return
	}
	chain := f.manager.relayHookV2Chain()
	if len(chain) == 0 || len(state.body) > maxRelayHookV2BodyBytes || !jsonObjectBytes(state.body) {
		return
	}

	timeout := f.manager.relayHookV2Timeout
	if timeout <= 0 {
		timeout = defaultRelayHookV2Timeout
	}
	chainCtx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	logger := sdk.LoggerFromContext(c.Request.Context())

	request := relayHookV2Request{
		Version:    relayHookV2Version,
		RequestID:  middleware.RequestIDFromGinContext(c),
		UserID:     state.keyInfo.UserID,
		APIKeyID:   state.keyInfo.KeyID,
		GroupID:    state.keyInfo.GroupID,
		Client:     c.GetHeader("User-Agent"),
		Endpoint:   relayHookV2Endpoint(state.requestPath),
		Protocol:   state.requestedPlatform,
		Model:      state.model,
		Stream:     state.stream,
		Body:       append(json.RawMessage(nil), state.body...),
		Candidates: f.relayHookV2Candidates(chainCtx, state),
	}

	for _, item := range chain {
		if chainCtx.Err() != nil {
			break
		}
		request.Body = append(request.Body[:0], state.body...)
		payload, err := json.Marshal(request)
		if err != nil {
			logger.Debug("relay_hook_v2_request_skipped", sdk.LogFieldPluginID, item.name, "reason", "invalid_request_payload")
			continue
		}
		response, called, err := item.hook.invoke(chainCtx, hookv2.Request{
			Method: http.MethodPost,
			Path:   relayHookV2BeforeDispatchPath,
			Header: map[string][]string{"Content-Type": {"application/json"}},
			Body:   payload,
		})
		if !called {
			continue
		}
		if err != nil {
			opened := item.hook.recordFailure(time.Now())
			logger.Warn("relay_hook_v2_call_failed",
				sdk.LogFieldPluginID, item.name,
				"error_code", relayHookV2ErrorCode(err),
				"circuit_opened", opened,
			)
			continue
		}
		if response.StatusCode == http.StatusNoContent {
			item.hook.recordSuccess()
			continue
		}
		if response.StatusCode != http.StatusOK {
			opened := item.hook.recordFailure(time.Now())
			logger.Warn("relay_hook_v2_response_rejected",
				sdk.LogFieldPluginID, item.name,
				"status_code", response.StatusCode,
				"reason", "unexpected_status",
				"circuit_opened", opened,
			)
			continue
		}

		var decision relayHookV2Decision
		if err := json.Unmarshal(response.Body, &decision); err != nil {
			opened := item.hook.recordFailure(time.Now())
			logger.Warn("relay_hook_v2_response_rejected",
				sdk.LogFieldPluginID, item.name,
				"reason", "invalid_json",
				"circuit_opened", opened,
			)
			continue
		}
		if decision.hasIgnoredRouting() {
			logger.Debug("relay_hook_v2_routing_ignored", sdk.LogFieldPluginID, item.name)
		}
		replacement, changed, err := normalizeRelayHookV2Body(state, decision)
		if err != nil {
			opened := item.hook.recordFailure(time.Now())
			logger.Warn("relay_hook_v2_response_rejected",
				sdk.LogFieldPluginID, item.name,
				"reason", relayHookV2ErrorCode(err),
				"circuit_opened", opened,
			)
			continue
		}
		item.hook.recordSuccess()
		if !changed {
			continue
		}
		state.body = replacement
		refreshForwardStateAfterRelayHook(f.manager, state)
		logger.Debug("relay_hook_v2_body_replaced",
			sdk.LogFieldPluginID, item.name,
			"request_body_bytes", len(replacement),
		)
	}
}

func (f *Forwarder) relayHookV2Candidates(ctx context.Context, state *forwardState) []relayHookV2Candidate {
	if f.scheduler == nil || state == nil || state.keyInfo == nil {
		return nil
	}
	candidates, err := f.scheduler.ListRouteCandidates(
		ctx,
		state.requestedPlatform,
		state.schedulingModelCandidates(),
		state.keyInfo.GroupID,
		state.accountReq,
	)
	if err != nil {
		sdk.LoggerFromContext(ctx).Debug("relay_hook_v2_candidates_unavailable", "error_code", relayHookV2ErrorCode(err))
		return nil
	}
	result := make([]relayHookV2Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		// Scheduler 只返回基础状态为 Normal 的只读摘要；这里仍做显式防御，
		// 确保 rate_limited/degraded/disabled 永远不会作为可用候选暴露给 Hook。
		if !strings.EqualFold(candidate.State, "active") {
			continue
		}
		result = append(result, relayHookV2Candidate{
			Kind:     "account",
			ID:       candidate.ID,
			Platform: candidate.Platform,
			Type:     candidate.Type,
			Priority: candidate.Priority,
			State:    "active",
		})
	}
	return result
}

func normalizeRelayHookV2Body(state *forwardState, decision relayHookV2Decision) ([]byte, bool, error) {
	if decision.Version != relayHookV2Version {
		return nil, false, fmt.Errorf("invalid_version")
	}
	if len(decision.RequestBody) == 0 {
		return nil, false, nil
	}
	if len(decision.RequestBody) > maxRelayHookV2BodyBytes {
		return nil, false, fmt.Errorf("body_too_large")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(decision.RequestBody, &object); err != nil || object == nil {
		return nil, false, fmt.Errorf("body_not_json_object")
	}

	modelRaw, modelExists := object["model"]
	if state.model != "" && !modelExists {
		return nil, false, fmt.Errorf("model_changed")
	}
	if modelExists {
		var model string
		if err := json.Unmarshal(modelRaw, &model); err != nil || model != state.model {
			return nil, false, fmt.Errorf("model_changed")
		}
	}
	stream := false
	if streamRaw, exists := object["stream"]; exists {
		if err := json.Unmarshal(streamRaw, &stream); err != nil {
			return nil, false, fmt.Errorf("stream_invalid")
		}
	}
	if stream != state.stream {
		return nil, false, fmt.Errorf("stream_changed")
	}
	return append([]byte(nil), decision.RequestBody...), true, nil
}

func refreshForwardStateAfterRelayHook(manager *Manager, state *forwardState) {
	parsed := parseBody(state.body, "application/json")
	state.reasoningEffort = parsed.ReasoningEffort
	state.sessionID = parsed.SessionID
	state.accountReq = accountRequirementsForRequestCached(manager, state.requestPath, state.model, &parsed)
	state.imageToolPayloadValid = parsed.imageToolPayloadValid
	state.imageToolPayload = parsed.imageToolPayload
}

func relayHookV2Endpoint(path string) string {
	path = strings.TrimSpace(path)
	if index := strings.IndexByte(path, '?'); index >= 0 {
		path = path[:index]
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for index := len(parts) - 1; index >= 0; index-- {
		if part := strings.TrimSpace(parts[index]); part != "" {
			return part
		}
	}
	return ""
}

func jsonObjectBytes(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) >= 2 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}' && json.Valid([]byte(trimmed))
}

func relayHookV2ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	code := strings.TrimSpace(err.Error())
	switch code {
	case "invalid_version", "body_too_large", "body_not_json_object", "model_changed", "stream_invalid", "stream_changed":
		return code
	default:
		return "plugin_error"
	}
}
