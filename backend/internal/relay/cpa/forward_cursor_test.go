package cpa

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
)

type firstWriteWriter struct {
	gin.ResponseWriter
	onWrite chan<- struct{}
	once    sync.Once
}

func (w *firstWriteWriter) Write(payload []byte) (int, error) {
	w.once.Do(func() { close(w.onWrite) })
	return w.ResponseWriter.Write(payload)
}

func TestRelayStreamCursor立即提交MessageStart(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	written := make(chan struct{})
	c.Writer = &firstWriteWriter{ResponseWriter: c.Writer, onWrite: written}

	chunks := make(chan cliproxyexecutor.StreamChunk, 1)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"message_start","message":{"content":[]}}`)}
	resultCh := make(chan ForwardResult, 1)
	go func() {
		resultCh <- (&Bridge{}).relayStreamSinceWithModelRewrite(
			context.Background(), c, &cliproxyexecutor.StreamResult{Chunks: chunks}, time.Now(), time.Time{},
			adaptor.EndpointMessages, "cursor", responseModelRewrite{},
		)
	}()

	select {
	case <-written:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("Cursor message_start 未在正文前提交")
	}

	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"你好"}}`)}
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"message_stop"}`)}
	close(chunks)
	result := <-resultCh
	if !result.Written || !result.Done || result.StreamErr != nil {
		t.Fatalf("Cursor 生命周期首帧提前提交后流应正常结束: %+v", result)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "message_start") || !strings.Contains(body, "content_block_delta") {
		t.Fatalf("响应缺少必要事件: %q", body)
	}
}

func TestAnthropicPayloadHasMessageStart(t *testing.T) {
	if anthropicPayloadHasMessageStart([]byte(`data: {"type":"content_block_delta"}`)) {
		t.Fatal("content_block_delta 不应识别为 message_start")
	}
	if !anthropicPayloadHasMessageStart([]byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n")) {
		t.Fatal("应识别 Anthropic message_start")
	}
}

func TestRelayStream非Cursor仍缓冲MessageStart以保留Failover(t *testing.T) {
	c, recorder := newStreamTestContext()
	chunks := make(chan cliproxyexecutor.StreamChunk, 2)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"message_start","message":{"content":[]}}`)}
	chunks <- cliproxyexecutor.StreamChunk{Err: errors.New("上游失败")}
	close(chunks)
	result := (&Bridge{}).relayStreamSinceWithModelRewrite(
		context.Background(), c, &cliproxyexecutor.StreamResult{Chunks: chunks}, time.Now(), time.Time{},
		adaptor.EndpointMessages, "claude", responseModelRewrite{},
	)
	if result.Written || result.NetErr == nil || recorder.Body.Len() != 0 {
		t.Fatalf("非 Cursor message_start 仍应保留整体 failover: result=%+v body=%q", result, recorder.Body.String())
	}
}
