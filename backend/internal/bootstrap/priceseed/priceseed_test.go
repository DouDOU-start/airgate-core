package priceseed

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	appmodelprice "github.com/DouDOU-start/airgate-core/internal/app/modelprice"
)

// fakeStore 是 PriceStore 的内存实现，避免依赖 ent / sqlite CGO。
type fakeStore struct {
	byModel map[string]appmodelprice.ModelPrice
	nextID  int
	// createErr 允许注入指定 model 的 Create 失败以测试降级。
	createErr map[string]error
	creates   []appmodelprice.CreateInput
}

func newFakeStore(existing ...string) *fakeStore {
	s := &fakeStore{byModel: map[string]appmodelprice.ModelPrice{}, createErr: map[string]error{}}
	for _, m := range existing {
		s.nextID++
		s.byModel[m] = appmodelprice.ModelPrice{ID: s.nextID, Model: m}
	}
	return s
}

func (s *fakeStore) ListAll(_ context.Context) ([]appmodelprice.ModelPrice, error) {
	out := make([]appmodelprice.ModelPrice, 0, len(s.byModel))
	for _, v := range s.byModel {
		out = append(out, v)
	}
	return out, nil
}

func (s *fakeStore) Create(_ context.Context, input appmodelprice.CreateInput) (appmodelprice.ModelPrice, error) {
	if err := s.createErr[input.Model]; err != nil {
		return appmodelprice.ModelPrice{}, err
	}
	s.creates = append(s.creates, input)
	s.nextID++
	mp := appmodelprice.ModelPrice{
		ID:                 s.nextID,
		Model:              input.Model,
		InputPrice:         input.InputPrice,
		OutputPrice:        input.OutputPrice,
		CachedInputPrice:   input.CachedInputPrice,
		CacheCreationPrice: input.CacheCreationPrice,
		PerRequestPrice:    input.PerRequestPrice,
	}
	s.byModel[input.Model] = mp
	return mp, nil
}

// TestInsertIfAbsent 证明已存在的 model 不被覆盖、缺失的 model 被插入。
func TestInsertIfAbsent(t *testing.T) {
	// 库里已有 claude-opus-4-8（价格被管理员改成非默认值），断言不被覆盖。
	store := newFakeStore("claude-opus-4-8", "mock-large")
	store.byModel["claude-opus-4-8"] = appmodelprice.ModelPrice{
		ID: 1, Model: "claude-opus-4-8", InputPrice: 999, OutputPrice: 888,
	}

	items := []appmodelprice.CreateInput{
		{Model: "claude-opus-4-8", InputPrice: 5, OutputPrice: 25},   // 已存在 → 跳过
		{Model: "gpt-5.4", InputPrice: 2.5, OutputPrice: 15},         // 缺失 → 插入
		{Model: "claude-sonnet-4-6", InputPrice: 3, OutputPrice: 15}, // 缺失 → 插入
	}

	inserted, skipped, err := insertMissing(context.Background(), store, items)
	if err != nil {
		t.Fatalf("insertMissing err = %v", err)
	}
	if inserted != 2 {
		t.Errorf("inserted = %d, want 2", inserted)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}

	// 已存在条目未被覆盖（价格仍为管理员改过的 999/888）。
	got := store.byModel["claude-opus-4-8"]
	if got.InputPrice != 999 || got.OutputPrice != 888 {
		t.Errorf("existing claude-opus-4-8 was overwritten: %+v", got)
	}
	// mock-large 依旧存在且未被触碰。
	if _, ok := store.byModel["mock-large"]; !ok {
		t.Error("mock-large disappeared")
	}
	// 缺失的被插入。
	if _, ok := store.byModel["gpt-5.4"]; !ok {
		t.Error("gpt-5.4 not inserted")
	}
	// 只有缺失的两条走了 Create。
	if len(store.creates) != 2 {
		t.Errorf("Create called %d times, want 2", len(store.creates))
	}
}

// TestInsertMissingIdempotent 证明重复运行只补缺失，第二次全部跳过。
func TestInsertMissingIdempotent(t *testing.T) {
	store := newFakeStore()
	items := []appmodelprice.CreateInput{
		{Model: "gpt-5.4", InputPrice: 2.5},
		{Model: "claude-opus-4-8", InputPrice: 5},
	}

	ins1, skip1, _ := insertMissing(context.Background(), store, items)
	if ins1 != 2 || skip1 != 0 {
		t.Fatalf("first run inserted=%d skipped=%d, want 2/0", ins1, skip1)
	}
	ins2, skip2, _ := insertMissing(context.Background(), store, items)
	if ins2 != 0 || skip2 != 2 {
		t.Fatalf("second run inserted=%d skipped=%d, want 0/2", ins2, skip2)
	}
}

