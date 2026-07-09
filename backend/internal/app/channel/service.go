package channel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/pkg/pagination"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// Reloader 注册表重载窄接口（由 relay/registry.Registry 实现，可为 nil——测试时不接）。
type Reloader interface {
	Reload(ctx context.Context) error
}

// Tester 渠道连通性测试接口：走完整 relay adaptor 链路发起一次真实请求。
// 本棒（P1 第一棒）不提供实现，由 relay 管线落地后注入。
type Tester interface {
	// endpoint 仅对 openai 协议渠道生效（chat_completions / responses，空值默认前者）。
	Test(ctx context.Context, ch Channel, model, endpoint string) (latencyMs int, err error)
}

// ModelFetcher 上游拉取接口：模型列表与账户余额。
type ModelFetcher interface {
	FetchModels(ctx context.Context, channelType, baseURL, apiKey string) ([]string, error)
	// FetchBalance 经 key 查上游余额（USD）；不支持的渠道类型返回 ErrBalanceUnsupported。
	FetchBalance(ctx context.Context, channelType, baseURL, apiKey string) (float64, error)
}

// ConcurrencyReader 渠道在途并发数批量读取（由 scheduler.ConcurrencyManager 实现）。
type ConcurrencyReader interface {
	GetChannelCurrentCounts(ctx context.Context, channelIDs []int) map[int]int
}

