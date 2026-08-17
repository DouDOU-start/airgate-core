package pipeline

import (
	"sort"
	"sync/atomic"
	"time"
)

const (
	maxRouteCatalogs    = 4096
	maxWeightedSchedule = 4096
)

type routeCatalogKey struct {
	groupID  int
	model    string
	protocol string
}

type routeRef struct {
	kind routeKind
	id   int
}

type weightedRouteRef struct {
	ref    routeRef
	weight int
}

type routeCatalog struct {
	channelVersion uint64
	accountVersion uint64
	expiresAt      time.Time
	buckets        []*routeCatalogBucket
}

type routeCatalogBucket struct {
	priority int
	schedule []routeRef
	cursor   atomic.Uint64
}

func (p *Pipeline) pickIndexedRouteWithChannelConfig(
	groupID int,
	model, protocol string,
	excludeKeys, excludeAccounts []int,
	channelConfig ChannelLatencyConfig,
) (routeTarget, bool) {
	catalog := p.loadRouteCatalog(groupID, model, protocol)
	if catalog == nil {
		return routeTarget{}, false
	}
	now := time.Now()
	for _, bucket := range catalog.buckets {
		var selected routeTarget
		selectedPosition := -1
		for range len(bucket.schedule) {
			position := bucket.cursor.Add(1) - 1
			ref := bucket.schedule[position%uint64(len(bucket.schedule))]
			target, ok := p.resolveIndexedRoute(ref, bucket.priority, groupID, model, protocol,
				excludeKeys, excludeAccounts, now)
			if !ok {
				continue
			}
			selected = target
			selectedPosition = int(position % uint64(len(bucket.schedule)))
			break
		}
		if selectedPosition < 0 {
			continue
		}
		// 对同类候选做有界探测：优先预计首字更快且负载更合适的目标。
		// 只读本实例内存状态，不增加 Redis/数据库 RTT；固定封顶避免退化为 O(n)。
		switch selected.kind {
		case routeAccount:
			if selected.account == nil {
				return selected, true
			}
			_, hasLatency := p.recentAccountFirstToken(selected.account.ID, model, now)
			if p.accountInflightCount(selected.account.ID) == 0 && !hasLatency {
				return selected, true
			}
			for offset := 1; offset < len(bucket.schedule) && offset <= accountLoadProbeLimit; offset++ {
				ref := bucket.schedule[(selectedPosition+offset)%len(bucket.schedule)]
				if ref.kind != routeAccount || ref.id == selected.account.ID {
					continue
				}
				candidate, ok := p.resolveIndexedRoute(ref, bucket.priority, groupID, model, protocol,
					excludeKeys, excludeAccounts, now)
				if ok {
					selected = p.preferAccountCandidate(selected, candidate, model, now)
				}
			}
		case routeChannel:
			if selected.channel == nil {
				return selected, true
			}
			if !channelConfig.Enabled {
				return selected, true
			}
			stats := p.recentChannelPerformanceWithConfig(selected.channel.KeyID, model, now, channelConfig)
			if p.channelInflightCount(selected.channel.KeyID) == 0 && !stats.hasLatency && stats.failureLevel == 0 {
				return selected, true
			}
			for offset := 1; offset < len(bucket.schedule) && offset <= channelConfig.ProbeLimit; offset++ {
				ref := bucket.schedule[(selectedPosition+offset)%len(bucket.schedule)]
				if ref.kind != routeChannel || ref.id == selected.channel.KeyID {
					continue
				}
				candidate, ok := p.resolveIndexedRoute(ref, bucket.priority, groupID, model, protocol,
					excludeKeys, excludeAccounts, now)
				if ok {
					selected = p.preferChannelCandidateWithConfig(selected, candidate, model, now, channelConfig)
				}
			}
		}
		return selected, true
	}
	return routeTarget{}, false
}

