package moderation

import "testing"

func TestEvaluateScores(t *testing.T) {
	// evaluateScores 的调用方约定传入完整合并后的阈值表（部分表会因缺省 0 全命中）。
	thresholds := MergeThresholds(DefaultThresholds(), map[string]float64{"hate": 0.65, "sexual": 0.65, "violence": 0.95})
	tests := []struct {
		name         string
		scores       map[string]float64
		wantFlagged  bool
		wantCategory string
		wantScore    float64
	}{
		{"全低分不命中", map[string]float64{"hate": 0.1, "sexual": 0.2}, false, "sexual", 0.2},
		{"等于阈值即命中", map[string]float64{"hate": 0.65}, true, "hate", 0.65},
		{"超过阈值命中", map[string]float64{"violence": 0.96}, true, "violence", 0.96},
		{"未配置阈值类别不触发但计入 highest", map[string]float64{"unknown-cat": 0.99}, false, "unknown-cat", 0.99},
		{"空分值不命中", map[string]float64{}, false, "harassment", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagged, category, score := evaluateScores(tt.scores, thresholds)
			if flagged != tt.wantFlagged {
				t.Fatalf("flagged = %v, want %v", flagged, tt.wantFlagged)
			}
			if category != tt.wantCategory {
				t.Fatalf("category = %q, want %q", category, tt.wantCategory)
			}
			if score != tt.wantScore {
				t.Fatalf("score = %v, want %v", score, tt.wantScore)
			}
		})
	}
}

// 默认阈值下 threshold=0 的坑：evaluateScores 用 score >= thresholds[category]，
// 未在 thresholds 里的已知类别阈值为 0——MergeThresholds 必须兜底默认表防误伤。
func TestEvaluateScoresWithMergedDefaults(t *testing.T) {
	merged := MergeThresholds(DefaultThresholds(), map[string]float64{"hate": 0.3})
	if merged["hate"] != 0.3 {
		t.Fatalf("override 未生效: %v", merged["hate"])
	}
	if merged["sexual"] != 0.65 {
		t.Fatalf("默认值丢失: %v", merged["sexual"])
	}
	flagged, _, _ := evaluateScores(map[string]float64{"violence": 0.5}, merged)
	if flagged {
		t.Fatal("低于默认阈值不应命中")
	}
	// clamp 到 [0,1]
	clamped := MergeThresholds(DefaultThresholds(), map[string]float64{"hate": 1.5, "sexual": -0.2})
	if clamped["hate"] != 1 || clamped["sexual"] != 0 {
		t.Fatalf("clamp 失败: hate=%v sexual=%v", clamped["hate"], clamped["sexual"])
	}
}
