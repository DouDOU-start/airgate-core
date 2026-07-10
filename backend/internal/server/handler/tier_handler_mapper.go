package handler

import (
	apptier "github.com/DouDOU-start/airgate-core/internal/app/tier"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func toTierRespFromDomain(item apptier.Tier) dto.TierResp {
	return dto.TierResp{
		ID:         int64(item.ID),
		Name:       item.Name,
		Rates:      item.Rates,
		Note:       item.Note,
		SortWeight: item.SortWeight,
		UserCount:  item.UserCount,
		TimeMixin: dto.TimeMixin{
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		},
	}
}
