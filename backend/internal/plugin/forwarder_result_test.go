package plugin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// TestMain 在所有并行测试启动前调一次 gin.SetMode，避免 SetMode 内部变量
// 被多个 t.Parallel() goroutine 同时写导致 -race 告警。
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// fakeState 构造测试用的最小 forwardState（只填 writeFailureResponse 读到的字段）。
func fakeState(stream bool) *forwardState {
	return &forwardState{
		stream:  stream,
		plugin:  &PluginInstance{Name: "test-plugin"},
		account: &ent.Account{ID: 1},
	}
}

func TestWriteFailureResponse_StreamBeforeResponseStarts(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	writeFailureResponse(c, fakeState(true), forwardExecution{
		outcome: sdk.ForwardOutcome{Kind: sdk.OutcomeUpstreamTransient, Reason: "boom"},
		err:     errors.New("upstream eof"),
	})

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "上游服务暂不可用") {
		t.Fatalf("body = %q, want contain '上游服务暂不可用'", body)
	}
}

func TestWriteFailureResponse_SanitizesUpstreamBody(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	writeFailureResponse(c, fakeState(false), forwardExecution{
		outcome: sdk.ForwardOutcome{
			Kind: sdk.OutcomeAccountDead,
			Upstream: sdk.UpstreamResponse{
				StatusCode: http.StatusUnauthorized,
				Body:       []byte(`{"error":{"message":"Your authentication token has been invalidated"}}`),
			},
			Reason: "HTTP 401: Your authentication token has been invalidated",
		},
	})

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
	if body := recorder.Body.String(); strings.Contains(body, "authentication token has been invalidated") {
		t.Fatalf("body = %q, want sanitized upstream error", body)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "上游账号不可用") {
		t.Fatalf("body = %q, want contain '上游账号不可用'", body)
	}
}

func TestWriteFailureResponse_StreamAfterResponseStarts(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Status(http.StatusOK)
	c.Writer.WriteHeaderNow()

	writeFailureResponse(c, fakeState(true), forwardExecution{
		outcome: sdk.ForwardOutcome{Kind: sdk.OutcomeStreamAborted},
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if body := recorder.Body.String(); body != "" {
		t.Fatalf("body = %q, want empty", body)
	}
}

func TestWriteClientErrorResponse_StreamBeforeResponseStarts(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	writeClientErrorResponse(c, sdk.ForwardOutcome{
		Kind: sdk.OutcomeClientError,
		Upstream: sdk.UpstreamResponse{
			StatusCode: http.StatusBadRequest,
		},
		Reason: "模型不支持",
	})

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "模型不支持") {
		t.Fatalf("body = %q, want contain '模型不支持'", body)
	}
}

func TestWriteClientErrorResponse_PassesThroughUpstreamBody(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := `{"error":{"message":"模型不存在","type":"invalid_request_error","code":"model_not_found"}}`

	writeClientErrorResponse(c, sdk.ForwardOutcome{
		Kind: sdk.OutcomeClientError,
		Upstream: sdk.UpstreamResponse{
			StatusCode: http.StatusBadRequest,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body:       []byte(body),
		},
		Reason: "HTTP 400: 模型不存在",
	})

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if got := recorder.Body.String(); got != body {
		t.Fatalf("body = %q, want upstream body", got)
	}
}

func TestWriteFailureResponse_NonStreamAlwaysWrites(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	writeFailureResponse(c, fakeState(false), forwardExecution{
		outcome: sdk.ForwardOutcome{Kind: sdk.OutcomeAccountDead, Reason: "token expired"},
	})

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "上游账号不可用") {
		t.Fatalf("body = %q, want contain '上游账号不可用'", body)
	}
}

func TestWriteAllRoutesFailed_SanitizesUpstreamBody(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := `{"error":{"message":"upstream secret request ID 349f8894"}}`
	var summary allRoutesFailureSummary
	summary.recordExecution(forwardExecution{
		outcome: sdk.ForwardOutcome{
			Kind: sdk.OutcomeUpstreamTransient,
			Upstream: sdk.UpstreamResponse{
				StatusCode: http.StatusBadGateway,
				Headers:    http.Header{"Content-Type": []string{"application/json"}},
				Body:       []byte(body),
			},
			Reason: "HTTP 502: upstream secret request ID 349f8894",
		},
	})

	writeAllRoutesFailed(c, summary)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
	got := recorder.Body.String()
	if strings.Contains(got, "upstream secret") || strings.Contains(got, "349f8894") {
		t.Fatalf("body = %q, want sanitized upstream error", got)
	}
	if !strings.Contains(got, "上游服务暂不可用") {
		t.Fatalf("body = %q, want contain sanitized upstream message", got)
	}
}

func TestWriteFailureResponse_RateLimitedReturns429(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	writeFailureResponse(c, fakeState(false), forwardExecution{
		outcome: sdk.ForwardOutcome{Kind: sdk.OutcomeAccountRateLimited},
	})

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTooManyRequests)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "限流") {
		t.Fatalf("body = %q, want contain '限流'", body)
	}
}

func TestSanitizedClientErrorMessage_ImageTooLarge(t *testing.T) {
	t.Parallel()

	outcome := sdk.ForwardOutcome{
		Kind: sdk.OutcomeClientError,
		Upstream: sdk.UpstreamResponse{
			StatusCode: http.StatusRequestEntityTooLarge,
			Body:       []byte(`{"error":{"message":"Request entity too large"}}`),
		},
		Reason: "upstream request entity too large",
	}

	if got := sanitizedClientErrorStatus(outcome); got != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", got, http.StatusRequestEntityTooLarge)
	}
	if got := sanitizedClientErrorMessage(outcome); got != imageTooLargeMessage {
		t.Fatalf("message = %q, want %q", got, imageTooLargeMessage)
	}
}

func TestSanitizedClientErrorMessage_UsesUpstreamMessage(t *testing.T) {
	t.Parallel()

	outcome := sdk.ForwardOutcome{
		Kind: sdk.OutcomeClientError,
		Upstream: sdk.UpstreamResponse{
			StatusCode: http.StatusBadRequest,
			Body:       []byte(`{"error":{"message":"messages 中未找到用户消息"}}`),
		},
		Reason: "image model via chat completions: no user message",
	}

	if got := sanitizedClientErrorStatus(outcome); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", got, http.StatusBadRequest)
	}
	if got := sanitizedClientErrorMessage(outcome); got != "messages 中未找到用户消息" {
		t.Fatalf("message = %q, want upstream message", got)
	}
}

func TestSanitizedClientErrorMessage_DefaultWhenNoMessage(t *testing.T) {
	t.Parallel()

	outcome := sdk.ForwardOutcome{
		Kind:     sdk.OutcomeClientError,
		Upstream: sdk.UpstreamResponse{StatusCode: http.StatusBadRequest},
	}

	if got := sanitizedClientErrorMessage(outcome); got != defaultClientErrorMessage {
		t.Fatalf("message = %q, want %q", got, defaultClientErrorMessage)
	}
}
