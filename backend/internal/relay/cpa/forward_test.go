package cpa

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
)

func TestXAIVideoEndpointsUseOpenAIVideoFormat(t *testing.T) {
	want := sdktranslator.FromString("openai-video")
	for _, endpoint := range []string{adaptor.EndpointXAIVideosGenerations, adaptor.EndpointXAIVideosRetrieve} {
		if got := sourceFormatFor(endpoint, "openai"); got != want {
			t.Errorf("端点 %s 的 CPA 格式 = %q，期望 %q", endpoint, got.String(), want.String())
		}
	}
}

func TestXAIImageEndpointsUseOpenAIImageFormat(t *testing.T) {
	want := sdktranslator.FromString("openai-image")
	for _, endpoint := range []string{adaptor.EndpointImagesGenerations, adaptor.EndpointImagesEdits} {
		if got := sourceFormatFor(endpoint, "openai"); got != want {
			t.Errorf("端点 %s 的 CPA 格式 = %q，期望 %q", endpoint, got.String(), want.String())
		}
	}
}

func TestRelayStreamDoesNotCommitHeadersBeforeFirstChunk(t *testing.T) {
	c, _ := newStreamTestContext()
	chunks := make(chan cliproxyexecutor.StreamChunk, 1)
	chunks <- cliproxyexecutor.StreamChunk{Err: errors.New("上游失败")}
	close(chunks)

	result := (&Bridge{}).relayStream(context.Background(), c, &cliproxyexecutor.StreamResult{Chunks: chunks}, time.Now(), adaptor.EndpointChatCompletions)

	if c.Writer.Written() {
		t.Fatal("首个数据块报错时不应提交响应头")
	}
	if result.NetErr == nil {
		t.Fatal("首个数据块报错应返回可重试错误")
	}
}

func TestRelayStreamRequiresExplicitCompletionMarker(t *testing.T) {
	c, recorder := newStreamTestContext()
	chunks := make(chan cliproxyexecutor.StreamChunk, 1)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")}
	close(chunks)

	result := (&Bridge{}).relayStream(context.Background(), c, &cliproxyexecutor.StreamResult{Chunks: chunks}, time.Now(), adaptor.EndpointChatCompletions)

	if !result.Written {
		t.Fatal("有效数据块应写入客户端")
	}
	if result.Done {
		t.Fatal("缺少结束标志的正常 EOF 不应视为完整流")
	}
	if recorder.Body.Len() == 0 {
		t.Fatal("响应体不应为空")
	}
}

func TestRelayStreamRecognizesDoneMarker(t *testing.T) {
	c, _ := newStreamTestContext()
	chunks := make(chan cliproxyexecutor.StreamChunk, 1)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte("data: [DONE]\n\n")}
	close(chunks)

	result := (&Bridge{}).relayStream(context.Background(), c, &cliproxyexecutor.StreamResult{Chunks: chunks}, time.Now(), adaptor.EndpointChatCompletions)

	if !result.Done {
		t.Fatal("显式结束标志应视为完整流")
	}
}

func TestRelayStreamRecognizesGeminiFinishReason(t *testing.T) {
	c, _ := newStreamTestContext()
	chunks := make(chan cliproxyexecutor.StreamChunk, 1)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")}
	close(chunks)

	result := (&Bridge{}).relayStream(context.Background(), c, &cliproxyexecutor.StreamResult{Chunks: chunks}, time.Now(), adaptor.EndpointGenerateContent)

	if !result.Done {
		t.Fatal("Gemini 末帧 finishReason 应视为明确结束标志")
	}
}

func TestRelayStreamFramesSplitResponsesEvents(t *testing.T) {
	c, recorder := newStreamTestContext()
	chunks := make(chan cliproxyexecutor.StreamChunk, 6)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte("event: response.created")}
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.created","response":{"id":"resp-1"}}`)}
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte("\n")}
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte("event: response.completed")}
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.completed","response":{"id":"resp-1","usage":{"input_tokens":2,"output_tokens":1}}}`)}
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte("\n")}
	close(chunks)

	result := (&Bridge{}).relayStream(context.Background(), c, &cliproxyexecutor.StreamResult{Chunks: chunks}, time.Now(), adaptor.EndpointResponses)

	parts := strings.Split(strings.TrimSpace(recorder.Body.String()), "\n\n")
	if len(parts) != 2 {
		t.Fatalf("应输出两个独立 SSE 事件，实际为 %d 个，响应体：%q", len(parts), recorder.Body.String())
	}
	if !strings.HasPrefix(parts[0], "event: response.created\ndata: ") {
		t.Fatalf("首个事件分帧错误：%q", parts[0])
	}
	if !strings.HasPrefix(parts[1], "event: response.completed\ndata: ") {
		t.Fatalf("完成事件分帧错误：%q", parts[1])
	}
	if !result.Done {
		t.Fatal("response.completed 应被识别为流已完成")
	}
	if result.Usage == nil || result.Usage.PromptTokens != 2 || result.Usage.CompletionTokens != 1 {
		t.Fatalf("完成事件的 usage 解析错误：%+v", result.Usage)
	}
}

func TestRelayStreamFramesDataOnlyResponsesChunks(t *testing.T) {
	c, recorder := newStreamTestContext()
	chunks := make(chan cliproxyexecutor.StreamChunk, 2)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.created","response":{"id":"resp-1"}}`)}
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.completed","response":{"id":"resp-1"}}`)}
	close(chunks)

	result := (&Bridge{}).relayStream(context.Background(), c, &cliproxyexecutor.StreamResult{Chunks: chunks}, time.Now(), adaptor.EndpointResponses)

	parts := strings.Split(strings.TrimSpace(recorder.Body.String()), "\n\n")
	if len(parts) != 2 {
		t.Fatalf("纯 data chunk 应输出两个独立 SSE 事件，实际为 %d 个，响应体：%q", len(parts), recorder.Body.String())
	}
	if !result.Done {
		t.Fatal("纯 data chunk 中的 response.completed 应被识别")
	}
}

func TestRelayStreamBuffersSplitResponsesJSON(t *testing.T) {
	c, recorder := newStreamTestContext()
	chunks := make(chan cliproxyexecutor.StreamChunk, 2)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.created"`)}
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`,"response":{"id":"resp-1"}}`)}
	close(chunks)

	result := (&Bridge{}).relayStream(context.Background(), c, &cliproxyexecutor.StreamResult{Chunks: chunks}, time.Now(), adaptor.EndpointResponses)

	want := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-1\"}}\n\n"
	if got := recorder.Body.String(); got != want {
		t.Fatalf("被拆分的 JSON 应重组为一个事件，实际：%q，期望：%q", got, want)
	}
	if !result.Written {
		t.Fatal("重组后的有效事件应写入客户端")
	}
}

func TestRelayStreamPreservesCompleteResponsesFrame(t *testing.T) {
	c, recorder := newStreamTestContext()
	chunks := make(chan cliproxyexecutor.StreamChunk, 1)
	frame := []byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\"}}\n\n")
	chunks <- cliproxyexecutor.StreamChunk{Payload: frame}
	close(chunks)

	result := (&Bridge{}).relayStream(context.Background(), c, &cliproxyexecutor.StreamResult{Chunks: chunks}, time.Now(), adaptor.EndpointResponses)

	if got := recorder.Body.String(); got != string(frame) {
		t.Fatalf("完整 SSE 事件不应被改写，实际：%q，期望：%q", got, string(frame))
	}
	if !result.Done {
		t.Fatal("完整的 response.completed 事件应被识别")
	}
}

func newStreamTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return c, recorder
}