func (p *Pipeline) resolveIndexedRoute(
	ref routeRef,
	priority, groupID int,
	model, protocol string,
	excludeKeys, excludeAccounts []int,
	now time.Time,
) (routeTarget, bool) {
	switch ref.kind {
	case routeAccount:
		if routeExcluded(excludeAccounts, ref.id) || p.accounts == nil {
			return routeTarget{}, false
		}
		account, ok := p.accounts.RouteCandidate(ref.id, groupID, model, now)
		if !ok || account.EffectivePriority(now) != priority {
			return routeTarget{}, false
		}
		return routeTarget{
			kind: routeAccount, priority: priority,
			weight: effectiveRouteWeight(account.Weight), account: account,
		}, true
	case routeChannel:
		if routeExcluded(excludeKeys, ref.id) || p.registry == nil {
			return routeTarget{}, false
		}
		channel, ok := p.registry.RouteCandidate(ref.id, groupID, model, protocol, now)
		if !ok || channel.Priority != priority {
			return routeTarget{}, false
		}
		weight := effectiveRouteWeight(channel.Weight)
		if channel.HealthStatus == "degraded" {
			weight = max(weight/2, 1)
		}
		return routeTarget{
			kind: routeChannel, priority: priority,
			weight: weight, channel: channel,
		}, true
	default:
		return routeTarget{}, false
	}
}

func (p *Pipeline) loadRouteCatalog(groupID int, model, protocol string) *routeCatalog {
	key := routeCatalogKey{groupID: groupID, model: model, protocol: protocol}
	if catalog := p.cachedRouteCatalog(key); catalog != nil {
		return catalog
	}

	// 配置重载很少发生。串行执行冷构建，避免重载后的突发请求
	// 为每个请求重复构建相同的加权调度序列。
	p.routeCatalogMu.Lock()
	defer p.routeCatalogMu.Unlock()
	if catalog := p.cachedRouteCatalog(key); catalog != nil {
		return catalog
	}

	var last *routeCatalog
	for range 2 {
		var channelIDs, accountIDs []int
		channelVersion := uint64(0)
		accountVersion := uint64(0)
		if p.registry != nil {
			channelVersion, channelIDs = p.registry.RouteCandidateIDs(groupID, model, protocol)
		}
		if p.accounts != nil {
			accountVersion, accountIDs = p.accounts.RouteCandidateIDs(groupID, model)
		}
		catalog := p.buildRouteCatalog(channelVersion, accountVersion, channelIDs, accountIDs)
		if (p.registry == nil || p.registry.RouteVersion() == channelVersion) &&
			(p.accounts == nil || p.accounts.RouteVersion() == accountVersion) {
			_, existed := p.routeCatalogs.Load(key)
			p.routeCatalogs.Store(key, catalog)
			if !existed && p.routeCatalogCount.Add(1) > maxRouteCatalogs {
				p.routeCatalogs.Clear()
				p.routeCatalogCount.Store(0)
				p.routeCatalogs.Store(key, catalog)
				p.routeCatalogCount.Store(1)
			}
			return catalog
		}
		last = catalog
	}
	// 两次构建期间版本都被撞：返回本次构建结果一次性使用（不入缓存），
	// 候选可用性由 RouteCandidate 实时复核，轻微陈旧无碍；返回 nil 会让
	// 请求跳过 failover 直接 503。
	return last
}

func (p *Pipeline) cachedRouteCatalog(key routeCatalogKey) *routeCatalog {
	channelVersion := uint64(0)
	accountVersion := uint64(0)
	if p.registry != nil {
		channelVersion = p.registry.RouteVersion()
	}
	if p.accounts != nil {
		accountVersion = p.accounts.RouteVersion()
	}
	if cached, ok := p.routeCatalogs.Load(key); ok {
		catalog := cached.(*routeCatalog)
		if catalog.channelVersion == channelVersion && catalog.accountVersion == accountVersion &&
			(catalog.expiresAt.IsZero() || time.Now().Before(catalog.expiresAt)) {
			return catalog
		}
	}
	return nil
}

func (p *Pipeline) buildRouteCatalog(
	channelVersion, accountVersion uint64,
	channelIDs, accountIDs []int,
) *routeCatalog {
	now := time.Now()
	byPriority := make(map[int][]weightedRouteRef)
	catalog := &routeCatalog{channelVersion: channelVersion, accountVersion: accountVersion}
	for _, id := range channelIDs {
		channel, ok := p.registry.Snapshot(id)
		if !ok || channel == nil {
			continue
		}
		weight := effectiveRouteWeight(channel.Weight)
		if channel.HealthStatus == "degraded" {
			weight = max(weight/2, 1)
		}
		byPriority[channel.Priority] = append(byPriority[channel.Priority], weightedRouteRef{
			ref: routeRef{kind: routeChannel, id: id}, weight: weight,
		})
	}
	for _, id := range accountIDs {
		account, ok := p.accounts.Snapshot(id)
		if !ok || account == nil {
			continue
		}
		priority := account.EffectivePriority(now)
		byPriority[priority] = append(byPriority[priority], weightedRouteRef{
			ref: routeRef{kind: routeAccount, id: id}, weight: effectiveRouteWeight(account.Weight),
		})
		if account.State == "degraded" && account.StateUntil != nil && account.StateUntil.After(now) &&
			(catalog.expiresAt.IsZero() || account.StateUntil.Before(catalog.expiresAt)) {
			catalog.expiresAt = *account.StateUntil
		}
	}

	priorities := make([]int, 0, len(byPriority))
	for priority := range byPriority {
		priorities = append(priorities, priority)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(priorities)))
	catalog.buckets = make([]*routeCatalogBucket, 0, len(priorities))
	for _, priority := range priorities {
		schedule := buildWeightedSchedule(byPriority[priority])
		if len(schedule) > 0 {
			catalog.buckets = append(catalog.buckets, &routeCatalogBucket{priority: priority, schedule: schedule})
		}
	}
	return catalog
}

