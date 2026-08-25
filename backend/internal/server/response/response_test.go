package response

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func decodeResponse(t *testing.T, body string) R {
	t.Helper()
	var got R
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("响应 JSON 解析失败: %v", err)
	}
	return got
}

func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)
	return c, w
}

func TestSuccessWritesUnifiedJSON(t *testing.T) {
	c, w := newTestContext()

	Success(c, gin.H{"name": "核心"})

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 %d", w.Code, http.StatusOK)
	}
	got := decodeResponse(t, w.Body.String())
	if got.Code != 0 || got.Message != "ok" {
		t.Fatalf("响应头部字段异常: %+v", got)
	}
	data, ok := got.Data.(map[string]interface{})
	if !ok || data["name"] != "核心" {
		t.Fatalf("响应 data = %#v，期望包含 name", got.Data)
	}
}

func TestErrorHelpersWriteExpectedStatusAndCode(t *testing.T) {
	tests := []struct {
		name       string
		call       func(*gin.Context)
		httpStatus int
		code       int
		message    string
	}{
		{"bad_request", func(c *gin.Context) { BadRequest(c, "参数错误") }, http.StatusBadRequest, 400, "参数错误"},
		{"unauthorized", func(c *gin.Context) { Unauthorized(c, "未登录") }, http.StatusUnauthorized, 401, "未登录"},
		{"forbidden", func(c *gin.Context) { Forbidden(c, "无权限") }, http.StatusForbidden, 403, "无权限"},
		{"not_found", func(c *gin.Context) { NotFound(c, "不存在") }, http.StatusNotFound, 404, "不存在"},
		{"internal_error", func(c *gin.Context) { InternalError(c, "内部错误") }, http.StatusInternalServerError, 500, "内部错误"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := newTestContext()
			tt.call(c)

			if w.Code != tt.httpStatus {
				t.Fatalf("状态码 = %d，期望 %d", w.Code, tt.httpStatus)
			}
			got := decodeResponse(t, w.Body.String())
			if got.Code != tt.code || got.Message != tt.message {
				t.Fatalf("响应 = %+v，期望 code=%d message=%q", got, tt.code, tt.message)
			}
		})
	}
}

func TestBindErrorHidesInternalError(t *testing.T) {
	c, w := newTestContext()

	BindError(c, errors.New("原始错误不应该返回给客户端"))

	got := decodeResponse(t, w.Body.String())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 %d", w.Code, http.StatusBadRequest)
	}
	if got.Message != "请求参数格式不正确，请检查输入" {
		t.Fatalf("消息 = %q，期望友好提示", got.Message)
	}
}

func TestPagedDataKeepsPaginationFields(t *testing.T) {
	data := PagedData([]string{"a", "b"}, 9, 2, 5)

	if data["total"] != int64(9) || data["page"] != 2 || data["page_size"] != 5 {
		t.Fatalf("分页字段异常: %#v", data)
	}
	if list, ok := data["list"].([]string); !ok || len(list) != 2 {
		t.Fatalf("列表字段异常: %#v", data["list"])
	}
}
