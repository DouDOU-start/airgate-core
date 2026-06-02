package billing

import (
	"math"
	"testing"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

const epsilon = 1e-9

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < epsilon
}

func TestCalculate_NoMarkup(t *testing.T) {
	c := NewCalculator()
	res := c.Calculate(CalculateInput{
		InputCost:       0.6,
		OutputCost:      0.3,
		CachedInputCost: 0.1,
		BillingRate:     0.3,
		SellRate:        0,
		AccountRate:     1.0,
	})

	if !almostEqual(res.TotalCost, 1.0) {
		t.Fatalf("TotalCost = %v, want 1.0", res.TotalCost)
	}
	if !almostEqual(res.ActualCost, 0.3) {
		t.Fatalf("ActualCost = %v, want 0.3", res.ActualCost)
	}
	// sell_rate=0 时 billed_cost 必须等于 actual_cost（向后兼容）
	if !almostEqual(res.BilledCost, res.ActualCost) {
		t.Fatalf("BilledCost = %v, want %v (= ActualCost)", res.BilledCost, res.ActualCost)
	}
	// account_rate=1 时 account_cost == total_cost
	if !almostEqual(res.AccountCost, res.TotalCost) {
		t.Fatalf("AccountCost = %v, want %v (= TotalCost)", res.AccountCost, res.TotalCost)
	}
	if !almostEqual(res.RateMultiplier, 0.3) {
		t.Fatalf("RateMultiplier = %v, want 0.3", res.RateMultiplier)
	}
	if res.SellRate != 0 {
		t.Fatalf("SellRate = %v, want 0", res.SellRate)
	}
}

func TestCalculate_AccountCostIndependent(t *testing.T) {
	// 三条管道完全独立：account_rate 既不影响 actual_cost 也不影响 billed_cost
	c := NewCalculator()
	res := c.Calculate(CalculateInput{
		InputCost:   1.0,
		OutputCost:  1.0,
		BillingRate: 0.3,
		SellRate:    0.6,
		AccountRate: 1.5,
	})

	if !almostEqual(res.ActualCost, 0.6) {
		t.Fatalf("ActualCost = %v, want 0.6 (total × billing_rate)", res.ActualCost)
	}
	if !almostEqual(res.BilledCost, 1.2) {
		t.Fatalf("BilledCost = %v, want 1.2 (total × sell_rate)", res.BilledCost)
	}
	if !almostEqual(res.AccountCost, 3.0) {
		t.Fatalf("AccountCost = %v, want 3.0 (total × account_rate)", res.AccountCost)
	}
	// 三个数字两两独立
	if res.AccountCost == res.ActualCost || res.AccountCost == res.BilledCost {
		t.Fatalf("AccountCost should be independent of actual/billed")
	}
}

func TestCalculate_ZeroAccountRate_DefaultsToOne(t *testing.T) {
	c := NewCalculator()
	res := c.Calculate(CalculateInput{
		InputCost:   2.0,
		BillingRate: 1.0,
		AccountRate: 0,
	})
	if !almostEqual(res.AccountCost, 2.0) {
		t.Fatalf("AccountCost = %v, want 2.0 (account_rate defaults to 1.0)", res.AccountCost)
	}
}

func TestCalculate_WithMarkup(t *testing.T) {
	c := NewCalculator()
	res := c.Calculate(CalculateInput{
		InputCost:       0.6,
		OutputCost:      0.3,
		CachedInputCost: 0.1,
		BillingRate:     0.3,
		SellRate:        0.6, // reseller 卖给客户的倍率
	})

	// 平台真实成本：base × billing_rate
	if !almostEqual(res.ActualCost, 0.3) {
		t.Fatalf("ActualCost = %v, want 0.3", res.ActualCost)
	}
	// 客户账面消耗：base × sell_rate（独立计算，与 billing_rate 无关）
	if !almostEqual(res.BilledCost, 0.6) {
		t.Fatalf("BilledCost = %v, want 0.6", res.BilledCost)
	}
	// 利润 = billed - actual = $0.30
	profit := res.BilledCost - res.ActualCost
	if !almostEqual(profit, 0.3) {
		t.Fatalf("profit = %v, want 0.3", profit)
	}
}

func TestCalculate_ZeroBillingRate_DefaultsToOne(t *testing.T) {
	c := NewCalculator()
	res := c.Calculate(CalculateInput{
		InputCost:   1.0,
		BillingRate: 0, // 应被替换为 1.0
	})
	if !almostEqual(res.ActualCost, 1.0) {
		t.Fatalf("ActualCost = %v, want 1.0", res.ActualCost)
	}
	if !almostEqual(res.RateMultiplier, 1.0) {
		t.Fatalf("RateMultiplier = %v, want 1.0", res.RateMultiplier)
	}
}