// RPMReader 渠道当前分钟 RPM 批量读取（由 scheduler.RPMCounter 实现）。
type RPMReader interface {
	GetChannelRPMs(ctx context.Context, channelIDs []int) map[int]int
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

// SetTester 注入渠道测试器（relay 管线落地后由 server 装配阶段调用）。
func (s *Service) SetTester(tester Tester) {
	s.tester = tester
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
	s.attachMoneyStats(ctx, list)
	return ListResult{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// attachMoneyStats 为列表页渠道批量填充累计成本/收益（基于 usage_logs 聚合）。
// 读取器未注入或查询失败时保持 0 值，不影响列表主流程。
func (s *Service) attachMoneyStats(ctx context.Context, list []Channel) {
	if s.stats == nil || len(list) == 0 {
		return
	}
	ids := make([]int, len(list))
	for i, ch := range list {
		ids[i] = ch.ID
	}
	stats, err := s.stats.GetChannelMoneyStats(ctx, ids)
	if err != nil {
		sdk.LoggerFromContext(ctx).Warn("channel_money_stats_failed", sdk.LogFieldError, err)
		return
	}
	for i := range list {
		list[i].TotalCost = stats[list[i].ID].Cost
		list[i].TotalRevenue = stats[list[i].ID].Revenue
	}
}

// attachRuntimeStats 为列表页渠道批量填充运行时观测指标（在途并发 / 当前分钟 RPM）。
// 读取器未注入或 Redis 不可用时保持 0 值，不影响列表主流程。
func (s *Service) attachRuntimeStats(ctx context.Context, list []Channel) {
	if len(list) == 0 {
		return
	}
	ids := make([]int, len(list))
	for i, ch := range list {
		ids[i] = ch.ID
	}
	var counts, rpms map[int]int
	if s.concurrency != nil {
		counts = s.concurrency.GetChannelCurrentCounts(ctx, ids)
	}
	if s.rpm != nil {
		rpms = s.rpm.GetChannelRPMs(ctx, ids)
	}
	for i := range list {
		list[i].CurrentConcurrency = counts[list[i].ID]
		list[i].CurrentRPM = rpms[list[i].ID]
	}
}

// Create 创建渠道：api_keys 在本层逐元素加密后落库。
func (s *Service) Create(ctx context.Context, input CreateInput) (Channel, error) {
	logger := sdk.LoggerFromContext(ctx)

	encrypted, err := s.encryptKeys(input.APIKeys)
	if err != nil {
		logger.Error("channel_api_key_encrypt_failed", "name", input.Name, sdk.LogFieldError, err)
		return Channel{}, err
	}
	input.APIKeys = encrypted

	item, err := s.repo.Create(ctx, input)
	if err != nil {
		logger.Error("channel_persist_failed", "op", "create", "name", input.Name, sdk.LogFieldError, err)
		return Channel{}, err
	}
	logger.Info("channel_created", "channel_id", item.ID, "name", item.Name, "type", item.Type)

	s.reloadRegistry(ctx)
	s.decorate(&item)
	return item, nil
}

// Update 更新渠道（partial）：api_keys 提供即整组替换（本层加密）。
func (s *Service) Update(ctx context.Context, id int, input UpdateInput) (Channel, error) {
	logger := sdk.LoggerFromContext(ctx)

	if len(input.APIKeys) > 0 {
		encrypted, err := s.encryptKeys(input.APIKeys)
		if err != nil {
			logger.Error("channel_api_key_encrypt_failed", "channel_id", id, sdk.LogFieldError, err)
			return Channel{}, err
		}
		input.APIKeys = encrypted
	}
	// 手动重新启用时清理上一轮状态残留（错误信息与冷却时间）。
	if input.Status != nil && *input.Status == StatusEnabled {
		emptyMsg := ""
		input.ErrorMsg = &emptyMsg
		input.ClearStatusUntil = true
	}

	item, err := s.repo.Update(ctx, id, input)
	if err != nil {
		logger.Error("channel_persist_failed", "op", "update", "channel_id", id, sdk.LogFieldError, err)
		return Channel{}, err
	}

	s.reloadRegistry(ctx)
	s.decorate(&item)
	return item, nil
}

// Delete 删除渠道。
func (s *Service) Delete(ctx context.Context, id int) error {
	logger := sdk.LoggerFromContext(ctx)
	if err := s.repo.Delete(ctx, id); err != nil {
		logger.Error("channel_persist_failed", "op", "delete", "channel_id", id, sdk.LogFieldError, err)
		return err
	}
	logger.Info("channel_deleted", "channel_id", id)

	s.reloadRegistry(ctx)
	return nil
}

// BulkUpdate 批量启停/删除/调优先级，返回受影响行数。
func (s *Service) BulkUpdate(ctx context.Context, input BulkUpdateInput) (int, error) {
	logger := sdk.LoggerFromContext(ctx)

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
		logger.Error("channel_persist_failed", "op", "bulk_"+input.Action, sdk.LogFieldError, err)
		return 0, err
	}
	logger.Info("channel_bulk_updated", "action", input.Action, "affected", affected)

	s.reloadRegistry(ctx)
	return affected, nil
}

// Test 测试渠道连通性：走 relay adaptor 链路发一次真实请求。
// 成功后记录响应耗时；若渠道处于 disabled_auto 则恢复 enabled 并清 error_msg。
func (s *Service) Test(ctx context.Context, id int, model, endpoint string) (int, error) {
	logger := sdk.LoggerFromContext(ctx)

	ch, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return 0, err
	}
	if s.tester == nil {
		return 0, ErrTesterNotReady
	}

	if model == "" {
		model = ch.TestModel
	}
	if model == "" && len(ch.Models) > 0 {
		model = ch.Models[0]
	}

	latency, err := s.tester.Test(ctx, ch, model, endpoint)
	if err != nil {
		logger.Warn("channel_test_failed", "channel_id", id, "model", model, sdk.LogFieldError, err)
		return 0, fmt.Errorf("%w: %v", ErrTestFailed, err)
	}

	if err := s.repo.UpdateTestResult(ctx, id, latency, time.Now()); err != nil {
		logger.Warn("channel_persist_failed", "op", "test_result", "channel_id", id, sdk.LogFieldError, err)
	}
	// 测试通过 → 自动禁用渠道恢复可用（手动禁用不恢复）。
	// 重读当前状态再判断：测试窗口（最长 30s）内管理员可能已改为手动禁用，
	// 凭测前快照恢复会静默推翻手动操作。
	if ch.Status == StatusDisabledAuto {
		current, err := s.repo.FindByID(ctx, id)
		if err != nil {
			logger.Warn("channel_persist_failed", "op", "test_recover_recheck", "channel_id", id, sdk.LogFieldError, err)
		} else if current.Status == StatusDisabledAuto {
			if err := s.repo.UpdateState(ctx, id, StatusEnabled, nil, ""); err != nil {
				logger.Warn("channel_persist_failed", "op", "test_recover", "channel_id", id, sdk.LogFieldError, err)
			} else {
				logger.Info("channel_recovered_by_test", "channel_id", id)
			}
		}
	}

	s.reloadRegistry(ctx)
	return latency, nil
}

// FetchModels 从上游拉取模型列表（用渠道第一个 API Key）。成败均写上游请求日志。
func (s *Service) FetchModels(ctx context.Context, id int) ([]string, error) {
	logger := sdk.LoggerFromContext(ctx)

	ch, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(ch.APIKeys) == 0 {
		return nil, ErrNoAPIKey
	}
	apiKey, err := auth.DecryptAPIKey(ch.APIKeys[0], s.secret)
	if err != nil {
		logger.Error("channel_api_key_decrypt_failed", "channel_id", id, sdk.LogFieldError, err)
		return nil, fmt.Errorf("%w: API Key 解密失败", ErrModelFetchFailed)
	}

	return s.fetchModels(ctx, ch.Type, ch.BaseURL, apiKey)
}

// FetchModelsWithKey 按给定连接参数（明文 key）拉取上游模型列表。
// 供渠道尚未保存时的预览拉取使用：表单填好 type/base_url/api_key 即可试拉，
// 不要求渠道已落库，解开「保存要先有模型、拉模型要先保存」的死锁。
// 结果当场返回给管理员，不留痕（留痕判据：事后排障需要且当场看不到）。
func (s *Service) FetchModelsWithKey(ctx context.Context, channelType, baseURL, apiKey string) ([]string, error) {
	return s.fetchModels(ctx, channelType, baseURL, apiKey)
}

// RefreshBalance 经渠道全部 key 查询上游余额并求和，落库后返回。
// 多 key 求和 = 该渠道背后总可用额度；单个 key 查询失败跳过并计入告警，
// 全部失败才整体报错。仅 openai_compatible 中转站可查（其余返回 ErrBalanceUnsupported）。
func (s *Service) RefreshBalance(ctx context.Context, id int) (float64, *time.Time, error) {
	logger := sdk.LoggerFromContext(ctx)

	ch, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return 0, nil, err
	}
	if len(ch.APIKeys) == 0 {
		return 0, nil, ErrNoAPIKey
	}

	var total float64
	var okCount, failCount int
	var lastErr error
	for _, encrypted := range ch.APIKeys {
		apiKey, derr := auth.DecryptAPIKey(encrypted, s.secret)
		if derr != nil {
			logger.Warn("channel_api_key_decrypt_failed", "channel_id", id, sdk.LogFieldError, derr)
			failCount++
			lastErr = derr
			continue
		}
		bal, berr := s.fetcher.FetchBalance(ctx, ch.Type, ch.BaseURL, apiKey)
		if berr != nil {
			// 类型不支持是确定性结果，无需逐 key 重试——直接透传。
			if errors.Is(berr, ErrBalanceUnsupported) {
				return 0, nil, ErrBalanceUnsupported
			}
			logger.Warn("channel_fetch_balance_failed", "channel_id", id, sdk.LogFieldError, berr)
			failCount++
			lastErr = berr
			continue
		}
		total += bal
		okCount++
	}
	if okCount == 0 {
		return 0, nil, fmt.Errorf("%w: %v", ErrBalanceFetchFailed, lastErr)
	}

	now := time.Now()
	if err := s.repo.UpdateBalance(ctx, id, total, now); err != nil {
		logger.Warn("channel_persist_failed", "op", "balance", "channel_id", id, sdk.LogFieldError, err)
		return 0, nil, err
	}
	if failCount > 0 {
		logger.Warn("channel_balance_partial", "channel_id", id, "ok", okCount, "fail", failCount)
	}
	return total, &now, nil
}

