package handler

import (
	apppluginadmin "github.com/DouDOU-start/airgate-core/internal/app/pluginadmin"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func toPluginResp(item apppluginadmin.PluginMeta) dto.PluginResp {
	resp := dto.PluginResp{
		Name:         item.Name,
		DisplayName:  item.DisplayName,
		Version:      item.Version,
		Author:       item.Author,
		Type:         item.Type,
		Platform:     item.Platform,
		HasWebAssets: item.HasWebAssets,
		IsDev:        item.IsDev,
	}
	for _, accountType := range item.AccountTypes {
		resp.AccountTypes = append(resp.AccountTypes, dto.AccountTypeResp{
			Key:         accountType.Key,
			Label:       accountType.Label,
			Description: accountType.Description,
		})
	}
	for _, page := range item.FrontendPages {
		resp.FrontendPages = append(resp.FrontendPages, dto.FrontendPageResp{
			Path:        page.Path,
			Title:       page.Title,
			Icon:        page.Icon,
			Description: page.Description,
		})
	}
	return resp
}

func toMarketplacePluginResp(item apppluginadmin.MarketplacePlugin) dto.MarketplacePluginResp {
	return dto.MarketplacePluginResp{
		Name:        item.Name,
		Version:     item.Version,
		Description: item.Description,
		Author:      item.Author,
		Type:        item.Type,
		Installed:   item.Installed,
	}
}
