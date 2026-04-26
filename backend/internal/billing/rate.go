package billing

import "github.com/DouDOU-start/airgate-core/internal/auth"

// ResolveBillingRate 决定一次请求该用什么倍率扣 reseller 的真实成本（actual_cost）。
//
// 优先级链（高于者赢）：
//  1. user.group_rates[group_id]   — 用户级专属调价（VIP/折扣）
//  2. group.rate_multiplier        — 分组档位
//  3. 1.0                          — 默认
//
// 注意：
//   - APIKey.sell_rate 不在这条链里。它是 reseller 对最终客户的"账面"售价，
//     与平台真实计费完全独立，由 Calculator 单独处理 BilledCost。
//   - Account.rate_multiplier 不在这条链里。它只服务于 scheduler 内部 window cost
//     追踪，从用户计费链路完全剥离，调用方需自行计算 windowCost = base × accountRate。
func ResolveBillingRate(keyInfo *auth.APIKeyInfo) float64 {
	if keyInfo == nil {
		return 1.0
	}
	return ResolveBillingRateForGroup(keyInfo.UserGroupRates, keyInfo.GroupID, keyInfo.GroupRateMultiplier)
}

// ResolveBillingRateForGroup 按指定 group 计算实际扣费倍率。
func ResolveBillingRateForGroup(userGroupRates map[int64]float64, groupID int, groupRate float64) float64 {
	if userGroupRates != nil {
		if r, ok := userGroupRates[int64(groupID)]; ok && r > 0 {
			return r
		}
	}
	if groupRate > 0 {
		return groupRate
	}
	return 1.0
}