// fetchModels 拉取主体（无留痕）。
func (s *Service) fetchModels(ctx context.Context, channelType, baseURL, apiKey string) ([]string, error) {
	models, err := s.fetcher.FetchModels(ctx, channelType, baseURL, apiKey)
	if err != nil {
		sdk.LoggerFromContext(ctx).Warn("channel_fetch_models_failed", "type", channelType, sdk.LogFieldError, err)
		return nil, fmt.Errorf("%w: %v", ErrModelFetchFailed, err)
	}
	return models, nil
}

// LoadAllForRegistry 实现 registry.Loader：全量加载渠道并
// 解密 api_keys，产出运行时快照。
func (s *Service) LoadAllForRegistry(ctx context.Context) ([]registry.ChannelSnapshot, error) {
	logger := sdk.LoggerFromContext(ctx)

	items, err := s.repo.ListAll(ctx)
	if err != nil {
		return nil, err
	}

	snaps := make([]registry.ChannelSnapshot, 0, len(items))
	for _, ch := range items {
		keys := make([]string, 0, len(ch.APIKeys))
		for _, encrypted := range ch.APIKeys {
			plain, err := auth.DecryptAPIKey(encrypted, s.secret)
			if err != nil {
				logger.Warn("channel_api_key_decrypt_failed", "channel_id", ch.ID, sdk.LogFieldError, err)
				continue
			}
			keys = append(keys, plain)
		}

		models := make(map[string]struct{}, len(ch.Models))
		for _, m := range ch.Models {
			models[m] = struct{}{}
		}
		groups := make(map[int]struct{}, len(ch.GroupIDs))
		for _, g := range ch.GroupIDs {
			groups[g] = struct{}{}
		}

		snaps = append(snaps, registry.ChannelSnapshot{
			ID:             ch.ID,
			Name:           ch.Name,
			Type:           ch.Type,
			BaseURL:        ch.BaseURL,
			APIKeys:        keys,
			Models:         models,
			ModelMapping:   ch.ModelMapping,
			ParamOverride:  ch.ParamOverride,
			HeaderOverride: ch.HeaderOverride,
			Priority:       ch.Priority,
			Weight:         ch.Weight,
			MaxConcurrency: ch.MaxConcurrency,
			MaxRPM:         ch.MaxRPM,
			CostRatio:      ch.CostRatio,
			Status:         ch.Status,
			StatusUntil:    ch.StatusUntil,
			GroupIDs:       groups,
			TestModel:      ch.TestModel,
		})
	}
	return snaps, nil
}

