package streamerr

import (
	"net/http"
	"testing"
)

func TestDetect(t *testing.T) {
	cases := []struct {
		name       string
		payload    string
		want       bool
		wantStatus int
	}{
		{
			name:       "Responses 上游过载",
			payload:    `data: {"type":"error","error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"overloaded"}}`,
			want:       true,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "Anthropic 过载",
			payload:    `data: {"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`,
			want:       true,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "上下文过长不应作为临时故障",
			payload:    `{"type":"error","error":{"type":"invalid_request_error","code":"context_too_large","message":"too long"}}`,
			want:       true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Responses failed 嵌套错误",
			payload:    `data: {"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"failed"}}}`,
			want:       true,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "SSE event error 携带字符串错误",
			payload:    "event: error\ndata: {\"error\":\"server_is_overloaded\",\"message\":\"overloaded\"}\n\n",
			want:       true,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:    "普通内容事件",
			payload: `data: {"type":"response.output_text.delta","delta":"你好"}`,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			event, ok := Detect([]byte(test.payload))
			if ok != test.want {
				t.Fatalf("Detect() ok=%v，期望 %v", ok, test.want)
			}
			if ok && event.StatusCode != test.wantStatus {
				t.Fatalf("状态码=%d，期望 %d", event.StatusCode, test.wantStatus)
			}
		})
	}
}
