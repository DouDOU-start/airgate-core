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
	Input         float64
	Output        float64
	CachedInput   float64
	CacheCreation float64
	// PerRequest 按次价：>0 时整单按次计费，忽略全部 token 单价。
	PerRequest float64
}

// Usage 一次请求的 token 用量（上游口径：PromptTokens 包含 CachedTokens）。
type Usage struct {
	PromptTokens        int
	CompletionTokens    int
	CachedTokens        int
	CacheCreationTokens int
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

// ComputeCosts 按价格与用量计算四段成本（纯函数）。
//
// 规则：
//   - 入口对四个 token 计数统一钳 0（上游为不可信第三方，负数计数会导致
//     input 费用虚增或负成本写入 usage_log）；
//   - PerRequest > 0：整单按次计费，input=PerRequest，其余为 0；
//   - 否则按 token 计费。上游口径 PromptTokens 包含 CachedTokens，
//     为避免双计，input 按 (prompt-cached) 扣减后计价，cached 部分单独按
//     CachedInput 计入 cached；cacheCreation 按 CacheCreation 单价另计。
func ComputeCosts(p Price, u Usage) (input, output, cached, cacheCreation float64) {
	u.PromptTokens = clampNonNegative(u.PromptTokens)
	u.CompletionTokens = clampNonNegative(u.CompletionTokens)
	u.CachedTokens = clampNonNegative(u.CachedTokens)
	u.CacheCreationTokens = clampNonNegative(u.CacheCreationTokens)

	if p.PerRequest > 0 {
		return p.PerRequest, 0, 0, 0
	}
	promptTokens := u.PromptTokens - u.CachedTokens
	if promptTokens < 0 {
		promptTokens = 0
	}
	input = float64(promptTokens) / 1e6 * p.Input
	output = float64(u.CompletionTokens) / 1e6 * p.Output
	cached = float64(u.CachedTokens) / 1e6 * p.CachedInput
	cacheCreation = float64(u.CacheCreationTokens) / 1e6 * p.CacheCreation
	return input, output, cached, cacheCreation
}

// clampNonNegative 负值钳 0。
func clampNonNegative(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
