package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
)

func TestPluginTokenAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const token = "仅用于测试的插件令牌"
	tests := []struct {
		name       string
		remoteAddr string
		token      string
		wantStatus int
	}{
		{name: "IPv4 回环与正确令牌", remoteAddr: "127.0.0.1:43210", token: token, wantStatus: http.StatusNoContent},
		{name: "IPv6 回环与正确令牌", remoteAddr: "[::1]:43210", token: token, wantStatus: http.StatusNoContent},
		{name: "错误令牌", remoteAddr: "127.0.0.1:43210", token: "错误令牌", wantStatus: http.StatusUnauthorized},
		{name: "缺少令牌", remoteAddr: "127.0.0.1:43210", wantStatus: http.StatusUnauthorized},
		{name: "非回环来源", remoteAddr: "192.0.2.10:43210", token: token, wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := gin.New()
			engine.GET("/internal", PluginTokenAuth(token), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodGet, "/internal", nil)
			request.RemoteAddr = tt.remoteAddr
			if tt.token != "" {
				request.Header.Set(protocol.CorePluginTokenHeader, tt.token)
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Fatalf("状态码 = %d，期望 %d，响应 = %s", response.Code, tt.wantStatus, response.Body.String())
			}
		})
	}
}

func TestPluginTokenAuth不信任代理来源头(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/internal", PluginTokenAuth("token"), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/internal", nil)
	request.RemoteAddr = "192.0.2.10:43210"
	request.Header.Set("X-Forwarded-For", "127.0.0.1")
	request.Header.Set(protocol.CorePluginTokenHeader, "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("伪造代理来源头后状态码 = %d，期望 %d", response.Code, http.StatusForbidden)
	}
}
