package relayhook

import (
	"fmt"
	"strings"
)

// NormalizeRoutePlan 将插件路由计划约束到当前请求可见账号范围内。
// 限流放行是单次请求授权，只接受同时出现在账号顺序中的 Codex OAuth 账号。
func NormalizeRoutePlan(candidates []Candidate, plan *RoutePlan) (*RoutePlan, error) {
	if plan == nil {
		return nil, nil
	}
	if plan.Fallback != FallbackCore {
		return nil, fmt.Errorf("不支持的 fallback %q", plan.Fallback)
	}

	visible := make(map[int]Candidate)
	for _, candidate := range candidates {
		if candidate.Kind == "account" && candidate.ID > 0 {
			visible[candidate.ID] = candidate
		}
	}

	seenAccounts := make(map[int]struct{}, len(plan.AccountIDs))
	accountIDs := make([]int, 0, len(plan.AccountIDs))
	for _, accountID := range plan.AccountIDs {
		if _, ok := visible[accountID]; !ok {
			continue
		}
		if _, duplicate := seenAccounts[accountID]; duplicate {
			continue
		}
		seenAccounts[accountID] = struct{}{}
		accountIDs = append(accountIDs, accountID)
	}
	if len(accountIDs) == 0 {
		return nil, nil
	}

	seenAllowed := make(map[int]struct{}, len(plan.AllowRateLimitedAccountIDs))
	allowRateLimited := make([]int, 0, len(plan.AllowRateLimitedAccountIDs))
	for _, accountID := range plan.AllowRateLimitedAccountIDs {
		if _, selected := seenAccounts[accountID]; !selected {
			continue
		}
		if _, duplicate := seenAllowed[accountID]; duplicate {
			continue
		}
		candidate := visible[accountID]
		if !strings.EqualFold(strings.TrimSpace(candidate.State), "rate_limited") ||
			!strings.EqualFold(strings.TrimSpace(candidate.Platform), "codex") ||
			!strings.EqualFold(strings.TrimSpace(candidate.Type), "oauth") {
			continue
		}
		seenAllowed[accountID] = struct{}{}
		allowRateLimited = append(allowRateLimited, accountID)
	}

	return &RoutePlan{
		AccountIDs:                 accountIDs,
		AllowRateLimitedAccountIDs: allowRateLimited,
		Fallback:                   FallbackCore,
	}, nil
}
