package handler

import (
	appapikey "github.com/DouDOU-start/airgate-core/internal/app/apikey"
	appuser "github.com/DouDOU-start/airgate-core/internal/app/user"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func toUserRespFromDomain(item appuser.User) dto.UserResp {
	return dto.UserResp{
		ID:                    int64(item.ID),
		Email:                 item.Email,
		Username:              item.Username,
		Balance:               item.Balance,
		Role:                  item.Role,
		MaxConcurrency:        item.MaxConcurrency,
		GroupRates:            item.GroupRates,
		AllowedGroupIDs:       item.AllowedGroupIDs,
		BalanceAlertThreshold: item.BalanceAlertThreshold,
		Status:                item.Status,
		TimeMixin: dto.TimeMixin{
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		},
	}
}

func toBalanceLogResp(item appuser.BalanceLog) dto.BalanceLogResp {
	return dto.BalanceLogResp{
		ID:            item.ID,
		Action:        item.Action,
		Amount:        item.Amount,
		BeforeBalance: item.BeforeBalance,
		AfterBalance:  item.AfterBalance,
		Remark:        item.Remark,
		CreatedAt:     item.CreatedAt,
	}
}

func toAPIKeyRespFromUserDomain(item appuser.APIKey, userID int) dto.APIKeyResp {
	keyPrefix := item.KeyHint
	if keyPrefix == "" {
		keyPrefix = appapikey.DisplayKeyPrefix(appapikey.Key{
			KeyHint:  item.KeyHint,
			KeyHash:  item.KeyHash,
			PlainKey: "",
		})
	}

	resp := dto.APIKeyResp{
		ID:            int64(item.ID),
		Name:          item.Name,
		KeyPrefix:     keyPrefix,
		UserID:        int64(userID),
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
		value := int64(*item.GroupID)
		resp.GroupID = &value
	}
	if item.ExpiresAt != nil {
		value := item.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")
		resp.ExpiresAt = &value
	}
	return resp
}
