package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

func TestRequestLoggerPropagatesRequestID(t *testing.T) {
	router := gin.New()
	router.Use(RequestLogger())
	router.GET("/ping", func(c *gin.Context) {
		if got := RequestIDFromGinContext(c); got != "req-123" {
			t.Fatalf("上下文 request_id = %q，期望 req-123", got)
		}
		c.String(http.StatusCreated, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(sdk.HeaderRequestID, "req-123")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("状态码 = %d，期望 %d", w.Code, http.StatusCreated)
	}
	if got := w.Header().Get(sdk.HeaderRequestID); got != "req-123" {
		t.Fatalf("响应 request_id = %q，期望 req-123", got)
	}
}

func TestRequestIDFromGinContextHandlesNil(t *testing.T) {
	if got := RequestIDFromGinContext(nil); got != "" {
		t.Fatalf("nil context request_id = %q，期望空字符串", got)
	}
}

func TestRecoveryReturnsJSONWithRequestID(t *testing.T) {
	router := gin.New()
	router.Use(RequestLogger(), Recovery())
	router.GET("/panic", func(c *gin.Context) {
		panic("测试 panic")
	})

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	req.Header.Set(sdk.HeaderRequestID, "panic-req")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d，期望 %d", w.Code, http.StatusInternalServerError)
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("响应 JSON 解析失败: %v", err)
	}
	if got["error"] != "internal_server_error" || got["request_id"] != "panic-req" {
		t.Fatalf("panic 响应异常: %#v", got)
	}
}
