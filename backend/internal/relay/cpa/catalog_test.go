package cpa

import (
	"testing"
)

func TestCatalogLoadsFromCPAModelsJSON(t *testing.T) {
	if err := CatalogLoadError(); err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	claude := DefaultModelInfos("claude", "")
	if len(claude) == 0 {
		t.Fatal("expected claude models from CPA catalog")
	}
	// CPA models.json 使用带日期的官方 ID
	foundSonnet := false
	for _, m := range claude {
		if m.ID == "claude-sonnet-4-5-20250929" || m.ID == "claude-sonnet-4-6" {
			foundSonnet = true
			if m.DisplayName == "" {
				t.Errorf("model %s missing display_name", m.ID)
			}
		}
	}
	if !foundSonnet {
		t.Fatalf("expected a Claude Sonnet entry, got first=%+v", claude[0])
	}
}

func TestCodexPlanTiers(t *testing.T) {
	free := DefaultModelsWithPlan("codex", "free")
	plus := DefaultModelsWithPlan("codex", "plus")
	pro := DefaultModelsWithPlan("codex", "pro")
	if len(free) == 0 || len(plus) == 0 || len(pro) == 0 {
		t.Fatalf("empty tiers free=%d plus=%d pro=%d", len(free), len(plus), len(pro))
	}
	// free 档通常比 pro 少
	if len(free) > len(pro) {
		t.Errorf("expected free tier <= pro tier: free=%d pro=%d", len(free), len(pro))
	}
	// 默认空 plan 走 pro
	def := DefaultModels("codex")
	if len(def) != len(pro) {
		t.Errorf("default codex should match pro: default=%d pro=%d", len(def), len(pro))
	}
	// gpt-5.3-codex-spark 自 CPA v7.2.131 起开放至 plus/pro，free 不可用
	const spark = "gpt-5.3-codex-spark"
	if !containsModelID(pro, spark) {
		t.Errorf("pro should include %s", spark)
	}
	if !containsModelID(plus, spark) {
		t.Errorf("plus should include %s", spark)
	}
	if containsModelID(free, spark) {
		t.Errorf("free must not include %s", spark)
	}
}

func containsModelID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestDefaultModelsNonEmptyForMainPlatforms(t *testing.T) {
	for _, p := range []string{"codex", "claude", "gemini", "antigravity", "kimi", "xai", "vertex", "aistudio"} {
		ids := DefaultModels(p)
		if len(ids) == 0 {
			t.Errorf("platform %s: empty DefaultModels", p)
		}
	}
}

func TestXAIDefaultModelsIncludeMediaBuiltins(t *testing.T) {
	models := DefaultModels("xai")
	for _, id := range []string{
		"grok-imagine-image",
		"grok-imagine-image-quality",
		"grok-imagine-video",
		"grok-imagine-video-1.5-preview",
	} {
		if !containsModelID(models, id) {
			t.Errorf("xAI 默认模型目录缺少媒体模型 %s", id)
		}
	}
}
