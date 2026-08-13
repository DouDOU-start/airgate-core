package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
	"github.com/DouDOU-start/airgate-core/internal/pkg/pagination"
	"github.com/DouDOU-start/airgate-core/internal/pkg/timezone"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// Reloader 注册表重载窄接口（由 relay/registry.Registry 实现，可为 nil——测试时不接）。
type Reloader interface {
	Reload(ctx context.Context) error
}

// Tester 密钥端点连通性测试接口：走完整 relay adaptor 链路发起一次真实请求。
type Tester interface {
	// endpoint 仅对 openai 协议 key 生效（chat_completions / responses，空值默认前者）。
	Test(ctx context.Context, key ChannelKey, model, endpoint string) (latencyMs int, err error)
}

// HealthResetter 探针引擎健康态重置窄接口（由 probe.Engine 实现，可为 nil——测试时不接）。
type HealthResetter interface {
	ResetHealth(keyID int)
}

// ModelFetcher 上游拉取接口：模型列表与账户余额。
type ModelFetcher interface {
	FetchModels(ctx context.Context, channelType, baseURL, apiKey string) ([]string, error)
	// FetchBalance 经 key 探测上游余额（USD）；协议类型不等同于余额接口能力。
	FetchBalance(ctx context.Context, channelType, baseURL, apiKey string) (float64, error)
}

// ConcurrencyReader 物理凭证在途并发数批量读取（由 scheduler.ConcurrencyManager 实现）。
type ConcurrencyReader interface {
	GetKeyCurrentCounts(ctx context.Context, channelKeyIDs []int) map[int]int
}

// RPMReader 物理凭证当前分钟 RPM 批量读取（由 scheduler.RPMCounter 实现）。
type RPMReader interface {
	GetKeyRPMs(ctx context.Context, channelKeyIDs []int) map[int]int
}

// Service 提供渠道域用例编排。
type Service struct {
	repo        Repository
	secret      string
	reloader    Reloader
	tester      Tester
	fetcher     ModelFetcher
	concurrency ConcurrencyReader
	rpm         RPMReader
	stats       StatsReader
	healthReset HealthResetter
}

// NewService 创建渠道服务。secret 为 API Key 加密密钥（注入仿 apikey service）。
func NewService(repo Repository, secret string) *Service {
	return &Service{
		repo:    repo,
		secret:  secret,
		fetcher: DefaultModelFetcher{},
	}
}

// SetReloader 注入注册表重载器（server 装配阶段调用；nil 安全）。
func (s *Service) SetReloader(reloader Reloader) {
	s.reloader = reloader
}

// SetTester 注入密钥端点测试器（relay 管线落地后由 server 装配阶段调用）。
func (s *Service) SetTester(tester Tester) {
	s.tester = tester
}

// SetHealthResetter 注入探针健康态重置器（server 装配阶段调用；nil 安全）。
func (s *Service) SetHealthResetter(resetter HealthResetter) {
	s.healthReset = resetter
}

// SetRuntimeStatsReaders 注入运行时指标读取器（server 装配阶段调用；nil 安全，
// 未注入时列表指标保持 0 值）。
func (s *Service) SetRuntimeStatsReaders(concurrency ConcurrencyReader, rpm RPMReader) {
	s.concurrency = concurrency
	s.rpm = rpm
}

// SetStatsReader 注入渠道金额聚合读取器（server 装配阶段调用；nil 安全，
// 未注入时列表成本/收益保持 0 值）。
func (s *Service) SetStatsReader(stats StatsReader) {
	s.stats = stats
}

