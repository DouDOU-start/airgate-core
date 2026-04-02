package account

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	sdk "github.com/DouDOU-start/airgate-sdk"

	"github.com/DouDOU-start/airgate-core/internal/plugin"
)

// PluginCatalog 账号域需要的插件能力集合。
type PluginCatalog interface {
	GetPluginByPlatform(string) *plugin.PluginInstance
	GetModels(string) []sdk.ModelInfo
	GetAccountTypes(string) []sdk.AccountType
	GetCredentialFields(string) []sdk.CredentialField
	GetAllPluginMeta() []plugin.PluginMeta
}

// ConcurrencyReader 并发读接口。
type ConcurrencyReader interface {
	GetCurrentCounts(context.Context, []int) map[int]int
}

// Service 提供账号域用例编排。
type Service struct {
	repo        Repository
	plugins     PluginCatalog
	concurrency ConcurrencyReader
	now         func() time.Time
}

// NewService 创建账号服务。
func NewService(repo Repository, plugins PluginCatalog, concurrency ConcurrencyReader) *Service {
	return &Service{
		repo:        repo,
		plugins:     plugins,
		concurrency: concurrency,
		now:         time.Now,
	}
}

// List 查询账号列表。
func (s *Service) List(ctx context.Context, filter ListFilter) (ListResult, error) {
	page, pageSize := NormalizePage(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	accounts, total, err := s.repo.List(ctx, filter)
	if err != nil {
		return ListResult{}, err
	}

	ids := make([]int, 0, len(accounts))
	for _, item := range accounts {
		ids = append(ids, item.ID)
	}
	counts := s.concurrency.GetCurrentCounts(ctx, ids)
	for index := range accounts {
		accounts[index].CurrentConcurrency = counts[accounts[index].ID]
	}

	return ListResult{
		List:     accounts,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// Create 创建账号。
func (s *Service) Create(ctx context.Context, input CreateInput) (Account, error) {
	return s.repo.Create(ctx, input)
}

// Update 更新账号。
func (s *Service) Update(ctx context.Context, id int, input UpdateInput) (Account, error) {
	return s.repo.Update(ctx, id, input)
}

// Delete 删除账号。
func (s *Service) Delete(ctx context.Context, id int) error {
	return s.repo.Delete(ctx, id)
}

// ToggleScheduling 快速切换账号调度状态。
func (s *Service) ToggleScheduling(ctx context.Context, id int) (ToggleResult, error) {
	item, err := s.repo.FindByID(ctx, id, LoadOptions{})
	if err != nil {
		return ToggleResult{}, err
	}

	newStatus := "disabled"
	if item.Status != "active" {
		newStatus = "active"
	}

	updated, err := s.repo.Update(ctx, id, UpdateInput{
		Status: &newStatus,
	})
	if err != nil {
		return ToggleResult{}, err
	}

	return ToggleResult{ID: updated.ID, Status: updated.Status}, nil
}

// PrepareConnectivityTest 准备账号连通性测试。
func (s *Service) PrepareConnectivityTest(ctx context.Context, id int, modelID string) (*ConnectivityTest, error) {
	item, err := s.repo.FindByID(ctx, id, LoadOptions{WithProxy: true})
	if err != nil {
		return nil, err
	}

	inst := s.plugins.GetPluginByPlatform(item.Platform)
	if inst == nil || inst.Gateway == nil {
		return nil, ErrPluginNotFound
	}

	if modelID == "" {
		models := s.plugins.GetModels(item.Platform)
		if len(models) > 0 {
			modelID = models[0].ID
		}
	}
	if modelID == "" {
		return nil, ErrModelRequired
	}

	testBody, _ := json.Marshal(map[string]any{
		"model":    modelID,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
		"stream":   true,
	})

	forwardReq := &sdk.ForwardRequest{
		Account: &sdk.Account{
			ID:          int64(item.ID),
			Name:        item.Name,
			Platform:    item.Platform,
			Type:        item.Type,
			Credentials: cloneStringMap(item.Credentials),
			ProxyURL:    buildProxyURL(item.Proxy),
		},
		Body:    testBody,
		Headers: http.Header{"Content-Type": {"application/json"}},
		Model:   modelID,
		Stream:  true,
	}

	return &ConnectivityTest{
		AccountName: item.Name,
		AccountType: item.Type,
		ModelID:     modelID,
		run: func(runCtx context.Context, writer http.ResponseWriter) error {
			req := *forwardReq
			req.Writer = writer
			_, forwardErr := inst.Gateway.Forward(runCtx, &req)
			return forwardErr
		},
	}, nil
}

// GetModels 获取账号平台的模型列表。
func (s *Service) GetModels(ctx context.Context, id int) ([]Model, error) {
	item, err := s.repo.FindByID(ctx, id, LoadOptions{})
	if err != nil {
		return nil, err
	}

	rawModels := s.plugins.GetModels(item.Platform)
	models := make([]Model, 0, len(rawModels))
	for _, raw := range rawModels {
		models = append(models, Model{ID: raw.ID, Name: raw.Name})
	}
	return models, nil
}

// GetAccountUsage 查询插件上报的账号额度。
func (s *Service) GetAccountUsage(ctx context.Context, platform string) (map[string]any, error) {
	type platformQuery struct {
		platform string
		inst     *plugin.PluginInstance
	}

	var queries []platformQuery
	if platform != "" {
		inst := s.plugins.GetPluginByPlatform(platform)
		if inst != nil {
			queries = append(queries, platformQuery{platform: platform, inst: inst})
		}
	} else {
		for _, meta := range s.plugins.GetAllPluginMeta() {
			if meta.Platform == "" {
				continue
			}
			inst := s.plugins.GetPluginByPlatform(meta.Platform)
			if inst != nil {
				queries = append(queries, platformQuery{platform: meta.Platform, inst: inst})
			}
		}
	}

	type accountUsageRequest struct {
		ID          int               `json:"id"`
		Credentials map[string]string `json:"credentials"`
	}

	merged := make(map[string]any)
	for _, query := range queries {
		accounts, err := s.repo.ListByPlatform(ctx, query.platform)
		if err != nil || len(accounts) == 0 {
			continue
		}

		reqList := make([]accountUsageRequest, 0, len(accounts))
		for _, item := range accounts {
			reqList = append(reqList, accountUsageRequest{
				ID:          item.ID,
				Credentials: cloneStringMap(item.Credentials),
			})
		}

		body, _ := json.Marshal(reqList)
		status, _, respBody, err := query.inst.Gateway.HandleHTTPRequest(ctx, "POST", "usage/accounts", "", nil, body)
		if err != nil || status != http.StatusOK {
			continue
		}

		var result struct {
			Accounts map[string]any `json:"accounts"`
			Errors   []struct {
				ID      int    `json:"id"`
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			continue
		}

		for key, value := range result.Accounts {
			merged[key] = value
		}
		for _, item := range result.Errors {
			_ = s.repo.MarkError(ctx, item.ID, item.Message)
		}
	}

	return merged, nil
}

// GetCredentialsSchema 获取指定平台凭证字段 schema。
func (s *Service) GetCredentialsSchema(platform string) CredentialSchema {
	if accountTypes := s.plugins.GetAccountTypes(platform); len(accountTypes) > 0 {
		result := CredentialSchema{
			AccountTypes: make([]AccountType, 0, len(accountTypes)),
		}
		for _, item := range accountTypes {
			accountType := AccountType{
				Key:         item.Key,
				Label:       item.Label,
				Description: item.Description,
			}
			for _, field := range item.Fields {
				accountType.Fields = append(accountType.Fields, CredentialField{
					Key:          field.Key,
					Label:        field.Label,
					Type:         field.Type,
					Required:     field.Required,
					Placeholder:  field.Placeholder,
					EditDisabled: field.EditDisabled,
				})
			}
			result.AccountTypes = append(result.AccountTypes, accountType)
		}
		if len(result.AccountTypes) > 0 {
			result.Fields = result.AccountTypes[0].Fields
		}
		return result
	}

	if fields := s.plugins.GetCredentialFields(platform); len(fields) > 0 {
		result := CredentialSchema{
			Fields: make([]CredentialField, 0, len(fields)),
		}
		for _, field := range fields {
			result.Fields = append(result.Fields, CredentialField{
				Key:          field.Key,
				Label:        field.Label,
				Type:         field.Type,
				Required:     field.Required,
				Placeholder:  field.Placeholder,
				EditDisabled: field.EditDisabled,
			})
		}
		return result
	}

	fallback := map[string]CredentialSchema{
		"openai": {
			Fields: []CredentialField{
				{Key: "api_key", Label: "API Key", Type: "password", Required: true, Placeholder: "sk-..."},
				{Key: "base_url", Label: "Base URL", Type: "text", Required: false, Placeholder: "https://api.openai.com/v1"},
			},
		},
		"claude": {
			Fields: []CredentialField{
				{Key: "api_key", Label: "API Key", Type: "password", Required: true, Placeholder: "sk-ant-..."},
				{Key: "base_url", Label: "Base URL", Type: "text", Required: false, Placeholder: "https://api.anthropic.com"},
			},
		},
		"gemini": {
			Fields: []CredentialField{
				{Key: "api_key", Label: "API Key", Type: "password", Required: true, Placeholder: "AIza..."},
			},
		},
	}

	if schema, ok := fallback[platform]; ok {
		return schema
	}

	return CredentialSchema{
		Fields: []CredentialField{
			{Key: "api_key", Label: "API Key", Type: "password", Required: true},
			{Key: "base_url", Label: "Base URL", Type: "text", Required: false},
		},
	}
}

// RefreshQuota 刷新账号额度。
func (s *Service) RefreshQuota(ctx context.Context, id int) (QuotaRefreshResult, error) {
	item, err := s.repo.FindByID(ctx, id, LoadOptions{})
	if err != nil {
		return QuotaRefreshResult{}, err
	}

	inst := s.plugins.GetPluginByPlatform(item.Platform)
	if inst == nil || inst.Gateway == nil {
		return QuotaRefreshResult{}, ErrQuotaRefreshUnsupported
	}

	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	quota, err := inst.Gateway.QueryQuota(callCtx, cloneStringMap(item.Credentials))
	if err != nil {
		return QuotaRefreshResult{}, fmt.Errorf("刷新额度失败: %w", err)
	}

	credentials := cloneStringMap(item.Credentials)
	updated := false
	for key, value := range quota.Extra {
		if value != "" && credentials[key] != value {
			credentials[key] = value
			updated = true
		}
	}
	if quota.ExpiresAt != "" {
		credentials["subscription_active_until"] = quota.ExpiresAt
		updated = true
	}
	if updated {
		if err := s.repo.SaveCredentials(ctx, id, credentials); err != nil {
			return QuotaRefreshResult{}, err
		}
	}

	return QuotaRefreshResult{
		PlanType:                credentials["plan_type"],
		Email:                   credentials["email"],
		SubscriptionActiveUntil: credentials["subscription_active_until"],
	}, nil
}

// GetStats 获取单个账号统计。
func (s *Service) GetStats(ctx context.Context, id int, query StatsQuery) (StatsResult, error) {
	item, err := s.repo.FindByID(ctx, id, LoadOptions{})
	if err != nil {
		return StatsResult{}, err
	}

	startDate, endDate, err := ResolveStatsRange(s.now(), query)
	if err != nil {
		return StatsResult{}, err
	}

	logs, err := s.repo.FindUsageLogs(ctx, id, startDate, endDate)
	if err != nil {
		return StatsResult{}, err
	}

	return BuildStatsResult(item, logs, s.now(), startDate, endDate), nil
}

func buildProxyURL(proxyInfo *Proxy) string {
	if proxyInfo == nil {
		return ""
	}
	if proxyInfo.Username != "" {
		return fmt.Sprintf("%s://%s:%s@%s:%d", proxyInfo.Protocol, proxyInfo.Username, proxyInfo.Password, proxyInfo.Address, proxyInfo.Port)
	}
	return fmt.Sprintf("%s://%s:%d", proxyInfo.Protocol, proxyInfo.Address, proxyInfo.Port)
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}
