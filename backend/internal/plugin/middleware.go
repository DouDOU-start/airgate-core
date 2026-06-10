package plugin

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// middleware.go：Core 与 middleware 类型插件的对接层。
//
// 约束：
//   - Priority 升序进 Begin、降序出 End（LIFO 栈语义）
//   - middleware 失败不阻塞主流程；唯一例外是 OnForwardBegin 返回 DecisionDeny
//   - 当前未实现 DecisionMutate 改写 header（只合并 metadata），read_body capability 也未传 body

const middlewarePerCallTimeout = 200 * time.Millisecond

// listMiddlewarePlugins 按 (Priority, Name) 稳定升序返回所有 middleware 实例。
func (m *Manager) listMiddlewarePlugins() []*PluginInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*PluginInstance, 0)
	for _, inst := range m.instances {
		if inst == nil || inst.Middleware == nil {
			continue
		}
		out = append(out, inst)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// runForwardBeginChain 调用所有 middleware 的 OnForwardBegin。
// 返回 (allowed, mergedMetadata)：allowed=false 时已写过 4xx 响应。
func (f *Forwarder) runForwardBeginChain(c *gin.Context, state *forwardState) (bool, map[string]string) {
	plugins := f.manager.listMiddlewarePlugins()
	if len(plugins) == 0 {
		return true, nil
	}

	bag := make(map[string]string)
	for _, p := range plugins {
		decision := callMiddlewareBegin(c.Request.Context(), p, buildMiddlewareRequest(state, bag))
		if decision == nil {
			continue // 失败 / 超时 → 跳过
		}
		mergeMetadata(bag, decision.Metadata)

		if decision.Action == sdk.DecisionDeny {
			status := int(decision.DenyStatusCode)
			if status == 0 {
				status = http.StatusForbidden
			}
			msg := decision.DenyMessage
			if msg == "" {
				msg = "请求被中间件拒绝"
			}
			protocolError(c, status, "middleware_denied", "middleware_denied", msg)
			return false, bag
		}
	}
	return true, bag
}

// runForwardEndChain 在 writeResult 之前按 Priority 降序触发 OnForwardEnd（LIFO）。
// bag 是 begin 阶段累积的 metadata。
func (f *Forwarder) runForwardEndChain(c *gin.Context, state *forwardState, execution forwardExecution, bag map[string]string) {
	plugins := f.manager.listMiddlewarePlugins()
	if len(plugins) == 0 {
		return
	}
	evt := buildMiddlewareEvent(state, execution, bag)
	ctx := finalizeRequestContext(c.Request.Context())
	for i := len(plugins) - 1; i >= 0; i-- {
		callMiddlewareEnd(ctx, plugins[i], evt)
	}
}

func callMiddlewareBegin(parent context.Context, p *PluginInstance, req *sdk.MiddlewareRequest) *sdk.MiddlewareDecision {
	ctx, cancel := context.WithTimeout(parent, middlewarePerCallTimeout)
	defer cancel()
	decision, err := p.Middleware.OnForwardBegin(ctx, req)
	if err != nil {
		return nil
	}
	return decision
}

func callMiddlewareEnd(parent context.Context, p *PluginInstance, evt *sdk.MiddlewareEvent) {
	ctx, cancel := context.WithTimeout(parent, middlewarePerCallTimeout)
	defer cancel()
	_ = p.Middleware.OnForwardEnd(ctx, evt)
}

func buildMiddlewareRequest(state *forwardState, bag map[string]string) *sdk.MiddlewareRequest {
	var (
		userID, groupID, accountID int64
		platform                   string
	)
	if state.keyInfo != nil {
		userID = int64(state.keyInfo.UserID)
		groupID = int64(state.keyInfo.GroupID)
	}
	if state.account != nil {
		accountID = int64(state.account.ID)
		platform = state.account.Platform
	}
	return &sdk.MiddlewareRequest{
		RequestID: state.requestID,
		UserID:    userID,
		GroupID:   groupID,
		AccountID: accountID,
		Platform:  platform,
		Model:     state.model,
		Stream:    state.stream,
		Metadata:  cloneMetadata(bag),
	}
}

func buildMiddlewareEvent(state *forwardState, execution forwardExecution, bag map[string]string) *sdk.MiddlewareEvent {
	var (
		userID, groupID, accountID int64
		platform                   string
	)
	if state.keyInfo != nil {
		userID = int64(state.keyInfo.UserID)
		groupID = int64(state.keyInfo.GroupID)
	}
	if state.account != nil {
		accountID = int64(state.account.ID)
		platform = state.account.Platform
	}

	evt := &sdk.MiddlewareEvent{
		RequestID: state.requestID,
		UserID:    userID,
		GroupID:   groupID,
		AccountID: accountID,
		Platform:  platform,
		Model:     state.model,
		Stream:    state.stream,
		Duration:  execution.duration,
		Metadata:  cloneMetadata(bag),
	}

	o := execution.outcome
	evt.StatusCode = int32(o.Upstream.StatusCode)
	if u := o.Usage; u != nil {
		evt.Usage = u
	}
	if o.Kind != sdk.OutcomeSuccess && o.Kind != sdk.OutcomeUnknown {
		evt.ErrorKind = o.Kind.String()
		evt.ErrorMsg = o.Reason
	}
	if execution.err != nil {
		if evt.ErrorKind == "" {
			evt.ErrorKind = "plugin_error"
		}
		if evt.ErrorMsg == "" {
			evt.ErrorMsg = execution.err.Error()
		}
	}
	return evt
}

func mergeMetadata(dst, src map[string]string) {
	for k, v := range src {
		dst[k] = v
	}
}

func cloneMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
