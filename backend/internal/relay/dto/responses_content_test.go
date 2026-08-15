package dto

import "testing"

func TestResponsesEventHasContent(t *testing.T) {
	tests := []struct {
		name string
		data string
		want bool
	}{
		{name: "文本增量", data: `{"type":"response.output_text.delta","delta":"你好"}`, want: true},
		{name: "文本完成", data: `{"type":"response.output_text.done","text":"你好"}`, want: true},
		{name: "内容部分完成", data: `{"type":"response.content_part.done","part":{"type":"output_text","text":"你好"}}`, want: true},
		{name: "工具参数完成", data: `{"type":"response.function_call_arguments.done","arguments":"{}"}`, want: true},
		{name: "响应完成带输出", data: `{"type":"response.completed","response":{"output":[{"type":"message"}]}}`, want: true},
		{name: "响应不完整带输出", data: `{"type":"response.incomplete","response":{"output":[{"type":"message"}]}}`, want: true},
		{name: "创建事件", data: `{"type":"response.created","response":{"output":[]}}`, want: false},
		{name: "添加输出项", data: `{"type":"response.output_item.added","item":{"type":"message"}}`, want: false},
		{name: "空文本完成", data: `{"type":"response.output_text.done","text":""}`, want: false},
		{name: "响应完成无输出", data: `{"type":"response.completed","response":{"output":[]}}`, want: false},
		{name: "非法 JSON", data: `not-json`, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ResponsesEventHasContent([]byte(test.data)); got != test.want {
				t.Fatalf("ResponsesEventHasContent() = %v，期望 %v", got, test.want)
			}
		})
	}
}
