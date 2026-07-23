package handler

import (
	appgroup "github.com/DouDOU-start/airgate-core/internal/app/group"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

// sanitizeAlphaSearchPrice 归一联网搜索覆盖价：nil 保持 nil（沿用全局设置），
// 负值视为非法一并按 nil 处理（回落全局）；0 合法（该分组免费）。
func sanitizeAlphaSearchPrice(p *float64) *float64 {
	if p == nil || *p < 0 {
		return nil
	}
	return p
}

func toGroupRespFromDomain(item appgroup.Group) dto.GroupResp {
	var fallbackID *int64
	if item.FallbackGroupID != nil {
		v := int64(*item.FallbackGroupID)
		fallbackID = &v
	}
	return dto.GroupResp{
		ID:               int64(item.ID),
		Name:             item.Name,
		Platform:         item.Platform,
		RateMultiplier:   item.RateMultiplier,
		AlphaSearchPrice: item.AlphaSearchPrice,
		EffectiveRate:    item.EffectiveRate,
		IsExclusive:      item.IsExclusive,
		StatusVisible:    item.StatusVisible,
		AllowedClients:   item.AllowedClients,
		FallbackGroupID:  fallbackID,
		Note:             item.Note,
		SortWeight:       item.SortWeight,

		CurrentConcurrency: item.CurrentConcurrency,
		CurrentRPM:         item.CurrentRPM,
		TimeMixin: dto.TimeMixin{
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		},
	}
}
