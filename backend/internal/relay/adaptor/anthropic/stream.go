package anthropic

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
)

// streamObserver Anthropic Messages SSE 的透传型 usage 观察器。
// 管线原样转发上游字节，本观察器旁路解析：
//   - message_start / message_delta 事件累积 usage（input 侧在 start、output 侧在 delta）；
//   - message_stop 事件为协议级完成信号（Done）；
//   - error 事件（如 overloaded_error）记为流内错误——管线据此按流中断处理，
//     不把截断响应伪装成完整（保留既有语义）。
type streamObserver struct {
	usage     normUsage
	done      bool
	streamErr error
}

// anthropicEvent SSE data 载荷的判别字段（观察所需最小集）。
type anthropicEvent struct {
	Type    string `json:"type"`
	Message *struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
	Usage *anthropicUsage `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// ObserveLine 旁路观察一行上游 SSE；非 data 行与无法解析的 data 一律忽略。
func (s *streamObserver) ObserveLine(line string) {
	if !strings.HasPrefix(line, "data:") {
		return
	}
	data := strings.TrimSpace(line[len("data:"):])
	if data == "" {
		return
	}
	var ev anthropicEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return
	}

	switch ev.Type {
	case "message_start":
		if ev.Message != nil {
			s.usage = s.usage.merge(normalizeUsage(ev.Message.Usage))
		}
	case "message_delta":
		if ev.Usage != nil {
			s.usage = s.usage.merge(normalizeUsage(*ev.Usage))
		}
	case "message_stop":
		s.done = true
	case "error":
		msg := "上游流中断"
		if ev.Error != nil && ev.Error.Message != "" {
			msg = ev.Error.Message
		}
		s.streamErr = errors.New(msg)
	}
}

// Usage 返回累积用量（含 message_start 已送达的 input/cache token）；
// 上游中途断连时管线据此计费兜底，避免记 0。
func (s *streamObserver) Usage() (dto.Usage, bool) {
	u := s.usage.dtoUsage()
	if u.PromptTokens == 0 && u.CompletionTokens == 0 && u.CachedTokens == 0 {
		return dto.Usage{}, false
	}
	return u, true
}

// Err 返回上游流内错误事件（非 nil 时管线按流中断处理）。
func (s *streamObserver) Err() error { return s.streamErr }

// Done 是否观察到 message_stop 完成信号（error 事件后不算完成）。
func (s *streamObserver) Done() bool { return s.done && s.streamErr == nil }
