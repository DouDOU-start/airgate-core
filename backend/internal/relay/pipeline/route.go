package pipeline

import (
	"math/rand/v2"
	"sort"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
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

// pickRoute 从渠道注册表与账号注册表取候选，按 priority 分档 + weight 加权随机。
//
// 规则与 registry.Pick 对齐：
//   - 最高 priority 档胜出
//   - 档内 weight+10 加权；渠道 degraded 健康状态权重减半
//   - 账号 degraded（未到期）EffectivePriority 已压到 0
//
// 两边皆无候选返回 (nil, false)。
func (p *Pipeline) pickRoute(
	groupID int,
	model, protocol string,
	excludeKeys, excludeAccounts []int,
) (*routeTarget, bool) {
	var cands []routeTarget
	now := time.Now()

	if p.registry != nil {
		for _, k := range p.registry.ListCandidates(groupID, model, protocol, excludeKeys) {
			w := k.Weight + 10
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
		for _, a := range p.accounts.ListCandidates(groupID, model, excludeAccounts) {
			cands = append(cands, routeTarget{
				kind:     routeAccount,
				priority: a.EffectivePriority(now),
				weight:   a.Weight + 10,
				account:  a,
			})
		}
	}
	if len(cands) == 0 {
		return nil, false
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
	// 稳定排序：渠道 key 在前（同 id 空间不重叠），再按 id。
	sort.Slice(tier, func(i, j int) bool {
		if tier[i].kind != tier[j].kind {
			return tier[i].kind < tier[j].kind
		}
		return routeID(tier[i]) < routeID(tier[j])
	})

	total := 0
	for _, t := range tier {
		total += t.weight
	}
	if total <= 0 {
		total = len(tier)
	}
	n := p.routeRand(total)
	for i := range tier {
		n -= tier[i].weight
		if n < 0 {
			return &tier[i], true
		}
	}
	return &tier[len(tier)-1], true
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

func (p *Pipeline) routeRand(n int) int {
	if n <= 0 {
		return 0
	}
	if p.randFn != nil {
		return p.randFn(n)
	}
	return rand.IntN(n)
}
