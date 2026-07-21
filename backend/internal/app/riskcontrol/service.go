package riskcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"

	appuser "github.com/DouDOU-start/airgate-core/internal/app/user"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/moderation"
	"github.com/DouDOU-start/airgate-core/internal/pkg/pagination"
)

// settings 表落点：group=risk_control 下两个 key——
// 总开关独立存（状态卡/热路径免解析 JSON），其余 30+ 项配置单 JSON 原子更新。
const (
	SettingsGroup    = "risk_control"
	settingKeyEnable = "risk_control_enabled"
	settingKeyConfig = "content_moderation_config"
)

// storedConfig 配置的持久化形态：审核 key 以 AES-256-GCM 密文存
// api_keys_encrypted，内嵌 Config 的明文 APIKeys 字段在落库前清空。
type storedConfig struct {
	moderation.Config
	APIKeysEncrypted []string `json:"api_keys_encrypted"`
}

// Service 风控中心管理面服务；同时实现 moderation.Engine 的
// ConfigSource（读配置+解密）与 UserBanner（自动封禁，管理员豁免）。
type Service struct {
	settings SettingsRepo
	logs     Repository
	hashes   moderation.HashCache
	users    UserRepo
	secret   string
	engine   *moderation.Engine
}

// NewService 创建风控管理服务。
func NewService(settings SettingsRepo, logs Repository, hashes moderation.HashCache, users UserRepo, secret string) *Service {
	return &Service{settings: settings, logs: logs, hashes: hashes, users: users, secret: secret}
}

// SetEngine 注入判定引擎（装配期调用；用于读运行时指标、key 探活与缓存失效）。
func (s *Service) SetEngine(e *moderation.Engine) { s.engine = e }

// Runtime 实现 moderation.ConfigSource：读总开关与配置并解密审核 key。
func (s *Service) Runtime(ctx context.Context) (bool, *moderation.Config, error) {
	values, err := s.settings.GroupValues(ctx, SettingsGroup)
	if err != nil {
		return false, nil, err
	}
	cfg, err := s.parseStored(values[settingKeyConfig])
	if err != nil {
		return false, nil, err
	}
	return values[settingKeyEnable] == "true", cfg, nil
}

// DisableUser 实现 moderation.UserBanner：管理员与已禁用用户跳过（返回 false 不报错）。
func (s *Service) DisableUser(ctx context.Context, userID int) (bool, error) {
	status, role, err := s.users.GetStatusAndRole(ctx, userID)
	if err != nil {
		return false, err
	}
	if role == "admin" {
		slog.Warn("riskcontrol.autoban_skipped_admin", "user_id", userID)
		return false, nil
	}
	if status == "disabled" {
		return false, nil
	}
	if err := s.users.UpdateStatus(ctx, userID, "disabled"); err != nil {
		return false, err
	}
	slog.Info("riskcontrol.user_auto_banned", "user_id", userID)
	return true, nil
}

// GetConfig 配置回显（key 只出掩码+健康状态）。
func (s *Service) GetConfig(ctx context.Context) (ConfigView, error) {
	values, err := s.settings.GroupValues(ctx, SettingsGroup)
	if err != nil {
		return ConfigView{}, err
	}
	cfg, err := s.parseStored(values[settingKeyConfig])
	if err != nil {
		return ConfigView{}, err
	}
	return s.configView(values[settingKeyEnable] == "true", cfg), nil
}

// UpdateConfig 增量更新配置：nil 字段保持现值；审核 key 支持追加/替换/按
// hash 删除/清空；落库前校验并加密，成功后立即失效引擎快照。
func (s *Service) UpdateConfig(ctx context.Context, input UpdateConfigInput) (ConfigView, error) {
	values, err := s.settings.GroupValues(ctx, SettingsGroup)
	if err != nil {
		return ConfigView{}, err
	}
	cfg, err := s.parseStored(values[settingKeyConfig])
	if err != nil {
		return ConfigView{}, err
	}
	applyConfigPatch(cfg, input)
	cfg.APIKeys = s.applyKeyPatch(cfg.APIKeys, input)
	cfg.Normalize()
	if err := validateConfig(cfg); err != nil {
		return ConfigView{}, err
	}
	if err := s.saveConfig(ctx, cfg); err != nil {
		return ConfigView{}, err
	}
	enabled := values[settingKeyEnable] == "true"
	if input.RiskControlEnabled != nil {
		enabled = *input.RiskControlEnabled
		if err := s.settings.UpsertValue(ctx, SettingsGroup, settingKeyEnable, strconv.FormatBool(enabled)); err != nil {
			return ConfigView{}, err
		}
	}
	s.engine.InvalidateSnapshot()
	return s.configView(enabled, cfg), nil
}

