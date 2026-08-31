package pipeline

import (
	"fmt"
	"log/slog"
	"sort"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/relayhook"
)

// applyRelayHook 在审核和并发闸门之前调用一次外部插件。任何错误或非法决策都
// fail-open，并且请求体与路由计划作为整体回退，避免只应用半份决策。
func (p *Pipeline) applyRelayHook(
	c *gin.Context,
	keyInfo *auth.APIKeyInfo,
	req *dto.ChatRequest,
	endpoint, protocol string,
) (*dto.ChatRequest, *relayhook.RoutePlan) {
	if p.relayHook == nil {
		return req, nil
	}
	body, err := req.Marshal()
	if err != nil {
		slog.Warn("序列化 Relay Hook 请求体失败，已沿用原请求", "error", err)
		return req, nil
	}
	candidates := p.relayHookCandidates(keyInfo.GroupID, req.Model, protocol)
	decision, err := p.relayHook.BeforeDispatch(c.Request.Context(), relayhook.Request{
		Version:    relayhook.VersionV1,
		RequestID:  requestIDOf(c),
		UserID:     keyInfo.UserID,
		APIKeyID:   keyInfo.KeyID,
		GroupID:    keyInfo.GroupID,
		Client:     relayClientType(c),
		Endpoint:   endpoint,
		Protocol:   protocol,
		Model:      req.Model,
		Stream:     req.Stream,
		Body:       body,
		Candidates: candidates,
	})
	if err != nil {
		slog.Warn("Relay Hook 调用失败，已沿用原请求和调度", "error", err)
		return req, nil
	}

	next, plan, err := validateRelayHookDecision(req, candidates, decision)
	if err != nil {
		slog.Warn("Relay Hook 决策无效，已沿用原请求和调度", "error", err)
		return req, nil
	}
	return next, plan
}

func (p *Pipeline) relayHookCandidates(groupID int, model, protocol string) []relayhook.Candidate {
	candidates := make([]relayhook.Candidate, 0, 16)
	if p.registry != nil {
		for _, item := range p.registry.ListCandidates(groupID, model, protocol, nil) {
			candidates = append(candidates, relayhook.Candidate{
				Kind:           "channel",
				ID:             item.KeyID,
				Name:           item.KeyName,
				Type:           item.Type,
				Priority:       item.Priority,
				Weight:         item.Weight,
				MaxConcurrency: item.MaxConcurrency,
				MaxRPM:         item.MaxRPM,
				State:          item.HealthStatus,
			})
		}
	}
	if p.accounts != nil {
		for _, item := range p.accounts.ListRelayHookCandidates(groupID, model) {
			candidates = append(candidates, relayhook.Candidate{
				Kind:           "account",
				ID:             item.ID,
				Name:           item.Name,
				Platform:       item.Platform,
				Type:           item.Type,
				Priority:       item.Priority,
				Weight:         item.Weight,
				MaxConcurrency: item.MaxConcurrency,
				MaxRPM:         item.MaxRPM,
				State:          item.State,
			})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Kind != candidates[j].Kind {
			return candidates[i].Kind < candidates[j].Kind
		}
		return candidates[i].ID < candidates[j].ID
	})
	return candidates
}

func validateRelayHookDecision(
	original *dto.ChatRequest,
	candidates []relayhook.Candidate,
	decision relayhook.Decision,
) (*dto.ChatRequest, *relayhook.RoutePlan, error) {
	if decision.Version == "" && len(decision.RequestBody) == 0 && decision.Route == nil {
		return original, nil, nil
	}
	if decision.Version != relayhook.VersionV1 {
		return nil, nil, fmt.Errorf("决策版本为 %q，期望 %q", decision.Version, relayhook.VersionV1)
	}

	var plan *relayhook.RoutePlan
	if decision.Route != nil {
		var err error
		plan, err = relayhook.NormalizeRoutePlan(candidates, decision.Route)
		if err != nil {
			return nil, nil, err
		}
	}

	next := original
	if len(decision.RequestBody) > 0 {
		if len(decision.RequestBody) > maxRequestBodyBytes {
			return nil, nil, fmt.Errorf("替换请求体超过 %d 字节限制", maxRequestBodyBytes)
		}
		parsed, err := dto.ParseChatRequest(decision.RequestBody)
		if err != nil {
			return nil, nil, fmt.Errorf("替换请求体不是 JSON 对象: %w", err)
		}
		if parsed.Model != original.Model {
			return nil, nil, fmt.Errorf("插件不得修改 model")
		}
		if parsed.Stream != original.Stream {
			return nil, nil, fmt.Errorf("插件不得修改 stream")
		}
		next = parsed
	}
	return next, plan, nil
}
