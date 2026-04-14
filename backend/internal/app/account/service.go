package account

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	sdk "github.com/DouDOU-start/airgate-sdk"

	"github.com/DouDOU-start/airgate-core/internal/pkg/timezone"
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
// usageCacheEntry 用量缓存条目
type usageCacheEntry struct {
	data      map[string]any
	fetchedAt time.Time
}

const usageCacheTTL = 5 * time.Minute

type Service struct {
	repo        Repository
	plugins     PluginCatalog
	concurrency ConcurrencyReader
	now         func() time.Time

	usageMu    sync.RWMutex
	usageCache map[string]*usageCacheEntry // platform -> cache
}

// NewService 创建账号服务。
func NewService(repo Repository, plugins PluginCatalog, concurrency ConcurrencyReader) *Service {
	return &Service{
		repo:        repo,
		plugins:     plugins,
		concurrency: concurrency,
		now:         time.Now,
		usageCache:  make(map[string]*usageCacheEntry),
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
	account, err := s.repo.Create(ctx, input)
	if err == nil {
		s.InvalidateUsageCache("") // 新账号创建后清除用量缓存
	}
	return account, err
}

// ExportAll 查询符合筛选条件的全部账号（用于导出，不分页、不带并发计数）。
func (s *Service) ExportAll(ctx context.Context, filter ListFilter) ([]Account, error) {
	return s.repo.ListAll(ctx, filter)
}

// Import 批量导入账号，逐条创建并收集失败信息（不使用事务，允许部分成功）。
func (s *Service) Import(ctx context.Context, items []CreateInput) ImportSummary {
	summary := ImportSummary{}
	for index, input := range items {
		input.GroupIDs = nil
		input.ProxyID = nil
		if _, err := s.repo.Create(ctx, input); err != nil {
			summary.Failed++
			summary.Errors = append(summary.Errors, ImportItemError{
				Index:   index,
				Name:    input.Name,
				Message: err.Error(),
			})
			continue
		}
		summary.Imported++
	}
	return summary
}

// Update 更新账号。
func (s *Service) Update(ctx context.Context, id int, input UpdateInput) (Account, error) {
	return s.repo.Update(ctx, id, input)
}

// Delete 删除账号。
func (s *Service) Delete(ctx context.Context, id int) error {
	err := s.repo.Delete(ctx, id)
	if err == nil {
		s.InvalidateUsageCache("")
	}
	return err
}

// BulkUpdate 批量更新账号。逐条执行并收集每个账号的成功/失败信息，允许部分成功。
// group_ids 为整体替换：若提供则覆盖账号原有分组，未提供则不触碰。
func (s *Service) BulkUpdate(ctx context.Context, input BulkUpdateInput) BulkResult {
	result := BulkResult{Results: make([]BulkResultItem, 0, len(input.IDs))}
	for _, id := range input.IDs {
		patch := UpdateInput{
			Status:         input.Status,
			Priority:       input.Priority,
			MaxConcurrency: input.MaxConcurrency,
			RateMultiplier: input.RateMultiplier,
		}
		if input.HasProxyID {
			patch.ProxyID = input.ProxyID
			patch.HasProxyID = true
		}
		if input.HasGroupIDs {
			patch.GroupIDs = input.GroupIDs
			patch.HasGroupIDs = true
		}
		if _, err := s.repo.Update(ctx, id, patch); err != nil {
			result.appendFailure(id, err)
			continue
		}
		result.appendSuccess(id)
	}
	return result
}

// BulkDelete 批量删除账号。
func (s *Service) BulkDelete(ctx context.Context, ids []int) BulkResult {
	result := BulkResult{Results: make([]BulkResultItem, 0, len(ids))}
	for _, id := range ids {
		if err := s.repo.Delete(ctx, id); err != nil {
			result.appendFailure(id, err)
			continue
		}
		result.appendSuccess(id)
	}
	return result
}

func (r *BulkResult) appendSuccess(id int) {
	r.Success++
	r.SuccessIDs = append(r.SuccessIDs, id)
	r.Results = append(r.Results, BulkResultItem{ID: id, Success: true})
}

func (r *BulkResult) appendFailure(id int, err error) {
	r.Failed++
	r.FailedIDs = append(r.FailedIDs, id)
	r.Results = append(r.Results, BulkResultItem{ID: id, Success: false, Error: err.Error()})
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

// InvalidateUsageCache 清除指定平台的用量缓存（创建/删除账号后调用）
func (s *Service) InvalidateUsageCache(platform string) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if platform == "" {
		s.usageCache = make(map[string]*usageCacheEntry)
	} else {
		delete(s.usageCache, platform)
	}
}

// GetAccountUsage 查询账号当前用量视图。
//
// 分层缓存策略（重要）：
//   - 上游 quota 数据（windows / credits）耗时（要调各平台 API），5 分钟内存缓存
//   - today_stats 是本地 usage_logs 聚合，**每次请求重新查询**，不进缓存
//
// 如果把 today_stats 和 upstream 数据一起缓存，刚写入的 usage_log 最多要等 5 分钟
// 才会在账号列表里显示，和"实时监控"的预期不符。
func (s *Service) GetAccountUsage(ctx context.Context, platform string) (map[string]any, error) {
	base, err := s.getUpstreamUsage(ctx, platform)
	if err != nil {
		return nil, err
	}

	// 浅克隆一份：后续把 today_stats 注入克隆体，避免污染缓存里那份"纯上游数据"。
	// 深度无需——我们只给外层 map 和每个 account map 加一个字段，不会动 windows / credits。
	result := cloneMergedShallow(base)

	// 每次调用都重新 seed 当前活跃账号到 result。
	// 原因：上游缓存里只有"上次 populate 时处于 active 的账号"，若某个账号之后才
	// 从 error/disabled 变回 active（或刚创建），就不会出现在缓存里，enrichTodayStats
	// 也遍历不到它，前端就看不到今日统计。这一步 DB 开销很小（单列 + 平台索引），
	// 但保证状态变更立即生效，不用等缓存过期。
	s.ensureActiveAccountsSeeded(ctx, platform, result)

	s.enrichTodayStats(ctx, result)
	return result, nil
}

// ensureActiveAccountsSeeded 确保所有当前活跃账号都在 merged 里有占位条目。
// 对于 apikey 类型的账号（没有上游 quota 接口），它们只靠 seed 占位才能走进
// enrichTodayStats，拿到今日聚合统计。
func (s *Service) ensureActiveAccountsSeeded(ctx context.Context, platform string, merged map[string]any) {
	var platforms []string
	if platform != "" {
		platforms = []string{platform}
	} else {
		for _, meta := range s.plugins.GetAllPluginMeta() {
			if meta.Platform != "" {
				platforms = append(platforms, meta.Platform)
			}
		}
	}

	for _, p := range platforms {
		accounts, err := s.repo.ListByPlatform(ctx, p)
		if err != nil || len(accounts) == 0 {
			continue
		}
		for _, item := range accounts {
			if item.Status != "active" {
				continue
			}
			key := strconv.Itoa(item.ID)
			if _, exists := merged[key]; !exists {
				merged[key] = map[string]any{}
			}
		}
	}
}

// getUpstreamUsage 拿到上游账号的 quota 窗口 / credits（带 5 分钟 TTL 内存缓存）。
// 返回的 map 结构是 map[accountID]map[string]any，对齐 sdk.AccountUsageInfo 的 JSON 形态。
// 这一层不包含 today_stats，由调用方单独注入。
func (s *Service) getUpstreamUsage(ctx context.Context, platform string) (map[string]any, error) {
	cacheKey := platform
	if cacheKey == "" {
		cacheKey = "__all__"
	}

	s.usageMu.RLock()
	if entry, ok := s.usageCache[cacheKey]; ok && time.Since(entry.fetchedAt) < usageCacheTTL {
		data := entry.data
		s.usageMu.RUnlock()
		return data, nil
	}
	s.usageMu.RUnlock()

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

		// 先给所有活跃账号（包括 apikey）在 merged 里留一个占位 entry，
		// 这样 enrichTodayStats 能遍历到它们。apikey 账号走不到下面的插件
		// 调用（没有 OAuth credentials），它们的占位会保持为空 map，
		// 前端渲染时跳过 windows/credits，只显示 today_stats。
		//
		// 只对"支持 OAuth quota 查询"的账号类型发 HTTP 请求：目前判定依据是
		// item.Type != "apikey" && 有 credentials，与原行为一致。
		// 建立 accountID → 是否池子 的查询表，用于后面插件返回 errors
		// 时判断是否应该跳过 MarkError（池子账号永远不自动标错）
		poolByID := make(map[int]bool, len(accounts))
		for _, item := range accounts {
			poolByID[item.ID] = item.UpstreamIsPool
		}

		reqList := make([]accountUsageRequest, 0, len(accounts))
		for _, item := range accounts {
			// 非活跃账号完全跳过
			if item.Status != "active" {
				continue
			}
			key := strconv.Itoa(item.ID)
			if _, exists := merged[key]; !exists {
				merged[key] = map[string]any{}
			}
			// apikey 类型不调插件（没有上游 quota 接口）
			if item.Type == "apikey" {
				continue
			}
			reqList = append(reqList, accountUsageRequest{
				ID:          item.ID,
				Credentials: cloneStringMap(item.Credentials),
			})
		}
		if len(reqList) == 0 {
			continue
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

		// 插件返回的 OAuth 账号会覆盖掉占位的空 map（插件响应里有完整的
		// windows / credits）；apikey 账号的占位保持原样。
		for key, value := range result.Accounts {
			merged[key] = value
		}
		for _, item := range result.Errors {
			// 池子账号在后台配额巡检里返回的错误（比如 "Upstream access forbidden"）
			// 只是池子暂时不可用，不代表本地账号坏了——不能标 error 永久关掉调度。
			// 非池子账号保持原有行为：标 error 并暴露给管理员排查。
			if poolByID[item.ID] {
				continue
			}
			_ = s.repo.MarkError(ctx, item.ID, item.Message)
		}
	}

	s.usageMu.Lock()
	s.usageCache[cacheKey] = &usageCacheEntry{data: merged, fetchedAt: time.Now()}
	s.usageMu.Unlock()

	return merged, nil
}

// cloneMergedShallow 浅克隆 map[accountID]accountMap 两层结构。
//
// 场景：上游缓存里存的是"纯上游数据"，返回前需要额外注入 today_stats，
// 但不能在缓存原件上打补丁（会造成并发读到半成品、或者今日 stats 被冻在缓存里）。
// 两层浅克隆就够了：我们只给外层 map 的每个 account entry 新增一个字段，
// 不会改动 windows / credits 等引用字段。
func cloneMergedShallow(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		if accountMap, ok := v.(map[string]any); ok {
			accountCopy := make(map[string]any, len(accountMap)+1)
			for ak, av := range accountMap {
				accountCopy[ak] = av
			}
			dst[k] = accountCopy
		} else {
			dst[k] = v
		}
	}
	return dst
}

