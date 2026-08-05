package cpa

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
)

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

func newStreamTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return c, recorder
}
