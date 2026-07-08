package handler

import (
	"testing"

	appgroup "github.com/DouDOU-start/airgate-core/internal/app/group"
)

// TestGroupHandleErrorMapping 分组错误 → HTTP 状态码映射；
// 重点：分组仍绑定渠道时拒绝删除并返回 400 与明确提示（防专属渠道静默变公共）。
func TestGroupHandleErrorMapping(t *testing.T) {
	h := &GroupHandler{}

	cases := []struct {
		name        string
		err         error
		wantCode    int
		wantMessage string
	}{
		{
			name:        "分组不存在 404",
			err:         appgroup.ErrGroupNotFound,
			wantCode:    404,
			wantMessage: appgroup.ErrGroupNotFound.Error(),
		},
		{
			name:        "仍有订阅 400",
			err:         appgroup.ErrGroupHasSubscriptions,
			wantCode:    400,
			wantMessage: appgroup.ErrGroupHasSubscriptions.Error(),
		},
		{
			name:        "仍绑定渠道 400 且带数量提示",
			err:         &appgroup.GroupHasChannelsError{Count: 3},
			wantCode:    400,
			wantMessage: "分组仍绑定 3 个渠道，请先解绑",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, message := h.handleError("log", "public", tc.err)
			if code != tc.wantCode {
				t.Errorf("code = %d, want %d", code, tc.wantCode)
			}
			if message != tc.wantMessage {
				t.Errorf("message = %q, want %q", message, tc.wantMessage)
			}
		})
	}
}
