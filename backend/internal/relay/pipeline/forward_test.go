package pipeline

import (
	"testing"
	"time"
)

// TestChannelSlotTTL 渠道并发槽 TTL：流式 30min（长流无总超时防僵尸清理），
// 非流式传 0 走 concurrency 层默认 5min。
func TestChannelSlotTTL(t *testing.T) {
	cases := []struct {
		name   string
		stream bool
		want   time.Duration
	}{
		{"流式 30min", true, 30 * time.Minute},
		{"非流式用默认（0）", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := channelSlotTTL(tc.stream); got != tc.want {
				t.Errorf("channelSlotTTL(%v) = %v, want %v", tc.stream, got, tc.want)
			}
		})
	}
}

// TestIsSSEContentType 流式分支只在上游确按 SSE 响应时进入。
func TestIsSSEContentType(t *testing.T) {
	cases := []struct {
		ct   string
		want bool
	}{
		{"text/event-stream", true},
		{"text/event-stream; charset=utf-8", true},
		{"TEXT/EVENT-STREAM", true},
		{" text/event-stream", true},
		{"application/json", false},
		{"text/html", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isSSEContentType(tc.ct); got != tc.want {
			t.Errorf("isSSEContentType(%q) = %v, want %v", tc.ct, got, tc.want)
		}
	}
}
