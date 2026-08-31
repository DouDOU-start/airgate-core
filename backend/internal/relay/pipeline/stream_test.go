package pipeline

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
)

// chunkedReader 按固定 chunk 大小切割数据，模拟跨 read 边界的半行场景。
type chunkedReader struct {
	data      []byte
	chunkSize int
	offset    int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	end := r.offset + r.chunkSize
	if end > len(r.data) {
		end = len(r.data)
	}
	n := copy(p, r.data[r.offset:end])
	r.offset += n
	return n, nil
}

// newSSEResponse 构造带指定 body reader 的上游响应。
func newSSEResponse(body io.Reader) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(body),
	}
}

func TestRelaySSE(t *testing.T) {
	usageChunk := `data: {"id":"c1","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":42,"prompt_tokens_details":{"cached_tokens":10}}}`
	stream := strings.Join([]string{
		`data: {"id":"c1","choices":[{"delta":{"content":"He"}}]}`,
		``,
		`data: {"id":"c1","choices":[{"delta":{"content":"llo"}}]}`,
		``,
		usageChunk,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	// forwardUsageChunk=false 时 usage-only chunk 被吞掉（其余行照常透传）。
	streamWithoutUsageChunk := strings.Join([]string{
		`data: {"id":"c1","choices":[{"delta":{"content":"He"}}]}`,
		``,
		`data: {"id":"c1","choices":[{"delta":{"content":"llo"}}]}`,
		``,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	cases := []struct {
		name              string
		body              string
		chunkSize         int
		forwardUsageChunk bool
		wantUsage         *dto.Usage
		wantDone          bool
		// wantBody 非空时精确断言透传体；空则期望与输入逐行一致。
		wantBody string
	}{
		{
			name:              "include_usage 末 chunk 捕获（客户端请求了 usage 则透传）",
			body:              stream,
			chunkSize:         1 << 20, // 一次读完
			forwardUsageChunk: true,
			wantUsage:         &dto.Usage{PromptTokens: 100, CompletionTokens: 42, CachedTokens: 10},
			wantDone:          true,
		},
		{
			name:              "跨 read 边界半行拼接",
			body:              stream,
			chunkSize:         7, // 每 7 字节一读，行必然被截断
			forwardUsageChunk: true,
			wantUsage:         &dto.Usage{PromptTokens: 100, CompletionTokens: 42, CachedTokens: 10},
			wantDone:          true,
		},
		{
			name:      "客户端未请求 include_usage：usage 捕获但 chunk 不下发",
			body:      stream,
			chunkSize: 1 << 20,
			wantUsage: &dto.Usage{PromptTokens: 100, CompletionTokens: 42, CachedTokens: 10},
			wantDone:  true,
			wantBody:  streamWithoutUsageChunk,
		},
		{
			name: "无 usage 流记 0",
			body: strings.Join([]string{
				`data: {"id":"c1","choices":[{"delta":{"content":"hi"}}]}`,
				``,
				`data: [DONE]`,
				``,
			}, "\n"),
			chunkSize:         1 << 20,
			forwardUsageChunk: true,
			wantUsage:         nil,
			wantDone:          true,
		},
		{
			name: "无 DONE 标志",
			body: strings.Join([]string{
				`data: {"id":"c1","choices":[{"delta":{"content":"hi"}}]}`,
				``,
			}, "\n"),
			chunkSize:         1 << 20,
			forwardUsageChunk: true,
			wantUsage:         nil,
			wantDone:          false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			upstream := newSSEResponse(&chunkedReader{data: []byte(tc.body), chunkSize: tc.chunkSize})
			result := relaySSE(w, upstream, time.Now(), dto.ExtractUsage, tc.forwardUsageChunk, chatFirstContentLine, sseMaxLineBytes, nil)

			if result.err != nil {
				t.Fatalf("relaySSE err = %v", result.err)
			}
			if !result.written {
				t.Error("written 应为 true")
			}
			if result.done != tc.wantDone {
				t.Errorf("done = %v, want %v", result.done, tc.wantDone)
			}
			if tc.wantUsage == nil {
				if result.usage != nil {
					t.Errorf("usage = %+v, want nil", result.usage)
				}
			} else if result.usage == nil || *result.usage != *tc.wantUsage {
				t.Errorf("usage = %+v, want %+v", result.usage, tc.wantUsage)
			}

			// 逐行透传：输出与输入逐行一致（尾部统一补 \n）；吞 usage chunk 场景单独断言。
			wantBody := tc.wantBody
			if wantBody == "" {
				wantBody = tc.body
			}
			if !strings.HasSuffix(wantBody, "\n") {
				wantBody += "\n"
			}
			if got := w.Body.String(); got != wantBody {
				t.Errorf("透传体不一致:\ngot  %q\nwant %q", got, wantBody)
			}
			if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
				t.Errorf("Content-Type = %q", ct)
			}
			if result.firstTokenMs < 0 {
				t.Errorf("firstTokenMs = %d", result.firstTokenMs)
			}
		})
	}
}

// 客户端断开后，网关应停止写下游但继续排空上游，直到捕获最终 usage 与完成标志。
func TestRelaySSE客户端写失败后继续捕获Usage(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"你好"}`,
		``,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":12,"output_tokens":3}}}`,
		``,
	}, "\n")
	w := &alwaysFailResponseWriter{header: make(http.Header)}

	result := relaySSE(w, newSSEResponse(strings.NewReader(body)), time.Now(),
		dto.ExtractResponsesUsage, true, responsesFirstContentLine, sseMaxLineBytesResponses, nil)

	if result.err != nil {
		t.Fatalf("客户端写失败不应中断上游排空：%v", result.err)
	}
	if result.usage == nil || result.usage.PromptTokens != 12 || result.usage.CompletionTokens != 3 {
		t.Fatalf("未捕获最终 usage：%+v", result.usage)
	}
	if w.writes != 1 {
		t.Fatalf("下游失败后应停止继续写入，实际写入次数：%d", w.writes)
	}
}