// GetStatus 运行时状态（引擎指标 + worker/队列 + key 负载 + hash 计数）。
func (s *Service) GetStatus(ctx context.Context) moderation.RuntimeStatus {
	return s.engine.Status(ctx)
}

// TestAPIKeys 探活/试审。
func (s *Service) TestAPIKeys(ctx context.Context, input moderation.TestKeysInput) (*moderation.TestKeysResult, error) {
	return s.engine.TestKeys(ctx, input)
}

// ListLogs 审核日志分页查询。
func (s *Service) ListLogs(ctx context.Context, filter ListFilter) ([]Record, int64, error) {
	filter.Page, filter.PageSize = pagination.Normalize(filter.Page, filter.PageSize)
	return s.logs.List(ctx, filter)
}

// UnbanUser 解封用户（置回 active；封禁传播依赖 5s 鉴权缓存 TTL）。
func (s *Service) UnbanUser(ctx context.Context, userID int) (UnbanResult, error) {
	if _, _, err := s.users.GetStatusAndRole(ctx, userID); err != nil {
		if errors.Is(err, appuser.ErrUserNotFound) {
			return UnbanResult{}, ErrUserNotFound
		}
		return UnbanResult{}, err
	}
	if err := s.users.UpdateStatus(ctx, userID, "active"); err != nil {
		return UnbanResult{}, err
	}
	slog.Info("riskcontrol.user_unbanned", "user_id", userID)
	return UnbanResult{UserID: userID, Status: "active"}, nil
}

// DeleteFlaggedHash 删除单条命中哈希。
func (s *Service) DeleteFlaggedHash(ctx context.Context, inputHash string) (bool, error) {
	inputHash = strings.ToLower(strings.TrimSpace(inputHash))
	if len(inputHash) != 64 {
		return false, ErrInvalidHash
	}
	if s.hashes == nil {
		return false, nil
	}
	return s.hashes.Delete(ctx, inputHash)
}

// ClearFlaggedHashes 清空命中哈希缓存。
func (s *Service) ClearFlaggedHashes(ctx context.Context) (int64, error) {
	if s.hashes == nil {
		return 0, nil
	}
	return s.hashes.Clear(ctx)
}

// ---- 内部 ----

func (s *Service) parseStored(raw string) (*moderation.Config, error) {
	stored := &storedConfig{Config: *moderation.DefaultConfig()}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), stored); err != nil {
			return nil, fmt.Errorf("%w: 配置 JSON 解析失败", ErrInvalidConfig)
		}
	}
	cfg := stored.Config
	cfg.APIKeys = nil
	for _, encrypted := range stored.APIKeysEncrypted {
		plain, err := auth.DecryptAPIKey(encrypted, s.secret)
		if err != nil {
			slog.Warn("riskcontrol.decrypt_api_key_failed", "error", err)
			continue
		}
		cfg.APIKeys = append(cfg.APIKeys, plain)
	}
	cfg.Normalize()
	return &cfg, nil
}

func (s *Service) saveConfig(ctx context.Context, cfg *moderation.Config) error {
	stored := storedConfig{Config: *cfg.Clone(), APIKeysEncrypted: []string{}}
	for _, key := range cfg.APIKeys {
		encrypted, err := auth.EncryptAPIKey(key, s.secret)
		if err != nil {
			return err
		}
		stored.APIKeysEncrypted = append(stored.APIKeysEncrypted, encrypted)
	}
	stored.APIKeys = nil
	raw, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	return s.settings.UpsertValue(ctx, SettingsGroup, settingKeyConfig, string(raw))
}

func (s *Service) configView(enabled bool, cfg *moderation.Config) ConfigView {
	keys := cfg.APIKeys
	masks := make([]string, 0, len(keys))
	for _, key := range keys {
		masks = append(masks, moderation.MaskSecretTail(key))
	}
	view := *cfg.Clone()
	view.APIKeys = nil
	return ConfigView{
		RiskControlEnabled: enabled,
		Config:             view,
		APIKeyCount:        len(keys),
		APIKeyMasks:        masks,
		APIKeyStatuses:     s.engine.KeyStatuses(keys),
	}
}

