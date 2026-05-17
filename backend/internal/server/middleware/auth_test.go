package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newAuthContext(method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	return c, w
}

func TestExtractBearerTokenAndHasAPIKey(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		apiKey        string
		wantToken     string
		wantHasKey    bool
	}{
		{"authorization_bearer", "Bearer sk-test", "", "sk-test", true},
		{"authorization_case_insensitive", "bearer token-123", "", "token-123", true},
		{"authorization_trim_space", "Bearer   token-123  ", "", "token-123", true},
		{"x_api_key_fallback", "", "sk-from-header", "sk-from-header", true},
		{"x_api_key_when_auth_not_bearer", "Basic abc", "sk-from-header", "sk-from-header", true},
		{"missing", "", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newAuthContext(http.MethodGet, "/v1/chat/completions")
			if tt.authorization != "" {
				c.Request.Header.Set("Authorization", tt.authorization)
			}
			if tt.apiKey != "" {
				c.Request.Header.Set("x-api-key", tt.apiKey)
			}

			if got := extractBearerToken(c); got != tt.wantToken {
				t.Fatalf("token = %q，期望 %q", got, tt.wantToken)
			}
			if got := HasAPIKey(c); got != tt.wantHasKey {
				t.Fatalf("HasAPIKey = %v，期望 %v", got, tt.wantHasKey)
			}
		})
	}
}

func TestAdminOnlyRejectsMissingOrNonAdminRole(t *testing.T) {
	tests := []struct {
		name string
		role string
	}{
		{"missing_role", ""},
		{"user_role", "user"},
		{"api_key_role", "api_key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				if tt.role != "" {
					c.Set(CtxKeyRole, tt.role)
				}
			})
			router.Use(AdminOnly())
			router.GET("/admin", func(c *gin.Context) {
				c.String(http.StatusOK, "ok")
			})

			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin", nil))

			if w.Code != http.StatusForbidden {
				t.Fatalf("状态码 = %d，期望 %d", w.Code, http.StatusForbidden)
			}
		})
	}
}

func TestRequireRoles(t *testing.T) {
	tests := []struct {
		name     string
		role     string
		wantCode int
	}{
		{"missing_role", "", http.StatusForbidden},
		{"api_key_role", "api_key", http.StatusForbidden},
		{"user_role", "user", http.StatusOK},
		{"admin_role", "admin", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				if tt.role != "" {
					c.Set(CtxKeyRole, tt.role)
				}
			})
			router.Use(RequireRoles("admin", "user"))
			router.GET("/account", func(c *gin.Context) {
				c.String(http.StatusOK, "ok")
			})

			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/account", nil))

			if w.Code != tt.wantCode {
				t.Fatalf("状态码 = %d，期望 %d", w.Code, tt.wantCode)
			}
		})
	}
}

func TestAdminOnlyAllowsAdminRole(t *testing.T) {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(CtxKeyRole, "admin")
	})
	router.Use(AdminOnly())
	router.GET("/admin", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 %d", w.Code, http.StatusOK)
	}
}

func TestAbortWithOpenAIError(t *testing.T) {
	c, w := newAuthContext(http.MethodGet, "/v1/models")

	abortWithOpenAIError(c, http.StatusPaymentRequired, "insufficient_quota", "额度不足")

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("状态码 = %d，期望 %d", w.Code, http.StatusPaymentRequired)
	}
	var got map[string]map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("响应 JSON 解析失败: %v", err)
	}
	errBody := got["error"]
	if errBody["message"] != "额度不足" || errBody["type"] != "authentication_error" || errBody["code"] != "insufficient_quota" {
		t.Fatalf("错误响应异常: %#v", errBody)
	}
	if !c.IsAborted() {
		t.Fatal("请求应该被终止")
	}
}