// TestRelaySSEFirstTokenOnlyOnDataLines first_token_ms 只在真实 data 载荷行触发：
// 注释行 / event: 行 / [DONE] 不算首 token。
func TestRelaySSEFirstTokenOnlyOnDataLines(t *testing.T) {
	// start 前移 50ms：任何记录必然 >= 50，与"未记录"（0）可区分。
	backdated := func() time.Time { return time.Now().Add(-50 * time.Millisecond) }

	t.Run("仅注释与 event 行不记录", func(t *testing.T) {
		body := strings.Join([]string{
			`: OPENROUTER PROCESSING`,
			`event: message`,
			``,
			`data: [DONE]`,
			``,
		}, "\n")
		w := httptest.NewRecorder()
		result := relaySSE(w, newSSEResponse(strings.NewReader(body)), backdated(), dto.ExtractUsage, true, chatFirstContentLine, sseMaxLineBytes, nil)
		if result.firstTokenMs != 0 {
			t.Errorf("firstTokenMs = %d, want 0（无真实 data 载荷）", result.firstTokenMs)
		}
	})

	t.Run("注释心跳后的首个 data 行才记录", func(t *testing.T) {
		body := strings.Join([]string{
			`: keepalive`,
			``,
			`data: {"id":"c1","choices":[{"delta":{"content":"hi"}}]}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n")
		w := httptest.NewRecorder()
		result := relaySSE(w, newSSEResponse(strings.NewReader(body)), backdated(), dto.ExtractUsage, true, chatFirstContentLine, sseMaxLineBytes, nil)
		if result.firstTokenMs < 50 {
			t.Errorf("firstTokenMs = %d, want >= 50（应由 data 行触发）", result.firstTokenMs)
		}
	})
}

// TestResponsesFirstContentLine Responses 首内容行谓词：内容增量以及 done/终态中的
// 非空最终输出算首 token；纯生命周期事件、非 JSON 和无 type 数据不算。
func TestResponsesFirstContentLine(t *testing.T) {
	cases := []struct {
		name string
		data string
		want bool
	}{
		{"output_text.delta", `{"type":"response.output_text.delta","delta":"hi"}`, true},
		{"output_audio.delta", `{"type":"response.output_audio.delta","delta":"aGk="}`, true},
		{"function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","delta":"{"}`, true},
		{"output_item.done", `{"type":"response.output_item.done","item":{"type":"function_call","name":"lookup","arguments":"{}"}}`, true},
		{"completed 带输出", `{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"完成"}]}]}}`, true},
		{"response.created", `{"type":"response.created","response":{}}`, false},
		{"response.in_progress", `{"type":"response.in_progress"}`, false},
		{"output_item.added", `{"type":"response.output_item.added"}`, false},
		{"content_part.added", `{"type":"response.content_part.added"}`, false},
		{"response.completed", `{"type":"response.completed","response":{}}`, false},
		{"非 JSON", `not-json`, false},
		{"无 type 字段", `{"delta":"hi"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := responsesFirstContentLine([]byte(tc.data)); got != tc.want {
				t.Errorf("responsesFirstContentLine(%q) = %v, want %v", tc.data, got, tc.want)
			}
		})
	}
}

// TestRelaySSEFirstTokenResponses 回归 Responses 首字口径：跳过 response.created 等
// 生命周期事件，在增量或终态真实输出处记录；chat 维持首个 data 载荷行即记。
func TestRelaySSEFirstTokenResponses(t *testing.T) {
	// start 前移 50ms：任何记录必然 >= 50，与"未记录"（0）可区分。
	backdated := func() time.Time { return time.Now().Add(-50 * time.Millisecond) }

	t.Run("responses: created 打头不记，随后 delta 才记", func(t *testing.T) {
		body := strings.Join([]string{
			`event: response.created`,
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			``,
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","delta":"Hi"}`,
			``,
		}, "\n")
		w := httptest.NewRecorder()
		result := relaySSE(w, newSSEResponse(strings.NewReader(body)), backdated(),
			dto.ExtractResponsesUsage, true, responsesFirstContentLine, sseMaxLineBytesResponses, nil)
		if result.firstTokenMs < 50 {
			t.Errorf("firstTokenMs = %d, want >= 50（应由 delta 行触发，非 created 行）", result.firstTokenMs)
		}
	})

	t.Run("responses: 仅 created + 无输出 completed 则不记", func(t *testing.T) {
		body := strings.Join([]string{
			`event: response.created`,
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5}}}`,
			``,
		}, "\n")
		w := httptest.NewRecorder()
		result := relaySSE(w, newSSEResponse(strings.NewReader(body)), backdated(),
			dto.ExtractResponsesUsage, true, responsesFirstContentLine, sseMaxLineBytesResponses, nil)
		if result.firstTokenMs != 0 {
			t.Errorf("firstTokenMs = %d, want 0（没有真实输出）", result.firstTokenMs)
		}
		if result.usage == nil {
			t.Error("completed 事件 usage 应被捕获")
		}
	})

	t.Run("responses: completed 携带最终输出时记录", func(t *testing.T) {
		body := strings.Join([]string{
			`event: response.created`,
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"完成"}]}],"usage":{"input_tokens":10,"output_tokens":5}}}`,
			``,
		}, "\n")
		w := httptest.NewRecorder()
		result := relaySSE(w, newSSEResponse(strings.NewReader(body)), backdated(),
			dto.ExtractResponsesUsage, true, responsesFirstContentLine, sseMaxLineBytesResponses, nil)
		if result.firstTokenMs < 50 {
			t.Errorf("firstTokenMs = %d, want >= 50（应由 completed 最终输出触发）", result.firstTokenMs)
		}
	})

	t.Run("chat: 首个 data 载荷行即记（回归）", func(t *testing.T) {
		body := strings.Join([]string{
			`data: {"id":"c1","choices":[{"delta":{"content":"hi"}}]}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n")
		w := httptest.NewRecorder()
		result := relaySSE(w, newSSEResponse(strings.NewReader(body)), backdated(),
			dto.ExtractUsage, true, chatFirstContentLine, sseMaxLineBytes, nil)
		if result.firstTokenMs < 50 {
			t.Errorf("firstTokenMs = %d, want >= 50（chat 首个 data 行即记）", result.firstTokenMs)
		}
	})
}

// TestRelaySSEResponsesLargeCompletedEvent 修2 回归：Responses 端点单个 completed 事件略超
// 8MB 时不报 ErrTooLong（用 64MB 上限），usage 仍被捕获；chat 维持 8MB 上限（超限报错）。
func TestRelaySSEResponsesLargeCompletedEvent(t *testing.T) {
	// 构造略超 8MB 的 completed 事件：内嵌大 output 文本，usage 在同一事件内。
	big := strings.Repeat("x", (8<<20)+(1<<10))
	body := strings.Join([]string{
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"` + big + `"}]}],"usage":{"input_tokens":1000,"output_tokens":500}}}`,
		``,
	}, "\n")

	t.Run("responses 64MB 上限：completed 事件不截断，usage 捕获", func(t *testing.T) {
		w := httptest.NewRecorder()
		result := relaySSE(w, newSSEResponse(strings.NewReader(body)), time.Now(),
			dto.ExtractResponsesUsage, true, responsesFirstContentLine, sseMaxLineBytesResponses, nil)
		if result.err != nil {
			t.Fatalf("relaySSE err = %v（64MB 上限不应报 ErrTooLong）", result.err)
		}
		if result.usage == nil || result.usage.PromptTokens != 1000 || result.usage.CompletionTokens != 500 {
			t.Errorf("usage = %+v, want input=1000 output=500", result.usage)
		}
	})

	t.Run("chat 8MB 上限：超限报 ErrTooLong", func(t *testing.T) {
		w := httptest.NewRecorder()
		result := relaySSE(w, newSSEResponse(strings.NewReader(body)), time.Now(),
			dto.ExtractUsage, true, chatFirstContentLine, sseMaxLineBytes, nil)
		if !errors.Is(result.err, bufio.ErrTooLong) {
			t.Errorf("err = %v, want bufio.ErrTooLong（chat 维持 8MB 上限）", result.err)
		}
	})
}

func TestIsUsageOnlyChunk(t *testing.T) {
	cases := []struct {
		name string
		data string
		want bool
	}{
		{"choices 空数组带 usage", `{"id":"c1","choices":[],"usage":{"prompt_tokens":1}}`, true},
		{"无 choices 字段带 usage", `{"id":"c1","usage":{"prompt_tokens":1}}`, true},
		{"choices 非空带 usage", `{"choices":[{"delta":{}}],"usage":{"prompt_tokens":1}}`, false},
		{"usage 为 null", `{"choices":[],"usage":null}`, false},
		{"无 usage", `{"choices":[]}`, false},
		{"非 JSON", `not-json`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUsageOnlyChunk([]byte(tc.data)); got != tc.want {
				t.Errorf("isUsageOnlyChunk(%q) = %v, want %v", tc.data, got, tc.want)
			}
		})
	}
}

