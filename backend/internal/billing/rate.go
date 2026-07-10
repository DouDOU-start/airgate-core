package billing

import "github.com/DouDOU-start/airgate-core/internal/auth"

// ResolveBillingRate 决定一次请求该用什么倍率扣 reseller 的真实成本（actual_cost）。
//
// 优先级链（高于者赢）：
//  1. user.group_rates[group_id]   — 用户级专属调价（一对一谈价）
//  2. tier.rates[group_id]         — 用户等级批量调价（VIP 分层）
//  3. group.rate_multiplier        — 分组档位
//  4. 1.0                          — 默认
//
// 注意：
//   - APIKey.sell_rate 不在这条链里。它是 reseller 对最终客户的"账面"售价，
//     与平台真实计费完全独立，由 Calculator 单独处理 BilledCost。
//   - Channel.cost_ratio 不在这条链里。它是渠道成本统计倍率（account_cost 列），
//     由 Calculator 经 AccountRate 单独计算，不影响用户扣费。
func ResolveBillingRate(keyInfo *auth.APIKeyInfo) float64 {
	if keyInfo == nil {
		return 1.0
	}
	return ResolveBillingRateForGroup(keyInfo.UserGroupRates, keyInfo.TierGroupRates, keyInfo.GroupID, keyInfo.GroupRateMultiplier)
}

// ExceedsKeyMaxRate 判断实际扣费倍率是否超出密钥设置的最高倍率（max_rate>0 时启用）。
// 超出时转发预检拒绝请求而非按新倍率静默扣费——管理员临时调价的保护闸，
// pipeline 与 task 两条转发链路共用的唯一判定点。
func ExceedsKeyMaxRate(keyInfo *auth.APIKeyInfo, rate float64) bool {
	return keyInfo != nil && keyInfo.MaxRate > 0 && rate > keyInfo.MaxRate
}

// ResolveBillingRateForGroup 按指定 group 计算实际扣费倍率。
func ResolveBillingRateForGroup(userGroupRates, tierGroupRates map[int64]float64, groupID int, groupRate float64) float64 {
	if r, ok := userGroupRates[int64(groupID)]; ok && r > 0 {
		return r
	}
	if r, ok := tierGroupRates[int64(groupID)]; ok && r > 0 {
		return r
	}
	if groupRate > 0 {
		return groupRate
	}
	return 1.0
}
