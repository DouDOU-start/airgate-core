package handler

import (
	apppluginadmin "github.com/DouDOU-start/airgate-core/internal/app/pluginadmin"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func toPluginResp(item apppluginadmin.PluginMeta) dto.PluginResp {
	resp := dto.PluginResp{
		Name:               item.Name,
		DisplayName:        item.DisplayName,
		Version:            item.Version,
		Author:             item.Author,
		Type:               item.Type,
		Platform:           item.Platform,
		InstructionPresets: item.InstructionPresets,
		Metadata:           item.Metadata,
		HasWebAssets:       item.HasWebAssets,
		IsDev:              item.IsDev,
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
			Audience:    page.Audience,
		})
	}
	if item.RichConfigSchema != nil {
		resp.ConfigSchemaVersion = item.RichConfigSchema.Version
		for _, field := range item.RichConfigSchema.Fields {
			resp.ConfigSchema = append(resp.ConfigSchema, dto.ConfigFieldResp{
				Key:         field.Key,
				FallbackKey: field.FallbackKey,
				Label:       field.Label,
				Type:        field.Type,
				Widget:      field.Widget,
				DataSource:  field.DataSource,
				Required:    field.Required,
				Default:     field.Default,
				Description: field.Description,
				Placeholder: field.Placeholder,
				Filter:      field.Filter,
			})
		}
	} else {
		for _, field := range item.ConfigSchema {
			resp.ConfigSchema = append(resp.ConfigSchema, dto.ConfigFieldResp{
				Key:         field.Key,
				Label:       field.Label,
				Type:        field.Type,
				Required:    field.Required,
				Default:     field.Default,
				Description: field.Description,
				Placeholder: field.Placeholder,
			})
		}
	}
	return resp
}

func toMarketplacePluginResp(item apppluginadmin.MarketplacePlugin) dto.MarketplacePluginResp {
	return dto.MarketplacePluginResp{
		Name:             item.Name,
		Version:          item.Version,
		Description:      item.Description,
		Author:           item.Author,
		Type:             item.Type,
		GithubRepo:       item.GithubRepo,
		Installed:        item.Installed,
		InstalledVersion: item.InstalledVersion,
		HasUpdate:        item.HasUpdate,
	}
}
