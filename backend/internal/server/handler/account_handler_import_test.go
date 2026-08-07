package handler

import (
	"fmt"
	"testing"
)

func TestHandleImportError映射上游状态(t *testing.T) {
	handler := &AccountHandler{}
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name:       "上游凭证错误不触发管理员登出",
			err:        fmt.Errorf("Antigravity 换票失败: %w", importStatusError{code: 401}),
			wantStatus: 400,
		},
		{
			name:       "上游服务错误映射网关错误",
			err:        fmt.Errorf("Antigravity 换票失败: %w", importStatusError{code: 503}),
			wantStatus: 502,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, message := handler.handleImportError("测试导入失败", tt.err)
			if status != tt.wantStatus || message != tt.err.Error() {
				t.Fatalf("status=%d message=%q，期望 status=%d message=%q", status, message, tt.wantStatus, tt.err.Error())
			}
		})
	}
}

type importStatusError struct {
	code int
}

func (e importStatusError) Error() string   { return "上游凭证或服务错误" }
func (e importStatusError) StatusCode() int { return e.code }
