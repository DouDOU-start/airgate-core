package gemini

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
)

// streamObserver Gemini streamGenerateContent SSE 的透传型 usage 观察器。
// Gemini SSE 每个 data 行即一个完整 generateContent JSON 分片；管线原样转发，
// 本观察器旁路解析：
//   - usageMetadata（末 chunk 携带，个别上游中间 chunk 也带）——取最后一个非空；
//   - candidates[0].finishReason 为协议级完成信号（Gemini 无显式结束事件，靠 EOF 收尾）；
//   - 顶层 error 对象记为流内错误——管线据此按流中断处理，不把截断响应伪装成完整。
type streamObserver struct {
	usage     geminiUsage
	sawFinish bool
	streamErr error
}

// observedChunk 观察所需的最小分片结构。
type observedChunk struct {
	Candidates []struct {
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata geminiUsage `json:"usageMetadata"`
	Error         *struct {
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
	var chunk observedChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return
	}
	if chunk.Error != nil {
		msg := "上游流中断"
		if chunk.Error.Message != "" {
			msg = chunk.Error.Message
		}
		s.streamErr = errors.New(msg)
		return
	}
	if !chunk.UsageMetadata.empty() {
		s.usage = chunk.UsageMetadata
	}
	if len(chunk.Candidates) > 0 && chunk.Candidates[0].FinishReason != "" {
		s.sawFinish = true
	}
}

// Usage 返回累积用量（含思考 token）；中途断连时供管线计费兜底。
func (s *streamObserver) Usage() (dto.Usage, bool) {
	if s.usage.empty() {
		return dto.Usage{}, false
	}
	return s.usage.dtoUsage(), true
}

// Err 返回上游流内错误（非 nil 时管线按流中断处理）。
func (s *streamObserver) Err() error { return s.streamErr }

// Done 是否观察到 finishReason 完成信号（error 后不算完成）。
func (s *streamObserver) Done() bool { return s.sawFinish && s.streamErr == nil }
