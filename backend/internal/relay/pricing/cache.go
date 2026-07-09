// Package pricing 提供模型价目表的进程内缓存与成本计算。
//
// 缓存策略：写失效——modelprice 写路径成功后调 Invalidate 清空缓存，
// 下一次 Get 惰性全量重载；无 TTL。
//
// 依赖约束：本包禁止 import ent 或 internal/app/*（防环）；
// 数据加载经 Loader 窄接口由外部注入（由 modelprice service 实现）。
package pricing

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Price 单模型价格快照。单位：USD / 1M tokens；PerRequest 为 USD / 次。
type Price struct {
	Input       float64
	Output      float64
	CachedInput float64
	// CacheCreation5m 缓存写入 5m TTL 单价（modelprice.cache_creation_price）。
	CacheCreation5m float64
	// CacheCreation1h 缓存写入 1h TTL 单价（modelprice.cache_creation_1h_price）。
	CacheCreation1h float64
	// PerRequest 按次价：>0 时整单按次计费，忽略全部 token 单价。
	PerRequest float64

	// ServiceTiers 服务档倍率表（如 priority=2.0、flex=0.5）；命中时整单各维度统一乘该倍率。
	ServiceTiers map[string]float64
	// LongContext 长上下文阶梯：完整 prompt 超过阈值时各维度单价按各自倍率放大。
	LongContext *LongContextRule
}

// LongContextRule 长上下文阶梯规则：Usage.PromptTokens > ThresholdTokens 时各维度单价乘对应倍率。
type LongContextRule struct {
	ThresholdTokens int
	InputMul        float64
	OutputMul       float64
	CachedMul       float64
}

// Usage 一次请求的 token 用量（上游口径：PromptTokens 包含 CachedTokens）。
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	// CacheCreationTokens 泛化缓存写入 token（无 5m/1h 明细时按 5m 档计价的回退）。
	CacheCreationTokens int
	// CacheCreation5mTokens / CacheCreation1hTokens Claude 双档缓存写入明细；openai 入口恒 0。
	CacheCreation5mTokens int
	CacheCreation1hTokens int
	// Calls 按次计费的计次数（图像端点=响应产出张数）；仅 PerRequest>0 时参与计算，
	// 0 或负数视为 1（chat 等 token 端点不设该字段，行为与历史一致）。
	Calls int
}

// Costs ComputeCosts 的分段成本结果（缓存写入拆 5m/1h 两档，供分列落账）。
type Costs struct {
	Input           float64
	Output          float64
	Cached          float64
	CacheCreation5m float64
	CacheCreation1h float64
}

// Loader 全量加载价目表（由 modelprice service 实现）。
type Loader interface {
	LoadAllPrices(ctx context.Context) (map[string]Price, error)
}

// Cache 价目表进程内缓存：RWMutex 保护的全量 map + 写失效惰性重载。
type Cache struct {
	loader Loader

	mu     sync.RWMutex
	prices map[string]Price
	loaded bool
	// gen 失效代际：Invalidate 时 +1。Reload 加载前记录代际，
	// 存储时代际已变则丢弃本次结果，防止 in-flight 重载把旧价目表写回、
	// 吞掉后到的失效（并发写价场景旧价无限期驻留）。
	gen uint64

	// reloadMu 串行化整个 load+store：并发惰性重载合并为一次加载（singleflight 等价）。
	reloadMu sync.Mutex

	// reloadTimeout 惰性重载超时（Get 无 ctx，用后台超时上下文兜底）。
	reloadTimeout time.Duration
}

// NewCache 创建价目表缓存。
func NewCache(loader Loader) *Cache {
	return &Cache{loader: loader, reloadTimeout: 5 * time.Second}
}

// Reload 全量重载价目表（启动预热与惰性加载共用）。
//
// reloadMu 串行化并发重载：后到者拿到锁时若缓存已有效直接复用，
// 消除 Invalidate 后并发 Get 的惊群（singleflight 等价）；
// 加载前记录代际、存储前比较代际，加载期间发生 Invalidate 则丢弃本次
// （可能早于最新写入的）快照并重新加载，防止旧价目表覆盖新失效。
func (c *Cache) Reload(ctx context.Context) error {
	c.reloadMu.Lock()
	defer c.reloadMu.Unlock()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		c.mu.RLock()
		startGen := c.gen
		loaded := c.loaded
		c.mu.RUnlock()
		if loaded {
			return nil
		}

		prices, err := c.loader.LoadAllPrices(ctx)
		if err != nil {
			return err
		}

		c.mu.Lock()
		if c.gen == startGen {
			c.prices = prices
			c.loaded = true
			c.mu.Unlock()
			return nil
		}
		// 加载期间发生 Invalidate：丢弃过期快照，携最新代际重新加载。
		c.mu.Unlock()
	}
}