func TestCalculate_MarkupIndependentOfBillingRate(t *testing.T) {
	// 关键不变量：sell_rate 完全独立于 billing_rate
	// 改变 billing_rate 不应改变 billed_cost；改变 sell_rate 不应改变 actual_cost。
	c := NewCalculator()

	base := CalculateInput{
		InputCost:   1.0,
		OutputCost:  1.0,
		BillingRate: 0.3,
		SellRate:    0.6,
	}
	res1 := c.Calculate(base)

	// 改变 billing_rate
	base2 := base
	base2.BillingRate = 0.5
	res2 := c.Calculate(base2)

	if !almostEqual(res1.BilledCost, res2.BilledCost) {
		t.Fatalf("BilledCost should not depend on BillingRate: %v vs %v", res1.BilledCost, res2.BilledCost)
	}
	if almostEqual(res1.ActualCost, res2.ActualCost) {
		t.Fatalf("ActualCost should depend on BillingRate but didn't change")
	}

	// 改变 sell_rate
	base3 := base
	base3.SellRate = 0.9
	res3 := c.Calculate(base3)

	if !almostEqual(res1.ActualCost, res3.ActualCost) {
		t.Fatalf("ActualCost should not depend on SellRate: %v vs %v", res1.ActualCost, res3.ActualCost)
	}
	if almostEqual(res1.BilledCost, res3.BilledCost) {
		t.Fatalf("BilledCost should depend on SellRate but didn't change")
	}
}

func TestCalculate_ImageCostUsesRatesWhenNoOverride(t *testing.T) {
	c := NewCalculator()
	res := c.Calculate(CalculateInput{
		InputCost:      0.1,
		ImageInputCost: 0.05,
		OutputCost:     0.2,
		ImageCost:      0.3,
		BillingRate:    0.5,
		SellRate:       2.0,
		AccountRate:    1.0,
	})

	if !almostEqual(res.TotalCost, 0.65) {
		t.Fatalf("TotalCost = %v, want 0.65", res.TotalCost)
	}
	if !almostEqual(res.ActualCost, 0.325) {
		t.Fatalf("ActualCost = %v, want 0.325", res.ActualCost)
	}
	if !almostEqual(res.BilledCost, 1.3) {
		t.Fatalf("BilledCost = %v, want 1.3", res.BilledCost)
	}
}

func TestCalculate_ImageFixedPriceKeepsNonImageTokenCosts(t *testing.T) {
	c := NewCalculator()
	fixedImagePrice := 0.08
	res := c.Calculate(CalculateInput{
		InputCost:                0.1,
		ImageInputCost:           0.05,
		OutputCost:               0.2,
		ImageCost:                0.3,
		BillingRate:              0.5,
		SellRate:                 2.0,
		AccountRate:              1.0,
		ImageBillingCostOverride: &fixedImagePrice,
	})

	if !almostEqual(res.TotalCost, 0.65) {
		t.Fatalf("TotalCost = %v, want 0.65", res.TotalCost)
	}
	if !almostEqual(res.ActualCost, 0.23) {
		t.Fatalf("ActualCost = %v, want 0.23", res.ActualCost)
	}
	if !almostEqual(res.BilledCost, 0.68) {
		t.Fatalf("BilledCost = %v, want 0.68", res.BilledCost)
	}
}

func TestCalculate_ImageFixedPriceCanReplaceWholeRequest(t *testing.T) {
	c := NewCalculator()
	fixedImagePrice := 0.08
	res := c.Calculate(CalculateInput{
		InputCost:                             0.1,
		OutputCost:                            0.2,
		ImageCost:                             0.3,
		BillingRate:                           0.5,
		SellRate:                              2.0,
		AccountRate:                           1.0,
		ImageBillingCostOverride:              &fixedImagePrice,
		ImageBillingCostOverrideReplacesTotal: true,
	})

	if !almostEqual(res.TotalCost, 0.6) {
		t.Fatalf("TotalCost = %v, want 0.6", res.TotalCost)
	}
	if !almostEqual(res.ActualCost, 0.08) {
		t.Fatalf("ActualCost = %v, want 0.08", res.ActualCost)
	}
	if !almostEqual(res.BilledCost, 0.08) {
		t.Fatalf("BilledCost = %v, want 0.08", res.BilledCost)
	}
}

