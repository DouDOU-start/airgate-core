package moderation

import (
	"strings"
	"testing"
)

func TestConfigNormalize(t *testing.T) {
	cfg := &Config{
		Mode:                "bogus",
		TimeoutMS:           99999999,
		SampleRate:          150,
		WorkerCount:         1000,
		QueueSize:           -1,
		RetryCount:          99,
		NonHitRetentionDays: 30,
		APIKeys:             []string{" k1 ", "k1", "", "k2"},
		BlockedKeywords:     []string{" 敏感 ", "敏感", strings.Repeat("x", 500)},
		ModelFilter:         ModelFilter{Type: "INCLUDE", Models: []string{"gpt-4o", "GPT-4o", ""}},
		Thresholds:          map[string]float64{"hate": 2},
	}
	cfg.Normalize()

	if cfg.Mode != ModePreBlock {
		t.Fatalf("mode = %q", cfg.Mode)
	}
	if cfg.TimeoutMS != maxTimeoutMS || cfg.SampleRate != 100 || cfg.WorkerCount != maxWorkerCount {
		t.Fatalf("clamp 失败: timeout=%d sample=%d worker=%d", cfg.TimeoutMS, cfg.SampleRate, cfg.WorkerCount)
	}
	if cfg.QueueSize != defaultQueueSize || cfg.RetryCount != maxRetryCount {
		t.Fatalf("queue=%d retry=%d", cfg.QueueSize, cfg.RetryCount)
	}
	if cfg.NonHitRetentionDays != maxNonHitRetentionDays {
		t.Fatalf("nonHitRetention = %d", cfg.NonHitRetentionDays)
	}
	if len(cfg.APIKeys) != 2 {
		t.Fatalf("APIKeys = %v", cfg.APIKeys)
	}
	if len(cfg.BlockedKeywords) != 2 || len([]rune(cfg.BlockedKeywords[1])) != maxBlockedKeywordRunes {
		t.Fatalf("BlockedKeywords = %d 条", len(cfg.BlockedKeywords))
	}
	if cfg.ModelFilter.Type != ModelFilterInclude || len(cfg.ModelFilter.Models) != 1 {
		t.Fatalf("ModelFilter = %+v", cfg.ModelFilter)
	}
	if cfg.Thresholds["hate"] != 1 {
		t.Fatalf("threshold clamp 失败: %v", cfg.Thresholds["hate"])
	}
	if cfg.BlockStatus != defaultBlockStatus || cfg.BlockMessage == "" {
		t.Fatalf("block 兜底失败: %d %q", cfg.BlockStatus, cfg.BlockMessage)
	}
}

func TestIncludesGroupAndModel(t *testing.T) {
	cfg := &Config{AllGroups: false, GroupIDs: []int{3, 5}}
	if cfg.includesGroup(4) || !cfg.includesGroup(5) || cfg.includesGroup(0) {
		t.Fatal("分组过滤失败")
	}
	cfg.AllGroups = true
	if !cfg.includesGroup(999) {
		t.Fatal("all_groups 应放行任意分组")
	}

	include := &Config{ModelFilter: ModelFilter{Type: ModelFilterInclude, Models: []string{"gpt-4o"}}}
	if !include.includesModel("GPT-4O") || include.includesModel("claude-3") {
		t.Fatal("include 过滤失败")
	}
	exclude := &Config{ModelFilter: ModelFilter{Type: ModelFilterExclude, Models: []string{"gpt-4o"}}}
	if exclude.includesModel("gpt-4o") || !exclude.includesModel("claude-3") {
		t.Fatal("exclude 过滤失败")
	}
}

func TestShouldSampleStable(t *testing.T) {
	full := &Config{SampleRate: 100}
	zero := &Config{SampleRate: 0}
	half := &Config{SampleRate: 50}

	hash := Input{Text: "stable"}.Hash()
	if !full.shouldSample(hash) {
		t.Fatal("100% 应恒采样")
	}
	if zero.shouldSample(hash) {
		t.Fatal("0% 应恒不采样")
	}
	// 同一 hash 多次判定结果稳定。
	first := half.shouldSample(hash)
	for i := 0; i < 10; i++ {
		if half.shouldSample(hash) != first {
			t.Fatal("同 hash 采样判定应稳定")
		}
	}
	// 大样本下采样率大致贴近设定值。
	hit := 0
	const n = 2000
	for i := 0; i < n; i++ {
		if half.shouldSample(Input{Text: strings.Repeat("x", i%97) + string(rune('a'+i%26))}.Hash()) {
			hit++
		}
	}
	ratio := float64(hit) / n
	if ratio < 0.4 || ratio > 0.6 {
		t.Fatalf("50%% 采样实测 %.2f 偏差过大", ratio)
	}
}
