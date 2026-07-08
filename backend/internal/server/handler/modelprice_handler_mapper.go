package handler

import (
	appmodelprice "github.com/DouDOU-start/airgate-core/internal/app/modelprice"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

// toModelPriceRespFromDomain 领域对象 → 响应 DTO。
func toModelPriceRespFromDomain(item appmodelprice.ModelPrice) dto.ModelPriceResp {
	return dto.ModelPriceResp{
		ID:                   int64(item.ID),
		Model:                item.Model,
		InputPrice:           item.InputPrice,
		OutputPrice:          item.OutputPrice,
		CachedInputPrice:     item.CachedInputPrice,
		CacheCreationPrice:   item.CacheCreationPrice,
		CacheCreation1hPrice: item.CacheCreation1hPrice,
		PerRequestPrice:      item.PerRequestPrice,
		PricingExtra:         item.PricingExtra,
		TimeMixin: dto.TimeMixin{
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		},
	}
}
