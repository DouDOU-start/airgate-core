package plugin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"entgo.io/ent/dialect/sql/schema"
	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/enttest"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
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
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
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

func TestWriteResultRecordsUsageForStreamAborted(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:forwarder_stream_aborted_usage?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()

	ctx := context.Background()
	user := db.User.Create().
		SetEmail("stream-aborted@example.com").
		SetPasswordHash("secret").
		SaveX(ctx)
	group := db.Group.Create().
		SetName("OpenAI").
		SetPlatform("openai").
		SaveX(ctx)
	account := db.Account.Create().
		SetName("acc").
		SetPlatform("openai").
		SaveX(ctx)
	key := db.APIKey.Create().
		SetName("key").
		SetKeyHash("hash").
		SetUserID(user.ID).
		SetGroupID(group.ID).
		SaveX(ctx)

	usageRecorder := billing.NewRecorder(db, 0)
	usageRecorder.Start()
	t.Cleanup(usageRecorder.Stop)

	f := &Forwarder{
		scheduler:  scheduler.NewScheduler(db, nil),
		calculator: billing.NewCalculator(),
		recorder:   usageRecorder,
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Status(http.StatusOK)
	c.Writer.WriteHeaderNow()

	f.writeResult(c, &forwardState{
		stream:      true,
		requestPath: "/v1/responses",
		model:       "gpt-5.4",
		plugin:      &PluginInstance{Name: "openai", Platform: "openai"},
		account:     account,
		keyInfo: &auth.APIKeyInfo{
			KeyID:               key.ID,
			UserID:              user.ID,
			UserEmail:           user.Email,
			GroupID:             group.ID,
			GroupRateMultiplier: 1,
			UserBalance:         100,
		},
	}, forwardExecution{
		outcome: sdk.ForwardOutcome{
			Kind: sdk.OutcomeStreamAborted,
			Usage: &sdk.Usage{
				Model:    "gpt-5.4",
				Currency: "USD",
				Metrics: []sdk.UsageMetric{{
					Key:         "images",
					Label:       "图片数量",
					Kind:        "image",
					Unit:        "image",
					Value:       3,
					AccountCost: 0.03,
					Currency:    "USD",
				}},
				CostDetails: []sdk.UsageCostDetail{{
					Key:         "image_tool",
					Label:       "图片生成",
					AccountCost: 0.03,
					Currency:    "USD",
					Metadata:    map[string]string{"image_count": "3", "size": "1024x1024"},
				}},
			},
		},
	})
	usageRecorder.Stop()

	logs, err := db.UsageLog.Query().All(ctx)
	if err != nil {
		t.Fatalf("query usage logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("usage log count = %d, want 1", len(logs))
	}
	if logs[0].ImageCost <= 0 {
		t.Fatalf("image_cost = %v, want > 0", logs[0].ImageCost)
	}
	if len(logs[0].UsageMetrics) != 1 || logs[0].UsageMetrics[0].Key != "images" || logs[0].UsageMetrics[0].Value != 3 {
		t.Fatalf("usage metrics = %+v, want images=3", logs[0].UsageMetrics)
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
