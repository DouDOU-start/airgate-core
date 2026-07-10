package dto

import (
	"testing"

	"github.com/gin-gonic/gin/binding"
)

// TestAPIKeySellRateBinding sell_rate 非负校验：负数须在 dto 绑定层被拒（干净 400），
// 不允许穿透到 ent 层的 Min(0) 校验报内部错误。
func TestAPIKeySellRateBinding(t *testing.T) {
	neg := -0.5
	zero := 0.0

	cases := []struct {
		name    string
		req     any
		wantErr bool
	}{
		{"create 负 sell_rate 拒绝", &CreateAPIKeyReq{Name: "k", GroupID: 1, SellRate: -1}, true},
		{"create 零 sell_rate 放行", &CreateAPIKeyReq{Name: "k", GroupID: 1}, false},
		{"create 正 sell_rate 放行", &CreateAPIKeyReq{Name: "k", GroupID: 1, SellRate: 1.2}, false},
		{"update 负 sell_rate 拒绝", &UpdateAPIKeyReq{SellRate: &neg}, true},
		{"update 零 sell_rate 放行", &UpdateAPIKeyReq{SellRate: &zero}, false},
		{"update 未传 sell_rate 放行", &UpdateAPIKeyReq{}, false},
		{"create 负 max_rate 拒绝", &CreateAPIKeyReq{Name: "k", GroupID: 1, MaxRate: -0.1}, true},
		{"create 正 max_rate 放行", &CreateAPIKeyReq{Name: "k", GroupID: 1, MaxRate: 0.2}, false},
		{"update 负 max_rate 拒绝", &UpdateAPIKeyReq{MaxRate: &neg}, true},
		{"update 零 max_rate 放行", &UpdateAPIKeyReq{MaxRate: &zero}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := binding.Validator.ValidateStruct(tc.req)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateStruct err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}
