package handler

import (
	"encoding/json"
	"strings"
	"testing"

	appusage "github.com/DouDOU-start/airgate-core/internal/app/usage"
)

// TestToCustomerUsageLogRespStripsResellerFields end customer 视角映射是防
// reseller 毛利泄漏的关键：只允许 billed_cost 出现，actual/total/单价/倍率/
// 渠道/IP/UA 等平台侧字段一律不得进入序列化结果。
func TestToCustomerUsageLogRespStripsResellerFields(t *testing.T) {
	record := appusage.LogRecord{
		ID:                    42,
		UserID:                7,
		UserEmail:             "leak-user@example.com",
		APIKeyID:              3,
		APIKeyName:            "customer-key",
		ChannelID:             9,
		ChannelName:           "leak-channel-name",
		GroupID:               2,
		Model:                 "gpt-test",
		InputTokens:           100,
		OutputTokens:          50,
		CachedInputTokens:     10,
		CacheCreationTokens:   5,
		CacheCreation5mTokens: 3,
		CacheCreation1hTokens: 2,
		Calls:                 1,
		InputPrice:            111.11,
		OutputPrice:           222.22,
		InputCost:             0.501,
		OutputCost:            0.502,
		TotalCost:             0.503,
		ActualCost:            0.504,
		BilledCost:            1.25,
		RateMultiplier:        1.7,
		SellRate:              2.5,
		AccountRateMultiplier: 0.3,
		ServiceTier:           "flex",
		Stream:                true,
		DurationMs:            1200,
		FirstTokenMs:          80,
		UserAgent:             "leak-user-agent",
		IPAddress:             "203.0.113.7",
		Endpoint:              "/v1/chat/completions",
		RequestID:             "req-1",
		CreatedAt:             "2026-07-10T00:00:00Z",
	}

	resp := toCustomerUsageLogResp(record)

	// 允许保留的字段正确复制
	if resp.ID != 42 || resp.APIKeyID != 3 || resp.Model != "gpt-test" {
		t.Fatalf("基础字段映射异常: %+v", resp)
	}
	if resp.BilledCost != 1.25 {
		t.Fatalf("BilledCost = %v, want 1.25", resp.BilledCost)
	}
	if resp.InputTokens != 100 || resp.OutputTokens != 50 || resp.CachedInputTokens != 10 ||
		resp.CacheCreationTokens != 5 || resp.CacheCreation5mTokens != 3 || resp.CacheCreation1hTokens != 2 {
		t.Fatalf("token 字段映射异常: %+v", resp)
	}
	if resp.ServiceTier != "flex" || !resp.Stream || resp.DurationMs != 1200 || resp.FirstTokenMs != 80 ||
		resp.Endpoint != "/v1/chat/completions" || resp.RequestID != "req-1" || resp.Calls != 1 {
		t.Fatalf("观测字段映射异常: %+v", resp)
	}

	// 序列化结果不得携带任何平台侧敏感值
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, leak := range []string{
		"0.501", "0.502", "0.503", "0.504", // input/output/total/actual cost
		"111.11", "222.22", // 单价
		"1.7", "2.5", "0.3", // rate_multiplier / sell_rate / account_rate_multiplier
		"leak-channel-name", "leak-user-agent", "203.0.113.7", "leak-user@example.com",
		"actual_cost", "total_cost", "sell_rate", "rate_multiplier", "channel", "ip_address", "user_agent",
	} {
		if strings.Contains(body, leak) {
			t.Fatalf("customer 响应泄漏 %q: %s", leak, body)
		}
	}
}

// TestToUserUsageLogRespStripsChannelFields 普通用户视角映射：
// 渠道拓扑对用户不可见（只能看到分组），channel_id/channel_name 与
// 渠道成本倍率快照一律不得进入序列化结果。
func TestToUserUsageLogRespStripsChannelFields(t *testing.T) {
	record := appusage.LogRecord{
		ID:                    42,
		UserID:                7,
		ChannelID:             9,
		ChannelName:           "leak-channel-name",
		GroupID:               2,
		Model:                 "gpt-test",
		AccountRateMultiplier: 0.8,
	}

	data, err := json.Marshal(toUserUsageLogResp(record))
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	body := string(data)

	for _, forbidden := range []string{"channel_id", "channel_name", "leak-channel-name", "account_rate_multiplier"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("用户视角响应泄漏字段 %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"group_id":2`) {
		t.Fatalf("用户视角应保留分组字段: %s", body)
	}
}
