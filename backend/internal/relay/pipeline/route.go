package pipeline

import (
	"time"

	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/relay/relayhook"
)

// routeKind 路由目标类型。
type routeKind int

const (
	routeChannel routeKind = iota
	routeAccount
)

// routeTarget 渠道 key 与账号的统一候选。
type routeTarget struct {
	kind     routeKind
	priority int
	weight   int
	channel  *registry.ChannelKeySnapshot
	account  *accountreg.Snapshot
}

type routeBalanceKey struct {
	groupID  int
	model    string
	protocol string
	kind     routeKind
	id       int
	priority int
}

type routeBalanceState struct {
	current  int64
	weight   int64
	lastSeen uint64
}

const (
	maxRouteWeight       = 1_000_000
	maxRouteBalanceState = 16_384
	routeStateIdlePicks  = 65_536
)

// pickRoute 从渠道注册表与账号注册表取候选，按 priority 分档并在档内平滑加权轮询。
//
// 规则与 registry.Pick 对齐：
//   - 最高 priority 档胜出
//   - 档内按配置 weight 原值调度；渠道 degraded 健康状态权重减半
//   - 账号 degraded（未到期）EffectivePriority 已压到 0
//
// 两边皆无候选返回 (nil, false)。
func (p *Pipeline) pickRoute(
	groupID int,
	model, protocol string,
	excludeKeys, excludeAccounts []int,
	plan *relayhook.RoutePlan,
) (routeTarget, bool) {
	if plan == nil {
		return p.pickIndexedRoute(groupID, model, protocol, excludeKeys, excludeAccounts)
	}
	var cands []routeTarget
	now := time.Now()

	if p.registry != nil {
		for _, k := range p.registry.ListCandidates(groupID, model, protocol, excludeKeys) {
			w := effectiveRouteWeight(k.Weight)
			if k.HealthStatus == "degraded" {
				w /= 2
				if w < 1 {
					w = 1
				}
			}
			cands = append(cands, routeTarget{
				kind:     routeChannel,
				priority: k.Priority,
				weight:   w,
				channel:  k,
			})
		}
	}
	if p.accounts != nil {
		var allowRateLimited []int
		if plan != nil {
			allowRateLimited = plan.AllowRateLimitedAccountIDs
		}
		for _, a := range p.accounts.ListCandidatesAllowRateLimited(groupID, model, excludeAccounts, allowRateLimited) {
			cands = append(cands, routeTarget{
				kind:     routeAccount,
				priority: a.EffectivePriority(now),
				weight:   effectiveRouteWeight(a.Weight),
				account:  a,
			})
		}
	}
	if len(cands) == 0 {
		return routeTarget{}, false
	}

	// 插件有序账号只改变本次请求的首选顺序。普通账号仍由 Core 完整过滤；只有
	// 当前计划显式授权的限流 Codex OAuth 账号可进入本轮候选。账号容量满后，
	// 下一轮自然选择 AccountIDs 中的下一项。
	if plan != nil {
		accounts := make(map[int]routeTarget, len(cands))
		for _, candidate := range cands {
			if candidate.kind == routeAccount && candidate.account != nil {
				accounts[candidate.account.ID] = candidate
			}
		}
		for _, accountID := range plan.AccountIDs {
			if candidate, ok := accounts[accountID]; ok {
				return candidate, true
			}
		}
		// v1 只接受 core fallback；Pipeline 在接收决策时已校验。这里保留防御性判断。
		if plan.Fallback != relayhook.FallbackCore {
			return routeTarget{}, false
		}
	}

	best := cands[0].priority
	for i := 1; i < len(cands); i++ {
		if cands[i].priority > best {
			best = cands[i].priority
		}
	}
	tier := make([]routeTarget, 0, len(cands))
	for _, c := range cands {
		if c.priority == best {
			tier = append(tier, c)
		}
	}
	selected := p.pickSmoothWeighted(groupID, model, protocol, tier)
	return selected, true
}

func routeID(t routeTarget) int {
	if t.kind == routeAccount && t.account != nil {
		return t.account.ID
	}
	if t.channel != nil {
		return t.channel.KeyID
	}
	return 0
}

func effectiveRouteWeight(weight int) int {
	if weight <= 0 {
		return 1
	}
	if weight > maxRouteWeight {
		return maxRouteWeight
	}
	return weight
}

// pickSmoothWeighted 在同一优先级档内执行平滑加权轮询。
func (p *Pipeline) pickSmoothWeighted(groupID int, model, protocol string, tier []routeTarget) routeTarget {
	if len(tier) == 1 {
		return tier[0]
	}
	p.routeMu.Lock()
	defer p.routeMu.Unlock()
	if p.routeWeights == nil {
		p.routeWeights = make(map[routeBalanceKey]routeBalanceState, len(tier))
	}
	p.routePicks++

	total := int64(0)
	selected := 0
	selectedCurrent := int64(-1 << 62)
	var selectedKey routeBalanceKey
	for i, candidate := range tier {
		weight := int64(effectiveRouteWeight(candidate.weight))
		total += weight
		key := routeBalanceKey{
			groupID: groupID, model: model, protocol: protocol,
			kind: candidate.kind, id: routeID(candidate), priority: candidate.priority,
		}
		state := p.routeWeights[key]
		if state.weight != 0 && state.weight != weight {
			state.current = 0
		}
		if state.lastSeen > 0 && p.routePicks-state.lastSeen > routeStateIdlePicks {
			state.current = 0
		}
		state.current += weight
		state.weight = weight
		state.lastSeen = p.routePicks
		p.routeWeights[key] = state
		if state.current > selectedCurrent ||
			(state.current == selectedCurrent && routeLess(candidate, tier[selected])) {
			selected = i
			selectedCurrent = state.current
			selectedKey = key
		}
	}
	state := p.routeWeights[selectedKey]
	state.current -= total
	p.routeWeights[selectedKey] = state
	p.pruneRouteWeights()
	return tier[selected]
}

func routeLess(left, right routeTarget) bool {
	if left.kind != right.kind {
		return left.kind < right.kind
	}
	return routeID(left) < routeID(right)
}

func (p *Pipeline) pruneRouteWeights() {
	if len(p.routeWeights) <= maxRouteBalanceState || p.routePicks%1024 != 0 {
		return
	}
	cutoff := uint64(0)
	if p.routePicks > routeStateIdlePicks {
		cutoff = p.routePicks - routeStateIdlePicks
	}
	for key, state := range p.routeWeights {
		if state.lastSeen < cutoff {
			delete(p.routeWeights, key)
		}
	}
	if len(p.routeWeights) > maxRouteBalanceState {
		clear(p.routeWeights)
	}
}
