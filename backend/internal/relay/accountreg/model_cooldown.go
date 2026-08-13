package accountreg

import (
	"sync"
	"time"
)

// (账号, 模型) 级 429 冷却：对齐 CLIProxyAPI 的模型级隔离——同一账号下
// 一个模型被限流不拖死其余模型。纯运行时状态（不落库，重启即清空），
// 与账号级 rate_limited 状态机并存：单模型限流只冷却该模型；当账号
// 全部可服务模型都在冷却时，调用方（pipeline）才升级为账号级
// rate_limited，走既有的单飞恢复探测。
//
// 退避语义对齐 CPA quotaCooldownAfterFailure：同一冷却窗口内的并发失败
// 复用当前窗口、退避等级每窗口最多升一级（防在途请求集中失败把等级
// 一次性推满）；冷却时长附加随机抖动，避免恢复时刻惊群。

type modelCooldownKey struct {
	accountID int
	model     string
}

type modelCooldownEntry struct {
	until        time.Time
	backoffLevel int
}

const (
	modelCooldownBackoffBase = 5 * time.Second
	modelCooldownBackoffMax  = 30 * time.Minute
	// modelCooldownSweepSize 超过该规模时在写路径顺带清理长期过期条目。
	modelCooldownSweepSize = 4096
	// modelCooldownStaleDur 过期超过该时长的条目连同退避等级一起遗忘。
	modelCooldownStaleDur = time.Hour
)

// modelCooldownState 与 Registry 组合（独立锁，避免与主快照锁互相阻塞）。
type modelCooldownState struct {
	mu      sync.RWMutex
	entries map[modelCooldownKey]modelCooldownEntry
}

// MarkModelRateLimited 记录 (账号, 模型) 一次 429：retryAfter>0 时优先采用
// 上游值，否则按指数退避（5s 起、30min 封顶、每窗口最多升一级）；返回冷却
// 截止时间。
func (r *Registry) MarkModelRateLimited(accountID int, model string, retryAfter time.Duration) time.Time {
	if r == nil || model == "" {
		return time.Time{}
	}
	now := time.Now()
	key := modelCooldownKey{accountID: accountID, model: model}

	r.modelCooldown.mu.Lock()
	defer r.modelCooldown.mu.Unlock()
	if r.modelCooldown.entries == nil {
		r.modelCooldown.entries = make(map[modelCooldownKey]modelCooldownEntry)
	}
	entry := r.modelCooldown.entries[key]
	if entry.until.After(now) {
		// 同窗口并发失败：只按上游 Retry-After 延展截止时间，不升级退避。
		if retryAfter > 0 {
			if until := now.Add(retryAfter); until.After(entry.until) {
				entry.until = until
				r.modelCooldown.entries[key] = entry
			}
		}
		return entry.until
	}

	var cooldown time.Duration
	if retryAfter > 0 {
		cooldown = retryAfter
	} else {
		cooldown = modelCooldownBackoff(entry.backoffLevel)
		entry.backoffLevel++
	}
	cooldown += r.modelCooldownJitter(cooldown)
	entry.until = now.Add(cooldown)
	r.modelCooldown.entries[key] = entry

	if len(r.modelCooldown.entries) > modelCooldownSweepSize {
		staleBefore := now.Add(-modelCooldownStaleDur)
		for k, e := range r.modelCooldown.entries {
			if e.until.Before(staleBefore) {
				delete(r.modelCooldown.entries, k)
			}
		}
	}
	return entry.until
}

// ClearModelRateLimited 该 (账号, 模型) 请求成功后清除冷却与退避等级。
func (r *Registry) ClearModelRateLimited(accountID int, model string) {
	if r == nil || model == "" {
		return
	}
	key := modelCooldownKey{accountID: accountID, model: model}
	r.modelCooldown.mu.RLock()
	_, exists := r.modelCooldown.entries[key]
	r.modelCooldown.mu.RUnlock()
	if !exists {
		return
	}
	r.modelCooldown.mu.Lock()
	delete(r.modelCooldown.entries, key)
	r.modelCooldown.mu.Unlock()
}

// isModelRateLimited 判定 (账号, 模型) 是否处于冷却（只读，不做惰性删除）。
func (r *Registry) isModelRateLimited(accountID int, model string, now time.Time) bool {
	if r == nil || model == "" {
		return false
	}
	r.modelCooldown.mu.RLock()
	entry, ok := r.modelCooldown.entries[modelCooldownKey{accountID: accountID, model: model}]
	r.modelCooldown.mu.RUnlock()
	return ok && entry.until.After(now)
}

// AllModelsRateLimited 账号全部可服务模型是否都在冷却中（升级账号级
// rate_limited 的判定条件）。模型集合未知（空）时返回 false，不聚合。
func (r *Registry) AllModelsRateLimited(accountID int, now time.Time) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	account := r.accounts[accountID]
	r.mu.RUnlock()
	if account == nil || len(account.Models) == 0 {
		return false
	}
	// Models 为 copy-on-write 只读 map，解锁后读取安全。
	r.modelCooldown.mu.RLock()
	defer r.modelCooldown.mu.RUnlock()
	for model := range account.Models {
		entry, ok := r.modelCooldown.entries[modelCooldownKey{accountID: accountID, model: model}]
		if !ok || !entry.until.After(now) {
			return false
		}
	}
	return true
}

func modelCooldownBackoff(level int) time.Duration {
	cooldown := modelCooldownBackoffBase
	for i := 0; i < level && cooldown < modelCooldownBackoffMax; i++ {
		cooldown *= 2
	}
	return min(cooldown, modelCooldownBackoffMax)
}

// modelCooldownJitter 返回 [0, min(cooldown/4, 2s)) 的随机抖动，防恢复惊群。
func (r *Registry) modelCooldownJitter(cooldown time.Duration) time.Duration {
	limit := min(cooldown/4, 2*time.Second)
	if limit <= 0 {
		return 0
	}
	return time.Duration(r.randFn(int(limit)))
}