// enrichTodayStats 为每个账号从 usage_logs 聚合**当天**（本地时区自然日）的
// 请求数 / token 数 / 账号成本 / 用户消耗，作为 account-level `today_stats` 字段
// 注入 merged 返回体。
//
// 和上游 quota 窗口（"5h"/"7d"/"7d_spark"）完全解耦：那些窗口来自插件上报的
// upstream API percentages，这里反映的是本地 gateway 视角的账号当天真实消耗。
//
// 实现：所有账号共用同一个 startTime（今天 00:00），一次批量聚合即可。
func (s *Service) enrichTodayStats(ctx context.Context, merged map[string]any) {
	if len(merged) == 0 {
		return
	}

	// 收集所有合法的 accountID
	accountIDs := make([]int, 0, len(merged))
	accountMaps := make(map[int]map[string]any, len(merged))
	for accountIDStr, raw := range merged {
		accountID, err := strconv.Atoi(accountIDStr)
		if err != nil {
			continue
		}
		accountMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		accountIDs = append(accountIDs, accountID)
		accountMaps[accountID] = accountMap
	}
	if len(accountIDs) == 0 {
		return
	}

	// 今天 00:00（服务器本地时区；time.Local 与 usage_logs.created_at 存储时区一致）
	todayStart := timezone.StartOfDay(s.now().In(time.Local))

	statsMap, err := s.repo.BatchWindowStats(ctx, accountIDs, todayStart)
	if err != nil {
		return
	}

	for accountID, accountMap := range accountMaps {
		stats, ok := statsMap[accountID]
		if !ok {
			// 没有任何请求时也回填 0，前端据此稳定展示"0 req / 0 / A $0.00 / U $0.00"
			stats = AccountWindowStats{}
		}
		accountMap["today_stats"] = map[string]any{
			"requests":     stats.Requests,
			"tokens":       stats.Tokens,
			"account_cost": stats.AccountCost,
			"user_cost":    stats.UserCost,
		}
	}
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

	loc := timezone.Resolve(query.TZ)
	now := s.now().In(loc)
	startDate, endDate, err := ResolveStatsRange(now, query)
	if err != nil {
		return StatsResult{}, err
	}

	logs, err := s.repo.FindUsageLogs(ctx, id, startDate, endDate)
	if err != nil {
		return StatsResult{}, err
	}

	return BuildStatsResult(item, logs, now, startDate, endDate), nil
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
