package openai

import (
	"encoding/json"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
)

// 确保实现了流观察能力接口（仅图像端点提供观察器，见 NewStreamObserver）。
var _ adaptor.StreamObserving = Adaptor{}

// NewStreamObserver 仅图像端点（generations/edits 流式）提供观察器；
// chat/responses 返回 nil——管线维持既有 OpenAI SSE 内联捕获语义
// （顶层/completed usage 提取、usage-only chunk 吞吐、[DONE] 判定）不变。
func (Adaptor) NewStreamObserver(info *adaptor.RelayInfo) adaptor.StreamObserver {
	switch info.Endpoint {
	case adaptor.EndpointImagesGenerations, adaptor.EndpointImagesEdits:
		return &imageStreamObserver{}
	default:
		return nil
	}
}

// imageStreamObserver OpenAI Images 流（gpt-image 系 stream:true）的透传型观察器。
// 事件流形态（type 前缀 image_generation / image_edit）：
//   - *.partial_image：渐进预览帧（内容增量，不计量）；
//   - *.completed：终图事件——携带整幅 b64 与 usage（gpt-image 系 token 计量），
//     既是协议级完成信号，也是按次计费的计次点（每个 completed 事件 = 1 张产出）。
//
// 管线把上游字节原样转发（无一被吞、不注入 [DONE]），本观察器仅旁路解析。
// 容错：异形上游把 usage 放在非 completed 事件也照收（最后一个非空生效），
// 且视作完成信号——有计量即不按「静默断流」记失败。
type imageStreamObserver struct {
	usage *dto.Usage
	calls int
	done  bool
}

// ObserveLine 旁路观察一行上游 SSE；非 data 行与无法解析的 data 一律忽略。
func (o *imageStreamObserver) ObserveLine(line string) {
	if !strings.HasPrefix(line, "data:") {
		return
	}
	data := strings.TrimSpace(line[len("data:"):])
	if data == "" || data == "[DONE]" {
		return
	}
	var probe struct {
		Type  string          `json:"type"`
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &probe); err != nil {
		return
	}
	if strings.HasSuffix(probe.Type, ".completed") {
		o.calls++
		o.done = true
	}
	if len(probe.Usage) > 0 && string(probe.Usage) != "null" {
		if u, parsed := dto.ParseUsage(probe.Usage); parsed {
			o.usage = &u
			o.done = true
		}
	}
}

// Usage 返回累积用量：token 计量（若上游给出）+ Calls=completed 事件数（产出张数）。
// 未见任何 completed/usage 时返回 false——管线按无 usage 处理（token 计 0 + 告警；
// 按次计费由 ComputeCosts 的 Calls 0→1 兜底，整单仍按 1 次收）。
func (o *imageStreamObserver) Usage() (dto.Usage, bool) {
	if o.usage == nil && o.calls == 0 {
		return dto.Usage{}, false
	}
	var u dto.Usage
	if o.usage != nil {
		u = *o.usage
	}
	u.Calls = o.calls
	return u, true
}

// Err 图像流无协议内错误事件形态（错误在流前以非 2xx 表达），恒 nil。
func (o *imageStreamObserver) Err() error { return nil }

// Done 是否观察到 completed 事件（或计量 usage）——区分完整流与静默断流。
func (o *imageStreamObserver) Done() bool { return o.done }