// Get 查询模型价格；未配置返回 (Price{}, false)。
// 缓存失效（或从未加载）时同步惰性重载，重载失败按缺价处理并记日志。
func (c *Cache) Get(model string) (Price, bool) {
	c.mu.RLock()
	if c.loaded {
		price, ok := c.prices[model]
		c.mu.RUnlock()
		return price, ok
	}
	c.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), c.reloadTimeout)
	defer cancel()
	if err := c.Reload(ctx); err != nil {
		slog.Error("model_price_cache_reload_failed", "error", err)
		return Price{}, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	price, ok := c.prices[model]
	return price, ok
}

// Invalidate 使缓存失效：清空数据并推进代际，下一次 Get 触发全量重载；
// in-flight 的旧重载结果会因代际不匹配被丢弃。
// modelprice 写路径（创建/更新/删除/导入）成功后调用。
func (c *Cache) Invalidate() {
	c.mu.Lock()
	c.prices = nil
	c.loaded = false
	c.gen++
	c.mu.Unlock()
}

// ComputeCosts 按价格与用量计算分段成本（纯函数）。
//
// 计算顺序（严格）：
//  1. 全部 token 计数统一钳 0（上游为不可信第三方，负数会虚增 input 费用或写入负成本）。
//  2. PerRequest > 0：整单按次计费，Input = PerRequest × max(Calls, 1)
//     （图像端点 Calls=响应产出张数；chat 等未设 Calls 恒按 1 次），
//     其余为 0（忽略全部 token 单价 / 服务档 / 长上下文）。
//  3. 取 base 单价 inR/outR/cachedR；若 LongContext!=nil 且 PromptTokens 超阈值，
//     各单价乘对应倍率（长上下文阶梯，阈值比较对象是含 cached 的完整 prompt）。
//  4. 缓存写入分档：cc5mTokens = CacheCreation5mTokens>0 ? 它 : CacheCreationTokens（泛化回退当 5m）；
//     cc1hTokens = CacheCreation1hTokens。
//  5. 分段计价：input 按 (prompt-cached) 扣减避免与 cached 双计。
//  6. 服务档：serviceTier 非空且非 standard/auto，且 ServiceTiers[serviceTier]>0，
//     则整单五项统一乘该倍率（priority/flex 对各维度倍率一致，按整单处理等价）。
func ComputeCosts(p Price, u Usage, serviceTier string) Costs {
	u.PromptTokens = clampNonNegative(u.PromptTokens)
	u.CompletionTokens = clampNonNegative(u.CompletionTokens)
	u.CachedTokens = clampNonNegative(u.CachedTokens)
	u.CacheCreationTokens = clampNonNegative(u.CacheCreationTokens)
	u.CacheCreation5mTokens = clampNonNegative(u.CacheCreation5mTokens)
	u.CacheCreation1hTokens = clampNonNegative(u.CacheCreation1hTokens)

	if p.PerRequest > 0 {
		calls := u.Calls
		if calls < 1 {
			calls = 1
		}
		return Costs{Input: p.PerRequest * float64(calls)}
	}

	inR, outR, cachedR := p.Input, p.Output, p.CachedInput
	if p.LongContext != nil && u.PromptTokens > p.LongContext.ThresholdTokens {
		inR *= p.LongContext.InputMul
		outR *= p.LongContext.OutputMul
		cachedR *= p.LongContext.CachedMul
	}

	// 计费忠实于上游 usage：给了 5m/1h 明细就按明细分档；仅当双档明细
	// 完全缺失时，泛化总量才按 5m 档兜底（Anthropic 旧形态 cache_creation_input_tokens
	// 语义即 5m 写入）。只看 5m 是否为零会把「纯 1h 缓存写」的总量误按 5m 再计一次（双计）。
	cc5mTokens := u.CacheCreation5mTokens
	cc1hTokens := u.CacheCreation1hTokens
	if cc5mTokens == 0 && cc1hTokens == 0 {
		cc5mTokens = u.CacheCreationTokens
	}

	promptTokens := u.PromptTokens - u.CachedTokens
	if promptTokens < 0 {
		promptTokens = 0
	}
	costs := Costs{
		Input:           float64(promptTokens) / 1e6 * inR,
		Output:          float64(u.CompletionTokens) / 1e6 * outR,
		Cached:          float64(u.CachedTokens) / 1e6 * cachedR,
		CacheCreation5m: float64(cc5mTokens) / 1e6 * p.CacheCreation5m,
		CacheCreation1h: float64(cc1hTokens) / 1e6 * p.CacheCreation1h,
	}

	if m, ok := serviceTierMultiplier(p.ServiceTiers, serviceTier); ok {
		costs.Input *= m
		costs.Output *= m
		costs.Cached *= m
		costs.CacheCreation5m *= m
		costs.CacheCreation1h *= m
	}
	return costs
}

// serviceTierMultiplier 返回服务档倍率：tier 为空或 standard/auto（默认档）不生效；
// 未在 ServiceTiers 声明或倍率 <=0 也不生效。
func serviceTierMultiplier(tiers map[string]float64, tier string) (float64, bool) {
	if tier == "" || tier == "standard" || tier == "auto" {
		return 0, false
	}
	m, ok := tiers[tier]
	if !ok || m <= 0 {
		return 0, false
	}
	return m, true
}

// clampNonNegative 负值钳 0。
func clampNonNegative(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
