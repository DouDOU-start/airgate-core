package priceseed

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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

func (s *fakeStore) EnsureTag(_ context.Context, name string) (int, error) {
	// 简单稳定映射：按名称长度+首字符构造正 ID，测试无需真实表。
	return len(name) + int(name[0]), nil
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

	items := []SeedItem{
		{CreateInput: appmodelprice.CreateInput{Model: "claude-opus-4-8", InputPrice: 5, OutputPrice: 25}},   // 已存在 → 跳过
		{CreateInput: appmodelprice.CreateInput{Model: "gpt-5.4", InputPrice: 2.5, OutputPrice: 15}},         // 缺失 → 插入
		{CreateInput: appmodelprice.CreateInput{Model: "claude-sonnet-4-6", InputPrice: 3, OutputPrice: 15}}, // 缺失 → 插入
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
	items := []SeedItem{
		{CreateInput: appmodelprice.CreateInput{Model: "gpt-5.4", InputPrice: 2.5}},
		{CreateInput: appmodelprice.CreateInput{Model: "claude-opus-4-8", InputPrice: 5}},
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
	items := []SeedItem{
		{CreateInput: appmodelprice.CreateInput{Model: "boom"}},
		{CreateInput: appmodelprice.CreateInput{Model: "gpt-5.4"}},
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

	byModel := map[string]SeedItem{}
	for _, it := range items {
		byModel[it.Model] = it
	}
	if len(byModel) != len(items) {
		t.Fatalf("seed contains duplicate model names: items=%d unique=%d", len(items), len(byModel))
	}

	// 覆盖总数：15 claude（含 5 别名）+ 8 openai（含 gpt-image-2）+ 7 gemini
	// + 54 grok（6 个现役文本规范模型、36 个文本别名、2 个历史兼容文本模型、
	// 4 个媒体规范模型、6 个媒体别名）= 84。
	if len(items) != 84 {
		t.Errorf("seed model count = %d, want 84", len(items))
	}

	// 抽样核对（claude/openai 值来自 airgate-claude/models.go 与 airgate-openai/registry.go，
	// gemini/grok 值来自官方价目 2026-07 核实快照）。
	cases := []struct {
		model                          string
		input, output, cached, cacheCr float64
	}{
		{"claude-opus-4-8", 5.0, 25.0, 0.5, 6.25},
		{"gpt-5.4", 2.5, 15.0, 0.25, 0},
		{"claude-opus-4-1-20250805", 15.0, 75.0, 1.5, 18.75},
		{"claude-fable-5", 10.0, 50.0, 1.0, 12.5},
		{"gpt-5.5", 5.0, 30.0, 0.5, 0},
		{"gpt-5.6-sol", 5.0, 30.0, 0.5, 6.25},
		{"gpt-5.6-terra", 2.5, 15.0, 0.25, 3.125},
		{"gpt-5.6-luna", 1.0, 6.0, 0.1, 1.25},
		{"gemini-2.5-pro", 1.25, 10.0, 0.31, 0},
		{"gemini-3.1-pro-preview", 2.0, 12.0, 0.2, 0},
		{"gemini-3.5-flash", 1.5, 9.0, 0.15, 0},
		{"grok-4.3", 1.25, 2.5, 0.2, 0},
		{"grok-4.5", 2.0, 6.0, 0.3, 0},
		{"grok-build-0.1", 1.0, 2.0, 0.2, 0},
		{"grok-3-mini", 0.3, 0.5, 0.075, 0},
		{"grok-3-mini-fast", 0.6, 4.0, 0.15, 0},
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

	// gpt-image-2 按张计费：pricing_extra.image.size_prices 须能被计费侧同一套解析
	// （ParseImageSizePrices）识别为 9 档表价；token 价为官方值（表未命中时兜底
	// 非标准分辨率，防计 0 放行），按次价 0。
	if img, ok := byModel["gpt-image-2"]; !ok {
		t.Error("model gpt-image-2 missing from seed")
	} else {
		if img.InputPrice != 5.0 || img.OutputPrice != 30.0 || img.PerRequestPrice != 0 {
			t.Errorf("gpt-image-2 token 兜底价 = in%.4g/out%.4g/pr%.4g, want 5/30/0",
				img.InputPrice, img.OutputPrice, img.PerRequestPrice)
		}
		prices := appmodelprice.ParseImageSizePrices(img.Model, img.PricingExtra)
		if len(prices) != 9 {
			t.Errorf("gpt-image-2 size_prices 档数 = %d, want 9", len(prices))
		}
		if got := prices["high:1024x1024"]; got != 0.211 {
			t.Errorf("gpt-image-2 high:1024x1024 = %v, want 0.211", got)
		}
		if got := prices["low:1024x1536"]; got != 0.005 {
			t.Errorf("gpt-image-2 low:1024x1536 = %v, want 0.005", got)
		}
	}

	// xAI Imagine 生图：按官方 1K/2K 输出图片价计费；响应不带分辨率时
	// per_request 使用默认 1K 价兜底，避免逆向渠道零价放行。
	for _, tc := range []struct {
		model            string
		perRequest, oneK float64
		twoK, inputImage float64
	}{
		{"grok-imagine-image", 0.02, 0.02, 0.02, 0.002},
		{"grok-imagine-image-quality", 0.05, 0.05, 0.07, 0.01},
	} {
		img, ok := byModel[tc.model]
		if !ok {
			t.Errorf("model %q missing from seed", tc.model)
			continue
		}
		if img.PerRequestPrice != tc.perRequest {
			t.Errorf("model %q per_request = %v, want %v", tc.model, img.PerRequestPrice, tc.perRequest)
		}
		prices := appmodelprice.ParseImageSizePrices(img.Model, img.PricingExtra)
		if len(prices) != 2 || prices["1k"] != tc.oneK || prices["2k"] != tc.twoK {
			t.Errorf("model %q size_prices = %#v, want 1k=%v/2k=%v", tc.model, prices, tc.oneK, tc.twoK)
		}
		imageExtra, _ := img.PricingExtra["image"].(map[string]interface{})
		if got, _ := imageExtra["input_image_price"].(float64); got != tc.inputImage {
			t.Errorf("model %q input_image_price = %v, want %v", tc.model, got, tc.inputImage)
		}
	}

	// xAI Imagine 视频：默认 720p 秒价写入 per_second，完整分辨率表保留在
	// resolution_prices；旧版还记录输入视频秒价。
	videoCases := []struct {
		model       string
		perSecond   float64
		resolutions map[string]float64
	}{
		{"grok-imagine-video", 0.07, map[string]float64{"480p": 0.05, "720p": 0.07}},
		{"grok-imagine-video-1.5", 0.14, map[string]float64{"480p": 0.08, "720p": 0.14, "1080p": 0.25}},
	}
	for _, tc := range videoCases {
		video, ok := byModel[tc.model]
		if !ok {
			t.Errorf("model %q missing from seed", tc.model)
			continue
		}
		videoExtra, ok := video.PricingExtra["video"].(map[string]interface{})
		if !ok {
			t.Errorf("model %q video pricing_extra missing: %#v", tc.model, video.PricingExtra)
			continue
		}
		if got, _ := videoExtra["per_second"].(float64); got != tc.perSecond {
			t.Errorf("model %q per_second = %v, want %v", tc.model, got, tc.perSecond)
		}
		resolutionRaw, ok := videoExtra["resolution_prices"].(map[string]interface{})
		if !ok {
			t.Errorf("model %q resolution_prices missing: %#v", tc.model, videoExtra)
			continue
		}
		for resolution, want := range tc.resolutions {
			if got, _ := resolutionRaw[resolution].(float64); got != want {
				t.Errorf("model %q resolution %q = %v, want %v", tc.model, resolution, got, want)
			}
		}
		if len(resolutionRaw) != len(tc.resolutions) {
			t.Errorf("model %q resolution_prices count = %d, want %d", tc.model, len(resolutionRaw), len(tc.resolutions))
		}
	}

	// 标签归类抽样：四个家族各取一个。
	tagCases := map[string]string{
		"claude-fable-5":   "claude",
		"gpt-5.5":          "openai",
		"gemini-2.5-flash": "gemini",
		"grok-4.3":         "grok",
	}
	for m, want := range tagCases {
		if got := byModel[m].TagName; got != want {
			t.Errorf("model %q tag = %q, want %q", m, got, want)
		}
	}

	// Claude 别名与其规范模型同价。
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

	// Grok 当前官方别名全部独立落种子，并与规范模型的基础价和 pricing_extra 完全一致。
	grokAliasGroups := map[string][]string{
		"grok-4.3": {
			"grok-4.3-latest", "grok-latest",
		},
		"grok-build-0.1": {
			"grok-code-fast-1", "grok-code-fast", "grok-code-fast-1-0825",
		},
		"grok-4.5": {
			"grok-4.5-latest", "grok-build-latest",
		},
		"grok-4.20-0309-reasoning": {
			"grok-4.20-reasoning-latest", "grok-4.20", "grok-4.20-reasoning", "grok-4.20-0309",
			"grok-4.20-beta-0309-reasoning", "grok-4.20-beta", "grok-4.20-beta-0309",
			"grok-4.20-beta-latest", "grok-4.20-beta-latest-reasoning", "grok-4.20-beta-reasoning",
			"grok-4.20-experimental-beta-0304-reasoning", "grok-4.20-experimental-beta-0304",
			"grok-4.20-experimental-beta-reasoning-latest", "grok-4.20-experimental-beta-latest",
			"grok-4.20-reasoning-gv2",
		},
		"grok-4.20-0309-non-reasoning": {
			"grok-4.20-non-reasoning", "grok-4.20-non-reasoning-latest",
			"grok-4.20-beta-non-reasoning", "grok-4.20-beta-latest-non-reasoning",
			"grok-4.20-experimental-beta-0304-non-reasoning",
			"grok-4.20-experimental-beta-non-reasoning-latest",
			"grok-4.20-beta-0309-non-reasoning", "grok-4.20-non-reasoning-gv2",
		},
		"grok-4.20-multi-agent-0309": {
			"grok-4.20-multi-agent", "grok-4.20-multi-agent-latest",
			"grok-4.20-multi-agent-beta-latest", "grok-4.20-multi-agent-experimental-beta-0304",
			"grok-4.20-multi-agent-experimental-beta-latest", "grok-4.20-multi-agent-beta-0309",
		},
		"grok-imagine-image": {
			"grok-imagine-image-2026-03-02",
		},
		"grok-imagine-image-quality": {
			"grok-imagine-image-quality-20260403", "grok-imagine-image-quality-latest", "grok-imagine-image-pro",
		},
		"grok-imagine-video-1.5": {
			"grok-imagine-video-1.5-preview", "grok-imagine-video-1.5-2026-05-30",
		},
	}
	for canonical, aliases := range grokAliasGroups {
		want, ok := byModel[canonical]
		if !ok {
			t.Errorf("canonical model %q missing from seed", canonical)
			continue
		}
		for _, alias := range aliases {
			got, ok := byModel[alias]
			if !ok {
				t.Errorf("grok alias %q missing from seed", alias)
				continue
			}
			if got.TagName != want.TagName || got.InputPrice != want.InputPrice || got.OutputPrice != want.OutputPrice ||
				got.CachedInputPrice != want.CachedInputPrice || got.CacheCreationPrice != want.CacheCreationPrice ||
				got.CacheCreation1hPrice != want.CacheCreation1hPrice || got.PerRequestPrice != want.PerRequestPrice ||
				!reflect.DeepEqual(got.PricingExtra, want.PricingExtra) {
				t.Errorf("grok alias %q price != canonical %q price", alias, canonical)
			}
		}
	}

	// gpt-5.6 之前的 openai 条目 cache_creation 应为 0（无缓存写入档）；
	// gpt-5.6 三档缓存写入价单独在抽样 cases 里核对。
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

// TestSeedPricingExtra 核对 OpenAI 与 Grok 的服务档倍率、长上下文阶梯。
func TestSeedPricingExtra(t *testing.T) {
	items, err := Parse(embeddedSeed)
	if err != nil {
		t.Fatalf("Parse(embeddedSeed) err = %v", err)
	}
	byModel := map[string]SeedItem{}
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
	for _, m := range []string{"gpt-5.3-codex-spark", "gpt-5.4", "gpt-5.4-mini", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
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

	// Grok 现役文本模型：priority=2×，且全部在 200K 后 input/output/cached 均为 2×。
	for _, m := range []string{
		"grok-4.3", "grok-build-0.1", "grok-4.5",
		"grok-4.20-0309-reasoning", "grok-4.20-0309-non-reasoning", "grok-4.20-multi-agent-0309",
	} {
		st := serviceTiers(m)
		if p := num(st["priority"]); p != 2.0 {
			t.Errorf("%s priority = %v, want 2.0", m, p)
		}
		if _, ok := st["flex"]; ok {
			t.Errorf("%s should not carry flex service tier", m)
		}
		_, lc := appmodelprice.ParsePricingExtra(m, byModel[m].PricingExtra)
		if lc == nil {
			t.Errorf("%s long_context missing", m)
			continue
		}
		if lc.ThresholdTokens != 200000 || lc.InputMultiplier != 2.0 ||
			lc.OutputMultiplier != 2.0 || lc.CachedMultiplier != 2.0 {
			t.Errorf("%s long_context = %#v, want threshold=200000 and all multipliers=2", m, lc)
		}
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
