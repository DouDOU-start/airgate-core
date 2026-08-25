package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{"", "zh"},
		{"en-US,en;q=0.8,zh;q=0.5", "en"},
		{"zh-CN,zh;q=0.9,en;q=0.1", "zh"},
		{"fr-FR,en;q=0.9", "en"},
		{"fr-FR", "zh"},
	}

	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			if got := detectLanguage(tt.header); got != tt.want {
				t.Fatalf("语言 = %q，期望 %q", got, tt.want)
			}
		})
	}
}

func TestI18nMiddlewareStoresLanguage(t *testing.T) {
	router := gin.New()
	router.Use(I18n())
	router.GET("/lang", func(c *gin.Context) {
		c.String(http.StatusOK, c.GetString(CtxKeyLang))
	})

	req := httptest.NewRequest(http.MethodGet, "/lang", nil)
	req.Header.Set("Accept-Language", "en-US,en;q=0.8")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "en" {
		t.Fatalf("语言 = %q，期望 en", w.Body.String())
	}
}
