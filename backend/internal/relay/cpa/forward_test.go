package cpa

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
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

func TestRefreshableAuthFailure识别认证型403(t *testing.T) {
	if !IsRefreshableAuthFailure("xai", http.StatusForbidden, `{"code":"unauthenticated:bad-credentials","error":"The OAuth2 access token expired"}`) {
		t.Fatal("xAI bad-credentials 403 应触发刷新")
	}
	if !IsRefreshableAuthFailure("antigravity", http.StatusForbidden, `{"status":"UNAUTHENTICATED","message":"invalid token credential"}`) {
		t.Fatal("明确的 UNAUTHENTICATED token 403 应触发刷新")
	}
	if IsRefreshableAuthFailure("claude", http.StatusForbidden, `{"error":"permission_denied"}`) {
		t.Fatal("普通权限不足 403 不应触发刷新")
	}
}

func TestAuthNeedsProactiveRefresh按平台提前量判断(t *testing.T) {
	now := time.Date(2026, 8, 7, 8, 0, 0, 0, time.UTC)
	auth := &coreauth.Auth{Provider: "xai", Metadata: map[string]any{
		"access_token":  "旧令牌",
		"refresh_token": "刷新令牌",
		"expired":       now.Add(4 * time.Minute).Format(time.RFC3339),
	}}
	if !authNeedsProactiveRefresh(auth, now) {
		t.Fatal("xAI 距离过期不足五分钟时应主动刷新")
	}
	auth.Metadata["expired"] = now.Add(30 * time.Minute).Format(time.RFC3339)
	if authNeedsProactiveRefresh(auth, now) {
		t.Fatal("xAI 距离过期较远时不应主动刷新")
	}
}

func TestDoNonStream在认证型403后刷新重试(t *testing.T) {
	executor := &refreshingTestExecutor{}
	auth := &coreauth.Auth{ID: "账号-1", Provider: "xai", Metadata: map[string]any{
		"access_token":  "旧令牌",
		"refresh_token": "旧刷新令牌",
	}}
	result := (&Bridge{}).doNonStream(
		context.Background(),
		executor,
		auth,
		cliproxyexecutor.Request{Model: "grok"},
		cliproxyexecutor.Options{},
	)
	if result.StatusCode != http.StatusOK || executor.executeCalls != 2 || executor.refreshCalls != 1 {
		t.Fatalf("刷新重试结果不符合预期：result=%+v execute=%d refresh=%d", result, executor.executeCalls, executor.refreshCalls)
	}
	if result.RefreshedCredentials["access_token"] != "新令牌" || result.RefreshedCredentials["refresh_token"] != "新刷新令牌" {
		t.Fatalf("刷新后的凭证未回传：%v", result.RefreshedCredentials)
	}
}

func TestNonStreamOK修正残留的SSE内容类型(t *testing.T) {
	result := nonStreamOK(cliproxyexecutor.Response{
		Headers: http.Header{"Content-Type": []string{"text/event-stream; charset=utf-8"}},
		Payload: []byte(`{"id":"msg-1","type":"message","usage":{"input_tokens":12,"output_tokens":3}}`),
	})

	if result.ContentType != "application/json" {
		t.Fatalf("非流式 JSON 内容类型 = %q，期望 application/json", result.ContentType)
	}
	if result.Usage == nil || result.Usage.PromptTokens != 12 || result.Usage.CompletionTokens != 3 {
		t.Fatalf("非流式 Anthropic usage 解析错误：%+v", result.Usage)
	}
}

func TestNonStreamOK保留JSON内容类型(t *testing.T) {
	result := nonStreamOK(cliproxyexecutor.Response{
		Headers: http.Header{"Content-Type": []string{"application/problem+json"}},
		Payload: []byte(`{"type":"message","usage":{"input_tokens":1,"output_tokens":1}}`),
	})

	if result.ContentType != "application/problem+json" {
		t.Fatalf("JSON 内容类型不应被改写，实际为 %q", result.ContentType)
	}
}

