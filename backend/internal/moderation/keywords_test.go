package moderation

import (
	"math/rand"
	"strings"
	"testing"
)

// naiveMatch 朴素实现：按配置序线性 Contains，作为自动机的行为基准。
func naiveMatch(text string, keywords []string) (string, bool) {
	if text == "" || len(keywords) == 0 {
		return "", false
	}
	lower := strings.ToLower(text)
	for _, kw := range keywords {
		if kw == "" {
			continue
		}
		if strings.Contains(lower, strings.ToLower(kw)) {
			return kw, true
		}
	}
	return "", false
}

func TestKeywordMatcher(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		keywords []string
	}{
		{"未命中", "clean prompt", []string{"blocked", "secret"}},
		{"大小写不敏感", "contains SECRET value", []string{"secret"}},
		{"配置序优先", "early appears before later", []string{"later", "early"}},
		{"重叠取配置序", "abc", []string{"bc", "abc"}},
		{"unicode", "这里包含敏感词和世界", []string{"世界", "敏感词"}},
		{"重复词", "duplicate", []string{"duplicate", "DUPLICATE"}},
		{"空词条", "blocked", []string{"", "blocked"}},
		{"空文本", "", []string{"blocked"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantKeyword, wantHit := naiveMatch(tt.text, tt.keywords)
			gotKeyword, gotHit := newKeywordMatcher(tt.keywords).Match(tt.text)
			if gotHit != wantHit || gotKeyword != wantKeyword {
				t.Fatalf("Match = (%q, %v), want (%q, %v)", gotKeyword, gotHit, wantKeyword, wantHit)
			}
		})
	}
}

func TestKeywordMatcherRandomizedParity(t *testing.T) {
	rng := rand.New(rand.NewSource(20260721))
	const alphabet = "abcXYZ"
	for iteration := 0; iteration < 1000; iteration++ {
		keywords := make([]string, 1+rng.Intn(30))
		for index := range keywords {
			length := 1 + rng.Intn(8)
			var value strings.Builder
			for i := 0; i < length; i++ {
				_ = value.WriteByte(alphabet[rng.Intn(len(alphabet))])
			}
			keywords[index] = value.String()
		}
		var text strings.Builder
		for i := 0; i < 20+rng.Intn(100); i++ {
			_ = text.WriteByte(alphabet[rng.Intn(len(alphabet))])
		}

		wantKeyword, wantHit := naiveMatch(text.String(), keywords)
		gotKeyword, gotHit := newKeywordMatcher(keywords).Match(text.String())
		if gotHit != wantHit || gotKeyword != wantKeyword {
			t.Fatalf("iteration %d: Match = (%q, %v), want (%q, %v)", iteration, gotKeyword, gotHit, wantKeyword, wantHit)
		}
	}
}
