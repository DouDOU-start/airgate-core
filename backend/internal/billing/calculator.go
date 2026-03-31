// Package billing 提供费用计算、模型价格管理和使用量异步记录
package billing

// Calculator 费用计算器
type Calculator struct{}

// NewCalculator 创建费用计算器
func NewCalculator() *Calculator {
	return &Calculator{}
}

// CalculateInput 计算输入参数
type CalculateInput struct {
	InputTokens       int // 输入 token 数量
	OutputTokens      int // 输出 token 数量
	CachedInputTokens int // 命中缓存的输入 token 数量
	ServiceTier       string
	Model             string
	Platform          string

	// 三层倍率
	GroupRateMultiplier   float64 // 分组倍率
	AccountRateMultiplier float64 // 账号倍率
	UserRateMultiplier    float64 // 用户自定义倍率（group_rates 中的值）
}

// CalculateResult 计算结果
type CalculateResult struct {
	InputCost             float64 // 输入 token 费用
	OutputCost            float64 // 输出 token 费用
	CachedInputCost       float64 // cached input token 费用
	TotalCost             float64 // 原始成本 = input + cached_input + output
	ActualCost            float64 // 最终计费 = TotalCost * group * account * user
	RateMultiplier        float64 // 最终综合倍率
	AccountRateMultiplier float64 // 账号倍率
}

// Calculate 计算费用
// 公式：
//
//	input_cost        = input_tokens * input_price
//	cached_input_cost = cached_input_tokens * cached_input_price
//	output_cost       = output_tokens * output_price
//	total_cost        = input_cost + cached_input_cost + output_cost
//	actual_cost       = total_cost * group_rate * account_rate * user_rate
func (c *Calculator) Calculate(input CalculateInput, price ModelPrice) CalculateResult {
	inputPrice := price.InputPerToken
	outputPrice := price.OutputPerToken
	cachedInputPrice := price.CachedInputPerToken
	if input.ServiceTier == "priority" {
		if price.InputPerTokenPriority > 0 {
			inputPrice = price.InputPerTokenPriority
		}
		if price.OutputPerTokenPriority > 0 {
			outputPrice = price.OutputPerTokenPriority
		}
		if price.CachedInputPerTokenPriority > 0 {
			cachedInputPrice = price.CachedInputPerTokenPriority
		}
	}

	inputCost := float64(input.InputTokens) * inputPrice
	outputCost := float64(input.OutputTokens) * outputPrice
	cachedInputCost := float64(input.CachedInputTokens) * cachedInputPrice
	totalCost := inputCost + outputCost + cachedInputCost

	// 倍率默认为 1.0
	groupRate := input.GroupRateMultiplier
	if groupRate <= 0 {
		groupRate = 1.0
	}
	accountRate := input.AccountRateMultiplier
	if accountRate <= 0 {
		accountRate = 1.0
	}
	userRate := input.UserRateMultiplier
	if userRate <= 0 {
		userRate = 1.0
	}

	combinedRate := groupRate * accountRate * userRate
	actualCost := totalCost * combinedRate

	return CalculateResult{
		InputCost:             inputCost,
		OutputCost:            outputCost,
		CachedInputCost:       cachedInputCost,
		TotalCost:             totalCost,
		ActualCost:            actualCost,
		RateMultiplier:        combinedRate,
		AccountRateMultiplier: accountRate,
	}
}
