package handler

import (
	appmodelprice "github.com/DouDOU-start/airgate-core/internal/app/modelprice"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

// toModelPriceRespFromDomain 领域对象 → 响应 DTO。
func toModelPriceRespFromDomain(item appmodelprice.ModelPrice) dto.ModelPriceResp {
	resp := dto.ModelPriceResp{
		ID:                   int64(item.ID),
		Model:                item.Model,
		InputPrice:           item.InputPrice,
		OutputPrice:          item.OutputPrice,
		CachedInputPrice:     item.CachedInputPrice,
		CacheCreationPrice:   item.CacheCreationPrice,
		CacheCreation1hPrice: item.CacheCreation1hPrice,
		PerRequestPrice:      item.PerRequestPrice,
		PricingExtra:         item.PricingExtra,
		MarketVisible:        item.MarketVisible,
		TimeMixin: dto.TimeMixin{
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		},
	}
	if item.TagID != nil && item.TagName != "" {
		resp.Tag = &dto.ModelTagRef{ID: int64(*item.TagID), Name: item.TagName}
	}
	return resp
}

// toModelTagRespFromDomain 标签领域对象 → 响应 DTO。
func toModelTagRespFromDomain(tag appmodelprice.Tag) dto.ModelTagResp {
	return dto.ModelTagResp{
		ID:         int64(tag.ID),
		Name:       tag.Name,
		ModelCount: tag.ModelCount,
	}
}

// tagIDFromReq 把 DTO 的 *int64 tag_id 转为领域层 *int（保留三态语义）。
func tagIDFromReq(id *int64) *int {
	if id == nil {
		return nil
	}
	v := int(*id)
	return &v
}

// toModelMarketItemRespFromDomain 领域对象 → 模型广场公开响应 DTO：价格子集 + 服务档
// 倍率/长上下文阶梯（与计费实际使用的 ParsePricingExtra 同一套解析，口径一致）。
func toModelMarketItemRespFromDomain(item appmodelprice.ModelPrice) dto.ModelMarketItemResp {
	resp := dto.ModelMarketItemResp{
		Model:                item.Model,
		InputPrice:           item.InputPrice,
		OutputPrice:          item.OutputPrice,
		CachedInputPrice:     item.CachedInputPrice,
		CacheCreationPrice:   item.CacheCreationPrice,
		CacheCreation1hPrice: item.CacheCreation1hPrice,
		PerRequestPrice:      item.PerRequestPrice,
	}
	if item.TagID != nil && item.TagName != "" {
		resp.Tag = &dto.ModelTagRef{ID: int64(*item.TagID), Name: item.TagName}
	}
	tiers, longCtx := appmodelprice.ParsePricingExtra(item.Model, item.PricingExtra)
	if len(tiers) > 0 {
		resp.ServiceTiers = tiers
	}
	if longCtx != nil {
		resp.LongContext = &dto.ModelMarketLongContext{
			ThresholdTokens:  longCtx.ThresholdTokens,
			InputMultiplier:  longCtx.InputMultiplier,
			OutputMultiplier: longCtx.OutputMultiplier,
			CachedMultiplier: longCtx.CachedMultiplier,
		}
	}
	if prices := appmodelprice.ParseImageSizePrices(item.Model, item.PricingExtra); len(prices) > 0 {
		resp.ImageSizePrices = prices
	}
	resp.VideoPerSecond = appmodelprice.ParseVideoPerSecond(item.Model, item.PricingExtra)
	if prices := appmodelprice.ParseVideoResolutionPrices(item.Model, item.PricingExtra); len(prices) > 0 {
		resp.VideoResolutionPrices = prices
	}
	return resp
}
