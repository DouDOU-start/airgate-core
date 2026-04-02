package handler

import (
	appproxy "github.com/DouDOU-start/airgate-core/internal/app/proxy"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func toProxyRespFromDomain(item appproxy.Proxy) dto.ProxyResp {
	return dto.ProxyResp{
		ID:       int64(item.ID),
		Name:     item.Name,
		Protocol: item.Protocol,
		Address:  item.Address,
		Port:     item.Port,
		Username: item.Username,
		Status:   item.Status,
		TimeMixin: dto.TimeMixin{
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		},
	}
}

func toTestProxyRespFromDomain(item appproxy.TestResult) dto.TestProxyResp {
	return dto.TestProxyResp{
		Success:     item.Success,
		Latency:     item.Latency,
		ErrorMsg:    item.ErrorMsg,
		IPAddress:   item.IPAddress,
		Country:     item.Country,
		CountryCode: item.CountryCode,
		City:        item.City,
	}
}
