package account

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
	"github.com/DouDOU-start/airgate-core/internal/pkg/pagination"
	"github.com/DouDOU-start/airgate-core/internal/pkg/timezone"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
)

// Reloader 账号注册表重载窄接口（由 accountreg.Registry 实现）。
type Reloader interface {
	Reload(ctx context.Context) error
}

// ConcurrencyReader 账号在途并发数批量读取（由 scheduler.ConcurrencyManager 实现）。
type ConcurrencyReader interface {
	GetAccountCurrentCounts(ctx context.Context, accountIDs []int) map[int]int
}

// RPMReader 账号当前分钟 RPM 批量读取（由 scheduler.RPMCounter 实现）。
type RPMReader interface {
	GetAccountRPMs(ctx context.Context, accountIDs []int) map[int]int
}

// 创建时的领域默认值。
const (
	defaultWeight         = 10
	defaultMaxConcurrency = 10
	defaultRateMultiplier = 1.0
)

// UsageSink 用量落账窄接口（*billing.Recorder 满足）。
type UsageSink interface {
	Record(record billing.UsageRecord)
}

// PriceLookup 模型价目查询（pricing.Cache 满足）。
type PriceLookup interface {
	Get(model string) (pricing.Price, bool)
}

// Service 提供账号域用例编排。
// 加解密在 service；store 只存/取 credentials_enc + email。
type Service struct {
	repo        Repository
	secret      string
	reloader    Reloader
	statsRepo   UsageStatsRepository
	concurrency ConcurrencyReader
	rpm         RPMReader
	// 账号测试落账（可选：未注入则测试不写 usage_log）
	usageSink   UsageSink
	priceLookup PriceLookup
	calculator  *billing.Calculator
}

// NewService 创建账号服务。secret 与渠道相同（APIKeySecret）。
func NewService(repo Repository, secret string) *Service {
	return &Service{repo: repo, secret: secret}
}

// SetReloader 注入账号注册表重载器（server 装配阶段；nil 安全）。
func (s *Service) SetReloader(reloader Reloader) {
	if s == nil {
		return
	}
	s.reloader = reloader
}

// SetRuntimeStatsReaders 注入运行时指标读取器（server 装配；nil 安全，未注入时列表指标为 0）。
func (s *Service) SetRuntimeStatsReaders(concurrency ConcurrencyReader, rpm RPMReader) {
	if s == nil {
		return
	}
	s.concurrency = concurrency
	s.rpm = rpm
}

// SetTestUsageDeps 注入账号测试落账依赖（server 装配；nil 安全）。
// 对齐渠道测试：成功落 usage_log（source=account_test，不扣用户余额）。
func (s *Service) SetTestUsageDeps(sink UsageSink, prices PriceLookup, calc *billing.Calculator) {
	if s == nil {
		return
	}
	s.usageSink = sink
	s.priceLookup = prices
	if calc != nil {
		s.calculator = calc
	} else {
		s.calculator = billing.NewCalculator()
	}
}

func (s *Service) reloadRegistry(ctx context.Context) {
	if s == nil || s.reloader == nil {
		return
	}
	if err := s.reloader.Reload(ctx); err != nil {
		slog.Warn("account_registry_reload_failed", "error", err)
	}
}

