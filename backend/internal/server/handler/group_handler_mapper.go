package handler

import (
	appgroup "github.com/DouDOU-start/airgate-core/internal/app/group"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func toGroupRespFromDomain(item appgroup.Group) dto.GroupResp {
	return dto.GroupResp{
		ID:               int64(item.ID),
		Name:             item.Name,
		Platform:         item.Platform,
		RateMultiplier:   item.RateMultiplier,
		IsExclusive:      item.IsExclusive,
		SubscriptionType: item.SubscriptionType,
		Quotas:           item.Quotas,
		ModelRouting:     item.ModelRouting,
		ServiceTier:      item.ServiceTier,
		SortWeight:       item.SortWeight,
		TimeMixin: dto.TimeMixin{
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		},
	}
}
