package plugin

import (
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestExtractModelAndStream(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-4.1","stream":true,"metadata":{"user_id":"sess-1"}}`)

	model, stream := extractModelAndStream(body)
	if model != "gpt-4.1" {
		t.Fatalf("extractModelAndStream() model = %q, want %q", model, "gpt-4.1")
	}
	if !stream {
		t.Fatalf("extractModelAndStream() stream = false, want true")
	}
}

func TestExtractSessionID(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-4.1","stream":false,"metadata":{"user_id":"session-123"}}`)

	if got := extractSessionID(body); got != "session-123" {
		t.Fatalf("extractSessionID() = %q, want %q", got, "session-123")
	}
}

func TestParseForwardRequestBodyResponsesRespectsStream(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-4.1","stream":false,"metadata":{"user_id":"session-123"}}`)

	parsed := parseForwardRequestBody(body, "/v1/responses")
	if parsed.Model != "gpt-4.1" {
		t.Fatalf("parseForwardRequestBody() model = %q, want %q", parsed.Model, "gpt-4.1")
	}
	if parsed.SessionID != "session-123" {
		t.Fatalf("parseForwardRequestBody() sessionID = %q, want %q", parsed.SessionID, "session-123")
	}
	if parsed.Stream {
		t.Fatalf("parseForwardRequestBody() stream = true, want false")
	}
}

func TestShouldPenalizeForwardError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "空错误不计入失败",
			err:  nil,
			want: false,
		},
		{
			name: "直接 EOF 不计入失败",
			err:  io.EOF,
			want: false,
		},
		{
			name: "WebSocket 连接 EOF 不计入失败",
			err:  fmt.Errorf("gRPC 流接收失败: rpc error: code = Unknown desc = WebSocket 连接失败: %w", io.EOF),
			want: false,
		},
		{
			name: "WebSocket 正常关闭不计入失败",
			err:  errors.New("gRPC WebSocket 流接收失败: websocket: close 1000 (normal)"),
			want: false,
		},
		{
			name: "普通上游错误计入失败",
			err:  errors.New("gRPC 流接收失败: rpc error: code = Unknown desc = upstream 502"),
			want: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldPenalizeForwardError(tc.err); got != tc.want {
				t.Fatalf("shouldPenalizeForwardError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
