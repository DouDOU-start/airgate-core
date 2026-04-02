package handler

import (
	"time"

	appsubscription "github.com/DouDOU-start/airgate-core/internal/app/subscription"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func toSubscriptionRespFromDomain(item appsubscription.Subscription) dto.SubscriptionResp {
	return dto.SubscriptionResp{
		ID:          int64(item.ID),
		UserID:      int64(item.UserID),
		GroupID:     int64(item.GroupID),
		GroupName:   item.GroupName,
		EffectiveAt: item.EffectiveAt.Format(time.RFC3339),
		ExpiresAt:   item.ExpiresAt.Format(time.RFC3339),
		Usage:       cloneUsage(item.Usage),
		Status:      item.Status,
		TimeMixin: dto.TimeMixin{
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		},
	}
}

func toSubscriptionProgressRespFromDomain(item appsubscription.SubscriptionProgress) dto.SubscriptionProgressResp {
	resp := dto.SubscriptionProgressResp{
		GroupID:   int64(item.GroupID),
		GroupName: item.GroupName,
	}
	if item.Daily != nil {
		resp.Daily = &dto.UsageWindow{
			Used:  item.Daily.Used,
			Limit: item.Daily.Limit,
			Reset: item.Daily.Reset,
		}
	}
	if item.Weekly != nil {
		resp.Weekly = &dto.UsageWindow{
			Used:  item.Weekly.Used,
			Limit: item.Weekly.Limit,
			Reset: item.Weekly.Reset,
		}
	}
	if item.Monthly != nil {
		resp.Monthly = &dto.UsageWindow{
			Used:  item.Monthly.Used,
			Limit: item.Monthly.Limit,
			Reset: item.Monthly.Reset,
		}
	}
	return resp
}

func cloneUsage(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}