func TestExtractSSEData(t *testing.T) {
	cases := []struct {
		name   string
		line   string
		want   string
		wantOK bool
	}{
		{"带空格", `data: {"a":1}`, `{"a":1}`, true},
		{"不带空格", `data:{"a":1}`, `{"a":1}`, true},
		{"DONE", `data: [DONE]`, `[DONE]`, true},
		{"event 行", `event: message`, ``, false},
		{"注释行", `: keepalive`, ``, false},
		{"空行", ``, ``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := extractSSEData(tc.line)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("extractSSEData(%q) = (%q,%v), want (%q,%v)", tc.line, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// fakeObserver 供 relaySSE 观察器路径（原生协议流透传）测试：记录观察到的行，
// 可注入 usage/err/done。
type fakeObserver struct {
	seen  []string
	usage *dto.Usage
	err   error
	done  bool
}

func (f *fakeObserver) ObserveLine(line string) { f.seen = append(f.seen, line) }
func (f *fakeObserver) Usage() (dto.Usage, bool) {
	if f.usage == nil {
		return dto.Usage{}, false
	}
	return *f.usage, true
}
func (f *fakeObserver) Err() error { return f.err }
func (f *fakeObserver) Done() bool { return f.done }

// 观察器路径为纯透传：上游字节逐行原样下发（无一被吞、不注入任何行），
// 观察器旁路看到每一行；usage/done 取自观察器。
func TestRelaySSEObserverPassthrough(t *testing.T) {
	upstream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":10}}}`,
		``,
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hi"}}`,
		``,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	obs := &fakeObserver{usage: &dto.Usage{PromptTokens: 10, CompletionTokens: 5}, done: true}

	w := httptest.NewRecorder()
	sr := relaySSE(w, newSSEResponse(strings.NewReader(upstream)), time.Now(),
		dto.ExtractUsage, false, anthropicFirstContentLine, sseMaxLineBytes, obs)

	if sr.err != nil {
		t.Fatalf("err = %v", sr.err)
	}
	// 输入以换行结尾，scanner 产出 6 行，逐行补 \n 后与输入逐字节一致。
	if got := w.Body.String(); got != upstream {
		t.Errorf("透传体不一致:\ngot  %q\nwant %q", got, upstream)
	}
	if len(obs.seen) != 6 {
		t.Errorf("观察行数 = %d, want 6（含 event/空行）", len(obs.seen))
	}
	if sr.usage == nil || sr.usage.PromptTokens != 10 || sr.usage.CompletionTokens != 5 {
		t.Errorf("usage = %+v, want 取自观察器", sr.usage)
	}
	if !sr.done {
		t.Error("done 应取自观察器 Done()")
	}
}

// 上游中途断连（scanner 未 EOF 干净）：观察器已累积 usage 须兜底取回，不记 0。
func TestRelaySSEObserverInterruptUsesAccumulatedUsage(t *testing.T) {
	obs := &fakeObserver{usage: &dto.Usage{PromptTokens: 100, CompletionTokens: 5}}
	body := &errAfterReader{data: []byte("data: {\"type\":\"content_block_delta\"}\n")}
	w := httptest.NewRecorder()
	sr := relaySSE(w, newSSEResponse(body), time.Now(),
		dto.ExtractUsage, false, anthropicFirstContentLine, sseMaxLineBytes, obs)

	if sr.err == nil {
		t.Fatal("expected scanner error")
	}
	if sr.usage == nil || sr.usage.PromptTokens != 100 {
		t.Errorf("usage = %+v, 期望中断回退累积值 prompt=100", sr.usage)
	}
	if sr.done {
		t.Error("中断流不得标记完成")
	}
	if !sr.dataReceived {
		t.Error("读取到上游 SSE 行后必须标记 dataReceived")
	}
}

// 首内容前的流内 error 事件不能提交响应头或错误帧，应交给 failover 状态机。
func TestRelaySSEObserverErrorEventBeforeContentCanFailover(t *testing.T) {
	upstream := `data: {"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}` + "\n"
	obs := &fakeObserver{err: errors.New("overloaded"), usage: &dto.Usage{PromptTokens: 10}}
	w := httptest.NewRecorder()
	sr := relaySSE(w, newSSEResponse(strings.NewReader(upstream)), time.Now(),
		dto.ExtractUsage, false, anthropicFirstContentLine, sseMaxLineBytes, obs)

	if sr.upstreamError == nil || sr.upstreamError.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("应识别为可切换的 503 协议错误，实际：%+v", sr.upstreamError)
	}
	if sr.written || w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatalf("内容前错误不应提交下游响应，written=%v code=%d body=%q", sr.written, w.Code, w.Body.String())
	}
	if sr.done {
		t.Error("错误事件不得标记完成")
	}
}

// 已下发真实内容后再出现错误时不可 failover，错误帧继续透传并按流中断收尾。
func TestRelaySSEObserverErrorEventAfterContentAborts(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"你好"}}`,
		``,
		`data: {"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`,
		``,
	}, "\n")
	obs := &fakeObserver{usage: &dto.Usage{PromptTokens: 10}}
	w := httptest.NewRecorder()
	sr := relaySSE(w, newSSEResponse(strings.NewReader(upstream)), time.Now(),
		dto.ExtractUsage, false, anthropicFirstContentLine, sseMaxLineBytes, obs)

	if !sr.written || sr.err == nil || sr.upstreamError != nil {
		t.Fatalf("内容后错误应作为已写出中断，written=%v err=%v upstreamError=%+v", sr.written, sr.err, sr.upstreamError)
	}
	if !strings.Contains(w.Body.String(), "overloaded_error") {
		t.Errorf("内容后的错误事件应原样透传：%q", w.Body.String())
	}
	if sr.usage == nil || sr.usage.PromptTokens != 10 {
		t.Errorf("usage 回退值 = %+v，期望 prompt=10", sr.usage)
	}
}