func buildWeightedSchedule(candidates []weightedRouteRef) []routeRef {
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ref.kind != candidates[j].ref.kind {
			return candidates[i].ref.kind < candidates[j].ref.kind
		}
		return candidates[i].ref.id < candidates[j].ref.id
	})
	weights := make([]int, len(candidates))
	gcd := 0
	for i, candidate := range candidates {
		weights[i] = effectiveRouteWeight(candidate.weight)
		gcd = greatestCommonDivisor(gcd, weights[i])
	}
	total := 0
	for i := range weights {
		weights[i] /= max(gcd, 1)
		total += weights[i]
	}
	if total > max(maxWeightedSchedule, len(candidates)) {
		target := max(maxWeightedSchedule, len(candidates))
		rawTotal := total
		total = 0
		for i, weight := range weights {
			weights[i] = max(1, int((int64(weight)*int64(target)+int64(rawTotal)/2)/int64(rawTotal)))
			total += weights[i]
		}
	}
	if total == len(candidates) {
		schedule := make([]routeRef, len(candidates))
		for i := range candidates {
			schedule[i] = candidates[i].ref
		}
		return schedule
	}

	nodes := make([]weightedScheduleNode, len(candidates))
	for i := range candidates {
		nodes[i] = weightedScheduleNode{candidate: i, count: weights[i]}
	}
	for i := len(nodes)/2 - 1; i >= 0; i-- {
		siftWeightedScheduleHeap(nodes, i, candidates)
	}
	schedule := make([]routeRef, 0, total)
	for range total {
		node := nodes[0]
		schedule = append(schedule, candidates[node.candidate].ref)
		node.emitted++
		if node.emitted == node.count {
			nodes[0] = nodes[len(nodes)-1]
			nodes = nodes[:len(nodes)-1]
		} else {
			nodes[0] = node
		}
		if len(nodes) > 0 {
			siftWeightedScheduleHeap(nodes, 0, candidates)
		}
	}
	return schedule
}

type weightedScheduleNode struct {
	candidate int
	emitted   int
	count     int
}

func siftWeightedScheduleHeap(nodes []weightedScheduleNode, root int, candidates []weightedRouteRef) {
	for {
		left := root*2 + 1
		if left >= len(nodes) {
			return
		}
		smallest := left
		if right := left + 1; right < len(nodes) && weightedScheduleNodeLess(nodes[right], nodes[left], candidates) {
			smallest = right
		}
		if !weightedScheduleNodeLess(nodes[smallest], nodes[root], candidates) {
			return
		}
		nodes[root], nodes[smallest] = nodes[smallest], nodes[root]
		root = smallest
	}
}

func weightedScheduleNodeLess(left, right weightedScheduleNode, candidates []weightedRouteRef) bool {
	// 使用中点位置，让重复候选均匀分散在整个调度周期内。
	leftPosition := int64(2*left.emitted+1) * int64(right.count)
	rightPosition := int64(2*right.emitted+1) * int64(left.count)
	if leftPosition != rightPosition {
		return leftPosition < rightPosition
	}
	leftRef := candidates[left.candidate].ref
	rightRef := candidates[right.candidate].ref
	if leftRef.kind != rightRef.kind {
		return leftRef.kind < rightRef.kind
	}
	return leftRef.id < rightRef.id
}

func greatestCommonDivisor(left, right int) int {
	for right != 0 {
		left, right = right, left%right
	}
	return left
}

func routeExcluded(excluded []int, id int) bool {
	for _, candidate := range excluded {
		if candidate == id {
			return true
		}
	}
	return false
}
