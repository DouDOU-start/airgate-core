package pipeline

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
)

// SSE 扫描缓冲：初始 64KB，单行上限 8MB（reasoning 大事件可能超 1MB）。
const (
	sseInitialBufSize = 64 << 10
	sseMaxLineBytes   = 8 << 20
	// sseMaxLineBytesResponses Responses 流的单行上限（64MB）。
	// Responses 的 usage 嵌在 response.completed 事件里，该事件内嵌完整 response
	// 对象（output 数组、可能的 base64 内容），单行体积远超 chat 独立小 usage chunk；
	// 沿用 8MB 会触发 scanner ErrTooLong → usage 记 0 + 客户端流被截断。
	sseMaxLineBytesResponses = 64 << 20
)

// streamResult SSE 逐行透传的结果。
type streamResult struct {
	// usage 旁路捕获的最后一个非空 usage（无则 nil，计 0 计费）。
	usage *dto.Usage
	// firstTokenMs 首行写出耗时（相对 start），未写出任何行为 0。
	firstTokenMs int64
	// written 是否已向客户端写出过字节（含响应头）——写出后不可 failover。
	written bool
	// err 中途失败（上游读错误或客户端写错误）；已写出字节时只能终止。
	err error
	// done 是否收到 data: [DONE] 完成标志。
	done bool
}

// relaySSE 把上游 SSE 流逐行透传给客户端，同时旁路捕获 usage chunk 与 first_token_ms。
//
//   - 响应头：透传上游 Content-Type（缺省 text/event-stream），补 SSE 标准头；
//   - 逐行写出并 Flush；
//   - data: [DONE] → 完成标志；其余 data 行经 extractUsage 探测 usage（最后一个非空生效）——
//     chat 用 dto.ExtractUsage（顶层 usage），responses 用 dto.ExtractResponsesUsage
//     （completed 事件的 data.response.usage）；
//   - forwardUsageChunk=false（客户端未显式请求 include_usage，注入系网关计费所需）
//     时吞掉 usage-only chunk（choices 为空数组且带 usage），[DONE] 照常下发；
//     responses 传 true——completed 事件是正常内容事件，全透传不吞；
//   - first_token_ms 只在 isFirstContentLine 判为「首内容行」的 data 载荷行触发
//     （注释行/event: 行/[DONE] 恒不算）——chat 传 chatFirstContentLine（任意 data 载荷
//     即算，维持现状），responses 传 responsesFirstContentLine（仅 type 含 .delta 的内容
//     增量事件才算，跳过 response.created 等 preamble 生命周期事件，避免 first_token 记成
//     上游 ack 延迟）；
//   - maxLineBytes 为 scanner 单行上限：chat 用 sseMaxLineBytes（8MB），responses 用
//     sseMaxLineBytesResponses（64MB，容纳内嵌完整 response 的 completed 事件）；
//   - bufio.Scanner 天然处理跨 read 边界的半行拼接。
func relaySSE(w http.ResponseWriter, upstream *http.Response, start time.Time, extractUsage func([]byte) (dto.Usage, bool), forwardUsageChunk bool, isFirstContentLine func([]byte) bool, maxLineBytes int) streamResult {
	result := streamResult{}

	contentType := upstream.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/event-stream"
	}
	header := w.Header()
	header.Set("Content-Type", contentType)
	if header.Get("Cache-Control") == "" {
		header.Set("Cache-Control", "no-cache")
	}
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(upstream.StatusCode)
	result.written = true

	flusher, _ := w.(http.Flusher)

	scanner := bufio.NewScanner(upstream.Body)
	scanner.Buffer(make([]byte, sseInitialBufSize), maxLineBytes)
	for scanner.Scan() {
		line := scanner.Text()

		if data, ok := extractSSEData(line); ok {
			if data == "[DONE]" {
				result.done = true
			} else {
				if u, found := extractUsage([]byte(data)); found {
					result.usage = &u
					// usage 已捕获计费；客户端未请求 include_usage 时该 chunk 不下发。
					if !forwardUsageChunk && isUsageOnlyChunk([]byte(data)) {
						continue
					}
				}
				if result.firstTokenMs == 0 && isFirstContentLine([]byte(data)) {
					result.firstTokenMs = time.Since(start).Milliseconds()
				}
			}
		}

		if _, err := io.WriteString(w, line+"\n"); err != nil {
			result.err = err
			return result
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	if err := scanner.Err(); err != nil {
		result.err = err
	}
	return result
}

// isUsageOnlyChunk 判断 SSE data 载荷是否为 usage-only chunk：
// choices 缺失或为空数组，且携带非空 usage（OpenAI include_usage 尾 chunk 形态）。
func isUsageOnlyChunk(data []byte) bool {
	var probe struct {
		Choices []json.RawMessage `json:"choices"`
		Usage   json.RawMessage   `json:"usage"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	if len(probe.Choices) != 0 {
		return false
	}
	return len(probe.Usage) > 0 && string(probe.Usage) != "null"
}

// chatFirstContentLine chat 端点首内容行谓词：任意 data 载荷行即算首 token，
// 维持既有口径（chat 流首个 chunk 即首个内容增量）。
func chatFirstContentLine([]byte) bool { return true }

// responsesFirstContentLine Responses 端点首内容行谓词：仅内容增量事件算首 token。
// Responses 流为语义事件流，首事件是 response.created（上游 ack，早于生成），其后可能有
// response.in_progress / output_item.added / content_part.added 等 preamble 生命周期事件；
// 若以首个 data 行计 first_token 会记成 ack 延迟而失真、与 chat 口径不一致。
// 只有内容增量事件（type 以 .delta 结尾，如 response.output_text.delta /
// response.output_audio.delta / response.function_call_arguments.delta）才算首内容行。
func responsesFirstContentLine(data []byte) bool {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return strings.HasSuffix(probe.Type, ".delta")
}

// extractSSEData 剥离 "data:" 前缀（容忍前缀后可选空格）；非 data 行返回 false。
func extractSSEData(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	return strings.TrimSpace(line[len("data:"):]), true
}
