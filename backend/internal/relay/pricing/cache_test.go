package pricing

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
)

func TestComputeCosts(t *testing.T) {
	cases := []struct {
		name              string
		price             Price
		usage             Usage
		wantInput         float64
		wantOutput        float64
		wantCached        float64
		wantCacheCreation float64
	}{
		{
			name:  "常规 token 计费（无缓存）",
			price: Price{Input: 3, Output: 15},
			usage: Usage{PromptTokens: 1_000_000, CompletionTokens: 2_000_000},
			// 1M * 3/1M = 3；2M * 15/1M = 30
			wantInput:  3,
			wantOutput: 30,
		},
		{
			name:  "cached 从 prompt 扣减避免双计",
			price: Price{Input: 3, Output: 15, CachedInput: 0.3},
			usage: Usage{PromptTokens: 1_000_000, CompletionTokens: 500_000, CachedTokens: 400_000},
			// input 按 (1M-0.4M)=0.6M 计：0.6*3=1.8；cached 按 0.4M*0.3=0.12
			wantInput:  1.8,
			wantOutput: 7.5,
			wantCached: 0.12,
		},
		{
			name:  "cache_creation 独立计价",
			price: Price{Input: 3, Output: 15, CachedInput: 0.3, CacheCreation: 3.75},
			usage: Usage{PromptTokens: 100_000, CacheCreationTokens: 200_000},
			// input 0.1M*3=0.3；cacheCreation 0.2M*3.75=0.75
			wantInput:         0.3,
			wantCacheCreation: 0.75,
		},
		{
			name:  "cached 超过 prompt 时 input 钳制为 0",
			price: Price{Input: 3, CachedInput: 0.3},
			usage: Usage{PromptTokens: 100_000, CachedTokens: 200_000},
			// prompt-cached 为负 → input 0；cached 仍按 0.2M 计
			wantInput:  0,
			wantCached: 0.06,
		},
		{
			name:      "per_request 整单替换，忽略全部 token 单价",
			price:     Price{Input: 3, Output: 15, CachedInput: 0.3, CacheCreation: 3.75, PerRequest: 0.02},
			usage:     Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000, CachedTokens: 500_000, CacheCreationTokens: 500_000},
			wantInput: 0.02,
		},
		{
			name:  "零用量零成本",
			price: Price{Input: 3, Output: 15},
			usage: Usage{},
		},
	}

	const eps = 1e-9
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, output, cached, cacheCreation := ComputeCosts(tc.price, tc.usage)
			check := func(field string, got, want float64) {
				t.Helper()
				if math.Abs(got-want) > eps {
					t.Errorf("%s 期望 %v，实际 %v", field, want, got)
				}
			}
			check("input", input, tc.wantInput)
			check("output", output, tc.wantOutput)
			check("cached", cached, tc.wantCached)
			check("cacheCreation", cacheCreation, tc.wantCacheCreation)
		})
	}
}

// fakePriceLoader 记录加载次数的价目表加载器。
type fakePriceLoader struct {
	mu     sync.Mutex
	prices map[string]Price
	err    error
	loads  int
}

func (f *fakePriceLoader) LoadAllPrices(context.Context) (map[string]Price, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loads++
	if f.err != nil {
		return nil, f.err
	}
	return f.prices, nil
}

func (f *fakePriceLoader) loadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loads
}

func TestCacheGetAndInvalidate(t *testing.T) {
	loader := &fakePriceLoader{prices: map[string]Price{
		"gpt-4o": {Input: 2.5, Output: 10},
	}}
	cache := NewCache(loader)

	// 首次 Get 惰性加载。
	price, ok := cache.Get("gpt-4o")
	if !ok || price.Input != 2.5 {
		t.Fatalf("期望命中 gpt-4o 价格，实际 ok=%v price=%+v", ok, price)
	}
	if loader.loadCount() != 1 {
		t.Fatalf("期望加载 1 次，实际 %d", loader.loadCount())
	}

	// 缓存命中不重复加载；未配置模型返回 false。
	if _, ok := cache.Get("unknown-model"); ok {
		t.Fatal("未配置模型不应命中")
	}
	if loader.loadCount() != 1 {
		t.Fatalf("缓存命中期仍触发加载：%d 次", loader.loadCount())
	}

	// 写失效后下一次 Get 重载并读到新价。
	loader.mu.Lock()
	loader.prices = map[string]Price{"gpt-4o": {Input: 5}}
	loader.mu.Unlock()
	cache.Invalidate()
	price, ok = cache.Get("gpt-4o")
	if !ok || price.Input != 5 {
		t.Fatalf("失效重载后期望 Input=5，实际 ok=%v price=%+v", ok, price)
	}
	if loader.loadCount() != 2 {
		t.Fatalf("期望共加载 2 次，实际 %d", loader.loadCount())
	}
}

func TestCacheGetLoaderError(t *testing.T) {
	loader := &fakePriceLoader{err: errors.New("db down")}
	cache := NewCache(loader)

	// 加载失败按缺价处理，不 panic；后续调用继续尝试重载。
	if _, ok := cache.Get("gpt-4o"); ok {
		t.Fatal("加载失败时不应命中价格")
	}
	loader.mu.Lock()
	loader.err = nil
	loader.prices = map[string]Price{"gpt-4o": {Input: 1}}
	loader.mu.Unlock()
	if _, ok := cache.Get("gpt-4o"); !ok {
		t.Fatal("加载恢复后应命中价格")
	}
}