func TestRefreshAuth仅有刷新令牌时复用并发结果(t *testing.T) {
	bridge := &Bridge{}
	executor := &refreshingTestExecutor{}
	auth := &coreauth.Auth{ID: "账号-仅RT", Provider: "xai", Metadata: map[string]any{
		"refresh_token": "旧刷新令牌",
	}}
	first, err := bridge.refreshAuth(context.Background(), executor, auth)
	if err != nil {
		t.Fatalf("首次刷新失败: %v", err)
	}
	second, err := bridge.refreshAuth(context.Background(), executor, auth)
	if err != nil {
		t.Fatalf("复用刷新结果失败: %v", err)
	}
	if executor.refreshCalls != 1 {
		t.Fatalf("同一个旧 refresh_token 只能消费一次，实际刷新 %d 次", executor.refreshCalls)
	}
	if authMetadataString(first, "access_token") != "新令牌" || authMetadataString(second, "access_token") != "新令牌" {
		t.Fatalf("复用结果不完整：first=%v second=%v", first.Metadata, second.Metadata)
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

func TestRelayStreamResponses内容前过载可切换(t *testing.T) {
	c, recorder := newStreamTestContext()
	chunks := make(chan cliproxyexecutor.StreamChunk, 2)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-1\"}}\n\n")}
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"service_unavailable_error\",\"code\":\"server_is_overloaded\",\"message\":\"overloaded\"}}\n\n")}
	close(chunks)

	result := (&Bridge{}).relayStream(context.Background(), c,
		&cliproxyexecutor.StreamResult{Chunks: chunks}, time.Now(), adaptor.EndpointResponses)

	if result.StatusCode != http.StatusServiceUnavailable || result.Written || result.NetErr != nil {
		t.Fatalf("内容前过载应返回未写出的 503，result=%+v", result)
	}
	if c.Writer.Written() || recorder.Body.Len() != 0 {
		t.Fatalf("response.created 与错误帧都不应提交客户端，body=%q", recorder.Body.String())
	}
}

func TestRelayStreamAnthropic内容前过载可切换(t *testing.T) {
	c, recorder := newStreamTestContext()
	chunks := make(chan cliproxyexecutor.StreamChunk, 2)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"message_start","message":{"content":[]}}`)}
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`)}
	close(chunks)

	result := (&Bridge{}).relayStream(context.Background(), c,
		&cliproxyexecutor.StreamResult{Chunks: chunks}, time.Now(), adaptor.EndpointMessages)

	if result.StatusCode != http.StatusServiceUnavailable || result.Written {
		t.Fatalf("message_start 后的过载应返回未写出的 503，result=%+v", result)
	}
	if c.Writer.Written() || recorder.Body.Len() != 0 {
		t.Fatalf("内容前事件不应提交客户端，body=%q", recorder.Body.String())
	}
}

func TestRelayStreamAnthropic内容后错误按中断处理(t *testing.T) {
	c, recorder := newStreamTestContext()
	chunks := make(chan cliproxyexecutor.StreamChunk, 2)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"你好"}}`)}
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`)}
	close(chunks)

	result := (&Bridge{}).relayStream(context.Background(), c,
		&cliproxyexecutor.StreamResult{Chunks: chunks}, time.Now(), adaptor.EndpointMessages)

	if !result.Written || result.StreamErr == nil || result.StatusCode != http.StatusOK {
		t.Fatalf("内容后错误应保留当前流并标记中断，result=%+v", result)
	}
	if !strings.Contains(recorder.Body.String(), "overloaded_error") {
		t.Fatalf("内容后的错误帧应透传客户端，body=%q", recorder.Body.String())
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

func TestRelayStreamRecognizesResponsesIncompleteTerminal(t *testing.T) {
	c, _ := newStreamTestContext()
	chunks := make(chan cliproxyexecutor.StreamChunk, 2)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.output_text.delta","delta":"部分输出"}`)}
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":12,"output_tokens":3}}}`)}
	close(chunks)

	result := (&Bridge{}).relayStream(context.Background(), c,
		&cliproxyexecutor.StreamResult{Chunks: chunks}, time.Now(), adaptor.EndpointResponses)

	if !result.Done {
		t.Fatal("response.incomplete 是显式终态，不应误判为断流")
	}
	if result.StreamErr != nil {
		t.Fatalf("显式 incomplete 终态不应产生流错误：%v", result.StreamErr)
	}
	if result.Usage == nil || result.Usage.PromptTokens != 12 || result.Usage.CompletionTokens != 3 {
		t.Fatalf("incomplete 终态 usage 解析错误：%+v", result.Usage)
	}
}

func TestRelayStreamRecognizesChatFinishReasonTerminal(t *testing.T) {
	c, _ := newStreamTestContext()
	chunks := make(chan cliproxyexecutor.StreamChunk, 1)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`)}
	close(chunks)

	result := (&Bridge{}).relayStream(context.Background(), c,
		&cliproxyexecutor.StreamResult{Chunks: chunks}, time.Now(), adaptor.EndpointChatCompletions)

	if !result.Done {
		t.Fatal("带 finish_reason 的 Chat Completions chunk 应视为显式终态")
	}
	if result.StreamErr != nil {
		t.Fatalf("Chat Completions 显式终态不应产生流错误：%v", result.StreamErr)
	}
}

func TestProtocolCompletionIgnoresNullChatFinishReason(t *testing.T) {
	if isProtocolCompletion([]byte(`{"choices":[{"finish_reason":null}]}`)) {
		t.Fatal("finish_reason=null 仍是生成中的普通 chunk，不应判定完成")
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

func TestRelayStream客户端写失败后继续捕获ResponsesUsage(t *testing.T) {
	c, recorder := newStreamTestContext()
	failing := &alwaysFailGinWriter{ResponseWriter: c.Writer}
	c.Writer = failing

	chunks := make(chan cliproxyexecutor.StreamChunk, 2)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.output_text.delta","delta":"你好"}`)}
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.completed","response":{"usage":{"input_tokens":12,"output_tokens":3}}}`)}
	close(chunks)

	result := (&Bridge{}).relayStream(context.Background(), c,
		&cliproxyexecutor.StreamResult{Chunks: chunks}, time.Now(), adaptor.EndpointResponses)

	if result.StreamErr != nil {
		t.Fatalf("客户端写失败不应中断上游排空：%v", result.StreamErr)
	}
	if result.Usage == nil || result.Usage.PromptTokens != 12 || result.Usage.CompletionTokens != 3 {
		t.Fatalf("未捕获最终 usage：%+v", result.Usage)
	}
	if !result.Done {
		t.Fatal("应识别 response.completed 完成事件")
	}
	if failing.writes != 1 {
		t.Fatalf("下游失败后应停止继续写入，实际写入次数：%d", failing.writes)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("模拟断开的客户端不应收到响应体：%q", recorder.Body.String())
	}
}