// TestInsertMissingSingleFailureDegrades 证明单条 Create 失败降级为跳过，其余继续。
func TestInsertMissingSingleFailureDegrades(t *testing.T) {
	store := newFakeStore()
	store.createErr["boom"] = os.ErrPermission
	items := []appmodelprice.CreateInput{
		{Model: "boom"},
		{Model: "gpt-5.4"},
	}
	inserted, _, err := insertMissing(context.Background(), store, items)
	if err != nil {
		t.Fatalf("insertMissing err = %v (single failure should degrade)", err)
	}
	if inserted != 1 {
		t.Errorf("inserted = %d, want 1", inserted)
	}
	if _, ok := store.byModel["gpt-5.4"]; !ok {
		t.Error("gpt-5.4 should still be inserted despite boom failure")
	}
}

// TestParseEmbeddedSeed 解析内嵌种子并抽样核对与源码一致的数值。
func TestParseEmbeddedSeed(t *testing.T) {
	items, err := Parse(embeddedSeed)
	if err != nil {
		t.Fatalf("Parse(embeddedSeed) err = %v", err)
	}

	byModel := map[string]appmodelprice.CreateInput{}
	for _, it := range items {
		byModel[it.Model] = it
	}

	// 覆盖总数：15 claude（含 5 别名）+ 4 openai = 19。
	if len(items) != 19 {
		t.Errorf("seed model count = %d, want 19", len(items))
	}

	// 抽样核对（值来自 airgate-claude/models.go 与 airgate-openai/registry.go）。
	cases := []struct {
		model                          string
		input, output, cached, cacheCr float64
	}{
		{"claude-opus-4-8", 5.0, 25.0, 0.5, 6.25},
		{"gpt-5.4", 2.5, 15.0, 0.25, 0},
		{"claude-opus-4-1-20250805", 15.0, 75.0, 1.5, 18.75},
		{"claude-fable-5", 10.0, 50.0, 1.0, 12.5},
		{"gpt-5.5", 5.0, 30.0, 0.5, 0},
	}
	for _, c := range cases {
		got, ok := byModel[c.model]
		if !ok {
			t.Errorf("model %q missing from seed", c.model)
			continue
		}
		if got.InputPrice != c.input || got.OutputPrice != c.output ||
			got.CachedInputPrice != c.cached || got.CacheCreationPrice != c.cacheCr {
			t.Errorf("model %q = in%.4g/out%.4g/cache%.4g/cc%.4g, want %.4g/%.4g/%.4g/%.4g",
				c.model, got.InputPrice, got.OutputPrice, got.CachedInputPrice, got.CacheCreationPrice,
				c.input, c.output, c.cached, c.cacheCr)
		}
	}

	// 别名与其规范模型同价。
	aliasPairs := [][2]string{
		{"claude-sonnet-4-5", "claude-sonnet-4-5-20250929"},
		{"claude-opus-4-5", "claude-opus-4-5-20251101"},
		{"claude-haiku-4-5", "claude-haiku-4-5-20251001"},
	}
	for _, pair := range aliasPairs {
		a, aok := byModel[pair[0]]
		b, bok := byModel[pair[1]]
		if !aok || !bok {
			t.Errorf("alias pair %v not both present", pair)
			continue
		}
		if a.InputPrice != b.InputPrice || a.OutputPrice != b.OutputPrice ||
			a.CachedInputPrice != b.CachedInputPrice || a.CacheCreationPrice != b.CacheCreationPrice {
			t.Errorf("alias %q price != canonical %q price", pair[0], pair[1])
		}
	}

	// 所有 openai 条目 cache_creation 应为 0（无 5m 缓存写入档）。
	for _, m := range []string{"gpt-5.3-codex-spark", "gpt-5.4", "gpt-5.4-mini", "gpt-5.5"} {
		if byModel[m].CacheCreationPrice != 0 {
			t.Errorf("openai model %q cache_creation = %v, want 0", m, byModel[m].CacheCreationPrice)
		}
	}

	// claude 1h 缓存写入档（值来自 airgate-claude/models.go 的 CacheCreation1hPrice 列）。
	cc1hCases := map[string]float64{
		"claude-fable-5":             20.0,
		"claude-opus-4-8":            10.0,
		"claude-opus-4-1-20250805":   30.0,
		"claude-sonnet-4-6":          6.0,
		"claude-haiku-4-5-20251001":  2.0,
		"claude-sonnet-4-5-20250929": 6.0,
	}
	for m, want := range cc1hCases {
		if got := byModel[m].CacheCreation1hPrice; got != want {
			t.Errorf("model %q cache_creation_1h = %v, want %v", m, got, want)
		}
	}
	// openai 无 1h 缓存写入档。
	for _, m := range []string{"gpt-5.4", "gpt-5.5"} {
		if byModel[m].CacheCreation1hPrice != 0 {
			t.Errorf("openai model %q cache_creation_1h = %v, want 0", m, byModel[m].CacheCreation1hPrice)
		}
	}
}