// TestComputeCostsClampsNegative 负数 token 一律钳 0：不虚增 input 费用、不产生负成本。
func TestComputeCostsClampsNegative(t *testing.T) {
	const eps = 1e-9
	cases := []struct {
		name       string
		price      Price
		usage      Usage
		wantInput  float64
		wantOutput float64
		wantCached float64
		wantCC     float64
	}{
		{
			name:  "负 cached 不得虚增 input（1000-(-500) 的漏洞）",
			price: Price{Input: 10, CachedInput: 5},
			usage: Usage{PromptTokens: 1_000_000, CachedTokens: -500_000},
			// cached 钳 0 → input 按 1M 计，而不是 1.5M
			wantInput: 10,
		},
		{
			name:       "负 completion 不产生负 output 成本",
			price:      Price{Output: 30},
			usage:      Usage{CompletionTokens: -1_000_000},
			wantOutput: 0,
		},
		{
			name:  "全负用量全零成本",
			price: Price{Input: 3, Output: 15, CachedInput: 0.3, CacheCreation: 3.75},
			usage: Usage{PromptTokens: -1, CompletionTokens: -2, CachedTokens: -3, CacheCreationTokens: -4},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, output, cached, cc := ComputeCosts(tc.price, tc.usage)
			check := func(field string, got, want float64) {
				t.Helper()
				if math.Abs(got-want) > eps {
					t.Errorf("%s 期望 %v，实际 %v", field, want, got)
				}
			}
			check("input", input, tc.wantInput)
			check("output", output, tc.wantOutput)
			check("cached", cached, tc.wantCached)
			check("cacheCreation", cc, tc.wantCC)
		})
	}
}

// blockingLoader 可阻塞的加载器：模拟 in-flight Reload 与 Invalidate 的竞态时序。
type blockingLoader struct {
	mu      sync.Mutex
	prices  map[string]Price
	loads   int
	started chan struct{} // 每次加载开始时发信号
	release chan struct{} // 收到信号后才返回
}

func (b *blockingLoader) LoadAllPrices(context.Context) (map[string]Price, error) {
	b.mu.Lock()
	b.loads++
	prices := make(map[string]Price, len(b.prices))
	for k, v := range b.prices {
		prices[k] = v
	}
	b.mu.Unlock()
	if b.started != nil {
		b.started <- struct{}{}
	}
	if b.release != nil {
		<-b.release
	}
	return prices, nil
}

func (b *blockingLoader) setPrices(p map[string]Price) {
	b.mu.Lock()
	b.prices = p
	b.mu.Unlock()
}

func (b *blockingLoader) loadCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.loads
}

// TestCacheInvalidateDuringReloadDiscardsStale 加载期间发生 Invalidate 时，
// in-flight 重载的旧快照必须被丢弃并重新加载，不得吞掉后到的失效（旧价无限期驻留）。
func TestCacheInvalidateDuringReloadDiscardsStale(t *testing.T) {
	loader := &blockingLoader{
		prices:  map[string]Price{"gpt-4o": {Input: 1}}, // 旧价
		started: make(chan struct{}, 4),
		release: make(chan struct{}, 4),
	}
	cache := NewCache(loader)

	done := make(chan error, 1)
	go func() { done <- cache.Reload(context.Background()) }()

	<-loader.started // 第一次加载已拿到旧价快照、尚未存储

	// 模拟写路径：新价提交 + Invalidate（gen++）。
	loader.setPrices(map[string]Price{"gpt-4o": {Input: 2}})
	cache.Invalidate()

	// 放行两次加载：第一次结果因代际不匹配被丢弃，Reload 内部用新代际重载。
	loader.release <- struct{}{}
	<-loader.started
	loader.release <- struct{}{}

	if err := <-done; err != nil {
		t.Fatalf("Reload 失败: %v", err)
	}
	price, ok := cache.Get("gpt-4o")
	if !ok || price.Input != 2 {
		t.Fatalf("旧快照未被丢弃: ok=%v price=%+v（want Input=2）", ok, price)
	}
	if loader.loadCount() != 2 {
		t.Errorf("加载次数 = %d, want 2（丢弃后重载一次）", loader.loadCount())
	}
}

// TestCacheConcurrentGetsMergeReload 失效后的并发惰性重载合并为一次加载（无惊群）。
func TestCacheConcurrentGetsMergeReload(t *testing.T) {
	loader := &blockingLoader{
		prices:  map[string]Price{"gpt-4o": {Input: 3}},
		started: make(chan struct{}, 16),
		release: make(chan struct{}, 16),
	}
	cache := NewCache(loader)

	const n = 8
	var wg sync.WaitGroup
	results := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, ok := cache.Get("gpt-4o")
			results[i] = ok
		}(i)
	}

	<-loader.started // 恰有一个 goroutine 进入加载
	loader.release <- struct{}{}
	wg.Wait()

	if got := loader.loadCount(); got != 1 {
		t.Errorf("并发 Get 触发加载 %d 次, want 1（应合并）", got)
	}
	for i, ok := range results {
		if !ok {
			t.Errorf("goroutine %d 未命中价格", i)
		}
	}
}
