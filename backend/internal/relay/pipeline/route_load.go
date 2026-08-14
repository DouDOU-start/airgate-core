package pipeline

import "sync/atomic"

const accountLoadProbeLimit = 8

// accountInflightCounter 返回账号的本实例在途计数器。
// 账号集合规模有明确上限，计数器保留复用可避免热路径反复分配与删除竞态。
func (p *Pipeline) accountInflightCounter(accountID int) *atomic.Int64 {
	if p == nil || accountID <= 0 {
		return nil
	}
	value, _ := p.accountInflight.LoadOrStore(accountID, &atomic.Int64{})
	return value.(*atomic.Int64)
}

func (p *Pipeline) accountInflightCount(accountID int) int64 {
	counter := p.accountInflightCounter(accountID)
	if counter == nil {
		return 0
	}
	return max(counter.Load(), 0)
}

// trackAccountAttempt 在真实 CPA attempt 生命周期内维护本实例账号在途量。
func (p *Pipeline) trackAccountAttempt(accountID int) func() {
	counter := p.accountInflightCounter(accountID)
	if counter == nil {
		return func() {}
	}
	counter.Add(1)
	var released atomic.Bool
	return func() {
		if !released.CompareAndSwap(false, true) {
			return
		}
		// 正常路径严格成对；CAS 兜底避免未来调用方误释放导致负数污染调度。
		for {
			current := counter.Load()
			if current <= 0 || counter.CompareAndSwap(current, current-1) {
				return
			}
		}
	}
}

// preferLessLoadedAccount 在两个同优先级账号之间执行加权最小在途比较。
// (在途+1)/权重 更小者优先；完全空闲时保留原加权轮询结果，维持配置分流比例。
func (p *Pipeline) preferLessLoadedAccount(current, candidate routeTarget) routeTarget {
	if current.kind != routeAccount || candidate.kind != routeAccount ||
		current.account == nil || candidate.account == nil {
		return current
	}
	currentLoad := p.accountInflightCount(current.account.ID)
	if currentLoad == 0 {
		return current
	}
	candidateLoad := p.accountInflightCount(candidate.account.ID)
	currentWeight := int64(effectiveRouteWeight(current.weight))
	candidateWeight := int64(effectiveRouteWeight(candidate.weight))
	if (candidateLoad+1)*currentWeight < (currentLoad+1)*candidateWeight {
		return candidate
	}
	return current
}
