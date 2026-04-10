package plugin

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	sdk "github.com/DouDOU-start/airgate-sdk"
)

// forwarder_middleware.go：Core 与 middleware 类型插件的对接层。
//
// 设计要点（详见 ADR-0001 Decision 2/3）：
//   - middleware 类型插件按 Priority 升序调用 OnForwardBegin、降序调用 OnForwardEnd
//     （LIFO 栈语义，类似常见 web 框架的 middleware）
//   - middleware 永远不能 block 生产流量：
//     - transport 层 error → log warn 并跳过该插件
//     - 单次调用超时（默认 200ms / 插件）→ 跳过该插件
//   - 唯一允许 block 的：OnForwardBegin 返回 Decision.Action == DENY
//   - middleware 之间通过 metadata bag 互相通信（map[string]string）
//
// 当前实现的简化（避免过早过度设计）：
//   - 暂不支持 Decision.Mutate 的 SetHeaders 真正改写请求 header（先记录 metadata，
//     等真正的中间件插件落地后再决定如何接入到 buildForwardHeaders）
//   - 暂不传 request_body / response_body（middleware.read_body capability 留给后续）

const (
	// middlewarePerCallTimeout 每次 OnForwardBegin / OnForwardEnd 的最长允许时间。
	// 超时直接 cancel + 跳过该 middleware，保证不拖慢主流程。
	middlewarePerCallTimeout = 200 * time.Millisecond
)

// listMiddlewarePlugins 按 Priority 升序返回所有当前加载的 middleware 类型插件实例。
//
// 调用方负责自己决定 begin（升序）还是 end（降序），end 阶段倒序遍历即可。
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
	// 稳定按 (priority, name) 排序，便于测试可预期
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// runForwardBeginChain 在选定账号之后、prepareForwardExecution 之前调用。
//
// 返回 (allowed, mergedMetadata)：
//   - allowed=false 表示某个 middleware 决定拒绝；调用方应直接给客户端返回错误
//     （此时已经写过 c.JSON / openAIError）
//   - mergedMetadata 是所有 middleware 在 Decision.Metadata 里设置的 KV 合并结果，
//     之后 OnForwardEnd 阶段会延续传递
func (f *Forwarder) runForwardBeginChain(c *gin.Context, state *forwardState) (bool, map[string]string) {
	plugins := f.manager.listMiddlewarePlugins()
	if len(plugins) == 0 {
		return true, nil
	}

	bag := make(map[string]string)
	for _, p := range plugins {
		req := buildMiddlewareRequest(state, bag)
		decision := callMiddlewareBegin(c.Request.Context(), p, req)
		if decision == nil {
			// 错误或超时 → 跳过该插件，继续下一个
			continue
		}
		mergeMetadata(bag, decision.Metadata)

		switch decision.Action {
		case sdk.DecisionDeny:
			status := int(decision.DenyStatusCode)
			if status == 0 {
				status = http.StatusForbidden
			}
			msg := decision.DenyMessage
			if msg == "" {
				msg = "请求被中间件拒绝"
			}
			openAIError(c, status, "middleware_denied", "middleware_denied", msg)
			return false, bag
		case sdk.DecisionMutate:
			// 简化版 mutate：metadata 已经 merge 了；SetHeaders 暂未接入 buildForwardHeaders
			// （TODO：等真实 middleware 插件落地后再决定接入路径）
			continue
		default:
			// DecisionAllow 或未知 → 放行
			continue
		}
	}
	return true, bag
}

// runForwardEndChain 在 finishForward 之前调用，按 Priority 降序触发所有 middleware 的 OnForwardEnd。
//
// bag 是 begin 阶段累积的 metadata，会原样作为 event.Metadata 传给所有 end handler。
// finalErr：如果 forward 失败，传 forwardExecution 计算出的错误信息
func (f *Forwarder) runForwardEndChain(c *gin.Context, state *forwardState, execution forwardExecution, bag map[string]string) {
	plugins := f.manager.listMiddlewarePlugins()
	if len(plugins) == 0 {
		return
	}
	// LIFO：降序遍历
	evt := buildMiddlewareEvent(state, execution, bag)
	for i := len(plugins) - 1; i >= 0; i-- {
		callMiddlewareEnd(c.Request.Context(), plugins[i], evt)
	}
}

// ============================================================================
// 单次调用 helpers
// ============================================================================

func callMiddlewareBegin(parent context.Context, p *PluginInstance, req *sdk.MiddlewareRequest) *sdk.MiddlewareDecision {
	ctx, cancel := context.WithTimeout(parent, middlewarePerCallTimeout)
	defer cancel()
	decision, err := p.Middleware.OnForwardBegin(ctx, req)
	if err != nil {
		// transport / timeout / panic 都走这里。middleware 失败永远不能 block 生产。
		// 这里只 log；调用方按 nil decision 处理（== 跳过该 middleware）。
		// 改为 debug 级别避免高 QPS 时刷屏；如果未来需要告警，独立指标更合适。
		return nil
	}
	return decision
}

func callMiddlewareEnd(parent context.Context, p *PluginInstance, evt *sdk.MiddlewareEvent) {
	ctx, cancel := context.WithTimeout(parent, middlewarePerCallTimeout)
	defer cancel()
	_ = p.Middleware.OnForwardEnd(ctx, evt)
}

// ============================================================================
// state → middleware payload 转换
// ============================================================================

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
		// RequestBody / RequestHeaders 暂不传：中间件 read_body capability 待实现
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

	if r := execution.result; r != nil {
		evt.StatusCode = int32(r.StatusCode)
		evt.InputTokens = int64(r.InputTokens)
		evt.OutputTokens = int64(r.OutputTokens)
		evt.CachedInputTokens = int64(r.CachedInputTokens)
		evt.FirstTokenMs = r.FirstTokenMs
		evt.InputCost = r.InputCost
		evt.OutputCost = r.OutputCost
		evt.CachedInputCost = r.CachedInputCost
		if r.StatusCode >= 400 {
			evt.ErrorKind = "upstream_error"
			evt.ErrorMsg = r.ErrorMessage
		}
	}
	if execution.err != nil {
		evt.ErrorKind = "forward_error"
		evt.ErrorMsg = execution.err.Error()
	}

	return evt
}

func mergeMetadata(dst map[string]string, src map[string]string) {
	if dst == nil || len(src) == 0 {
		return
	}
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
