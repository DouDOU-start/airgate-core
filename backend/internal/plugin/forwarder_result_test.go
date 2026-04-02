package plugin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMaybeWriteForwardError_StreamBeforeResponseStarts(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	maybeWriteForwardError(c, &forwardState{stream: true}, forwardExecution{
		err: errors.New("upstream eof"),
	})

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "插件转发失败") {
		t.Fatalf("body = %q, want contain 插件转发失败", body)
	}
}

func TestMaybeWriteForwardError_StreamAfterResponseStarts(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Status(http.StatusOK)
	c.Writer.WriteHeaderNow()

	maybeWriteForwardError(c, &forwardState{stream: true}, forwardExecution{
		err: errors.New("upstream eof"),
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if body := recorder.Body.String(); body != "" {
		t.Fatalf("body = %q, want empty", body)
	}
}

func TestMaybeWriteForwardError_NonStreamAlwaysWrites(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	maybeWriteForwardError(c, &forwardState{stream: false}, forwardExecution{
		err: errors.New("upstream eof"),
	})

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
}