// PersistState 实现 registry.Persister：渠道调度状态异步落库。
func (s *Service) PersistState(ctx context.Context, id int, status string, until *time.Time, errMsg string) error {
	return s.repo.UpdateState(ctx, id, status, until, errMsg)
}

// encryptKeys 逐元素加密 API Key（去除首尾空白、跳过空行）。
func (s *Service) encryptKeys(keys []string) ([]string, error) {
	encrypted := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		cipher, err := auth.EncryptAPIKey(key, s.secret)
		if err != nil {
			return nil, err
		}
		encrypted = append(encrypted, cipher)
	}
	if len(encrypted) == 0 {
		return nil, ErrNoAPIKey
	}
	return encrypted, nil
}

// decorate 为领域对象补充解密派生字段（api_key_hints）。
func (s *Service) decorate(ch *Channel) {
	hints := make([]string, 0, len(ch.APIKeys))
	for _, encrypted := range ch.APIKeys {
		plain, err := auth.DecryptAPIKey(encrypted, s.secret)
		if err != nil {
			hints = append(hints, "（无法解密）")
			continue
		}
		hints = append(hints, buildChannelKeyHint(plain))
	}
	ch.APIKeyHints = hints
}

// buildChannelKeyHint 生成密钥提示：前 3 字符 + 省略号 + 尾 4 位（形如 "sk-…f3ab"）。
func buildChannelKeyHint(key string) string {
	if len(key) <= 8 {
		return "…"
	}
	return key[:3] + "…" + key[len(key)-4:]
}

// reloadRegistry 写操作成功后触发注册表重载；失败仅记日志，不影响主流程。
func (s *Service) reloadRegistry(ctx context.Context) {
	if s.reloader == nil {
		return
	}
	if err := s.reloader.Reload(ctx); err != nil {
		sdk.LoggerFromContext(ctx).Error("channel_registry_reload_failed", sdk.LogFieldError, err)
	}
}
