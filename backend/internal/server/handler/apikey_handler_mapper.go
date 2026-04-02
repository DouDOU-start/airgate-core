package handler

import (
	appapikey "github.com/DouDOU-start/airgate-core/internal/app/apikey"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func toAPIKeyResp(item appapikey.Key) dto.APIKeyResp {
	resp := dto.APIKeyResp{
		ID:            int64(item.ID),
		Name:          item.Name,
		Key:           item.PlainKey,
		KeyPrefix:     appapikey.DisplayKeyPrefix(item),
		UserID:        int64(item.UserID),
		IPWhitelist:   item.IPWhitelist,
		IPBlacklist:   item.IPBlacklist,
		QuotaUSD:      item.QuotaUSD,
		UsedQuota:     item.UsedQuota,
		TodayCost:     item.TodayCost,
		ThirtyDayCost: item.ThirtyDayCost,
		Status:        item.Status,
		TimeMixin: dto.TimeMixin{
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		},
	}
	if item.GroupID != nil {
		groupID := int64(*item.GroupID)
		resp.GroupID = &groupID
	}
	if item.ExpiresAt != nil {
		expiresAt := item.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")
		resp.ExpiresAt = &expiresAt
	}
	return resp
}
