package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"

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

const autoQuotaRefreshInterval = 6 * time.Hour

// StateWriter 管理员巡检场景下对账号状态的写入口。
// 由 scheduler 包实现；让 account service 不直接依赖 scheduler。
type StateWriter interface {
	// MarkRateLimited 把账号打入 rate_limited 状态直到 until。
	MarkRateLimited(ctx context.Context, accountID int, until time.Time, reason string)
	// ClearRateLimited 账号已从限流中恢复，回到 active。
	ClearRateLimited(ctx context.Context, accountID int)
	// ClearRateLimitMarkers 清除账号上的临时限流标记。
	ClearRateLimitMarkers(ctx context.Context, accountID int) int
	// MarkDisabled 永久禁用（凭证失效等，需要人工重新验证）。
	MarkDisabled(ctx context.Context, accountID int, reason string)
	// ManualRecover 手动恢复到 active 并清除路由缓存。
	ManualRecover(ctx context.Context, accountID int) error
	// ManualDisable 手动禁用并清除路由缓存。
	ManualDisable(ctx context.Context, accountID int, reason string) error
}

type Service struct {
	repo        Repository
	plugins     PluginCatalog
	concurrency ConcurrencyReader
	stateWriter StateWriter
	now         func() time.Time

	usageMu    sync.RWMutex
	usageCache map[string]*usageCacheEntry
}

// NewService 创建账号服务。
// stateWriter 可传 nil（测试场景）；nil 时额度巡检不会主动标记账号状态。
func NewService(repo Repository, plugins PluginCatalog, concurrency ConcurrencyReader, stateWriter StateWriter) *Service {
	return &Service{
		repo:        repo,
		plugins:     plugins,
		concurrency: concurrency,
		stateWriter: stateWriter,
		now:         time.Now,
		usageCache:  make(map[string]*usageCacheEntry),
	}
}

// StartQuotaRefreshLoop periodically refreshes OAuth account plan metadata written into credentials.
func (s *Service) StartQuotaRefreshLoop(ctx context.Context) {
	go s.runQuotaRefreshLoop(ctx)
}

func (s *Service) runQuotaRefreshLoop(ctx context.Context) {
	s.refreshAllOAuthQuotas(ctx)

	ticker := time.NewTicker(autoQuotaRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshAllOAuthQuotas(ctx)
		}
	}
}

func (s *Service) refreshAllOAuthQuotas(ctx context.Context) {
	logger := sdk.LoggerFromContext(ctx)
	accounts, err := s.repo.ListAll(ctx, ListFilter{})
	if err != nil {
		logger.Warn("account_quota_auto_refresh_list_failed", sdk.LogFieldError, err)
		return
	}

	success, failed, skipped := 0, 0, 0
	for _, item := range accounts {
		if ctx.Err() != nil {
			return
		}
		if !shouldAutoRefreshQuota(item) {
			skipped++
			continue
		}
		if _, err := s.refreshQuota(ctx, item, false); err != nil {
			failed++
			logger.Warn("account_quota_auto_refresh_failed",
				sdk.LogFieldAccountID, item.ID,
				sdk.LogFieldPlatform, item.Platform,
				sdk.LogFieldError, err)
			continue
		}
		success++
	}

	logger.Info("account_quota_auto_refresh_complete", "success", success, "failed", failed, "skipped", skipped)
}

func shouldAutoRefreshQuota(item Account) bool {
	if len(item.Credentials) == 0 {
		return false
	}
	accountType := strings.ToLower(strings.TrimSpace(item.Type))
	if accountType == "apikey" || accountType == "api_key" {
		return false
	}
	return strings.TrimSpace(item.Credentials["access_token"]) != "" ||
		strings.TrimSpace(item.Credentials["refresh_token"]) != ""
}

