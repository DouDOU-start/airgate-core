package handler

import (
	"time"

	appaccount "github.com/DouDOU-start/airgate-core/internal/app/account"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func toAccountResp(account appaccount.Account) dto.AccountResp {
	resp := dto.AccountResp{
		ID:                      int64(account.ID),
		Name:                    account.Name,
		Platform:                account.Platform,
		Type:                    account.Type,
		Credentials:             appaccount.RedactCredentials(account.Credentials),
		Email:                   account.Email,
		State:                   account.State,
		PlanType:                account.PlanType,
		SubscriptionActiveUntil: account.SubscriptionActiveUntil,
		Priority:                account.Priority,
		Weight:                  account.Weight,
		MaxConcurrency:          account.MaxConcurrency,
		CurrentConcurrency:      account.CurrentConcurrency,
		CurrentRPM:              account.CurrentRPM,
		MaxRPM:                  account.MaxRPM,
		RateMultiplier:          account.RateMultiplier,
		ErrorMsg:                account.ErrorMsg,
		Extra:                   account.Extra,
		Models:                  account.Models,
		TotalCost:               account.TotalCost,
		TotalRevenue:            account.TotalRevenue,
		TodayCost:               account.TodayCost,
		TodayRevenue:            account.TodayRevenue,
		GroupIDs:                int64SliceToInt(account.GroupIDs),
		TimeMixin: dto.TimeMixin{
			CreatedAt: account.CreatedAt,
			UpdatedAt: account.UpdatedAt,
		},
	}
	if resp.Credentials == nil {
		resp.Credentials = map[string]string{}
	}
	if resp.Models == nil {
		resp.Models = []string{}
	}
	if resp.GroupIDs == nil {
		resp.GroupIDs = []int{}
	}
	if account.LastUsedAt != nil {
		lastUsedAt := account.LastUsedAt.UTC().Format("2006-01-02T15:04:05Z")
		resp.LastUsedAt = &lastUsedAt
	}
	if account.StateUntil != nil {
		until := account.StateUntil.UTC().Format("2006-01-02T15:04:05Z")
		resp.StateUntil = &until
	}
	if account.Proxy != nil {
		proxyID := int64(account.Proxy.ID)
		resp.ProxyID = &proxyID
		resp.ProxyName = account.Proxy.Name
	}
	if account.Usage != nil {
		resp.Usage = toAccountUsageResp(account.Usage)
	} else if u := appaccount.UsageFromExtra(account.Extra); u != nil {
		resp.Usage = toAccountUsageResp(u)
	}
	return resp
}

func toAccountUsageResp(u *appaccount.UsageSnapshot) *dto.AccountUsageResp {
	if u == nil {
		return nil
	}
	out := &dto.AccountUsageResp{
		CapturedAt:            u.CapturedAt.UTC().Format(time.RFC3339),
		Stale:                 u.Stale,
		PlanType:              u.PlanType,
		Platform:              u.Platform,
		ResetCreditsAvailable: u.ResetCreditsAvailable,
		Error:                 u.Error,
		Windows:               make([]dto.AccountUsageWindowResp, 0, len(u.Windows)),
	}
	if out.CapturedAt == "0001-01-01T00:00:00Z" {
		out.CapturedAt = ""
	}
	for _, w := range u.Windows {
		item := dto.AccountUsageWindowResp{
			Key:           w.Key,
			Label:         w.Label,
			UsedPercent:   w.UsedPercent,
			WindowMinutes: w.WindowMinutes,
			LimitID:       w.LimitID,
			LimitName:     w.LimitName,
		}
		if w.ResetsAt != nil {
			s := w.ResetsAt.UTC().Format(time.RFC3339)
			item.ResetsAt = &s
		}
		out.Windows = append(out.Windows, item)
	}
	if u.Credits != nil {
		out.Credits = &dto.AccountUsageCreditsResp{
			HasCredits: u.Credits.HasCredits,
			Unlimited:  u.Credits.Unlimited,
			Balance:    u.Credits.Balance,
		}
	}
	return out
}

func toAccountUsageStatsResp(s appaccount.AccountUsageStats) dto.AccountUsageStatsResp {
	out := dto.AccountUsageStatsResp{
		History: make([]dto.AccountDayHistoryResp, 0, len(s.History)),
		Models:  make([]dto.AccountModelStatResp, 0, len(s.Models)),
		Summary: dto.AccountUsageSummaryResp{
			Days:              s.Summary.Days,
			ActualDaysUsed:    s.Summary.ActualDaysUsed,
			TotalCost:         s.Summary.TotalCost,
			TotalUserCost:     s.Summary.TotalUserCost,
			TotalStandardCost: s.Summary.TotalStandardCost,
			TotalRequests:     s.Summary.TotalRequests,
			TotalTokens:       s.Summary.TotalTokens,
			AvgDailyCost:      s.Summary.AvgDailyCost,
			AvgDailyUserCost:  s.Summary.AvgDailyUserCost,
			AvgDailyRequests:  s.Summary.AvgDailyRequests,
			AvgDailyTokens:    s.Summary.AvgDailyTokens,
			AvgDurationMs:     s.Summary.AvgDurationMs,
		},
	}
	for _, h := range s.History {
		out.History = append(out.History, dto.AccountDayHistoryResp{
			Date:       h.Date,
			Label:      h.Label,
			Requests:   h.Requests,
			Tokens:     h.Tokens,
			Cost:       h.Cost,
			ActualCost: h.ActualCost,
			UserCost:   h.UserCost,
		})
	}
	for _, m := range s.Models {
		out.Models = append(out.Models, dto.AccountModelStatResp{
			Model:      m.Model,
			Requests:   m.Requests,
			Tokens:     m.Tokens,
			TotalCost:  m.TotalCost,
			ActualCost: m.ActualCost,
		})
	}
	if s.Summary.Today != nil {
		out.Summary.Today = mapDayHighlight(s.Summary.Today)
	}
	if s.Summary.HighestCostDay != nil {
		out.Summary.HighestCostDay = mapDayHighlight(s.Summary.HighestCostDay)
	}
	if s.Summary.HighestRequestDay != nil {
		out.Summary.HighestRequestDay = mapDayHighlight(s.Summary.HighestRequestDay)
	}
	return out
}

func mapDayHighlight(h *appaccount.AccountDayHighlight) *dto.AccountDayHighlightResp {
	if h == nil {
		return nil
	}
	return &dto.AccountDayHighlightResp{
		Date:     h.Date,
		Label:    h.Label,
		Cost:     h.Cost,
		UserCost: h.UserCost,
		Requests: h.Requests,
		Tokens:   h.Tokens,
	}
}

func toAccountExportItem(account appaccount.Account) dto.AccountExportItem {
	creds := account.Credentials
	if creds == nil {
		creds = map[string]string{}
	}
	return dto.AccountExportItem{
		Name:           account.Name,
		Platform:       account.Platform,
		Type:           account.Type,
		Credentials:    creds,
		Priority:       account.Priority,
		Weight:         account.Weight,
		MaxConcurrency: account.MaxConcurrency,
		RateMultiplier: account.RateMultiplier,
		Extra:          account.Extra,
	}
}

// toAccountImportInput 将导出项转换为新账号。
// 分组和代理均为服务本地资源，跨服务导入时不得沿用旧 ID。
func toAccountImportInput(item dto.AccountExportItem) appaccount.CreateInput {
	return appaccount.CreateInput{
		Name:           item.Name,
		Platform:       item.Platform,
		Type:           item.Type,
		Credentials:    item.Credentials,
		Priority:       item.Priority,
		Weight:         item.Weight,
		MaxConcurrency: item.MaxConcurrency,
		RateMultiplier: item.RateMultiplier,
		Extra:          item.Extra,
	}
}

func toBulkOpResp(r appaccount.BulkResult) dto.BulkOpResp {
	items := make([]dto.BulkOpItemResp, 0, len(r.Results))
	for _, item := range r.Results {
		items = append(items, dto.BulkOpItemResp{
			ID:      item.ID,
			Success: item.Success,
			Error:   item.Error,
		})
	}
	successIDs := r.SuccessIDs
	if successIDs == nil {
		successIDs = []int{}
	}
	failedIDs := r.FailedIDs
	if failedIDs == nil {
		failedIDs = []int{}
	}
	return dto.BulkOpResp{
		Success:    r.Success,
		Failed:     r.Failed,
		SuccessIDs: successIDs,
		FailedIDs:  failedIDs,
		Results:    items,
	}
}

func toCredentialSchemaResp(schema appaccount.CredentialSchema) dto.CredentialSchemaResp {
	resp := dto.CredentialSchemaResp{
		Fields:       make([]dto.CredentialFieldResp, 0, len(schema.Fields)),
		AccountTypes: make([]dto.AccountTypeResp, 0, len(schema.AccountTypes)),
	}
	for _, field := range schema.Fields {
		resp.Fields = append(resp.Fields, dto.CredentialFieldResp{
			Key:          field.Key,
			Label:        field.Label,
			Type:         field.Type,
			Required:     field.Required,
			Placeholder:  field.Placeholder,
			EditDisabled: field.EditDisabled,
		})
	}
	for _, accountType := range schema.AccountTypes {
		item := dto.AccountTypeResp{
			Key:         accountType.Key,
			Label:       accountType.Label,
			Description: accountType.Description,
			Fields:      make([]dto.CredentialFieldResp, 0, len(accountType.Fields)),
		}
		for _, field := range accountType.Fields {
			item.Fields = append(item.Fields, dto.CredentialFieldResp{
				Key:          field.Key,
				Label:        field.Label,
				Type:         field.Type,
				Required:     field.Required,
				Placeholder:  field.Placeholder,
				EditDisabled: field.EditDisabled,
			})
		}
		resp.AccountTypes = append(resp.AccountTypes, item)
	}
	return resp
}

func int64SliceToInt(values []int64) []int {
	if values == nil {
		return nil
	}
	out := make([]int, 0, len(values))
	for _, v := range values {
		out = append(out, int(v))
	}
	return out
}

func intSliceToInt64(values []int) []int64 {
	if values == nil {
		return nil
	}
	out := make([]int64, 0, len(values))
	for _, v := range values {
		out = append(out, int64(v))
	}
	return out
}

func toOAuthSessionResp(s appaccount.OAuthSession) dto.OAuthSessionResp {
	return dto.OAuthSessionResp{
		ID:                      s.ID,
		Platform:                s.Platform,
		Status:                  s.Status,
		Flow:                    s.Flow,
		Message:                 s.Message,
		Error:                   s.Error,
		AuthorizeURL:            s.AuthorizeURL,
		UserCode:                s.UserCode,
		VerificationURI:         s.VerificationURI,
		VerificationURIComplete: s.VerificationURIComplete,
		AccountID:               s.AccountID,
		AccountName:             s.AccountName,
		CreatedAt:               s.CreatedAt.UTC().Format(time.RFC3339),
	}
}