// applyKeyPatch 审核 key 的增量语义：clear 清空 → replace/append → 按 hash 删除。
func (s *Service) applyKeyPatch(current []string, input UpdateConfigInput) []string {
	keys := append([]string(nil), current...)
	if input.ClearAPIKeys {
		keys = nil
	}
	if input.APIKeys != nil {
		if strings.EqualFold(input.APIKeysMode, "replace") {
			keys = append([]string(nil), (*input.APIKeys)...)
		} else {
			keys = append(keys, (*input.APIKeys)...)
		}
	}
	if len(input.DeleteAPIKeyHashes) > 0 {
		deleteSet := make(map[string]struct{}, len(input.DeleteAPIKeyHashes))
		for _, h := range input.DeleteAPIKeyHashes {
			deleteSet[strings.ToLower(strings.TrimSpace(h))] = struct{}{}
		}
		kept := keys[:0]
		for _, key := range keys {
			if _, ok := deleteSet[moderation.KeyHash(key)]; !ok {
				kept = append(kept, key)
			}
		}
		keys = kept
	}
	return keys
}

func applyConfigPatch(cfg *moderation.Config, in UpdateConfigInput) {
	setIf(&cfg.Enabled, in.Enabled)
	setIf(&cfg.Mode, in.Mode)
	setIf(&cfg.BaseURL, in.BaseURL)
	setIf(&cfg.Model, in.Model)
	setIf(&cfg.TimeoutMS, in.TimeoutMS)
	setIf(&cfg.SampleRate, in.SampleRate)
	setIf(&cfg.AllGroups, in.AllGroups)
	setIf(&cfg.RecordNonHits, in.RecordNonHits)
	setIf(&cfg.WorkerCount, in.WorkerCount)
	setIf(&cfg.QueueSize, in.QueueSize)
	setIf(&cfg.BlockStatus, in.BlockStatus)
	setIf(&cfg.BlockMessage, in.BlockMessage)
	setIf(&cfg.EmailOnHit, in.EmailOnHit)
	setIf(&cfg.AutoBanEnabled, in.AutoBanEnabled)
	setIf(&cfg.BanThreshold, in.BanThreshold)
	setIf(&cfg.ViolationWindowHours, in.ViolationWindowHours)
	setIf(&cfg.RetryCount, in.RetryCount)
	setIf(&cfg.HitRetentionDays, in.HitRetentionDays)
	setIf(&cfg.NonHitRetentionDays, in.NonHitRetentionDays)
	setIf(&cfg.PreHashCheckEnabled, in.PreHashCheckEnabled)
	setIf(&cfg.KeywordBlockingMode, in.KeywordBlockingMode)
	if in.GroupIDs != nil {
		cfg.GroupIDs = append([]int(nil), (*in.GroupIDs)...)
	}
	if in.Thresholds != nil {
		cfg.Thresholds = moderation.MergeThresholds(moderation.DefaultThresholds(), *in.Thresholds)
	}
	if in.BlockedKeywords != nil {
		cfg.BlockedKeywords = append([]string(nil), (*in.BlockedKeywords)...)
	}
	if in.ModelFilter != nil {
		cfg.ModelFilter = *in.ModelFilter
	}
}

func setIf[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

// validateConfig 落库前校验（Normalize 已兜底大部分字段，这里拦语义性错误）。
func validateConfig(cfg *moderation.Config) error {
	if _, err := url.ParseRequestURI(cfg.BaseURL); err != nil {
		return fmt.Errorf("%w: 审核 API Base URL 无效", ErrInvalidConfig)
	}
	if cfg.BlockStatus < 400 || cfg.BlockStatus > 599 {
		return fmt.Errorf("%w: 拦截 HTTP 状态码必须在 400-599 之间", ErrInvalidConfig)
	}
	if cfg.ModelFilter.Type != moderation.ModelFilterAll && len(cfg.ModelFilter.Models) == 0 {
		return fmt.Errorf("%w: 指定或排除模型时至少需要配置 1 个模型", ErrInvalidConfig)
	}
	return nil
}

var (
	_ moderation.ConfigSource = (*Service)(nil)
	_ moderation.UserBanner   = (*Service)(nil)
)
