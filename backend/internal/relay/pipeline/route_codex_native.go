package pipeline

import (
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/relayhook"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
)

// pickNativeCodexRoute selects only canonical Codex accounts. The official
// realtime SDP bootstrap does not always carry a model, and realtime models
// need not appear in the Responses catalog, so selection falls back to the
// group's first schedulable native-account catalog entry when an exact model
// bucket does not exist.
func (p *Pipeline) pickNativeCodexRoute(groupID int, model string, excludeAccounts []int) (routeTarget, bool) {
	if p == nil || p.accounts == nil {
		return routeTarget{}, false
	}

	seen := make(map[int]struct{})
	candidates := make([]routeTarget, 0)
	appendAccount := func(account *accountreg.Snapshot) {
		if !isNativeCodexAccount(account) {
			return
		}
		if _, exists := seen[account.ID]; exists {
			return
		}
		seen[account.ID] = struct{}{}
		candidates = append(candidates, routeTarget{
			kind: routeAccount, priority: account.EffectivePriority(time.Now()),
			weight: effectiveRouteWeight(account.Weight), account: account,
		})
	}
	appendModel := func(candidateModel string) {
		for _, account := range p.accounts.ListCandidates(groupID, candidateModel, excludeAccounts) {
			appendAccount(account)
		}
	}

	requestedModel := strings.TrimSpace(model)
	if requestedModel != "" {
		appendModel(requestedModel)
		// Add model-less native accounts as wildcards alongside exact catalog
		// matches. They participate in the same priority/weight tier rather than
		// being used only when the exact-model bucket is empty.
		for _, account := range p.accounts.ListGroupCandidatesForModel(groupID, requestedModel, excludeAccounts) {
			if len(account.Models) == 0 {
				appendAccount(account)
			}
		}
	} else {
		// A model-less native Realtime request may use any account. Include all
		// group-bound native accounts directly, regardless of model catalog.
		for _, account := range p.accounts.ListGroupCandidates(groupID, excludeAccounts) {
			appendAccount(account)
		}
	}
	if len(candidates) == 0 {
		return routeTarget{}, false
	}

	bestPriority := candidates[0].priority
	for _, candidate := range candidates[1:] {
		if candidate.priority > bestPriority {
			bestPriority = candidate.priority
		}
	}
	tier := make([]routeTarget, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.priority == bestPriority {
			tier = append(tier, candidate)
		}
	}
	return p.pickSmoothWeighted(groupID, model, "codex_native", tier), true
}

// pickNativeCodexRouteWithPlan gives a Relay Hook's explicit account order
// precedence without allowing a channel (or a non-Codex account) to enter the
// native leg. If the plan contains no currently schedulable native account,
// FallbackCore semantics continue with the normal native weighted picker.
func (p *Pipeline) pickNativeCodexRouteWithPlan(
	groupID int,
	model string,
	excludeAccounts []int,
	plan *relayhook.RoutePlan,
) (routeTarget, bool) {
	if p == nil || p.accounts == nil {
		return routeTarget{}, false
	}
	if plan != nil && len(plan.AccountIDs) > 0 {
		allowRateLimited := append([]int(nil), plan.AllowRateLimitedAccountIDs...)
		candidates := make(map[int]*accountreg.Snapshot)
		appendCandidates := func(items []*accountreg.Snapshot) {
			for _, account := range items {
				if account == nil || !isNativeCodexAccount(account) {
					continue
				}
				candidates[account.ID] = account
			}
		}
		if strings.TrimSpace(model) != "" {
			appendCandidates(p.accounts.ListCandidatesAllowRateLimited(groupID, model, excludeAccounts, allowRateLimited))
			// Empty model catalogs are wildcards and are not present in the
			// model-index based method above.
			appendCandidates(p.accounts.ListGroupCandidatesForModel(groupID, model, excludeAccounts))
		} else {
			appendCandidates(p.accounts.ListGroupCandidates(groupID, excludeAccounts))
		}
		for _, accountID := range plan.AccountIDs {
			account := candidates[accountID]
			if account == nil {
				continue
			}
			return routeTarget{
				kind: routeAccount, priority: account.EffectivePriority(time.Now()),
				weight: effectiveRouteWeight(account.Weight), account: account,
			}, true
		}
	}
	return p.pickNativeCodexRoute(groupID, model, excludeAccounts)
}

func isNativeCodexAccount(account *accountreg.Snapshot) bool {
	return account != nil && providertransport.IsCodexPlatform(account.Platform) &&
		providertransport.IsCodexNativeAuthType(account.Type)
}

// pickNativeCodexAccount selects one exact account for a multi-step native
// contract. Files finalize must use the account that created the file because
// the provider's file id is account-scoped; this helper re-checks group,
// platform, model/catalog and schedulability instead of trusting a stale id.
func (p *Pipeline) pickBoundNativeCodexAccount(groupID int, model string, accountID int) (routeTarget, bool) {
	if p == nil || p.accounts == nil || accountID <= 0 {
		return routeTarget{}, false
	}
	for _, account := range p.accounts.ListGroupCandidatesForModel(groupID, model, nil) {
		if account == nil || account.ID != accountID || !isNativeCodexAccount(account) {
			continue
		}
		return routeTarget{kind: routeAccount, priority: account.EffectivePriority(time.Now()), weight: effectiveRouteWeight(account.Weight), account: account}, true
	}
	return routeTarget{}, false
}
