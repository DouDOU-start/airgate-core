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
	InputCost         float64 // 输入费用
	OutputCost        float64 // 输出费用
	CachedInputCost   float64 // 缓存读取费用
	CacheCreationCost float64 // 缓存写入费用

	// BillingRate 平台真实计费倍率（已由 ResolveBillingRate 解析过的单值，不再相乘）。
	// 用于扣 reseller 的 user.balance 和写入 actual_cost。
	BillingRate float64

	// SellRate Reseller 设置的销售倍率（>0 启用 markup，与 BillingRate 完全独立）。
	// 用于计算 billed_cost（对客户的账面消耗），累加到 APIKey.used_quota。
	// 平台账户体系永远不读这个字段。
	SellRate float64

	// AccountRate 渠道成本倍率（channel.cost_ratio，采购折扣）。
	// 仅作快照落库（account_rate_multiplier），渠道成本 = total × 快照 由查询期现算。
	AccountRate float64
}

// CalculateResult 计算结果
type CalculateResult struct {
	InputCost             float64 // 输入费用
	OutputCost            float64 // 输出费用
	CachedInputCost       float64 // cached input 费用（cache read）
	CacheCreationCost     float64 // cache creation 费用（cache write）
	TotalCost             float64 // 原始基础成本 = input + cached_input + cache_creation + output（未乘任何倍率）
	ActualCost            float64 // 平台真实扣费（扣 reseller 余额）
	BilledCost            float64 // 客户账面消耗；sell_rate<=0 时回退为 ActualCost
	RateMultiplier        float64 // 快照：本次生效的 BillingRate
	SellRate              float64 // 快照：本次生效的 SellRate
	AccountRateMultiplier float64 // 快照：本次生效的 AccountRate（渠道成本查询期现算）
}

// Calculate 计算费用
//
// 两条落账管道 + 一个成本快照：
//
//	total_cost  = input_cost + cached_input_cost + cache_creation_cost + output_cost
//	actual_cost = total_cost × billing_rate → 扣 User.balance（平台真实计费）
//	billed_cost = total_cost × sell_rate    → 累加 APIKey.used_quota（end customer 可见）；
//	              sell_rate <= 0 时回退为 actual_cost
//
// 渠道成本不再单独落列：account_rate 仅快照进 account_rate_multiplier，
// 需要时按 total_cost × 快照 现算（全路径精确恒等）。
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

	return CalculateResult{
		InputCost:             input.InputCost,
		OutputCost:            input.OutputCost,
		CachedInputCost:       input.CachedInputCost,
		CacheCreationCost:     input.CacheCreationCost,
		TotalCost:             totalCost,
		ActualCost:            actualCost,
		BilledCost:            billedCost,
		RateMultiplier:        billingRate,
		SellRate:              input.SellRate,
		AccountRateMultiplier: accountRate,
	}
}