// List 查询账号列表。
func (s *Service) List(ctx context.Context, filter ListFilter) (ListResult, error) {
	page, pageSize := NormalizePage(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize
	filter = s.normalizeListFilter(filter)

	accounts, total, err := s.repo.List(ctx, filter)
	if err != nil {
		return ListResult{}, err
	}

	ids := make([]int, 0, len(accounts))
	openaiIDs := make([]int, 0, len(accounts))
	for _, item := range accounts {
		ids = append(ids, item.ID)
		// 生图统计仅 OpenAI 平台账号需要：其它平台没有 image endpoint，跑 SQL 也是 0 行白浪费。
		if item.Platform == "openai" {
			openaiIDs = append(openaiIDs, item.ID)
		}
	}
	counts := s.concurrency.GetCurrentCounts(ctx, ids)
	for index := range accounts {
		accounts[index].CurrentConcurrency = counts[accounts[index].ID]
	}

	// 生图请求计数：今日 + 累计。BatchImageStats 失败不阻断主响应（运维路径优先稳定）。
	if len(openaiIDs) > 0 {
		todayStart := timezone.StartOfDay(s.now().In(time.Local))
		if imageStats, err := s.repo.BatchImageStats(ctx, openaiIDs, todayStart); err == nil {
			for index := range accounts {
				if accounts[index].Platform != "openai" {
					continue
				}
				if entry, ok := imageStats[accounts[index].ID]; ok {
					stats := entry
					accounts[index].ImageStats = &stats
				} else {
					// 没记录：显式给个零值结构，让前端拿到 today=0/total=0 而不是 nil（区分"没数据"和"非 openai"）
					accounts[index].ImageStats = &AccountImageStats{}
				}
			}
		}
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
	logger := sdk.LoggerFromContext(ctx)
	account, err := s.repo.Create(ctx, input)
	if err != nil {
		logger.Error("account_credential_persist_failed",
			sdk.LogFieldPlatform, input.Platform,
			"type", input.Type,
			"name", input.Name,
			sdk.LogFieldError, err)
		return account, err
	}
	logger.Info("account_created",
		sdk.LogFieldAccountID, account.ID,
		sdk.LogFieldPlatform, account.Platform,
		"type", account.Type,
		"name", account.Name)
	s.InvalidateUsageCache("") // 新账号创建后清除用量缓存
	return account, err
}

// ExportAll 查询符合筛选条件的全部账号（用于导出，不分页、不带并发计数）。
func (s *Service) ExportAll(ctx context.Context, filter ListFilter) ([]Account, error) {
	filter = s.normalizeListFilter(filter)
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
	logger := sdk.LoggerFromContext(ctx)
	updated, err := s.repo.Update(ctx, id, input)
	if err != nil {
		logger.Error("account_credential_persist_failed",
			sdk.LogFieldAccountID, id,
			sdk.LogFieldError, err)
		return updated, err
	}
	switch {
	case input.State != nil:
		logger.Info("account_status_changed",
			sdk.LogFieldAccountID, id,
			"state", *input.State)
	case input.MaxConcurrency != nil || input.RateMultiplier != nil:
		logger.Info("account_quota_updated",
			sdk.LogFieldAccountID, id)
	}
	return updated, err
}

// Delete 删除账号。
func (s *Service) Delete(ctx context.Context, id int) error {
	logger := sdk.LoggerFromContext(ctx)
	err := s.repo.Delete(ctx, id)
	if err != nil {
		logger.Error("account_credential_persist_failed",
			sdk.LogFieldAccountID, id,
			"op", "delete",
			sdk.LogFieldError, err)
		return err
	}
	logger.Info("account_deleted", sdk.LogFieldAccountID, id)
	s.InvalidateUsageCache("")
	return err
}

// BulkUpdate 批量更新账号。逐条执行并收集每个账号的成功/失败信息，允许部分成功。
// group_ids 为整体替换：若提供则覆盖账号原有分组，未提供则不触碰。
func (s *Service) BulkUpdate(ctx context.Context, input BulkUpdateInput) BulkResult {
	result := BulkResult{Results: make([]BulkResultItem, 0, len(input.IDs))}
	for _, id := range input.IDs {
		patch := UpdateInput{
			State:          input.State,
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

// ToggleScheduling 快速切换账号调度状态。active ↔ disabled。
// 其它中间态（rate_limited / degraded）一律视为"非 disabled"，切换后目标 = disabled。
//
// 通过 StateWriter 走状态机路径，确保路由缓存立即失效——
// 旧实现直接写 repo 绕过状态机，导致缓存里的旧快照让账号"自己起来"。
func (s *Service) ToggleScheduling(ctx context.Context, id int) (ToggleResult, error) {
	logger := sdk.LoggerFromContext(ctx)
	item, err := s.repo.FindByID(ctx, id, LoadOptions{})
	if err != nil {
		logger.Error("account_lookup_failed",
			sdk.LogFieldAccountID, id,
			sdk.LogFieldError, err)
		return ToggleResult{}, err
	}

	var newState string
	if item.State == "disabled" {
		newState = "active"
		if s.stateWriter != nil {
			if err := s.stateWriter.ManualRecover(ctx, id); err != nil {
				logger.Error("account_manual_recover_failed",
					sdk.LogFieldAccountID, id, sdk.LogFieldError, err)
				return ToggleResult{}, err
			}
		}
	} else {
		newState = "disabled"
		if s.stateWriter != nil {
			if err := s.stateWriter.ManualDisable(ctx, id, "手动关闭"); err != nil {
				logger.Error("account_manual_disable_failed",
					sdk.LogFieldAccountID, id, sdk.LogFieldError, err)
				return ToggleResult{}, err
			}
		}
	}

	logger.Info("account_status_changed",
		sdk.LogFieldAccountID, id,
		"state", newState)
	return ToggleResult{ID: id, State: newState}, nil
}

// PrepareConnectivityTest 准备账号连通性测试。
func (s *Service) PrepareConnectivityTest(ctx context.Context, id int, modelID string) (*ConnectivityTest, error) {
	logger := sdk.LoggerFromContext(ctx)
	item, err := s.repo.FindByID(ctx, id, LoadOptions{WithProxy: true})
	if err != nil {
		logger.Error("account_lookup_failed",
			sdk.LogFieldAccountID, id,
			sdk.LogFieldError, err)
		return nil, err
	}

	inst := s.plugins.GetPluginByPlatform(item.Platform)
	if inst == nil || inst.Gateway == nil {
		logger.Warn("account_credential_validation_failed",
			sdk.LogFieldAccountID, id,
			sdk.LogFieldPlatform, item.Platform,
			sdk.LogFieldReason, "plugin_not_found")
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

	// X-Airgate-Internal 让下游网关（如 gateway-claude 的 claude_code_only 开关）
	// 能识别这是管理后台自家的探测流量，跳过面向外部客户端的身份闸。
	forwardReq := &sdk.ForwardRequest{
		Account: &sdk.Account{
			ID:          int64(item.ID),
			Name:        item.Name,
			Platform:    item.Platform,
			Type:        item.Type,
			Credentials: cloneStringMap(item.Credentials),
			ProxyURL:    buildProxyURL(item.Proxy),
		},
		Body: testBody,
		Headers: http.Header{
			"Content-Type":       {"application/json"},
			"X-Airgate-Internal": {"test"},
		},
		Model:  modelID,
		Stream: true,
	}

	return &ConnectivityTest{
		AccountName: item.Name,
		AccountType: item.Type,
		ModelID:     modelID,
		run: func(runCtx context.Context, writer http.ResponseWriter) error {
			req := *forwardReq
			req.Writer = writer
			outcome, forwardErr := inst.Gateway.Forward(runCtx, &req)
			if forwardErr != nil {
				return forwardErr
			}
			// 测试路径严格判定：只有 OutcomeSuccess 算通过；任何其它 Kind 都报告失败。
			// 正常请求路径的"4xx 透传给客户端"在这里被故意跳过——测试就是要把
			// invalid api key / rate limit / upstream 5xx 都暴露出来。
			if outcome.Kind == sdk.OutcomeSuccess {
				return nil
			}
			msg := connectivityTestErrorMessage(outcome)
			if msg == "" && outcome.Upstream.StatusCode > 0 {
				msg = fmt.Sprintf("upstream returned HTTP %d", outcome.Upstream.StatusCode)
			}
			if msg == "" {
				msg = fmt.Sprintf("plugin returned %s", outcome.Kind)
			}
			return errors.New(msg)
		},
	}, nil
}

func connectivityTestErrorMessage(outcome sdk.ForwardOutcome) string {
	if msg := extractBodyError(outcome.Upstream.Body); msg != "" {
		return formatConnectivityHTTPMessage(outcome.Upstream.StatusCode, msg)
	}

	reason := strings.TrimSpace(outcome.Reason)
	if isConnectivityInternalDiagnostic(reason) {
		reason = ""
	}

	switch outcome.Kind {
	case sdk.OutcomeClientError:
		if reason == "" {
			reason = "请求参数或测试模型不被上游接受"
		}
		return formatConnectivityHTTPMessage(outcome.Upstream.StatusCode, reason)
	case sdk.OutcomeAccountRateLimited:
		if outcome.RetryAfter > 0 {
			return fmt.Sprintf("上游账号当前被限流，请在 %s 后重试", outcome.RetryAfter)
		}
		return "上游账号当前被限流，请稍后重试"
	case sdk.OutcomeAccountDead:
		if reason != "" {
			return "上游账号不可用: " + reason
		}
		return "上游账号不可用，请检查凭证或账号状态"
	case sdk.OutcomeStreamAborted:
		return "上游响应流中断，请稍后重试或查看上游日志"
	case sdk.OutcomeUpstreamTransient:
		if reason != "" {
			return "上游服务暂不可用: " + reason
		}
		return "上游未返回有效响应，请检查测试模型是否被该上游账号支持或查看上游日志"
	default:
		return reason
	}
}

func formatConnectivityHTTPMessage(statusCode int, msg string) string {
	if statusCode >= 400 && !strings.HasPrefix(strings.ToUpper(msg), "HTTP ") {
		return fmt.Sprintf("HTTP %d: %s", statusCode, msg)
	}
	return msg
}

func isConnectivityInternalDiagnostic(reason string) bool {
	return strings.Contains(reason, "上游流式响应为空") ||
		strings.Contains(reason, "未收到上游流式完成事件")
}

// extractBodyError 从上游错误响应 body 中提取人类可读的错误消息。
//
// Claude 等插件的 extractErrorMessage 只认 Anthropic 标准嵌套格式
// {"error":{"type":"...","message":"..."}}，对于以下变体会失败：
//   - 顶层 code+message：{"code":"INVALID_API_KEY","message":"Invalid API key"}
//     （某些池子转发器 / 反代会用这种格式）
//   - 顶层只有 message：{"message":"..."}
//   - error 是字符串：{"error":"some plain text"}
//   - error.message 但没有 error.type
//
// 这里把这些格式都覆盖一遍。返回空字符串表示无法提取。
func extractBodyError(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return ""
	}

	asString := func(v any) string {
		if s, ok := v.(string); ok {
			return s
		}
		return ""
	}

	// 1. {"error": {"type": "...", "message": "..."}} (Anthropic 标准)
	if errObj, ok := raw["error"].(map[string]any); ok {
		t := asString(errObj["type"])
		m := asString(errObj["message"])
		switch {
		case t != "" && m != "":
			return t + ": " + m
		case m != "":
			return m
		case t != "":
			return t
		}
	}

	// 2. {"error": "plain text"}
	if s := asString(raw["error"]); s != "" {
		return s
	}

	// 3. 顶层 {"code": "...", "message": "..."}（池子转发器常见格式）
	code := asString(raw["code"])
	msg := asString(raw["message"])
	switch {
	case code != "" && msg != "":
		return code + ": " + msg
	case msg != "":
		return msg
	case code != "":
		return code
	}

	return ""
}

// GetModels 获取账号平台的模型列表。
func (s *Service) GetModels(ctx context.Context, id int) ([]Model, error) {
	item, err := s.repo.FindByID(ctx, id, LoadOptions{WithProxy: true})
	if err != nil {
		return nil, err
	}

	if item.Platform == "openai" && item.Type == "apikey" {
		if models, err := getAPIKeyUpstreamModels(ctx, item); err == nil && len(models) > 0 {
			return models, nil
		}
	}

	rawModels := s.plugins.GetModels(item.Platform)
	models := make([]Model, 0, len(rawModels))
	for _, raw := range rawModels {
		models = append(models, Model{ID: raw.ID, Name: raw.Name})
	}
	return models, nil
}

func getAPIKeyUpstreamModels(ctx context.Context, item Account) ([]Model, error) {
	apiKey := strings.TrimSpace(item.Credentials["api_key"])
	if apiKey == "" {
		return nil, errors.New("missing api_key")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildAPIKeyModelsURL(item.Credentials["base_url"]), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client, err := accountHTTPClient(item.Proxy)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("/v1/models returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return parseOpenAIModelsResponse(body), nil
}

func buildAPIKeyModelsURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/models"
	}
	return baseURL + "/v1/models"
}

func accountHTTPClient(proxyInfo *Proxy) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxyURL := buildProxyURL(proxyInfo); proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second}, nil
}

func parseOpenAIModelsResponse(body []byte) []Model {
	var payload struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	models := make([]Model, 0, len(payload.Data))
	seen := make(map[string]bool, len(payload.Data))
	for _, raw := range payload.Data {
		id := strings.TrimSpace(raw.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		name := strings.TrimSpace(raw.Name)
		if name == "" {
			name = id
		}
		models = append(models, Model{ID: id, Name: name})
	}
	return models
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

	// 每次调用都重新 seed 所有账号到 result。
	// 原因：上游缓存里只有"上次 populate 时发起过 quota 查询的账号"，error/disabled
	// 的账号不在上游调用里，不会出现在缓存里，enrichTodayStats 也遍历不到它们，
	// 前端就看不到今日统计。这一步 DB 开销很小（单列 + 平台索引），但保证用量数据
	// 对所有状态的账号都能持续显示（包括 error/disabled，便于事后排查）。
	s.ensureAccountsSeeded(ctx, platform, result)

	s.enrichTodayStats(ctx, result)
	return result, nil
}

// ensureAccountsSeeded 确保所有账号（不论 status）都在 merged 里有占位条目。
//
// 历史上这里只 seed status=active 账号，导致 error / disabled 账号在前端
// 用量列直接显示"-"。但"用量数据"本身是历史维度的统计（usage_logs 聚合、
// 上游 quota 快照），和当前能否调度无关——账号即便暂时不可调度，运维也
// 需要看到它之前的消耗画像来定位问题（是不是 quota 打满导致的限流 / 封号）。
// 因此对所有账号都 seed 占位，enrichTodayStats 会把今日聚合写进来；上游
// quota 窗口由 getUpstreamUsage 决定要不要刷新。
//
// 不按 plugin meta 的 platform 列表去遍历 —— 那样会在插件尚未加载 / 加载失败
// 时漏 seed（进而导致前端的 usage_window 整列因为 accounts map 空而被隐藏）。
// 直接 ListAll 最稳，单次查询，DB 开销微不足道。
func (s *Service) ensureAccountsSeeded(ctx context.Context, platform string, merged map[string]any) {
	accounts, err := s.repo.ListAll(ctx, ListFilter{Platform: platform})
	if err != nil {
		return
	}
	for _, item := range accounts {
		key := strconv.Itoa(item.ID)
		if _, exists := merged[key]; !exists {
			merged[key] = map[string]any{}
		}
	}
}

// getUpstreamUsage 拿到上游账号的 quota 窗口 / credits（带 5 分钟 TTL 内存缓存）。
// 返回的 map 结构是 map[accountID]map[string]any，对齐插件 usage/accounts 的 JSON 形态。
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

		// 所有账号（含 disabled）都给 merged 占位，确保前端能看到用量窗口。
		// apikey 账号走不到下面的插件调用（没有上游 quota 接口），只显示 today_stats。
		// disabled 账号也查配额但不参与限流状态推导，避免覆盖手动关闭调度的状态。
		// 建立 accountID → 是否池子 的查询表，用于后面插件返回 errors
		// 时判断是否应该跳过 MarkError（池子账号永远不自动标错）
		poolByID := make(map[int]bool, len(accounts))
		for _, item := range accounts {
			poolByID[item.ID] = item.UpstreamIsPool
		}

		disabledIDs := make(map[int]bool, len(accounts))
		reqList := make([]accountUsageRequest, 0, len(accounts))
		for _, item := range accounts {
			key := strconv.Itoa(item.ID)
			if _, exists := merged[key]; !exists {
				merged[key] = map[string]any{}
			}
			if item.State == "disabled" {
				disabledIDs[item.ID] = true
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
		// 根据每个账号的 windows 反推限流恢复时间并持久化到 DB。
		// 已 disabled 的账号不参与限流状态推导，避免覆盖手动关闭调度的状态。
		activeAccounts := make(map[string]any, len(result.Accounts))
		for key, value := range result.Accounts {
			id, _ := strconv.Atoi(key)
			if !disabledIDs[id] {
				activeAccounts[key] = value
			}
		}
		s.persistRateLimitFromWindows(ctx, activeAccounts)

		for _, item := range result.Errors {
			// 池账号 / 已禁用账号不自动 MarkDisabled（避免覆盖人工关闭的 reason）。
			if poolByID[item.ID] || disabledIDs[item.ID] || s.stateWriter == nil {
				continue
			}
			s.stateWriter.MarkDisabled(ctx, item.ID, item.Message)
		}
	}

	s.usageMu.Lock()
	s.usageCache[cacheKey] = &usageCacheEntry{data: merged, fetchedAt: time.Now()}
	s.usageMu.Unlock()

	return merged, nil
}

// persistRateLimitFromWindows 扫描每个账号的 windows，把"有窗口已 100%"的情况
// 当作限流态通过状态机写入（与真实 429 走同一入口）。
// 插件可在 window 上返回 ignore_limit=true，表示该窗口仅用于展示，不参与调度限流。
//
//   - 任意窗口 used_percent >= 100 → MarkRateLimited 到所有已满窗口中最晚的 reset_at
//   - 所有窗口 < 100%              → ClearRateLimited，账号回到 active
func (s *Service) persistRateLimitFromWindows(ctx context.Context, accounts map[string]any) {
	if s.stateWriter == nil {
		return
	}
	now := time.Now()
	for key, raw := range accounts {
		accountMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		windowsRaw, ok := accountMap["windows"].([]any)
		if !ok {
			continue
		}
		id, err := strconv.Atoi(key)
		if err != nil {
			continue
		}
		var latestReset *time.Time
		anyMaxed := false
		for _, w := range windowsRaw {
			wm, ok := w.(map[string]any)
			if !ok {
				continue
			}
			if usageWindowIgnoresLimit(wm) {
				continue
			}
			pct, _ := wm["used_percent"].(float64)
			if pct < 100 {
				continue
			}
			anyMaxed = true
			reset := parseWindowReset(wm, now)
			if reset == nil {
				continue
			}
			if latestReset == nil || reset.After(*latestReset) {
				latestReset = reset
			}
		}

		switch {
		case anyMaxed && latestReset != nil:
			s.stateWriter.MarkRateLimited(ctx, id, *latestReset, "quota window saturated")
		case !anyMaxed:
			s.stateWriter.ClearRateLimited(ctx, id)
		}
	}
}

func usageWindowIgnoresLimit(w map[string]any) bool {
	if ignore, ok := w["ignore_limit"].(bool); ok && ignore {
		return true
	}
	if enforce, ok := w["enforce_limit"].(bool); ok && !enforce {
		return true
	}
	return false
}

// parseWindowReset 从 window map 解析 reset 时间。
// 优先使用绝对时间 reset_at（RFC3339），回退到相对秒数 reset_seconds。
func parseWindowReset(w map[string]any, now time.Time) *time.Time {
	if s, ok := w["reset_at"].(string); ok && s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return &t
		}
	}
	if secs, ok := w["reset_seconds"].(float64); ok && secs > 0 {
		t := now.Add(time.Duration(secs) * time.Second)
		return &t
	}
	return nil
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
	logger := sdk.LoggerFromContext(ctx)
	item, err := s.repo.FindByID(ctx, id, LoadOptions{})
	if err != nil {
		logger.Error("account_lookup_failed",
			sdk.LogFieldAccountID, id,
			sdk.LogFieldError, err)
		return QuotaRefreshResult{}, err
	}

	return s.refreshQuota(ctx, item, true)
}

func (s *Service) refreshQuota(ctx context.Context, item Account, probeUsage bool) (QuotaRefreshResult, error) {
	logger := sdk.LoggerFromContext(ctx)
	inst := s.plugins.GetPluginByPlatform(item.Platform)
	if inst == nil || inst.Gateway == nil {
		logger.Warn("account_credential_validation_failed",
			sdk.LogFieldAccountID, item.ID,
			sdk.LogFieldPlatform, item.Platform,
			sdk.LogFieldReason, "quota_refresh_unsupported")
		return QuotaRefreshResult{}, ErrQuotaRefreshUnsupported
	}

	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	quota, err := s.queryQuotaRefresh(callCtx, inst, item)
	if err != nil {
		if errors.Is(err, ErrQuotaRefreshUnsupported) {
			logger.Warn("account_credential_validation_failed",
				sdk.LogFieldAccountID, item.ID,
				sdk.LogFieldPlatform, item.Platform,
				sdk.LogFieldReason, "quota_refresh_unsupported")
			return QuotaRefreshResult{}, ErrQuotaRefreshUnsupported
		}
		if errors.Is(err, ErrReauthRequired) {
			logger.Warn("account_credential_validation_failed",
				sdk.LogFieldAccountID, item.ID,
				sdk.LogFieldPlatform, item.Platform,
				sdk.LogFieldReason, "reauth_required")
			return QuotaRefreshResult{}, ErrReauthRequired
		}
		logger.Error("account_credential_validation_failed",
			sdk.LogFieldAccountID, item.ID,
			sdk.LogFieldPlatform, item.Platform,
			sdk.LogFieldError, err)
		return QuotaRefreshResult{}, fmt.Errorf("刷新额度失败: %w", err)
	}

	// refresh_warning 是降级信号，不落库；取出后从 Extra 删除，避免写入 credentials。
	warning := quota.ReauthWarning
	if quota.Extra != nil {
		if w, ok := quota.Extra["refresh_warning"]; ok {
			warning = w
			delete(quota.Extra, "refresh_warning")
		}
	}

	credentials := cloneStringMap(item.Credentials)
	updated := false
	for key, value := range quota.Extra {
		if shouldPersistQuotaExtra(key, value) && credentials[key] != value {
			credentials[key] = value
			updated = true
		}
	}
	if quota.ExpiresAt != "" && credentials["subscription_active_until"] != quota.ExpiresAt {
		credentials["subscription_active_until"] = quota.ExpiresAt
		updated = true
	}
	if updated {
		if err := s.repo.SaveCredentials(ctx, item.ID, credentials); err != nil {
			logger.Error("account_credential_persist_failed",
				sdk.LogFieldAccountID, item.ID,
				"op", "save_credentials",
				sdk.LogFieldError, err)
			return QuotaRefreshResult{}, err
		}
	}

	if probeUsage {
		// 顺手触发一次用量强制重探测：账号额度刷新只负责刷订阅信息（plan_type / 过期时间），
		// 不动用量窗口缓存。用户点"刷新"时如果账号从没探测过，还是看不到 5h/7d 进度条。
		// 主动调一次 usage/probe 把窗口数据灌进插件内存缓存，下次 usage/accounts 就能读到。
		// 探测失败不阻断主流程——订阅信息已经成功返回，窗口数据下一个 5 分钟周期还会再试。
		s.triggerUsageProbe(ctx, inst, item.ID, credentials)
	}
	if s.stateWriter != nil {
		s.stateWriter.ClearRateLimitMarkers(ctx, item.ID)
	}

	return QuotaRefreshResult{
		PlanType:                credentials["plan_type"],
		Email:                   credentials["email"],
		SubscriptionActiveUntil: credentials["subscription_active_until"],
		ReauthWarning:           warning,
	}, nil
}

type quotaRefreshRequest struct {
	ID          int               `json:"id"`
	Credentials map[string]string `json:"credentials"`
}

type quotaRefreshResponse struct {
	ExpiresAt     string            `json:"expires_at"`
	Extra         map[string]string `json:"extra"`
	ErrorCode     string            `json:"error_code"`
	ErrorMessage  string            `json:"error_message"`
	ReauthWarning string            `json:"reauth_warning"`
}

func (s *Service) queryQuotaRefresh(ctx context.Context, inst *plugin.PluginInstance, item Account) (quotaRefreshResponse, error) {
	reqBody, err := json.Marshal(quotaRefreshRequest{
		ID:          item.ID,
		Credentials: cloneStringMap(item.Credentials),
	})
	if err != nil {
		return quotaRefreshResponse{}, err
	}

	statusCode, _, respBody, err := inst.Gateway.HandleHTTPRequest(ctx, "POST", "accounts/quota", "", nil, reqBody)
	if err != nil {
		return quotaRefreshResponse{}, err
	}
	if statusCode == http.StatusNotFound || statusCode == http.StatusMethodNotAllowed {
		return quotaRefreshResponse{}, ErrQuotaRefreshUnsupported
	}
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		var resp quotaRefreshResponse
		_ = json.Unmarshal(respBody, &resp)
		if resp.ErrorCode == "reauth_required" {
			return quotaRefreshResponse{}, ErrReauthRequired
		}
		return quotaRefreshResponse{}, ErrReauthRequired
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return quotaRefreshResponse{}, fmt.Errorf("刷新额度失败: HTTP %d", statusCode)
	}

	var resp quotaRefreshResponse
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return quotaRefreshResponse{}, fmt.Errorf("刷新额度失败: %w", err)
		}
	}
	if resp.ErrorCode == "reauth_required" {
		return quotaRefreshResponse{}, ErrReauthRequired
	}
	return resp, nil
}

