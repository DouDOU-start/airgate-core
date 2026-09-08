package pipeline

import (
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
)

func TestResolveUsageBilling(t *testing.T) {
	tests := []struct {
		name           string
		endpoint       string
		price          pricing.Price
		usage          dto.Usage
		wantMode       string
		wantCalls      int
		wantInputPrice float64
	}{
		{
			name:           "普通 Token 计费",
			endpoint:       adaptor.EndpointChatCompletions,
			price:          pricing.Price{Input: 5},
			usage:          dto.Usage{PromptTokens: 100},
			wantMode:       billing.BillingModeToken,
			wantInputPrice: 5,
		},
		{
			name:           "通用按次计费补一计次",
			endpoint:       adaptor.EndpointAlphaSearch,
			price:          pricing.Price{PerRequest: 0.02},
			wantMode:       billing.BillingModePerRequest,
			wantCalls:      1,
			wantInputPrice: 0.02,
		},
		{
			name:           "图片按次价按实际张数计费",
			endpoint:       adaptor.EndpointImagesGenerations,
			price:          pricing.Price{PerRequest: 0.04},
			usage:          dto.Usage{Calls: 2},
			wantMode:       billing.BillingModePerImage,
			wantCalls:      2,
			wantInputPrice: 0.04,
		},
		{
			name:           "图片分辨率价优先",
			endpoint:       adaptor.EndpointImagesEdits,
			price:          pricing.Price{PerRequest: 0.04, ImageSizePrices: map[string]float64{"high:1024x1024": 0.167}},
			usage:          dto.Usage{Calls: 1, ImageSize: "1024x1024", ImageQuality: "high"},
			wantMode:       billing.BillingModePerImage,
			wantCalls:      1,
			wantInputPrice: 0.167,
		},
		{
			name:           "Gemini Imagen predict 按张计费",
			endpoint:       adaptor.EndpointPredict,
			price:          pricing.Price{PerRequest: 0.03},
			usage:          dto.Usage{Calls: 3},
			wantMode:       billing.BillingModePerImage,
			wantCalls:      3,
			wantInputPrice: 0.03,
		},
		{
			name:           "Token 计费图片保留产出张数但模式仍为 Token",
			endpoint:       adaptor.EndpointImagesGenerations,
			price:          pricing.Price{Input: 5, Output: 20},
			usage:          dto.Usage{PromptTokens: 100, CompletionTokens: 50, Calls: 2},
			wantMode:       billing.BillingModeToken,
			wantCalls:      2,
			wantInputPrice: 5,
		},
		{
			name:           "quality 空且 size 已知：回退 medium:size 按张",
			endpoint:       adaptor.EndpointImagesGenerations,
			price:          pricing.Price{Input: 5, Output: 30, ImageSizePrices: map[string]float64{"medium:1024x1024": 0.053, "high:1024x1024": 0.211}},
			usage:          dto.Usage{Calls: 2, ImageSize: "1024x1024"},
			wantMode:       billing.BillingModePerImage,
			wantCalls:      2,
			wantInputPrice: 0.053,
		},
		{
			name:           "无档位无 token：表内默认档按张兜底",
			endpoint:       adaptor.EndpointImagesGenerations,
			price:          pricing.Price{Input: 5, Output: 30, ImageSizePrices: map[string]float64{"medium:1024x1024": 0.053, "high:1024x1024": 0.211}},
			usage:          dto.Usage{Calls: 1},
			wantMode:       billing.BillingModePerImage,
			wantCalls:      1,
			wantInputPrice: 0.053,
		},
		{
			name:           "非标分辨率有 token：仍走 token 不计默认档",
			endpoint:       adaptor.EndpointImagesGenerations,
			price:          pricing.Price{Input: 5, Output: 30, ImageSizePrices: map[string]float64{"medium:1024x1024": 0.053}},
			usage:          dto.Usage{PromptTokens: 100, CompletionTokens: 4000, Calls: 1, ImageSize: "2048x2048", ImageQuality: "high"},
			wantMode:       billing.BillingModeToken,
			wantCalls:      1,
			wantInputPrice: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveUsageBilling(tt.endpoint, tt.price, tt.usage)
			if got.Mode != tt.wantMode || got.Calls != tt.wantCalls || got.InputPrice != tt.wantInputPrice {
				t.Fatalf("resolveUsageBilling() = %+v，期望 mode=%s calls=%d input_price=%v",
					got, tt.wantMode, tt.wantCalls, tt.wantInputPrice)
			}
		})
	}
}