func TestResponses首字只认内容增量(t *testing.T) {
	if responsesPayloadHasContentDelta([]byte(`data: {"type":"response.created","response":{"output":[]}}`)) {
		t.Fatal("response.created 不应记录首字")
	}
	if !responsesPayloadHasContentDelta([]byte(`data: {"type":"response.output_text.delta","delta":"你好"}`)) {
		t.Fatal("response.output_text.delta 应记录首字")
	}
}

func TestAnthropic首字只认内容增量(t *testing.T) {
	if anthropicPayloadHasContentDelta([]byte(`data: {"type":"message_start","message":{"content":[]}}`)) {
		t.Fatal("message_start 不应记录首字或提交响应头")
	}
	if !anthropicPayloadHasContentDelta([]byte(`data: {"type":"content_block_delta","delta":{"text":"你好"}}`)) {
		t.Fatal("content_block_delta 应记录首字")
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
	chunks := make(chan cliproxyexecutor.StreamChunk, 3)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.created"`)}
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`,"response":{"id":"resp-1"}}`)}
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.completed","response":{"id":"resp-1"}}`)}
	close(chunks)

	result := (&Bridge{}).relayStream(context.Background(), c, &cliproxyexecutor.StreamResult{Chunks: chunks}, time.Now(), adaptor.EndpointResponses)

	want := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-1\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\"}}\n\n"
	if got := recorder.Body.String(); got != want {
		t.Fatalf("被拆分的 JSON 应重组为一个事件，实际：%q，期望：%q", got, want)
	}
	if !result.Written {
		t.Fatal("重组后的生命周期事件应在完成时写入客户端")
	}
	if !result.Done {
		t.Fatal("response.completed 应标记完整流")
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

type alwaysFailGinWriter struct {
	gin.ResponseWriter
	writes int
}

func (w *alwaysFailGinWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, errors.New("客户端已断开")
}

type refreshingTestExecutor struct {
	executeCalls int
	refreshCalls int
}

func (e *refreshingTestExecutor) Identifier() string { return "xai" }

func (e *refreshingTestExecutor) Execute(
	context.Context,
	*coreauth.Auth,
	cliproxyexecutor.Request,
	cliproxyexecutor.Options,
) (cliproxyexecutor.Response, error) {
	e.executeCalls++
	if e.executeCalls == 1 {
		return cliproxyexecutor.Response{}, testHTTPStatusError{
			status: http.StatusForbidden,
			body:   `{"code":"unauthenticated:bad-credentials","error":"OAuth2 access token expired"}`,
		}
	}
	return cliproxyexecutor.Response{Payload: []byte(`{"id":"resp-1"}`)}, nil
}

func (e *refreshingTestExecutor) ExecuteStream(
	context.Context,
	*coreauth.Auth,
	cliproxyexecutor.Request,
	cliproxyexecutor.Options,
) (*cliproxyexecutor.StreamResult, error) {
	return nil, errors.New("未使用")
}

func (e *refreshingTestExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	e.refreshCalls++
	refreshed := auth.Clone()
	refreshed.Metadata["access_token"] = "新令牌"
	refreshed.Metadata["refresh_token"] = "新刷新令牌"
	return refreshed, nil
}

func (e *refreshingTestExecutor) CountTokens(
	context.Context,
	*coreauth.Auth,
	cliproxyexecutor.Request,
	cliproxyexecutor.Options,
) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, errors.New("未使用")
}

func (e *refreshingTestExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, errors.New("未使用")
}

type testHTTPStatusError struct {
	status int
	body   string
}

func (e testHTTPStatusError) Error() string   { return e.body }
func (e testHTTPStatusError) StatusCode() int { return e.status }
