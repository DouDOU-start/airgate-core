package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	apphealthmon "github.com/DouDOU-start/airgate-core/internal/app/healthmon"
	coreauth "github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

type channelStatusTestRepository struct {
	apphealthmon.Repository
	visibleUserID int
}

func (r *channelStatusTestRepository) ListUserVisibleGroupMeta(_ context.Context, userID int) ([]apphealthmon.GroupMeta, error) {
	r.visibleUserID = userID
	return []apphealthmon.GroupMeta{{ID: 17, Name: "Visible Group", Platform: "openai"}}, nil
}

func (r *channelStatusTestRepository) AggregateSuccessByGroupIDs(context.Context, time.Time, []int) ([]apphealthmon.SuccessAgg, error) {
	return []apphealthmon.SuccessAgg{{
		DimID:       17,
		Count:       10,
		AvgDuration: 1200,
		MaxDuration: 9000,
		AvgTTFT:     450,
		MaxTTFT:     7000,
	}}, nil
}

func (r *channelStatusTestRepository) AggregateFailureRawsByGroupIDs(context.Context, time.Time, []int) ([]apphealthmon.FailureRaw, error) {
	return nil, nil
}

func newChannelStatusTestRouter(repo apphealthmon.Repository, jwtManager *coreauth.JWTManager) *gin.Engine {
	gin.SetMode(gin.TestMode)
	service := apphealthmon.NewService(repo)
	handler := NewHealthmonHandler(service)
	router := gin.New()
	api := router.Group("/api/v1")
	api.Use(middleware.JWTAuth(jwtManager))
	account := api.Group("")
	account.Use(middleware.RequireRoles("admin", "user"))
	account.GET("/channel-status/overview", handler.UserOverview)
	account.GET("/channel-status/groups", handler.UserGroups)
	return router
}

func TestChannelStatusRoutesRequireFullUserSession(t *testing.T) {
	repo := &channelStatusTestRepository{}
	jwtManager := coreauth.NewJWTManager("channel-status-test-secret", 1)
	router := newChannelStatusTestRouter(repo, jwtManager)

	userToken, err := jwtManager.GenerateToken(42, "user", "user@example.com")
	if err != nil {
		t.Fatalf("generate user token: %v", err)
	}
	adminToken, err := jwtManager.GenerateToken(7, "admin", "admin@example.com")
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}
	customerToken, err := jwtManager.GenerateToken(42, "customer", "customer@example.com")
	if err != nil {
		t.Fatalf("generate customer token: %v", err)
	}
	apiKeyToken, err := jwtManager.GenerateAPIKeyToken(42, "user", "user@example.com", 99)
	if err != nil {
		t.Fatalf("generate API-key session token: %v", err)
	}

	tests := []struct {
		name     string
		token    string
		wantCode int
		wantUser int
	}{
		{name: "missing JWT", wantCode: http.StatusUnauthorized},
		{name: "API-key session", token: apiKeyToken, wantCode: http.StatusForbidden},
		{name: "customer role", token: customerToken, wantCode: http.StatusForbidden},
		{name: "full user", token: userToken, wantCode: http.StatusOK, wantUser: 42},
		{name: "admin user", token: adminToken, wantCode: http.StatusOK, wantUser: 7},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo.visibleUserID = 0
			req := httptest.NewRequest(http.MethodGet, "/api/v1/channel-status/groups?window=1h", nil)
			if test.token != "" {
				req.Header.Set("Authorization", "Bearer "+test.token)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != test.wantCode {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, test.wantCode, rec.Body.String())
			}
			if test.wantCode == http.StatusOK && repo.visibleUserID != test.wantUser {
				t.Fatalf("repository user = %d, want %d", repo.visibleUserID, test.wantUser)
			}
			if test.wantCode != http.StatusOK && repo.visibleUserID != 0 {
				t.Fatalf("rejected request reached repository with user %d", repo.visibleUserID)
			}
		})
	}
}

func TestChannelStatusResponsesExposeOnlyPublicFields(t *testing.T) {
	repo := &channelStatusTestRepository{}
	jwtManager := coreauth.NewJWTManager("channel-status-test-secret", 1)
	router := newChannelStatusTestRouter(repo, jwtManager)
	token, err := jwtManager.GenerateToken(42, "user", "user@example.com")
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	t.Run("overview", func(t *testing.T) {
		data := requestChannelStatusObject(t, router, token, "/api/v1/channel-status/overview?window=6h")
		assertChannelStatusJSONKeys(t, data,
			"window", "sample", "success_rate", "error_rate", "latency", "ttft", "health_score", "updated_at")
		assertChannelStatusJSONKeys(t, mustChannelStatusObject(t, data["sample"]), "idle", "low_sample")
		assertChannelStatusJSONKeys(t, mustChannelStatusObject(t, data["latency"]), "avg_ms")
		assertChannelStatusJSONKeys(t, mustChannelStatusObject(t, data["ttft"]), "avg_ms")
	})

	t.Run("groups", func(t *testing.T) {
		data := requestChannelStatusArray(t, router, token, "/api/v1/channel-status/groups?window=24h")
		if len(data) != 1 {
			t.Fatalf("len(data) = %d, want 1; data=%v", len(data), data)
		}
		group := data[0]
		assertChannelStatusJSONKeys(t, group,
			"id", "name", "platform", "status", "window", "sample", "success_rate", "error_rate", "latency", "ttft", "health_score")
		assertChannelStatusJSONKeys(t, mustChannelStatusObject(t, group["sample"]), "idle", "low_sample")
		assertChannelStatusJSONKeys(t, mustChannelStatusObject(t, group["latency"]), "avg_ms")
		assertChannelStatusJSONKeys(t, mustChannelStatusObject(t, group["ttft"]), "avg_ms")
	})
}

func requestChannelStatusObject(t *testing.T, router http.Handler, token, path string) map[string]any {
	t.Helper()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	requestChannelStatusJSON(t, router, token, path, &envelope)
	return envelope.Data
}

func requestChannelStatusArray(t *testing.T, router http.Handler, token, path string) []map[string]any {
	t.Helper()
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	requestChannelStatusJSON(t, router, token, path, &envelope)
	return envelope.Data
}

func requestChannelStatusJSON(t *testing.T, router http.Handler, token, path string, target any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
}

func mustChannelStatusObject(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value = %#v, want JSON object", value)
	}
	return object
}

func assertChannelStatusJSONKeys(t *testing.T, object map[string]any, expected ...string) {
	t.Helper()
	actual := make([]string, 0, len(object))
	for key := range object {
		actual = append(actual, key)
	}
	sort.Strings(actual)
	sort.Strings(expected)
	if strings.Join(actual, ",") != strings.Join(expected, ",") {
		t.Fatalf("JSON keys = %v, want exactly %v", actual, expected)
	}
}