// TestAnthropicFirstContentLine Anthropic 流首内容行谓词：仅 content_block_delta 算首 token。
func TestAnthropicFirstContentLine(t *testing.T) {
	cases := []struct {
		name string
		data string
		want bool
	}{
		{"content_block_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`, true},
		{"message_start", `{"type":"message_start","message":{}}`, false},
		{"content_block_start", `{"type":"content_block_start"}`, false},
		{"ping", `{"type":"ping"}`, false},
		{"message_stop", `{"type":"message_stop"}`, false},
		{"非 JSON", `not-json`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := anthropicFirstContentLine([]byte(tc.data)); got != tc.want {
				t.Errorf("anthropicFirstContentLine(%q) = %v, want %v", tc.data, got, tc.want)
			}
		})
	}
}

// errAfterReader 读完 data 后返回错误（模拟传输中断，非干净 EOF）。
type errAfterReader struct {
	data []byte
	off  int
}

type alwaysFailResponseWriter struct {
	header http.Header
	status int
	writes int
}

func (w *alwaysFailResponseWriter) Header() http.Header { return w.header }

func (w *alwaysFailResponseWriter) WriteHeader(statusCode int) { w.status = statusCode }

func (w *alwaysFailResponseWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, errors.New("客户端已断开")
}

func (w *alwaysFailResponseWriter) Flush() {}

func (r *errAfterReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, errors.New("connection reset")
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}