// List 查询渠道列表。
func (s *Service) List(ctx context.Context, filter ListFilter) (ListResult, error) {
	page, pageSize := pagination.Normalize(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.List(ctx, filter)
	if err != nil {
		return ListResult{}, err
	}
	for i := range list {
		s.decorate(&list[i])
	}
	s.attachRuntimeStats(ctx, list)
	s.attachMoneyStats(ctx, list, filter.TZ)
	return ListResult{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// ExportChannels 全量查询渠道（不分页、不装饰运行时/金额），供 JSON 导出用。
func (s *Service) ExportChannels(ctx context.Context, filter ListFilter) ([]Channel, error) {
	filter.Page = 1
	filter.PageSize = 100000

	list, _, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	for i := range list {
		s.decorate(&list[i])
	}
	return list, nil
}

// ImportChannels 批量导入渠道与密钥（按渠道分组，每渠道含 ≥1 把 key）。
func (s *Service) ImportChannels(ctx context.Context, items []ImportChannelInput) (ImportResult, error) {
	logger := logx.LoggerFromContext(ctx)
	var result ImportResult
	for _, item := range items {
		ch, err := s.repo.Create(ctx, CreateInput{Name: item.Name, BaseURL: item.BaseURL})
		if err != nil {
			return result, fmt.Errorf("创建渠道 %q 失败: %w", item.Name, err)
		}
		result.Channels++
		created := 0
		for _, k := range item.Keys {
			if strings.TrimSpace(k.APIKey) == "" {
				continue
			}
			if err := validateProtocolSet(&k); err != nil {
				return result, fmt.Errorf("渠道 %q 的协议组合无效: %w", item.Name, err)
			}
			cipher, err := s.encryptPlainKey(k.APIKey, true)
			if err != nil {
				return result, fmt.Errorf("渠道 %q 密钥加密失败: %w", item.Name, err)
			}
			k.APIKey = cipher
			if _, err := s.repo.CreateKey(ctx, ch.ID, k); err != nil {
				return result, fmt.Errorf("渠道 %q 下添加密钥 %q 失败: %w", item.Name, k.Name, err)
			}
			result.Keys++
			created++
		}
		logger.Info("channel_imported", "channel_id", ch.ID, "name", item.Name, "keys", created, "skipped", len(item.Keys)-created)
	}
	s.reloadRegistry(ctx)
	return result, nil
}

// ListKeys 密钥视图：跨渠道平铺分页查询 key（priority/weight 等排序），
// 统计口径与渠道视图一致，仅不做渠道级汇总。
func (s *Service) ListKeys(ctx context.Context, filter KeyListFilter) (KeyListResult, error) {
	page, pageSize := pagination.Normalize(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.ListKeys(ctx, filter)
	if err != nil {
		return KeyListResult{}, err
	}
	for i := range list {
		list[i].APIKeyHint = s.keyHint(list[i].APIKey)
	}
	s.attachRuntimeStatsToKeys(ctx, list)
	s.attachMoneyStatsToKeys(ctx, list, filter.TZ)
	return KeyListResult{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// attachMoneyStats 为列表页各 key 批量填充成本收益（基于 usage_logs 按 key 聚合），
// 并把各 key 汇总到所属渠道（rollup）。
func (s *Service) attachMoneyStats(ctx context.Context, list []Channel, tz string) {
	if len(list) == 0 {
		return
	}
	// 先按各 key 的余额汇总渠道 Balance（即便无金额读取器也生效）。
	s.rollupBalance(list)

	var keyIDs []int
	for i := range list {
		for j := range list[i].Keys {
			keyIDs = append(keyIDs, list[i].Keys[j].ID)
		}
	}
	stats := s.fetchMoneyStats(ctx, keyIDs, tz)
	if stats == nil {
		return
	}
	for i := range list {
		var tc, tr, dc, dr float64
		for j := range list[i].Keys {
			m := stats[list[i].Keys[j].ID]
			list[i].Keys[j].TotalCost = m.Cost
			list[i].Keys[j].TotalRevenue = m.Revenue
			list[i].Keys[j].TodayCost = m.TodayCost
			list[i].Keys[j].TodayRevenue = m.TodayRevenue
			list[i].Keys[j].AvgFirstTokenMs = m.AvgFirstTokenMs
			tc += m.Cost
			tr += m.Revenue
			dc += m.TodayCost
			dr += m.TodayRevenue
		}
		list[i].TotalCost = tc
		list[i].TotalRevenue = tr
		list[i].TodayCost = dc
		list[i].TodayRevenue = dr
	}
}

// attachMoneyStatsToKeys 密钥视图：为平铺 key 列表批量填充成本收益（无需渠道汇总）。
func (s *Service) attachMoneyStatsToKeys(ctx context.Context, keys []ChannelKey, tz string) {
	keyIDs := make([]int, len(keys))
	for i := range keys {
		keyIDs[i] = keys[i].ID
	}
	stats := s.fetchMoneyStats(ctx, keyIDs, tz)
	if stats == nil {
		return
	}
	for i := range keys {
		m := stats[keys[i].ID]
		keys[i].TotalCost = m.Cost
		keys[i].TotalRevenue = m.Revenue
		keys[i].TodayCost = m.TodayCost
		keys[i].TodayRevenue = m.TodayRevenue
		keys[i].AvgFirstTokenMs = m.AvgFirstTokenMs
	}
}

// fetchMoneyStats 按 key ID 批量拉取金额统计；未注入读取器、无 key 或查询失败时返回 nil（调用方跳过填充）。
func (s *Service) fetchMoneyStats(ctx context.Context, keyIDs []int, tz string) map[int]MoneyStats {
	if s.stats == nil || len(keyIDs) == 0 {
		return nil
	}
	todayStart := timezone.StartOfDay(time.Now().In(timezone.Resolve(tz)))
	stats, err := s.stats.GetChannelKeyMoneyStats(ctx, keyIDs, todayStart)
	if err != nil {
		logx.LoggerFromContext(ctx).Warn("channel_key_money_stats_failed", logx.LogFieldError, err)
		return nil
	}
	return stats
}

// rollupBalance 把各 key 的余额汇总到渠道 Balance（求和）、BalanceUpdatedAt 取最新一次刷新。
func (s *Service) rollupBalance(list []Channel) {
	for i := range list {
		var total float64
		var latest *time.Time
		seenCredentials := map[int]struct{}{}
		for j := range list[i].Keys {
			k := list[i].Keys[j]
			if _, seen := seenCredentials[k.CredentialID]; seen {
				continue
			}
			seenCredentials[k.CredentialID] = struct{}{}
			total += k.Balance
			if k.BalanceUpdatedAt != nil && (latest == nil || k.BalanceUpdatedAt.After(*latest)) {
				latest = k.BalanceUpdatedAt
			}
		}
		list[i].Balance = total
		list[i].BalanceUpdatedAt = latest
	}
}

// attachRuntimeStats 为列表页各 key 批量填充运行时观测指标（在途并发 / 当前分钟 RPM）。
// 读取器未注入或 Redis 不可用时保持 0 值，不影响列表主流程。
func (s *Service) attachRuntimeStats(ctx context.Context, list []Channel) {
	var credentialIDs []int
	for i := range list {
		for j := range list[i].Keys {
			credentialIDs = append(credentialIDs, list[i].Keys[j].CredentialID)
		}
	}
	counts, rpms := s.fetchRuntimeStats(ctx, credentialIDs)
	for i := range list {
		for j := range list[i].Keys {
			id := list[i].Keys[j].CredentialID
			list[i].Keys[j].CurrentConcurrency = counts[id]
			list[i].Keys[j].CurrentRPM = rpms[id]
		}
	}
}

// attachRuntimeStatsToKeys 密钥视图：为平铺 key 列表批量填充运行时观测指标。
func (s *Service) attachRuntimeStatsToKeys(ctx context.Context, keys []ChannelKey) {
	credentialIDs := make([]int, len(keys))
	for i := range keys {
		credentialIDs[i] = keys[i].CredentialID
	}
	counts, rpms := s.fetchRuntimeStats(ctx, credentialIDs)
	for i := range keys {
		keys[i].CurrentConcurrency = counts[keys[i].CredentialID]
		keys[i].CurrentRPM = rpms[keys[i].CredentialID]
	}
}

// fetchRuntimeStats 按 credential_id 批量拉取运行时观测指标（在途并发 / 当前分钟 RPM）；
// 读取器未注入或 Redis 不可用时返回 nil map，读取仍安全（零值）。
func (s *Service) fetchRuntimeStats(ctx context.Context, keyIDs []int) (map[int]int, map[int]int) {
	if len(keyIDs) == 0 {
		return nil, nil
	}
	var counts, rpms map[int]int
	if s.concurrency != nil {
		counts = s.concurrency.GetKeyCurrentCounts(ctx, keyIDs)
	}
	if s.rpm != nil {
		rpms = s.rpm.GetKeyRPMs(ctx, keyIDs)
	}
	return counts, rpms
}

// Create 创建渠道（仅 name/base_url；key 建后单独添加）。
func (s *Service) Create(ctx context.Context, input CreateInput) (Channel, error) {
	logger := logx.LoggerFromContext(ctx)

	item, err := s.repo.Create(ctx, input)
	if err != nil {
		logger.Error("channel_persist_failed", "op", "create", "name", input.Name, logx.LogFieldError, err)
		return Channel{}, err
	}
	logger.Info("channel_created", "channel_id", item.ID, "name", item.Name)

	s.reloadRegistry(ctx)
	s.decorate(&item)
	return item, nil
}

// Update 更新渠道（partial，仅 name/base_url）。
func (s *Service) Update(ctx context.Context, id int, input UpdateInput) (Channel, error) {
	logger := logx.LoggerFromContext(ctx)

	item, err := s.repo.Update(ctx, id, input)
	if err != nil {
		logger.Error("channel_persist_failed", "op", "update", "channel_id", id, logx.LogFieldError, err)
		return Channel{}, err
	}

	s.reloadRegistry(ctx)
	s.decorate(&item)
	return item, nil
}

// AddKey 在指定渠道下新增一条物理凭证及其协议端点（明文密钥在本层加密）。
// 允许不绑定分组：未绑定分组的端点不会被任何分组调度到（registry.Pick 按分组过滤，空集合天然不命中）。
func (s *Service) AddKey(ctx context.Context, channelID int, key KeyInput) (ChannelKey, error) {
	logger := logx.LoggerFromContext(ctx)
	if err := validateProtocolSet(&key); err != nil {
		return ChannelKey{}, err
	}

	cipher, err := s.encryptPlainKey(key.APIKey, true)
	if err != nil {
		return ChannelKey{}, err
	}
	key.APIKey = cipher

	item, err := s.repo.CreateKey(ctx, channelID, key)
	if err != nil {
		logger.Error("channel_persist_failed", "op", "add_key", "channel_id", channelID, logx.LogFieldError, err)
		return ChannelKey{}, err
	}
	logger.Info("channel_key_added", "channel_id", channelID, "channel_key_id", item.ID, "type", item.Type)

	s.reloadRegistry(ctx)
	item.APIKeyHint = s.keyHint(item.APIKey)
	return item, nil
}

// UpdateKey 更新物理凭证共享配置和当前协议端点；Types 非 nil 时同步完整协议集合。
// APIKey 提供即加密替换（空串保持原密钥）；GroupIDs 非 nil 即整组替换（允许显式传空，
// 即解绑全部分组，未绑定分组的 key 不会被任何分组调度到），nil 表示不改动分组。
func (s *Service) UpdateKey(ctx context.Context, keyID int, key KeyInput) (ChannelKey, error) {
	logger := logx.LoggerFromContext(ctx)
	if key.Types != nil {
		if err := validateProtocolSet(&key); err != nil {
			return ChannelKey{}, err
		}
	}

	cipher, err := s.encryptPlainKey(key.APIKey, false)
	if err != nil {
		return ChannelKey{}, err
	}
	key.APIKey = cipher

	item, err := s.repo.UpdateKey(ctx, keyID, key)
	if err != nil {
		logger.Error("channel_persist_failed", "op", "update_key", "channel_key_id", keyID, logx.LogFieldError, err)
		return ChannelKey{}, err
	}

	s.reloadRegistry(ctx)
	item.APIKeyHint = s.keyHint(item.APIKey)
	return item, nil
}

// DeleteKey 删除一把 key。
func (s *Service) DeleteKey(ctx context.Context, keyID int) error {
	logger := logx.LoggerFromContext(ctx)
	if err := s.repo.DeleteKey(ctx, keyID); err != nil {
		logger.Error("channel_persist_failed", "op", "delete_key", "channel_key_id", keyID, logx.LogFieldError, err)
		return err
	}
	logger.Info("channel_key_deleted", "channel_key_id", keyID)

	s.reloadRegistry(ctx)
	return nil
}

// Delete 删除渠道（级联删除其 key）。
func (s *Service) Delete(ctx context.Context, id int) error {
	logger := logx.LoggerFromContext(ctx)
	if err := s.repo.Delete(ctx, id); err != nil {
		logger.Error("channel_persist_failed", "op", "delete", "channel_id", id, logx.LogFieldError, err)
		return err
	}
	logger.Info("channel_deleted", "channel_id", id)

	s.reloadRegistry(ctx)
	return nil
}

// BulkUpdate 批量启停/删除/调优先级（作用于选中渠道下的全部 key），返回受影响渠道数。
func (s *Service) BulkUpdate(ctx context.Context, input BulkUpdateInput) (int, error) {
	logger := logx.LoggerFromContext(ctx)

	switch input.Action {
	case BulkActionEnable, BulkActionDisable, BulkActionDelete:
	case BulkActionSetPriority:
		if input.Priority == nil {
			return 0, ErrInvalidBulkAction
		}
	default:
		return 0, ErrInvalidBulkAction
	}

	affected, err := s.repo.BulkUpdate(ctx, input)
	if err != nil {
		logger.Error("channel_persist_failed", "op", "bulk_"+input.Action, logx.LogFieldError, err)
		return 0, err
	}
	logger.Info("channel_bulk_updated", "action", input.Action, "affected", affected)

	s.reloadRegistry(ctx)
	return affected, nil
}

// Test 测试单把密钥端点连通性：走 relay adaptor 链路发一次真实请求。
// 成功后记录响应耗时；若 key 处于 disabled_auto 则恢复 enabled 并清 error_msg。
func (s *Service) Test(ctx context.Context, keyID int, model, endpoint string) (int, error) {
	logger := logx.LoggerFromContext(ctx)

	key, err := s.repo.FindKeyByID(ctx, keyID)
	if err != nil {
		return 0, err
	}
	if s.tester == nil {
		return 0, ErrTesterNotReady
	}

	if model == "" {
		model = key.TestModel
	}
	if model == "" && len(key.Models) > 0 {
		model = key.Models[0]
	}

	latency, err := s.tester.Test(ctx, key, model, endpoint)
	if err != nil {
		logger.Warn("channel_key_test_failed", "channel_key_id", keyID, "model", model, logx.LogFieldError, err)
		return 0, fmt.Errorf("%w: %v", ErrTestFailed, err)
	}

	if err := s.repo.UpdateKeyTestResult(ctx, keyID, latency, time.Now()); err != nil {
		logger.Warn("channel_persist_failed", "op", "test_result", "channel_key_id", keyID, logx.LogFieldError, err)
	}
	// 测试通过 → 自动禁用 key 恢复可用（手动禁用不恢复）。
	// 重读当前状态再判断：测试窗口（最长 30s）内管理员可能已改为手动禁用，
	// 凭测前快照恢复会静默推翻手动操作。
	if key.Status == StatusDisabledAuto {
		current, err := s.repo.FindKeyByID(ctx, keyID)
		if err != nil {
			logger.Warn("channel_persist_failed", "op", "test_recover_recheck", "channel_key_id", keyID, logx.LogFieldError, err)
		} else if current.Status == StatusDisabledAuto {
			if err := s.repo.UpdateKeyState(ctx, keyID, StatusEnabled, ""); err != nil {
				logger.Warn("channel_persist_failed", "op", "test_recover", "channel_key_id", keyID, logx.LogFieldError, err)
			} else {
				logger.Info("channel_key_recovered_by_test", "channel_key_id", keyID)
			}
		}
	}
	if key.CredentialStatus == StatusDisabledAuto {
		current, err := s.repo.FindKeyByID(ctx, keyID)
		if err == nil && current.CredentialStatus == StatusDisabledAuto {
			if err := s.repo.UpdateCredentialState(ctx, current.CredentialID, StatusEnabled, ""); err != nil {
				logger.Warn("channel_persist_failed", "op", "test_recover_credential", "credential_id", current.CredentialID, logx.LogFieldError, err)
			}
		}
	}
	// 恢复后同步清零健康态：残留的 suspended/计数会让探针状态机脱轨
	// （suspended 态下后续失败不再触发自动禁用，DB health 也停在 suspended）。
	if key.Status == StatusDisabledAuto || key.CredentialStatus == StatusDisabledAuto {
		if err := s.repo.UpdateKeyHealthState(ctx, keyID, HealthHealthy, 0, 0); err != nil {
			logger.Warn("channel_persist_failed", "op", "test_recover_health", "channel_key_id", keyID, logx.LogFieldError, err)
		}
		if s.healthReset != nil {
			s.healthReset.ResetHealth(keyID)
		}
	}

	s.reloadRegistry(ctx)
	return latency, nil
}

// FetchModels 从上游拉取模型列表（用指定密钥端点）。
func (s *Service) FetchModels(ctx context.Context, keyID int) ([]string, error) {
	logger := logx.LoggerFromContext(ctx)

	key, err := s.repo.FindKeyByID(ctx, keyID)
	if err != nil {
		return nil, err
	}
	if key.APIKey == "" {
		return nil, ErrNoAPIKey
	}
	apiKey, err := auth.DecryptAPIKey(key.APIKey, s.secret)
	if err != nil {
		logger.Error("channel_api_key_decrypt_failed", "channel_key_id", keyID, logx.LogFieldError, err)
		return nil, fmt.Errorf("%w: API Key 解密失败", ErrModelFetchFailed)
	}

	return s.fetchModels(ctx, key.Type, key.BaseURL, apiKey)
}

// FetchModelsWithKey 按给定连接参数（明文 key）拉取上游模型列表。
// 供 key 尚未保存时的预览拉取使用：表单填好 type/base_url/api_key 即可试拉，
// 不要求已落库，解开「保存要先有模型、拉模型要先保存」的死锁。
func (s *Service) FetchModelsWithKey(ctx context.Context, channelType, baseURL, apiKey string) ([]string, error) {
	return s.fetchModels(ctx, channelType, baseURL, apiKey)
}

// RefreshBalance 查询指定 key 的上游余额并落库返回（key 级）。
// 所有协议类型均可尝试；是否参与自动刷新由 balance_check_enabled 控制。
func (s *Service) RefreshBalance(ctx context.Context, keyID int) (float64, *time.Time, error) {
	logger := logx.LoggerFromContext(ctx)

	key, err := s.repo.FindKeyByID(ctx, keyID)
	if err != nil {
		return 0, nil, err
	}
	if key.APIKey == "" {
		return 0, nil, ErrNoAPIKey
	}
	apiKey, derr := auth.DecryptAPIKey(key.APIKey, s.secret)
	if derr != nil {
		logger.Warn("channel_api_key_decrypt_failed", "channel_key_id", keyID, logx.LogFieldError, derr)
		return 0, nil, fmt.Errorf("%w: %v", ErrBalanceFetchFailed, derr)
	}
	bal, berr := s.fetcher.FetchBalance(ctx, key.Type, key.BaseURL, apiKey)
	if berr != nil {
		logger.Warn("channel_fetch_balance_failed", "channel_key_id", keyID, logx.LogFieldError, berr)
		return 0, nil, fmt.Errorf("%w: %v", ErrBalanceFetchFailed, berr)
	}

	now := time.Now()
	if err := s.repo.UpdateKeyBalance(ctx, keyID, bal, now); err != nil {
		logger.Warn("channel_persist_failed", "op", "balance", "channel_key_id", keyID, logx.LogFieldError, err)
		return 0, nil, err
	}
	return bal, &now, nil
}

// fetchModels 拉取主体（无留痕）。
func (s *Service) fetchModels(ctx context.Context, channelType, baseURL, apiKey string) ([]string, error) {
	models, err := s.fetcher.FetchModels(ctx, channelType, baseURL, apiKey)
	if err != nil {
		logx.LoggerFromContext(ctx).Warn("channel_fetch_models_failed", "type", channelType, logx.LogFieldError, err)
		return nil, fmt.Errorf("%w: %v", ErrModelFetchFailed, err)
	}
	return models, nil
}

// LoadAllForRegistry 实现 registry.Loader：全量加载渠道及其 key，
// 逐 key 解密 api_key，产出运行时快照（每把 key 一份，携带所属渠道 base_url）。
func (s *Service) LoadAllForRegistry(ctx context.Context) ([]registry.ChannelKeySnapshot, error) {
	logger := logx.LoggerFromContext(ctx)

	items, err := s.repo.ListAll(ctx)
	if err != nil {
		return nil, err
	}

	var snaps []registry.ChannelKeySnapshot
	for _, ch := range items {
		for _, key := range ch.Keys {
			plain, err := auth.DecryptAPIKey(key.APIKey, s.secret)
			if err != nil {
				logger.Warn("channel_api_key_decrypt_failed", "channel_key_id", key.ID, logx.LogFieldError, err)
				continue
			}

			models := make(map[string]struct{}, len(key.Models))
			for _, m := range key.Models {
				models[m] = struct{}{}
			}
			groups := make(map[int]struct{}, len(key.GroupIDs))
			for _, g := range key.GroupIDs {
				groups[g] = struct{}{}
			}

			snaps = append(snaps, registry.ChannelKeySnapshot{
				KeyID:                  key.ID,
				CredentialID:           key.CredentialID,
				KeyName:                key.Name,
				ChannelID:              ch.ID,
				ChannelName:            ch.Name,
				BaseURL:                ch.BaseURL,
				Type:                   key.Type,
				APIKey:                 plain,
				Models:                 models,
				ModelMapping:           key.ModelMapping,
				ParamOverride:          key.ParamOverride,
				HeaderOverride:         key.HeaderOverride,
				Priority:               key.Priority,
				Weight:                 key.Weight,
				MaxConcurrency:         key.MaxConcurrency,
				MaxRPM:                 key.MaxRPM,
				CostRatio:              key.CostRatio,
				UpstreamRate:           key.UpstreamRate,
				UseUpstreamRateForCost: key.UpstreamRateEnabled && key.UseUpstreamRateForCost,
				Status:                 key.Status,
				CredentialStatus:       key.CredentialStatus,
				GroupIDs:               groups,
				TestModel:              key.TestModel,
				HealthStatus:           key.HealthStatus,
			})
		}
	}
	return snaps, nil
}

// PersistState 实现 registry.Persister：密钥端点调度状态异步落库。
func (s *Service) PersistState(ctx context.Context, keyID int, status string, errMsg string) error {
	return s.repo.UpdateKeyState(ctx, keyID, status, errMsg)
}

// PersistCredentialState 实现 registry 的可选凭证状态持久化接口。
func (s *Service) PersistCredentialState(ctx context.Context, credentialID int, status string, errMsg string) error {
	return s.repo.UpdateCredentialState(ctx, credentialID, status, errMsg)
}

// ---- 探针引擎适配器 ----

// TestKeyForProbe 探针引擎调用：对指定 key 发一次轻量测试请求。
// 探针模型优先级：probe_model → test_model → 首个 model。
func (s *Service) TestKeyForProbe(ctx context.Context, keyID int) error {
	key, err := s.repo.FindKeyByID(ctx, keyID)
	if err != nil {
		return err
	}
	if s.tester == nil {
		return ErrTesterNotReady
	}
	model := key.ProbeModel
	if model == "" {
		model = key.TestModel
	}
	if model == "" && len(key.Models) > 0 {
		model = key.Models[0]
	}
	_, err = s.tester.Test(ctx, key, model, "")
	return err
}

// SyncBalanceForProbe 探针引擎调用：刷新指定 key 的上游余额。
func (s *Service) SyncBalanceForProbe(ctx context.Context, keyID int) error {
	_, _, err := s.RefreshBalance(ctx, keyID)
	return err
}

// UpdateUpstreamRate 保存上游倍率探测结果。
func (s *Service) UpdateUpstreamRate(ctx context.Context, keyID int, rate float64, at time.Time) error {
	if err := s.repo.UpdateUpstreamRate(ctx, keyID, rate, at); err != nil {
		return err
	}
	s.reloadRegistry(ctx)
	return nil
}

// ProbeKeyBilling 探针引擎调用：查询上游的计费倍率。
//
// 兼容两种上游协议：
//   - AirGate：GET {base_url}/v1/airgate/billing → {"rate_multiplier": 1.5}
//   - Sub2API：GET {base_url}/v1/sub2api/billing → {"effective_rate_multiplier": 1.5}
//
// 路径可由 key.UpstreamRatePath 自定义，空串默认 /v1/airgate/billing。
func (s *Service) ProbeKeyBilling(ctx context.Context, keyID int) (float64, error) {
	key, err := s.repo.FindKeyByID(ctx, keyID)
	if err != nil {
		return 0, err
	}
	apiKey, err := auth.DecryptAPIKey(key.APIKey, s.secret)
	if err != nil {
		return 0, fmt.Errorf("decrypt api key: %w", err)
	}

	path := key.UpstreamRatePath
	if path == "" {
		path = "/v1/airgate/billing"
	}
	url := strings.TrimSuffix(key.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, fmt.Errorf("upstream billing probe: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		RateMultiplier          float64  `json:"rate_multiplier"`
		EffectiveRateMultiplier *float64 `json:"effective_rate_multiplier"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("upstream billing probe: decode: %w", err)
	}
	// sub2api 格式优先取 effective_rate_multiplier，兼容 AirGate 的 rate_multiplier
	if result.EffectiveRateMultiplier != nil {
		return *result.EffectiveRateMultiplier, nil
	}
	return result.RateMultiplier, nil
}

// validateProtocolSet 校验并去重同一物理凭证的协议集合。视频和音乐任务协议
// 生命周期与同步协议不同，暂不允许和其他协议共享同一凭证。
func validateProtocolSet(key *KeyInput) error {
	types := key.Types
	if types == nil {
		types = []string{key.Type}
	}
	allowed := map[string]struct{}{
		"openai_compatible": {},
		"anthropic":         {},
		"gemini":            {},
		"openai_video":      {},
		"suno":              {},
	}
	seen := make(map[string]struct{}, len(types))
	normalized := make([]string, 0, len(types))
	for _, channelType := range types {
		channelType = strings.TrimSpace(channelType)
		if _, ok := allowed[channelType]; !ok {
			return ErrInvalidProtocolSet
		}
		if _, ok := seen[channelType]; ok {
			continue
		}
		seen[channelType] = struct{}{}
		normalized = append(normalized, channelType)
	}
	if len(normalized) == 0 {
		return ErrInvalidProtocolSet
	}
	if len(normalized) > 1 {
		if _, ok := seen["openai_video"]; ok {
			return ErrInvalidProtocolSet
		}
		if _, ok := seen["suno"]; ok {
			return ErrInvalidProtocolSet
		}
	}
	if key.Types != nil {
		key.Types = normalized
	}
	return nil
}

// encryptPlainKey 加密明文密钥；requireKey=true（新增）时必须非空，
// false（更新）时空串返回空串（表示保持原密钥不变）。
func (s *Service) encryptPlainKey(plain string, requireKey bool) (string, error) {
	plain = strings.TrimSpace(plain)
	if plain == "" {
		if requireKey {
			return "", ErrNoAPIKey
		}
		return "", nil
	}
	return auth.EncryptAPIKey(plain, s.secret)
}

// decorate 为渠道下各 key 补充解密派生字段（api_key_hint）。
//
// 有意保留"列表时逐 key 解密生成 hint"的实现：AES-GCM 解密为纯内存操作
// （微秒级），渠道数量为管理面小规模数据，代价可忽略。
func (s *Service) decorate(ch *Channel) {
	for i := range ch.Keys {
		ch.Keys[i].APIKeyHint = s.keyHint(ch.Keys[i].APIKey)
	}
}

// keyHint 解密密文生成密钥提示（前 3 字符 + 省略号 + 尾 4 位，形如 "sk-…f3ab"）。
func (s *Service) keyHint(cipher string) string {
	plain, err := auth.DecryptAPIKey(cipher, s.secret)
	if err != nil {
		return "（无法解密）"
	}
	if len(plain) <= 8 {
		return "…"
	}
	return plain[:3] + "…" + plain[len(plain)-4:]
}

// reloadRegistry 写操作成功后触发注册表重载；失败仅记日志，不影响主流程。
func (s *Service) reloadRegistry(ctx context.Context) {
	if s.reloader == nil {
		return
	}
	if err := s.reloader.Reload(ctx); err != nil {
		logx.LoggerFromContext(ctx).Error("channel_registry_reload_failed", logx.LogFieldError, err)
	}
}