// triggerUsageProbe 调用插件的 usage/probe 路径强制重探测单账号用量窗口。
// 只在插件声明支持时有效；失败只记日志，不影响调用方。
func shouldPersistQuotaExtra(key, value string) bool {
	if value != "" {
		return true
	}
	switch key {
	case "plan_type", "subscription_active_until":
		return true
	default:
		return false
	}
}

func (s *Service) triggerUsageProbe(ctx context.Context, inst *plugin.PluginInstance, id int, credentials map[string]string) {
	if inst == nil || inst.Gateway == nil {
		return
	}
	reqBody, _ := json.Marshal(map[string]any{
		"id":          id,
		"credentials": credentials,
	})
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	status, _, _, err := inst.Gateway.HandleHTTPRequest(probeCtx, "POST", "usage/probe", "", nil, reqBody)
	if err != nil || status != http.StatusOK {
		slog.Debug("account_usage_probe_failed",
			sdk.LogFieldAccountID, id,
			sdk.LogFieldStatus, status,
			sdk.LogFieldError, err)
	}
	// 清掉本进程的 usage 5 分钟缓存，让下一次 GetAccountUsage 重新从插件拉窗口数据。
	s.InvalidateUsageCache("")
}

// GetStats 获取单个账号统计。
func (s *Service) GetStats(ctx context.Context, id int, query StatsQuery) (StatsResult, error) {
	logger := sdk.LoggerFromContext(ctx)
	item, err := s.repo.FindByID(ctx, id, LoadOptions{})
	if err != nil {
		logger.Error("account_lookup_failed",
			sdk.LogFieldAccountID, id,
			sdk.LogFieldError, err)
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
		logger.Error("account_lookup_failed",
			sdk.LogFieldAccountID, id,
			"op", "find_usage_logs",
			sdk.LogFieldError, err)
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