// TestSeedPricingExtra 核对 openai 服务档倍率与 gpt-5.4 长上下文阶梯（值来自 airgate-openai/registry.go）。
func TestSeedPricingExtra(t *testing.T) {
	items, err := Parse(embeddedSeed)
	if err != nil {
		t.Fatalf("Parse(embeddedSeed) err = %v", err)
	}
	byModel := map[string]appmodelprice.CreateInput{}
	for _, it := range items {
		byModel[it.Model] = it
	}

	num := func(v interface{}) float64 {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		default:
			t.Fatalf("value %v (%T) not numeric", v, v)
			return 0
		}
	}
	serviceTiers := func(model string) map[string]interface{} {
		extra := byModel[model].PricingExtra
		if extra == nil {
			t.Fatalf("model %q pricing_extra missing", model)
		}
		st, ok := extra["service_tiers"].(map[string]interface{})
		if !ok {
			t.Fatalf("model %q service_tiers missing/typed wrong: %#v", model, extra["service_tiers"])
		}
		return st
	}

	// std 家族：priority=2×、flex=0.5×。
	for _, m := range []string{"gpt-5.3-codex-spark", "gpt-5.4", "gpt-5.4-mini"} {
		st := serviceTiers(m)
		if p := num(st["priority"]); p != 2.0 {
			t.Errorf("%s priority = %v, want 2.0", m, p)
		}
		if f := num(st["flex"]); f != 0.5 {
			t.Errorf("%s flex = %v, want 0.5", m, f)
		}
	}
	// gpt-5.5：withPriorityMultiplier(...,2.5) → priority=2.5×、flex=0.5×。
	st55 := serviceTiers("gpt-5.5")
	if p := num(st55["priority"]); p != 2.5 {
		t.Errorf("gpt-5.5 priority = %v, want 2.5", p)
	}
	if f := num(st55["flex"]); f != 0.5 {
		t.Errorf("gpt-5.5 flex = %v, want 0.5", f)
	}

	// gpt-5.4 长上下文阶梯：阈值 272000，input×2 / output×1.5 / cached×2。
	lc, ok := byModel["gpt-5.4"].PricingExtra["long_context"].(map[string]interface{})
	if !ok {
		t.Fatalf("gpt-5.4 long_context missing")
	}
	if v := num(lc["threshold_tokens"]); v != 272000 {
		t.Errorf("gpt-5.4 threshold_tokens = %v, want 272000", v)
	}
	if v := num(lc["input_multiplier"]); v != 2.0 {
		t.Errorf("gpt-5.4 input_multiplier = %v, want 2.0", v)
	}
	if v := num(lc["output_multiplier"]); v != 1.5 {
		t.Errorf("gpt-5.4 output_multiplier = %v, want 1.5", v)
	}
	if v := num(lc["cached_multiplier"]); v != 2.0 {
		t.Errorf("gpt-5.4 cached_multiplier = %v, want 2.0", v)
	}

	// 非长上下文家族不应带 long_context。
	if _, ok := byModel["gpt-5.5"].PricingExtra["long_context"]; ok {
		t.Errorf("gpt-5.5 should not carry long_context")
	}
	// claude 家族无 pricing_extra。
	if byModel["claude-opus-4-8"].PricingExtra != nil {
		t.Errorf("claude-opus-4-8 pricing_extra should be nil, got %#v", byModel["claude-opus-4-8"].PricingExtra)
	}
}

// TestEmbeddedSeedMatchesDataFile 确保内嵌副本与部署样例 backend/data 内容一致，防漂移。
//
// 注意：backend/data/ 目录被 .gitignore 忽略（运行时数据目录），该样例文件不随仓库追踪，
// 全新 clone 上可能缺失——此时跳过，不视为失败。内嵌副本才是随仓库追踪的权威默认种子。
func TestEmbeddedSeedMatchesDataFile(t *testing.T) {
	dataPath := filepath.Join("..", "..", "..", "data", "model-prices.seed.yaml")
	dataBytes, err := os.ReadFile(dataPath)
	if os.IsNotExist(err) {
		t.Skipf("部署样例 %s 不存在（data/ 被 gitignore），跳过一致性核对", dataPath)
	}
	if err != nil {
		t.Fatalf("read data seed %q: %v", dataPath, err)
	}
	if string(dataBytes) != string(embeddedSeed) {
		t.Errorf("embedded seed differs from %s — keep them identical", dataPath)
	}
}
