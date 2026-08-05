package pricing

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
)

// checkCosts 逐段断言 Costs（eps 容差）。
func checkCosts(t *testing.T, got, want Costs) {
	t.Helper()
	const eps = 1e-9
	cmp := func(field string, g, w float64) {
		if math.Abs(g-w) > eps {
			t.Errorf("%s 期望 %v，实际 %v", field, w, g)
		}
	}
	cmp("input", got.Input, want.Input)
	cmp("output", got.Output, want.Output)
	cmp("cached", got.Cached, want.Cached)
	cmp("cacheCreation5m", got.CacheCreation5m, want.CacheCreation5m)
	cmp("cacheCreation1h", got.CacheCreation1h, want.CacheCreation1h)
}

// longCtxRule gpt-5.4 家族长上下文阶梯：阈值 272k，input×2 / output×1.5 / cached×2。
var longCtxRule = &LongContextRule{ThresholdTokens: 272_000, InputMul: 2, OutputMul: 1.5, CachedMul: 2}

func TestComputeCosts(t *testing.T) {
	cases := []struct {
		name  string
		price Price
		usage Usage
		tier  string
		want  Costs
	}{
		{
			name:  "常规 token 计费（无缓存）",
			price: Price{Input: 3, Output: 15},
			usage: Usage{PromptTokens: 1_000_000, CompletionTokens: 2_000_000},
			want:  Costs{Input: 3, Output: 30},
		},
		{
			name:  "cached 从 prompt 扣减避免双计",
			price: Price{Input: 3, Output: 15, CachedInput: 0.3},
			usage: Usage{PromptTokens: 1_000_000, CompletionTokens: 500_000, CachedTokens: 400_000},
			want:  Costs{Input: 1.8, Output: 7.5, Cached: 0.12},
		},
		{
			name:  "cache_creation 5m 泛化回退计价",
			price: Price{Input: 3, CacheCreation5m: 3.75},
			usage: Usage{PromptTokens: 100_000, CacheCreationTokens: 200_000},
			// 无 5m 明细 → CacheCreationTokens 当 5m：0.2M*3.75=0.75
			want: Costs{Input: 0.3, CacheCreation5m: 0.75},
		},
		{
			name:  "cache_creation 5m 明细优先于泛化回退",
			price: Price{CacheCreation5m: 3.75},
			usage: Usage{CacheCreation5mTokens: 100_000, CacheCreationTokens: 999_000},
			// 5m 明细 >0 → 用 100k，不用泛化的 999k：0.1M*3.75=0.375
			want: Costs{CacheCreation5m: 0.375},
		},
		{
			name:  "cache_creation 1h 独立档计价",
			price: Price{CacheCreation5m: 3.75, CacheCreation1h: 6.0},
			usage: Usage{CacheCreation5mTokens: 100_000, CacheCreation1hTokens: 200_000},
			// 5m 0.1M*3.75=0.375；1h 0.2M*6=1.2
			want: Costs{CacheCreation5m: 0.375, CacheCreation1h: 1.2},
		},
		{
			name:  "纯 1h 缓存写：总量不得按 5m 回退双计",
			price: Price{CacheCreation5m: 3.75, CacheCreation1h: 6.0},
			usage: Usage{CacheCreationTokens: 200_000, CacheCreation1hTokens: 200_000},
			// Anthropic 只用 1h 缓存时总量=1h 明细、5m 明细为 0；
			// 总量不得再按 5m 档兜底：只计 1h 0.2M*6=1.2
			want: Costs{CacheCreation1h: 1.2},
		},
		{
			name:  "cached 超过 prompt 时 input 钳制为 0",
			price: Price{Input: 3, CachedInput: 0.3},
			usage: Usage{PromptTokens: 100_000, CachedTokens: 200_000},
			want:  Costs{Cached: 0.06},
		},
		{
			name:  "per_request 整单替换，忽略 token/服务档/长上下文",
			price: Price{Input: 3, Output: 15, CachedInput: 0.3, CacheCreation5m: 3.75, CacheCreation1h: 6, PerRequest: 0.02, ServiceTiers: map[string]float64{"priority": 2}, LongContext: longCtxRule},
			usage: Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000, CachedTokens: 500_000, CacheCreationTokens: 500_000},
			tier:  "priority",
			want:  Costs{Input: 0.02},
		},
		{
			name:  "per_request × 张数（图像端点 Calls=产出张数）",
			price: Price{PerRequest: 0.04},
			usage: Usage{Calls: 3},
			want:  Costs{Input: 0.12},
		},
		{
			name:  "per_request Calls=0 视为 1（chat 等既有路径回归）",
			price: Price{PerRequest: 0.02},
			usage: Usage{PromptTokens: 1_000_000, CompletionTokens: 500_000},
			want:  Costs{Input: 0.02},
		},
		{
			name:  "per_request 负 Calls 钳为 1（上游不可信）",
			price: Price{PerRequest: 0.02},
			usage: Usage{Calls: -5},
			want:  Costs{Input: 0.02},
		},
		{
			name:  "分辨率表 quality:size 命中 × 张数",
			price: Price{ImageSizePrices: map[string]float64{"high:1024x1024": 0.167}},
			usage: Usage{Calls: 2, ImageSize: "1024x1024", ImageQuality: "high"},
			want:  Costs{Input: 0.334},
		},
		{
			name:  "分辨率表裸 size 键回退（不分质量档的模型）",
			price: Price{ImageSizePrices: map[string]float64{"1024x1024": 0.04}},
			usage: Usage{Calls: 1, ImageSize: "1024x1024", ImageQuality: "high"},
			want:  Costs{Input: 0.04},
		},
		{
			name:  "分辨率表命中优先于 per_request 并忽略 token usage（互斥防双计）",
			price: Price{Input: 3, Output: 15, PerRequest: 0.02, ImageSizePrices: map[string]float64{"low:512x512": 0.011}},
			usage: Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000, Calls: 1, ImageSize: "512x512", ImageQuality: "low"},
			want:  Costs{Input: 0.011},
		},
		{
			name:  "分辨率表配置但响应无 size：落回 per_request",
			price: Price{PerRequest: 0.02, ImageSizePrices: map[string]float64{"high:1024x1024": 0.167}},
			usage: Usage{Calls: 2},
			want:  Costs{Input: 0.04},
		},
		{
			name:  "分辨率表未命中且 per_request=0：落回 token 计价",
			price: Price{Input: 3, Output: 15, ImageSizePrices: map[string]float64{"high:1024x1024": 0.167}},
			usage: Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000, ImageSize: "2048x2048", ImageQuality: "high"},
			want:  Costs{Input: 3, Output: 15},
		},
		{
			name:  "分辨率表命中 Calls=0 钳为 1",
			price: Price{ImageSizePrices: map[string]float64{"1024x1024": 0.04}},
			usage: Usage{ImageSize: "1024x1024"},
			want:  Costs{Input: 0.04},
		},
		{
			name:  "token 计费不受 Calls 影响（PerRequest==0 时张数不参与）",
			price: Price{Input: 3, Output: 15},
			usage: Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000, Calls: 7},
			want:  Costs{Input: 3, Output: 15},
		},
		{
			name:  "长上下文：prompt 未超阈值用 base 单价",
			price: Price{Input: 2.5, Output: 15, CachedInput: 0.25, LongContext: longCtxRule},
			usage: Usage{PromptTokens: 272_000, CompletionTokens: 0},
			// 272000 == 阈值（非 >），不放大：0.272M*2.5=0.68
			want: Costs{Input: 0.68},
		},
		{
			name:  "长上下文：prompt 超阈值各维度单价翻倍",
			price: Price{Input: 2.5, Output: 15, CachedInput: 0.25, LongContext: longCtxRule},
			usage: Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000},
			// input 1M*2.5*2=5；output 1M*15*1.5=22.5
			want: Costs{Input: 5, Output: 22.5},
		},
		{
			name:  "服务档 priority 整单乘倍率",
			price: Price{Input: 2.5, Output: 15, ServiceTiers: map[string]float64{"priority": 2, "flex": 0.5}},
			usage: Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000},
			tier:  "priority",
			want:  Costs{Input: 5, Output: 30},
		},
		{
			name:  "服务档 flex 整单打折",
			price: Price{Input: 2.5, Output: 15, ServiceTiers: map[string]float64{"priority": 2, "flex": 0.5}},
			usage: Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000},
			tier:  "flex",
			want:  Costs{Input: 1.25, Output: 7.5},
		},
		{
			name:  "长上下文 × priority 叠乘",
			price: Price{Input: 2.5, Output: 15, ServiceTiers: map[string]float64{"priority": 2}, LongContext: longCtxRule},
			usage: Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000},
			tier:  "priority",
			// input 2.5×2(longctx)×2(tier)=10；output 15×1.5×2=45
			want: Costs{Input: 10, Output: 45},
		},
		{
			name:  "未知 tier 不生效",
			price: Price{Input: 2.5, ServiceTiers: map[string]float64{"priority": 2}},
			usage: Usage{PromptTokens: 1_000_000},
			tier:  "gold",
			want:  Costs{Input: 2.5},
		},
		{
			name:  "standard tier 视为默认档不套倍率",
			price: Price{Input: 2.5, ServiceTiers: map[string]float64{"priority": 2}},
			usage: Usage{PromptTokens: 1_000_000},
			tier:  "standard",
			want:  Costs{Input: 2.5},
		},
		{
			name:  "零用量零成本",
			price: Price{Input: 3, Output: 15},
			usage: Usage{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkCosts(t, ComputeCosts(tc.price, tc.usage, tc.tier), tc.want)
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

func TestImagePriceFor(t *testing.T) {
	table := map[string]float64{"high:1024x1024": 0.167, "1024x1536": 0.06, "medium:512x512": 0}
	cases := []struct {
		name    string
		price   Price
		quality string
		size    string
		want    float64
		wantOK  bool
	}{
		{name: "quality:size 精确命中", price: Price{ImageSizePrices: table}, quality: "high", size: "1024x1024", want: 0.167, wantOK: true},
		{name: "quality 未配置回退裸 size 键", price: Price{ImageSizePrices: table}, quality: "high", size: "1024x1536", want: 0.06, wantOK: true},
		{name: "quality 为空只查裸 size 键", price: Price{ImageSizePrices: table}, size: "1024x1536", want: 0.06, wantOK: true},
		{name: "size 为空不命中（响应未带档位）", price: Price{ImageSizePrices: table}, quality: "high", wantOK: false},
		{name: "表价 <=0 视为未配置", price: Price{ImageSizePrices: table}, quality: "medium", size: "512x512", wantOK: false},
		{name: "空表不命中", price: Price{}, quality: "high", size: "1024x1024", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ImagePriceFor(tc.price, tc.quality, tc.size)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("ImagePriceFor(%q, %q) = (%v, %v), want (%v, %v)", tc.quality, tc.size, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestVideoPriceFor(t *testing.T) {
	price := Price{
		VideoPerSecond: 0.07,
		VideoResolutionPrices: map[string]float64{
			"480p":  0.05,
			"720p":  0.07,
			"1080p": 0.25,
		},
	}
	cases := []struct {
		name       string
		resolution string
		want       float64
		wantOK     bool
	}{
		{name: "精确命中", resolution: "480p", want: 0.05, wantOK: true},
		{name: "大小写和空白不敏感", resolution: " 1080P ", want: 0.25, wantOK: true},
		{name: "未知分辨率回退基础秒价", resolution: "4k", want: 0.07, wantOK: true},
		{name: "空分辨率回退基础秒价", resolution: "", want: 0.07, wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := VideoPriceFor(price, tc.resolution)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("VideoPriceFor() = (%v,%v), want (%v,%v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
	if got, ok := VideoPriceFor(Price{}, "720p"); got != 0 || ok {
		t.Errorf("空价格 = (%v,%v), want (0,false)", got, ok)
	}
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
	cases := []struct {
		name  string
		price Price
		usage Usage
		want  Costs
	}{
		{
			name:  "负 cached 不得虚增 input（1000-(-500) 的漏洞）",
			price: Price{Input: 10, CachedInput: 5},
			usage: Usage{PromptTokens: 1_000_000, CachedTokens: -500_000},
			// cached 钳 0 → input 按 1M 计，而不是 1.5M
			want: Costs{Input: 10},
		},
		{
			name:  "负 completion 不产生负 output 成本",
			price: Price{Output: 30},
			usage: Usage{CompletionTokens: -1_000_000},
		},
		{
			name:  "负 5m/1h 缓存写入不产生负成本",
			price: Price{CacheCreation5m: 3.75, CacheCreation1h: 6},
			usage: Usage{CacheCreation5mTokens: -100_000, CacheCreation1hTokens: -200_000},
		},
		{
			name:  "全负用量全零成本",
			price: Price{Input: 3, Output: 15, CachedInput: 0.3, CacheCreation5m: 3.75, CacheCreation1h: 6},
			usage: Usage{PromptTokens: -1, CompletionTokens: -2, CachedTokens: -3, CacheCreationTokens: -4, CacheCreation5mTokens: -5, CacheCreation1hTokens: -6},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkCosts(t, ComputeCosts(tc.price, tc.usage, ""), tc.want)
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