func TestCalculate_OutputBillingCostOverride(t *testing.T) {
	c := NewCalculator()
	outputOverride := 0.08
	res := c.Calculate(CalculateInput{
		InputCost:                 0.10,
		OutputCost:                0.40,
		BillingRate:               0.50,
		OutputBillingCostOverride: &outputOverride,
		AccountRate:               1.25,
	})

	if !almostEqual(res.TotalCost, 0.50) {
		t.Fatalf("TotalCost = %v, want 0.50", res.TotalCost)
	}
	if !almostEqual(res.ActualCost, 0.13) {
		t.Fatalf("ActualCost = %v, want 0.13", res.ActualCost)
	}
	if !almostEqual(res.BilledCost, res.ActualCost) {
		t.Fatalf("BilledCost = %v, want %v", res.BilledCost, res.ActualCost)
	}
	if !almostEqual(res.AccountCost, 0.625) {
		t.Fatalf("AccountCost = %v, want 0.625", res.AccountCost)
	}
	if !almostEqual(res.RateMultiplier, 0.50) {
		t.Fatalf("RateMultiplier = %v, want original billing rate 0.50", res.RateMultiplier)
	}
}

func TestEnrichUsageCostDetails_ResponseFixedImagePriceKeepsTokenUserCost(t *testing.T) {
	items := enrichUsageCostDetails(UsageRecord{
		InputCost:      0.00025,
		ImageCost:      0.00613,
		TotalCost:      0.00638,
		ActualCost:     0.10025,
		RateMultiplier: 1,
		UsageCostDetails: []sdk.UsageCostDetail{
			{Key: "input_tokens", Label: "输入 Token", AccountCost: 0.00025},
			{Key: "image_input_tokens", Label: "图片输入 Token", AccountCost: 0.000375},
			{
				Key:         "images",
				Label:       "图片生成",
				AccountCost: 0.00613,
				Metadata: map[string]string{
					"image_count": "1",
					"unit_price":  "30",
					"unit":        "USD/1M tokens",
				},
			},
		},
	})

	if len(items) != 3 {
		t.Fatalf("len(items) = %d, want 3", len(items))
	}
	if !almostEqual(items[0].UserCost, 0.00025) || !almostEqual(items[0].BillingMultiplier, 1) {
		t.Fatalf("input detail = %+v, want token user cost/multiplier", items[0])
	}
	if !almostEqual(items[1].UserCost, 0) || !almostEqual(items[1].BillingMultiplier, 0) {
		t.Fatalf("image input detail = %+v, want zero user cost/multiplier", items[1])
	}
	if !almostEqual(items[2].UserCost, 0.1) {
		t.Fatalf("image user cost = %v, want 0.1", items[2].UserCost)
	}
	if got := items[2].Metadata["billing_mode"]; got != "fixed_image_price" {
		t.Fatalf("billing_mode = %q, want fixed_image_price", got)
	}
	if got := items[2].Metadata["fixed_unit_price"]; got != "0.1" {
		t.Fatalf("fixed_unit_price = %q, want 0.1", got)
	}
}

func TestEnrichUsageCostDetails_ResponseImageTokenBillingMergesIntoInputAndOutput(t *testing.T) {
	items := enrichUsageCostDetails(UsageRecord{
		InputCost:       0.00937,
		CachedInputCost: 0.002112,
		OutputCost:      0.00258,
		ImageCost:       0.05268,
		TotalCost:       0.067097,
		ActualCost:      0.0536776,
		RateMultiplier:  0.8,
		UsageCostDetails: []sdk.UsageCostDetail{
			{Key: "input_tokens", Label: "输入 Token", AccountCost: 0.00937},
			{Key: "cached_input_tokens", Label: "缓存输入 Token", AccountCost: 0.002112},
			{Key: "output_tokens", Label: "输出 Token", AccountCost: 0.00258},
			{Key: "image_input_tokens", Label: "图片输入 Token", AccountCost: 0.000355},
			{
				Key:         "image_tool",
				Label:       "图片生成",
				AccountCost: 0.05268,
				Metadata: map[string]string{
					"image_count": "1",
					"unit_price":  "30",
					"unit":        "USD/1M tokens",
				},
			},
		},
	})

	if len(items) != 3 {
		t.Fatalf("len(items) = %d, want 3", len(items))
	}
	if items[0].Key != "input_tokens" || !almostEqual(items[0].UserCost, 0.00778) || !almostEqual(items[0].BillingMultiplier, 0.8) {
		t.Fatalf("input detail = %+v, want merged input user cost/multiplier", items[0])
	}
	if items[2].Key != "output_tokens" || !almostEqual(items[2].UserCost, 0.044208) || !almostEqual(items[2].BillingMultiplier, 0.8) {
		t.Fatalf("output detail = %+v, want merged output user cost/multiplier", items[2])
	}
	if got := items[2].Metadata["billing_mode"]; got != "" {
		t.Fatalf("billing_mode = %q, want empty", got)
	}
	if _, ok := items[2].Metadata["fixed_unit_price"]; ok {
		t.Fatalf("fixed_unit_price should be absent for token billing, got %q", items[2].Metadata["fixed_unit_price"])
	}
}

