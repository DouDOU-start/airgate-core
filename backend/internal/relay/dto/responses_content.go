package dto

import (
	"bytes"
	"encoding/json"
	"strings"
)

// ResponsesEventHasContent 判断单个 Responses SSE data 事件是否包含真实输出。
// created/in_progress/added 等生命周期事件即使带有空 output/content 字段也不算首字；
// 某些上游没有 delta，只在 done 或终态事件中返回最终正文和工具调用，需要一并识别。
func ResponsesEventHasContent(data []byte) bool {
	var event struct {
		Type      string          `json:"type"`
		Item      json.RawMessage `json:"item"`
		Part      json.RawMessage `json:"part"`
		Text      json.RawMessage `json:"text"`
		Arguments json.RawMessage `json:"arguments"`
		Response  struct {
			Output json.RawMessage `json:"output"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &event) != nil {
		return false
	}
	if strings.HasSuffix(event.Type, ".delta") {
		return true
	}
	if strings.HasSuffix(event.Type, ".done") &&
		(rawJSONHasContent(event.Item) || rawJSONHasContent(event.Part) ||
			rawJSONHasContent(event.Text) || rawJSONHasContent(event.Arguments)) {
		return true
	}
	switch event.Type {
	case "response.completed", "response.incomplete", "response.done":
		return rawJSONHasContent(event.Response.Output)
	default:
		return false
	}
}

func rawJSONHasContent(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return false
	}
	switch raw[0] {
	case '"':
		var text string
		return json.Unmarshal(raw, &text) == nil && strings.TrimSpace(text) != ""
	case '[', '{':
		if len(raw) < 2 {
			return false
		}
		return len(bytes.TrimSpace(raw[1:len(raw)-1])) > 0
	default:
		return true
	}
}
