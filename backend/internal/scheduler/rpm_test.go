package scheduler

import (
	"context"
	"testing"
	"time"
)

// TestChannelMinuteKey 分钟窗口 key 由调用方传入的 minute 决定，
// 保证 increment 与失败回退 decrement 落在同一窗口（不重取当前时间）。
func TestChannelMinuteKey(t *testing.T) {
	cases := []struct {
		name      string
		channelID int
		minute    int64
		want      string
	}{
		{"常规窗口", 5, 100, "rpm:channel:5:100"},
		{"跨分钟边界仍用原窗口", 5, 99, "rpm:channel:5:99"},
		{"不同渠道隔离", 7, 100, "rpm:channel:7:100"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := channelMinuteKey(tc.channelID, tc.minute); got != tc.want {
				t.Errorf("channelMinuteKey(%d, %d) = %q, want %q", tc.channelID, tc.minute, got, tc.want)
			}
		})
	}
}

// TestTryIncrementChannelRPMReturnsMinute TryIncrement 返回本次计数所用的分钟窗口；
// 请求跨分钟后回退时用该窗口而非重算当前时间。
func TestTryIncrementChannelRPMReturnsMinute(t *testing.T) {
	r := NewRPMCounter(nil) // nil redis：no-op 但窗口语义仍须成立

	before := time.Now().Unix() / 60
	ok, minute, err := r.TryIncrementChannelRPM(context.Background(), 5, 10)
	after := time.Now().Unix() / 60
	if err != nil {
		t.Fatalf("TryIncrementChannelRPM err = %v", err)
	}
	if !ok {
		t.Fatal("nil redis 应 fail-open 放行")
	}
	if minute < before || minute > after {
		t.Errorf("minute = %d, want 在 [%d, %d] 内", minute, before, after)
	}

	// 回退接受任意历史窗口（nil redis no-op，不 panic 即可）。
	r.DecrementChannelRPM(context.Background(), 5, minute)
	r.DecrementChannelRPM(context.Background(), 5, minute-1)
}
