package pipeline

import "time"

const (
	// 粘性迁移比新会话调度更保守：至少 8 个近期样本、慢 50% 且绝对差 1.5 秒，
	// 才牺牲一次 prompt cache 粘性。该机制只重选持续慢账号，不做失败冷却。
	accountAffinityRebindMinSamples = 8
	accountAffinityRebindRatio      = 1.50
	accountAffinityRebindMinGapMs   = 1_500

	// 绑定账号已有在途请求时，替代账号预计完成分数至少快 20% 才临时分流。
	// 临时分流不改主绑定，后续串行请求仍能复用原账号的 prompt cache。
	accountAffinitySpillRatio = 0.80
)

type affinityAccountAction uint8

const (
	affinityAccountKeep affinityAccountAction = iota
	affinityAccountSpill
	affinityAccountRebind
)

type affinityAccountDecision struct {
	target routeTarget
	action affinityAccountAction

	boundLatencyMs  int64
	targetLatencyMs int64
	boundInflight   int64
	targetInflight  int64
}

type affinityAccountCandidate struct {
	target      routeTarget
	latencyMs   int64
	samples     uint64
	inflight    int64
	baseScore   float64
	loadedScore float64
}

// rebalanceAffinityAccount 对已命中的账号粘性做纯内存复核。
//
//   - 持续显著慢：迁移主绑定，避免活跃会话因滑动 TTL 永久粘在退化账号；
//   - 仅当前繁忙：本次请求临时分流，不修改主绑定；
//   - 候选不足或差距不明显：保持原绑定，优先保住 prompt cache。
//
// 只比较同一有效优先级档的账号，不跨越管理员配置的硬优先级。
func (p *Pipeline) rebalanceAffinityAccount(
	bound routeTarget,
	groupID int,
	model string,
	excludeAccounts []int,
) affinityAccountDecision {
	decision := affinityAccountDecision{target: bound, action: affinityAccountKeep}
	if p == nil || p.accounts == nil || bound.kind != routeAccount || bound.account == nil {
		return decision
	}
	now := time.Now()
	boundLatency, boundSamples, ok := p.recentAccountFirstTokenStats(bound.account.ID, model, now)
	if !ok {
		return decision
	}
	boundInflight := p.accountInflightCount(bound.account.ID)
	boundWeight := float64(effectiveRouteWeight(bound.weight))
	boundBaseScore := float64(boundLatency) / boundWeight
	boundLoadedScore := float64(boundInflight+1) * boundBaseScore
	decision.boundLatencyMs = boundLatency
	decision.boundInflight = boundInflight

	_, accountIDs := p.accounts.RouteCandidateIDs(groupID, model)
	var bestBase affinityAccountCandidate
	var bestLoaded affinityAccountCandidate
	hasBestBase := false
	hasBestLoaded := false
	for _, accountID := range accountIDs {
		if accountID == bound.account.ID || routeExcluded(excludeAccounts, accountID) {
			continue
		}
		account, candidateOK := p.accounts.RouteCandidate(accountID, groupID, model, now)
		if !candidateOK || account.EffectivePriority(now) != bound.priority {
			continue
		}
		latency, samples, latencyOK := p.recentAccountFirstTokenStats(account.ID, model, now)
		if !latencyOK {
			continue
		}
		candidate := affinityAccountCandidate{
			target: routeTarget{
				kind: routeAccount, priority: bound.priority,
				weight: effectiveRouteWeight(account.Weight), account: account,
			},
			latencyMs: latency,
			samples:   samples,
			inflight:  p.accountInflightCount(account.ID),
		}
		candidate.baseScore = float64(candidate.latencyMs) / float64(candidate.target.weight)
		candidate.loadedScore = float64(candidate.inflight+1) * candidate.baseScore
		if !hasBestBase || candidate.baseScore < bestBase.baseScore {
			bestBase = candidate
			hasBestBase = true
		}
		if !hasBestLoaded || candidate.loadedScore < bestLoaded.loadedScore {
			bestLoaded = candidate
			hasBestLoaded = true
		}
	}

	if hasBestBase && boundSamples >= accountAffinityRebindMinSamples &&
		bestBase.samples >= accountAffinityRebindMinSamples &&
		boundLatency-bestBase.latencyMs >= accountAffinityRebindMinGapMs &&
		bestBase.baseScore*accountAffinityRebindRatio <= boundBaseScore &&
		bestBase.loadedScore <= boundLoadedScore {
		decision.target = bestBase.target
		decision.action = affinityAccountRebind
		decision.targetLatencyMs = bestBase.latencyMs
		decision.targetInflight = bestBase.inflight
		return decision
	}

	if boundInflight > 0 && hasBestLoaded &&
		bestLoaded.loadedScore <= boundLoadedScore*accountAffinitySpillRatio {
		decision.target = bestLoaded.target
		decision.action = affinityAccountSpill
		decision.targetLatencyMs = bestLoaded.latencyMs
		decision.targetInflight = bestLoaded.inflight
	}
	return decision
}
