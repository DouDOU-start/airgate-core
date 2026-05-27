// Package billing 提供费用计算和使用量异步记录
package billing

// Calculator 费用计算器
type Calculator struct{}

// NewCalculator 创建费用计算器
func NewCalculator() *Calculator {
	return &Calculator{}
}

// CalculateInput 计算输入参数
type CalculateInput struct {
	InputCost         float64 // 插件已计算的输入费用
	OutputCost        float64 // 插件已计算的输出费用
	CachedInputCost   float64 // 插件已计算的缓存读取费用
	CacheCreationCost float64 // 插件已计算的缓存写入费用

	// BillingRate 平台真实计费倍率（已由 ResolveBillingRate 解析过的单值，不再相乘）。
	// 用于扣 reseller 的 user.balance 和写入 actual_cost。
	BillingRate float64

	// SellRate Reseller 设置的销售倍率（>0 启用 markup，与 BillingRate 完全独立）。
	// 用于计算 billed_cost（对客户的账面消耗），累加到 APIKey.used_quota。
	// 平台账户体系永远不读这个字段。
	SellRate float64

	// AccountRate 账号实际成本倍率（账号自身相对上游的成本系数，比如代购账号 1.2x）。
	// 用于计算 account_cost（账号实际消耗），写入 usage_log，仅供"账号计费"统计使用。
	// 与用户计费 (BillingRate) 完全独立，不影响 actual_cost / User.balance。
	AccountRate float64

	// BillingCostOverride 可覆盖 actual_cost / billed_cost 的最终计费结果。
	// 用于分组图片 1K/2K/4K 固定价：配置后整次请求按图片单张价 × 数量计费，
	// 不再叠加 token × BillingRate / SellRate 的结果。
	BillingCostOverride *float64
}

// CalculateResult 计算结果
type CalculateResult struct {
	InputCost             float64 // 输入费用
	OutputCost            float64 // 输出费用
	CachedInputCost       float64 // cached input 费用（cache read）
	CacheCreationCost     float64 // cache creation 费用（cache write）
	TotalCost             float64 // 原始基础成本 = input + cached_input + cache_creation + output（未乘任何倍率）
	ActualCost            float64 // 平台真实成本 = TotalCost × BillingRate（扣 reseller 余额）
	BilledCost            float64 // 客户账面消耗 = TotalCost × SellRate（sell_rate=0 时回退为 ActualCost）
	AccountCost           float64 // 账号实际成本 = TotalCost × AccountRate（仅服务于"账号计费"统计）
	RateMultiplier        float64 // 快照：本次生效的 BillingRate
	SellRate              float64 // 快照：本次生效的 SellRate
	AccountRateMultiplier float64 // 快照：本次生效的 AccountRate
}

// Calculate 计算费用
//
// 三条独立管道：
//
//	total_cost   = input_cost + cached_input_cost + output_cost
//
//	actual_cost  = total_cost × billing_rate          → 扣 User.balance（平台真实计费）
//	billed_cost  = total_cost × sell_rate             → 累加 APIKey.used_quota（end customer 可见）
//	             或 actual_cost（sell_rate <= 0 时回退）
//	account_cost = total_cost × account_rate          → 写入 usage_log，仅服务"账号计费"统计
//
// 三者互不影响，各自存储在独立列里。
func (c *Calculator) Calculate(input CalculateInput) CalculateResult {
	totalCost := input.InputCost + input.OutputCost + input.CachedInputCost + input.CacheCreationCost

	billingRate := input.BillingRate
	if billingRate <= 0 {
		billingRate = 1.0
	}
	accountRate := input.AccountRate
	if accountRate <= 0 {
		accountRate = 1.0
	}

	actualCost := totalCost * billingRate

	billedCost := actualCost
	if input.SellRate > 0 {
		billedCost = totalCost * input.SellRate
	}
	if input.BillingCostOverride != nil {
		actualCost = *input.BillingCostOverride
		billedCost = *input.BillingCostOverride
	}

	accountCost := totalCost * accountRate

	return CalculateResult{
		InputCost:             input.InputCost,
		OutputCost:            input.OutputCost,
		CachedInputCost:       input.CachedInputCost,
		CacheCreationCost:     input.CacheCreationCost,
		TotalCost:             totalCost,
		ActualCost:            actualCost,
		BilledCost:            billedCost,
		AccountCost:           accountCost,
		RateMultiplier:        billingRate,
		SellRate:              input.SellRate,
		AccountRateMultiplier: accountRate,
	}
}
