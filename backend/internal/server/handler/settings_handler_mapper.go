package handler

import (
	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func toSettingResp(item appsettings.Setting) dto.SettingResp {
	return dto.SettingResp{
		Key:   item.Key,
		Value: item.Value,
		Group: item.Group,
	}
}
