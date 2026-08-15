package pipeline

import (
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
)

// usageBillingSnapshot 是一次同步转发落账时的统一计费依据。
// Mode 决定 Calls 的单位；InputPrice 与 Calls 保证可直接对账 InputCost。
type usageBillingSnapshot struct {
	Mode       string
	Calls      int
	InputPrice float64
}

// resolveUsageBilling 按 ComputeCosts 相同的优先级解析计费方式：
// 图片分辨率表 > 图片按次价（按张）> 通用按次价 > Token。
// endpoint 使用入口语义而非模型名，避免新模型接入后继续增加名称特判。
func resolveUsageBilling(endpoint string, price pricing.Price, usage dto.Usage) usageBillingSnapshot {
	snapshot := usageBillingSnapshot{
		Mode:       billing.BillingModeToken,
		Calls:      usage.Calls,
		InputPrice: price.Input,
	}
	imageEndpoint := isImageBillingEndpoint(endpoint)

	if perImage, ok := pricing.ImagePriceFor(price, usage.ImageQuality, usage.ImageSize); ok {
		snapshot.Mode = billing.BillingModePerImage
		snapshot.InputPrice = perImage
		if snapshot.Calls < 1 {
			snapshot.Calls = 1
		}
		return snapshot
	}
	if price.PerRequest > 0 {
		if imageEndpoint {
			snapshot.Mode = billing.BillingModePerImage
		} else {
			snapshot.Mode = billing.BillingModePerRequest
		}
		snapshot.InputPrice = price.PerRequest
		if snapshot.Calls < 1 {
			snapshot.Calls = 1
		}
	}
	return snapshot
}

func isImageBillingEndpoint(endpoint string) bool {
	switch endpoint {
	case adaptor.EndpointImagesGenerations, adaptor.EndpointImagesEdits, adaptor.EndpointPredict:
		return true
	default:
		return false
	}
}