// List 分页查询账号列表（解密凭证；脱敏由 handler 负责）。
// SortBy 为 concurrency/rpm 时走运行时指标排序；其余走 DB 排序。
func (s *Service) List(ctx context.Context, filter ListFilter) (ListResult, error) {
	page, pageSize := pagination.Normalize(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	if filter.IsRuntimeStatSort() {
		return s.listByRuntimeStat(ctx, filter)
	}

	list, total, err := s.repo.List(ctx, filter)
	if err != nil {
		logx.LoggerFromContext(ctx).Error("account_lookup_failed",
			"op", "list",
			logx.LogFieldError, err)
		return ListResult{}, err
	}
	if err := s.enrichAccounts(ctx, list); err != nil {
		return ListResult{}, err
	}
	s.attachMoneyStats(ctx, list, filter.TZ)
	return ListResult{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// listByRuntimeStat 按并发数/RPM 排序：筛选全量 id → Redis 指标 → 内存排序分页 → 回表详情。
func (s *Service) listByRuntimeStat(ctx context.Context, filter ListFilter) (ListResult, error) {
	logger := logx.LoggerFromContext(ctx)
	ids, err := s.repo.ListIDs(ctx, filter)
	if err != nil {
		logger.Error("account_lookup_failed",
			"op", "list_ids",
			logx.LogFieldError, err)
		return ListResult{}, err
	}
	if len(ids) > maxRuntimeStatSortCandidates {
		logger.Warn("account_sort_rejected",
			"reason", "too_many_sort_candidates",
			"sort_by", filter.SortBy,
			"candidates", len(ids))
		return ListResult{}, ErrTooManySortCandidates
	}

	var metrics map[int]int
	switch filter.SortBy {
	case SortByConcurrency:
		if s.concurrency != nil {
			metrics = s.concurrency.GetAccountCurrentCounts(ctx, ids)
		}
	case SortByRPM:
		if s.rpm != nil {
			metrics = s.rpm.GetAccountRPMs(ctx, ids)
		}
	}

	asc := filter.SortOrder == sortOrderAsc
	sort.SliceStable(ids, func(i, j int) bool {
		vi, vj := metrics[ids[i]], metrics[ids[j]]
		if vi == vj {
			return ids[i] > ids[j]
		}
		if asc {
			return vi < vj
		}
		return vi > vj
	})

	total := len(ids)
	start := (filter.Page - 1) * filter.PageSize
	if start > total {
		start = total
	}
	end := start + filter.PageSize
	if end > total {
		end = total
	}
	pageIDs := ids[start:end]

	list, err := s.repo.ListByIDs(ctx, pageIDs)
	if err != nil {
		logger.Error("account_lookup_failed",
			"op", "list_by_ids",
			logx.LogFieldError, err)
		return ListResult{}, err
	}
	list = reorderAccountsByIDs(list, pageIDs)
	if err := s.enrichAccounts(ctx, list); err != nil {
		return ListResult{}, err
	}
	s.attachMoneyStats(ctx, list, filter.TZ)
	return ListResult{
		List:     list,
		Total:    int64(total),
		Page:     filter.Page,
		PageSize: filter.PageSize,
	}, nil
}

// enrichAccounts 解密 + 用量/档位派生 + 运行时指标。
func (s *Service) enrichAccounts(ctx context.Context, list []Account) error {
	if err := s.decryptAccounts(list); err != nil {
		return err
	}
	for i := range list {
		list[i].Usage = UsageFromExtra(list[i].Extra)
		list[i].PlanType = resolvePlanType(list[i])
		list[i].SubscriptionActiveUntil = resolveSubscriptionActiveUntil(list[i])
		list[i].MaxRPM = maxRPMFromExtra(list[i].Extra)
		list[i].Models = modelsFromAccountExtra(list[i].Extra)
	}
	s.attachRuntimeStats(ctx, list)
	return nil
}

// attachMoneyStats 为列表批量填充今日/累计成本与收益（usage_logs.account_id）。
func (s *Service) attachMoneyStats(ctx context.Context, list []Account, tz string) {
	if s == nil || s.statsRepo == nil || len(list) == 0 {
		return
	}
	ids := make([]int, len(list))
	for i := range list {
		ids[i] = list[i].ID
	}
	todayStart := timezone.StartOfDay(time.Now().In(timezone.Resolve(tz)))
	stats, err := s.statsRepo.GetAccountMoneyStats(ctx, ids, todayStart)
	if err != nil {
		logx.LoggerFromContext(ctx).Warn("account_money_stats_failed", logx.LogFieldError, err)
		return
	}
	for i := range list {
		m := stats[list[i].ID]
		list[i].TotalCost = m.Cost
		list[i].TotalRevenue = m.Revenue
		list[i].TodayCost = m.TodayCost
		list[i].TodayRevenue = m.TodayRevenue
	}
}

// normalizeModelsList 去空白、去重，保序。
func normalizeModelsList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, m := range in {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// reorderAccountsByIDs 按 ids 顺序重排（ListByIDs 不保证顺序）。
func reorderAccountsByIDs(items []Account, ids []int) []Account {
	byID := make(map[int]Account, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	out := make([]Account, 0, len(ids))
	for _, id := range ids {
		if item, ok := byID[id]; ok {
			out = append(out, item)
		}
	}
	return out
}

// attachRuntimeStats 为列表填充在途并发 / 当前分钟 RPM（Redis 观测）。
func (s *Service) attachRuntimeStats(ctx context.Context, list []Account) {
	if len(list) == 0 {
		return
	}
	ids := make([]int, len(list))
	for i := range list {
		ids[i] = list[i].ID
	}
	var counts, rpms map[int]int
	if s.concurrency != nil {
		counts = s.concurrency.GetAccountCurrentCounts(ctx, ids)
	}
	if s.rpm != nil {
		rpms = s.rpm.GetAccountRPMs(ctx, ids)
	}
	for i := range list {
		id := list[i].ID
		if counts != nil {
			list[i].CurrentConcurrency = counts[id]
		}
		if rpms != nil {
			list[i].CurrentRPM = rpms[id]
		}
	}
}

func maxRPMFromExtra(extra map[string]any) int {
	if extra == nil {
		return 0
	}
	raw, ok := extra["max_rpm"]
	if !ok || raw == nil {
		return 0
	}
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err == nil {
			return n
		}
	}
	return 0
}

// FindByID 按 ID 查询并解密凭证。
func (s *Service) FindByID(ctx context.Context, id int, opts LoadOptions) (Account, error) {
	item, err := s.repo.FindByID(ctx, id, opts)
	if err != nil {
		logx.LoggerFromContext(ctx).Error("account_lookup_failed",
			"op", "find_by_id",
			logx.LogFieldAccountID, id,
			logx.LogFieldError, err)
		return Account{}, err
	}
	if err := s.decryptAccount(&item); err != nil {
		return Account{}, err
	}
	item.Usage = UsageFromExtra(item.Extra)
	item.PlanType = resolvePlanType(item)
	item.SubscriptionActiveUntil = resolveSubscriptionActiveUntil(item)
	item.MaxRPM = maxRPMFromExtra(item.Extra)
	item.Models = modelsFromAccountExtra(item.Extra)
	return item, nil
}

// Create 创建账号：明文凭证 → AES-GCM 落库；email 从 credentials["email"] 冗余写出。
func (s *Service) Create(ctx context.Context, input CreateInput) (Account, error) {
	logger := logx.LoggerFromContext(ctx)
	applyCreateDefaults(&input)
	input.Type = NormalizeAccountType(input.Type)
	// 账号池场景走渠道配置，账号侧不再维护 upstream_is_pool。
	input.UpstreamIsPool = false

	creds, err := prepareCreateCredentials(ctx, input)
	if err != nil {
		logger.Error("account_credential_prepare_failed",
			logx.LogFieldPlatform, input.Platform,
			"name", input.Name,
			logx.LogFieldError, err)
		return Account{}, err
	}
	input.Credentials = creds

	enc, email, err := s.prepareCredentials(input.Credentials)
	if err != nil {
		logger.Error("account_credential_encrypt_failed",
			logx.LogFieldPlatform, input.Platform,
			"name", input.Name,
			logx.LogFieldError, err)
		return Account{}, err
	}

	item, err := s.repo.Create(ctx, PersistCreateInput{
		Name:           input.Name,
		Platform:       input.Platform,
		Type:           input.Type,
		CredentialsEnc: enc,
		Email:          email,
		Priority:       input.Priority,
		Weight:         input.Weight,
		MaxConcurrency: input.MaxConcurrency,
		ProxyID:        input.ProxyID,
		RateMultiplier: input.RateMultiplier,
		GroupIDs:       input.GroupIDs,
		UpstreamIsPool: false,
		Extra:          input.Extra,
	})
	if err != nil {
		logger.Error("account_credential_persist_failed",
			"op", "create",
			logx.LogFieldPlatform, input.Platform,
			"type", input.Type,
			"name", input.Name,
			logx.LogFieldError, err)
		return Account{}, err
	}
	if err := s.decryptAccount(&item); err != nil {
		return Account{}, err
	}

	logger.Info("account_created",
		logx.LogFieldAccountID, item.ID,
		logx.LogFieldPlatform, item.Platform,
		"type", item.Type,
		"name", item.Name)
	s.reloadRegistry(ctx)
	return item, nil
}

// Update 更新账号；State 仅允许 active/disabled。
func (s *Service) Update(ctx context.Context, id int, input UpdateInput) (Account, error) {
	logger := logx.LoggerFromContext(ctx)

	if input.Type != nil {
		normalized := NormalizeAccountType(*input.Type)
		input.Type = &normalized
	}
	// 账号池走渠道，账号侧强制关闭 upstream_is_pool。
	off := false
	input.UpstreamIsPool = &off

	persist := PersistUpdateInput{
		Name:           input.Name,
		Type:           input.Type,
		Priority:       input.Priority,
		Weight:         input.Weight,
		MaxConcurrency: input.MaxConcurrency,
		RateMultiplier: input.RateMultiplier,
		UpstreamIsPool: input.UpstreamIsPool,
		GroupIDs:       input.GroupIDs,
		HasGroupIDs:    input.HasGroupIDs,
		ProxyID:        input.ProxyID,
		HasProxyID:     input.HasProxyID,
		Extra:          input.Extra,
		HasExtra:       input.HasExtra,
	}

	if input.State != nil {
		if err := validateManualState(*input.State); err != nil {
			return Account{}, err
		}
		persist.State = input.State
		persist.ClearStateUntil = true
		if *input.State == StateActive {
			persist.ClearErrorMsg = true
		}
	}

	// 模型白名单：合并进 extra.models（保留 usage 等其它 extra 字段）
	if input.Models != nil {
		existing, err := s.repo.FindByID(ctx, id, LoadOptions{})
		if err != nil {
			logger.Error("account_lookup_failed",
				"op", "update_models",
				logx.LogFieldAccountID, id,
				logx.LogFieldError, err)
			return Account{}, err
		}
		extra := cloneAnyMap(existing.Extra)
		if persist.HasExtra && persist.Extra != nil {
			// 若本次还带了其它 Extra 补丁，先叠上再写 models
			extra = cloneAnyMap(persist.Extra)
		}
		if extra == nil {
			extra = map[string]any{}
		}
		models := normalizeModelsList(*input.Models)
		if len(models) == 0 {
			delete(extra, "models")
		} else {
			extra["models"] = models
		}
		persist.Extra = extra
		persist.HasExtra = true
	}

	if input.Credentials != nil {
		// 编辑场景常只带 email/plan_type 等非敏感字段，或前端把脱敏 "***" 回传。
		// 必须与库内现有凭证合并，禁止整包覆盖把 token 抹掉。
		existing, err := s.repo.FindByID(ctx, id, LoadOptions{})
		if err != nil {
			logger.Error("account_lookup_failed",
				"op", "update_merge_credentials",
				logx.LogFieldAccountID, id,
				logx.LogFieldError, err)
			return Account{}, err
		}
		if err := s.decryptAccount(&existing); err != nil {
			return Account{}, err
		}
		merged := mergeCredentialsUpdate(existing.Credentials, input.Credentials)
		enc, email, err := s.prepareCredentials(merged)
		if err != nil {
			logger.Error("account_credential_encrypt_failed",
				logx.LogFieldAccountID, id,
				logx.LogFieldError, err)
			return Account{}, err
		}
		persist.CredentialsEnc = &enc
		persist.Email = &email
	}

	updated, err := s.repo.Update(ctx, id, persist)
	if err != nil {
		logger.Error("account_credential_persist_failed",
			"op", "update",
			logx.LogFieldAccountID, id,
			logx.LogFieldError, err)
		return Account{}, err
	}
	if err := s.decryptAccount(&updated); err != nil {
		return Account{}, err
	}
	updated.Usage = UsageFromExtra(updated.Extra)
	updated.PlanType = resolvePlanType(updated)
	updated.SubscriptionActiveUntil = resolveSubscriptionActiveUntil(updated)
	updated.MaxRPM = maxRPMFromExtra(updated.Extra)
	updated.Models = modelsFromAccountExtra(updated.Extra)

	switch {
	case input.State != nil:
		logger.Info("account_status_changed",
			logx.LogFieldAccountID, id,
			"state", *input.State)
	case input.MaxConcurrency != nil || input.RateMultiplier != nil || input.Weight != nil:
		logger.Info("account_quota_updated", logx.LogFieldAccountID, id)
	default:
		logger.Info("account_updated", logx.LogFieldAccountID, id)
	}
	s.reloadRegistry(ctx)
	return updated, nil
}

// Delete 删除账号。
func (s *Service) Delete(ctx context.Context, id int) error {
	logger := logx.LoggerFromContext(ctx)
	if err := s.repo.Delete(ctx, id); err != nil {
		logger.Error("account_credential_persist_failed",
			"op", "delete",
			logx.LogFieldAccountID, id,
			logx.LogFieldError, err)
		return err
	}
	logger.Info("account_deleted", logx.LogFieldAccountID, id)
	s.reloadRegistry(ctx)
	return nil
}

// ExportAll 导出符合筛选的全部账号（明文 Credentials，不分页）。
func (s *Service) ExportAll(ctx context.Context, filter ListFilter) ([]Account, error) {
	list, err := s.repo.ListAll(ctx, filter)
	if err != nil {
		logx.LoggerFromContext(ctx).Error("account_lookup_failed",
			"op", "export",
			logx.LogFieldError, err)
		return nil, err
	}
	if err := s.decryptAccounts(list); err != nil {
		return nil, err
	}
	return list, nil
}

// Import 批量导入：逐条 Create，允许部分成功；保留导出文件中的分组和代理绑定。
// Create 内部已 reload；此处不再额外 reload。
func (s *Service) Import(ctx context.Context, items []CreateInput) ImportResult {
	result := ImportResult{}
	for index, input := range items {
		if _, err := s.Create(ctx, input); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, ImportItemError{
				Index:   index,
				Name:    input.Name,
				Message: err.Error(),
			})
			continue
		}
		result.Imported++
	}
	return result
}

// ToggleScheduling active ↔ disabled；其它中间态一律切到 disabled。
func (s *Service) ToggleScheduling(ctx context.Context, id int) (ToggleResult, error) {
	logger := logx.LoggerFromContext(ctx)

	item, err := s.repo.FindByID(ctx, id, LoadOptions{})
	if err != nil {
		logger.Error("account_lookup_failed",
			"op", "toggle",
			logx.LogFieldAccountID, id,
			logx.LogFieldError, err)
		return ToggleResult{}, err
	}

	newState := StateDisabled
	if item.State == StateDisabled {
		newState = StateActive
	}
	state := newState
	_, err = s.Update(ctx, id, UpdateInput{State: &state})
	if err != nil {
		return ToggleResult{}, err
	}
	logger.Info("account_status_changed",
		logx.LogFieldAccountID, id,
		"state", newState)
	return ToggleResult{ID: id, State: newState}, nil
}

// BulkUpdate 批量更新；允许部分成功。
func (s *Service) BulkUpdate(ctx context.Context, input BulkUpdateInput) BulkResult {
	result := BulkResult{Results: make([]BulkResultItem, 0, len(input.IDs))}
	if input.State != nil {
		if err := validateManualState(*input.State); err != nil {
			for _, id := range input.IDs {
				result.appendFailure(id, err)
			}
			return result
		}
	}

	for _, id := range input.IDs {
		patch := UpdateInput{
			State:          input.State,
			Priority:       input.Priority,
			Weight:         input.Weight,
			MaxConcurrency: input.MaxConcurrency,
			RateMultiplier: input.RateMultiplier,
			Models:         input.Models,
		}
		if input.HasProxyID {
			patch.ProxyID = input.ProxyID
			patch.HasProxyID = true
		}
		if input.HasGroupIDs {
			patch.GroupIDs = input.GroupIDs
			patch.HasGroupIDs = true
		}
		if _, err := s.Update(ctx, id, patch); err != nil {
			result.appendFailure(id, err)
			continue
		}
		result.appendSuccess(id)
	}
	return result
}

// BulkDelete 批量删除。
func (s *Service) BulkDelete(ctx context.Context, ids []int) BulkResult {
	result := BulkResult{Results: make([]BulkResultItem, 0, len(ids))}
	for _, id := range ids {
		if err := s.Delete(ctx, id); err != nil {
			result.appendFailure(id, err)
			continue
		}
		result.appendSuccess(id)
	}
	return result
}

// RedactCredentials 返回脱敏后的凭证副本。
func RedactCredentials(creds map[string]string) map[string]string {
	if creds == nil {
		return nil
	}
	out := make(map[string]string, len(creds))
	for k, v := range creds {
		if isSensitiveCredentialKey(k) {
			if v == "" {
				out[k] = ""
			} else {
				out[k] = "***"
			}
			continue
		}
		out[k] = v
	}
	return out
}

// mergeCredentialsUpdate 将 patch 合并进现有凭证。
// 敏感键：空值、"***"、以及 "***..." 占位一律保留旧值（防止编辑表单/脱敏回写抹 token）。
// 非敏感键：patch 中出现则覆盖（含显式空串，便于清理 email 等）。
func mergeCredentialsUpdate(existing, patch map[string]string) map[string]string {
	out := cloneStringMap(existing)
	if out == nil {
		out = map[string]string{}
	}
	if patch == nil {
		return out
	}
	for k, v := range patch {
		if isSensitiveCredentialKey(k) {
			trimmed := strings.TrimSpace(v)
			if trimmed == "" || isRedactedCredentialPlaceholder(trimmed) {
				continue
			}
			out[k] = v
			continue
		}
		out[k] = v
	}
	return out
}

// isRedactedCredentialPlaceholder 判断是否为 API 脱敏占位（*** / ******** 等）。
func isRedactedCredentialPlaceholder(v string) bool {
	if v == "" {
		return false
	}
	// 常见：整段 "***"，或 "sk-***" 一类（全是 * 或去掉可见前缀后全 *）
	if strings.Trim(v, "*") == "" {
		return true
	}
	return v == "***" || strings.HasPrefix(v, "***")
}

// encryptCredentials 将凭证 map 序列化为 JSON 后 AES-GCM 加密。
func encryptCredentials(secret string, creds map[string]string) (string, error) {
	if creds == nil {
		creds = map[string]string{}
	}
	raw, err := json.Marshal(creds)
	if err != nil {
		return "", err
	}
	return auth.EncryptAPIKey(string(raw), secret)
}

// decryptCredentials 解密 AES-GCM 密文并反序列化为凭证 map。
func decryptCredentials(secret string, enc string) (map[string]string, error) {
	if enc == "" {
		return map[string]string{}, nil
	}
	plain, err := auth.DecryptAPIKey(enc, secret)
	if err != nil {
		return nil, err
	}
	var creds map[string]string
	if err := json.Unmarshal([]byte(plain), &creds); err != nil {
		return nil, err
	}
	if creds == nil {
		creds = map[string]string{}
	}
	return creds, nil
}

func (r *BulkResult) appendSuccess(id int) {
	r.Success++
	r.SuccessIDs = append(r.SuccessIDs, id)
	r.Results = append(r.Results, BulkResultItem{ID: id, Success: true})
}

func (r *BulkResult) appendFailure(id int, err error) {
	r.Failed++
	r.FailedIDs = append(r.FailedIDs, id)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	r.Results = append(r.Results, BulkResultItem{ID: id, Success: false, Error: msg})
}

func applyCreateDefaults(input *CreateInput) {
	// Priority 为 0 时保留 0（schema 默认 50 仅在未 Set 时生效，此处显式写入）。
	if input.Weight == 0 {
		input.Weight = defaultWeight
	}
	if input.MaxConcurrency == 0 {
		input.MaxConcurrency = defaultMaxConcurrency
	}
	if input.RateMultiplier == 0 {
		input.RateMultiplier = defaultRateMultiplier
	}
	if input.Type == "" {
		input.Type = TypeOAuth
	}
}

// prepareCreateCredentials 创建前规范化凭证。
// Codex OAuth 仅贴 refresh_token 时，用 RT 换 access_token（与 airgate-openai import-refresh 一致）。
func prepareCreateCredentials(ctx context.Context, input CreateInput) (map[string]string, error) {
	creds := input.Credentials
	if creds == nil {
		creds = map[string]string{}
	}
	// 复制，避免改调用方 map
	out := make(map[string]string, len(creds)+4)
	for k, v := range creds {
		if strings.TrimSpace(v) == "" {
			continue
		}
		out[k] = strings.TrimSpace(v)
	}

	platform := strings.ToLower(strings.TrimSpace(input.Platform))
	typ := NormalizeAccountType(input.Type)

	// Codex：仅有 RT 时自动换 token，类型仍为 oauth。
	if platform == "codex" && typ == TypeOAuth {
		rt := out["refresh_token"]
		at := out["access_token"]
		if rt != "" && at == "" {
			proxyURL := ""
			if input.Extra != nil {
				if v, ok := input.Extra["proxy_url"].(string); ok {
					proxyURL = v
				}
			}
			// 创建请求里的 ProxyURL 不在 credentials；代理出站由账号 proxy_id 绑定，
			// RT 换 token 阶段暂不强制代理（与手工粘贴 token 路径一致）。
			_ = proxyURL
			exchanged, err := ImportCodexRefreshToken(ctx, rt, "", out["client_id"])
			if err != nil {
				return nil, fmt.Errorf("codex refresh_token 导入失败: %w", err)
			}
			for k, v := range exchanged {
				if v != "" {
					out[k] = v
				}
			}
		}
	}

	if len(out) == 0 {
		return nil, ErrEmptyCredentials
	}
	return out, nil
}

func validateManualState(state string) error {
	if state == StateActive || state == StateDisabled {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalidState, state)
}

func (s *Service) prepareCredentials(creds map[string]string) (enc, email string, err error) {
	if creds == nil {
		creds = map[string]string{}
	}
	enc, err = encryptCredentials(s.secret, creds)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidCredentials, err)
	}
	email = creds["email"]
	return enc, email, nil
}

func (s *Service) decryptAccounts(list []Account) error {
	for i := range list {
		if err := s.decryptAccount(&list[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) decryptAccount(item *Account) error {
	if item == nil {
		return nil
	}
	if item.Proxy != nil && item.Proxy.Password != "" {
		password, err := auth.DecryptSecretValue(item.Proxy.Password, s.secret)
		if err != nil {
			return fmt.Errorf("解密代理密码失败: %w", err)
		}
		item.Proxy.Password = password
	}
	if item.CredentialsEnc == "" {
		if item.Credentials == nil {
			item.Credentials = map[string]string{}
		}
		return nil
	}
	creds, err := decryptCredentials(s.secret, item.CredentialsEnc)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCredentials, err)
	}
	item.Credentials = creds
	item.CredentialsEnc = ""
	return nil
}

func isSensitiveCredentialKey(key string) bool {
	for _, k := range SensitiveCredentialKeys {
		if k == key {
			return true
		}
	}
	return false
}
