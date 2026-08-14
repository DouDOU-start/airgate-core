package pipeline

import (
	"errors"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
)

func TestUsageStatusFor(t *testing.T) {
	cases := []struct {
		name        string
		result      attemptResult
		usage       dto.Usage
		billedCalls int
		want        string
	}{
		{
			name:   "正常完成且有计量",
			result: attemptResult{usage: &dto.Usage{PromptTokens: 1}},
			usage:  dto.Usage{PromptTokens: 1},
			want:   billing.UsageStatusCompleted,
		},
		{
			name:   "正常完成但计量缺失",
			result: attemptResult{},
			want:   billing.UsageStatusMissing,
		},
		{
			name:   "流中断但已有部分计量",
			result: attemptResult{written: true, streamErr: errors.New("断流"), usage: &dto.Usage{PromptTokens: 2}},
			usage:  dto.Usage{PromptTokens: 2},
			want:   billing.UsageStatusStreamAborted,
		},
		{
			name:   "流中断且无计量",
			result: attemptResult{written: true, streamErr: errors.New("断流")},
			want:   billing.UsageStatusStreamAbortedUsageMissing,
		},
		{
			name:        "按次计费无需上游 token usage",
			result:      attemptResult{},
			billedCalls: 1,
			want:        billing.UsageStatusCompleted,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := usageStatusFor(tc.result, tc.usage, tc.billedCalls); got != tc.want {
				t.Fatalf("usageStatusFor() = %q，期望 %q", got, tc.want)
			}
		})
	}
}

func TestAccountUsageFirstToken优先请求级耗时(t *testing.T) {
	if got := accountUsageFirstToken(attemptResult{firstTokenMs: 120, requestFirstTokenMs: 860}); got != 860 {
		t.Fatalf("使用记录首字 = %d，期望请求级耗时 860", got)
	}
	if got := accountUsageFirstToken(attemptResult{firstTokenMs: 120}); got != 120 {
		t.Fatalf("请求级耗时缺失时应回退 attempt 耗时，实际 %d", got)
	}
}