func TestEnrichUsageCostDetails_ImageOnlyTokenBillingUsesInputAndOutputRows(t *testing.T) {
	items := enrichUsageCostDetails(UsageRecord{
		InputCost:      0.000215,
		ImageCost:      0.01113,
		TotalCost:      0.011345,
		ActualCost:     0.009076,
		RateMultiplier: 0.8,
		UsageCostDetails: []sdk.UsageCostDetail{
			{Key: "input_tokens", Label: "输入 Token", AccountCost: 0.000215},
			{
				Key:         "images",
				Label:       "图片生成",
				AccountCost: 0.01113,
				Metadata: map[string]string{
					"image_count": "1",
					"unit_price":  "30",
					"unit":        "USD/1M tokens",
				},
			},
		},
	})

	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].Key != "input_tokens" || !almostEqual(items[0].UserCost, 0.000172) {
		t.Fatalf("input detail = %+v, want input token cost", items[0])
	}
	if items[1].Key != "output_tokens" || items[1].Label != "输出 Token" || !almostEqual(items[1].UserCost, 0.008904) {
		t.Fatalf("output detail = %+v, want image output merged into output token cost", items[1])
	}
	if got := items[1].Metadata["billing_mode"]; got != "" {
		t.Fatalf("billing_mode = %q, want empty", got)
	}
}

func TestEnrichUsageCostDetails_ImageOnlyFixedPriceZeroesTokenUserCost(t *testing.T) {
	items := enrichUsageCostDetails(UsageRecord{
		InputCost:                    0.00025,
		ImageCost:                    0.00613,
		TotalCost:                    0.00638,
		ActualCost:                   0.1,
		RateMultiplier:               1,
		ImageFixedPriceReplacesTotal: true,
		UsageCostDetails: []sdk.UsageCostDetail{
			{Key: "input_tokens", Label: "输入 Token", AccountCost: 0.00025},
			{
				Key:         "images",
				Label:       "图片生成",
				AccountCost: 0.00613,
				Metadata: map[string]string{
					"image_count": "1",
					"unit_price":  "30",
					"unit":        "USD/1M tokens",
				},
			},
		},
	})

	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if !almostEqual(items[0].UserCost, 0) || !almostEqual(items[0].BillingMultiplier, 0) {
		t.Fatalf("input detail = %+v, want zero user cost/multiplier", items[0])
	}
	if !almostEqual(items[1].UserCost, 0.1) {
		t.Fatalf("image user cost = %v, want 0.1", items[1].UserCost)
	}
}

func TestEnrichUsageCostDetails_FreeFixedImagePriceDoesNotFallBackToTokenCost(t *testing.T) {
	items := enrichUsageCostDetails(UsageRecord{
		InputCost:                    0.00025,
		ImageCost:                    0.00613,
		TotalCost:                    0.00638,
		ActualCost:                   0,
		RateMultiplier:               1,
		ImageFixedPriceReplacesTotal: true,
		UsageCostDetails: []sdk.UsageCostDetail{
			{Key: "input_tokens", Label: "输入 Token", AccountCost: 0.00025},
			{
				Key:         "images",
				Label:       "图片生成",
				AccountCost: 0.00613,
				Metadata: map[string]string{
					"image_count": "1",
					"unit_price":  "30",
					"unit":        "USD/1M tokens",
				},
			},
		},
	})

	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if !almostEqual(items[0].UserCost, 0) || !almostEqual(items[1].UserCost, 0) {
		t.Fatalf("user costs = %v/%v, want both zero", items[0].UserCost, items[1].UserCost)
	}
	if !almostEqual(items[1].BillingMultiplier, 0) {
		t.Fatalf("image multiplier = %v, want 0", items[1].BillingMultiplier)
	}
	if got := items[1].Metadata["billing_mode"]; got != "fixed_image_price" {
		t.Fatalf("billing_mode = %q, want fixed_image_price", got)
	}
}
