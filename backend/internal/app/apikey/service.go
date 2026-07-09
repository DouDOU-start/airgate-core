package apikey

import (
	"context"
	"log/slog"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/pkg/pagination"
	"github.com/DouDOU-start/airgate-core/internal/pkg/timezone"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// Service API Key 应用服务。
type Service struct {
	repo   Repository
	secret string
}

// NewService 创建 API Key 服务。
func NewService(repo Repository, secret string) *Service {
	return &Service{repo: repo, secret: secret}
}

// ListByUser 查询当前用户的 API Key 列表。
// tz 决定每个 key 的"今日成本"起点；为空时回退到服务器本地时区。
func (s *Service) ListByUser(ctx context.Context, userID int, filter ListFilter, tz string) (ListResult, error) {
	logger := sdk.LoggerFromContext(ctx)
	page, pageSize := pagination.Normalize(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.ListByUser(ctx, userID, filter)
	if err != nil {
		logger.Error("api_key_lookup_failed",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldReason, "list",
			sdk.LogFieldError, err,
		)
		return ListResult{}, err
	}

	keyIDs := make([]int, 0, len(list))
	for _, item := range list {
		keyIDs = append(keyIDs, item.ID)
	}
	loc := timezone.Resolve(tz)
	todayStart := timezone.StartOfDay(time.Now().In(loc))
	todayMap, thirtyDayMap, err := s.repo.KeyUsage(ctx, keyIDs, todayStart)
	if err != nil {
		logger.Error("api_key_lookup_failed",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldReason, "key_usage",
			sdk.LogFieldError, err,
		)
		return ListResult{}, err
	}
	for index := range list {
		list[index].TodayCost = todayMap[list[index].ID]
		list[index].ThirtyDayCost = thirtyDayMap[list[index].ID]
	}

	return ListResult{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// ListAdmin 查询全局 API Key 列表。
// 仅用于管理员选择器等轻量查询，不附加用量聚合，避免搜索时触发额外统计查询。
func (s *Service) ListAdmin(ctx context.Context, filter ListFilter) (ListResult, error) {
	logger := sdk.LoggerFromContext(ctx)
	page, pageSize := pagination.Normalize(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.ListAdmin(ctx, filter)
	if err != nil {
		logger.Error("api_key_lookup_failed",
			sdk.LogFieldReason, "admin_list",
			sdk.LogFieldError, err,
		)
		return ListResult{}, err
	}

	return ListResult{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// CreateOwned 创建当前用户的 API Key。
func (s *Service) CreateOwned(ctx context.Context, userID int, input CreateInput) (Key, error) {
	logger := sdk.LoggerFromContext(ctx)

	groupID := int(input.GroupID)
	if err := s.ensureUserCanUseGroup(ctx, userID, groupID); err != nil {
		logger.Warn("api_key_create_rejected",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldGroupID, groupID,
			sdk.LogFieldReason, "group_access",
			sdk.LogFieldError, err,
		)
		return Key{}, err
	}

	rawKey, keyHash, err := auth.GenerateAPIKey()
	if err != nil {
		logger.Error("api_key_create_failed",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldReason, "generate_key",
			sdk.LogFieldError, err,
		)
		return Key{}, err
	}
	encrypted, err := auth.EncryptAPIKey(rawKey, s.secret)
	if err != nil {
		logger.Error("api_key_create_failed",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldReason, "encrypt_key",
			sdk.LogFieldError, err,
		)
		return Key{}, err
	}
	expiresAt, hasExpiresAt, err := parseExpiresAt(input.ExpiresAt)
	if err != nil {
		logger.Warn("api_key_create_rejected",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldReason, "invalid_expires_at",
		)
		return Key{}, err
	}

	maxConc := input.MaxConcurrency
	if maxConc < 0 {
		maxConc = 0
	}
	item, err := s.repo.Create(ctx, Mutation{
		Name:           &input.Name,
		KeyHint:        stringPtr(buildKeyHint(rawKey)),
		KeyHash:        &keyHash,
		KeyEncrypted:   &encrypted,
		UserID:         &userID,
		GroupID:        &groupID,
		IPWhitelist:    cloneStringSlice(input.IPWhitelist),
		HasIPWhitelist: input.IPWhitelist != nil,
		IPBlacklist:    cloneStringSlice(input.IPBlacklist),
		HasIPBlacklist: input.IPBlacklist != nil,
		QuotaUSD:       &input.QuotaUSD,
		SellRate:       &input.SellRate,
		MaxConcurrency: &maxConc,
		ExpiresAt:      expiresAt,
		HasExpiresAt:   hasExpiresAt,
	})
	if err != nil {
		logger.Error("api_key_create_failed",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldGroupID, groupID,
			sdk.LogFieldReason, "persist",
			sdk.LogFieldError, err,
		)
		return Key{}, err
	}

	logger.Info("api_key_created",
		sdk.LogFieldUserID, userID,
		sdk.LogFieldAPIKeyID, item.ID,
		sdk.LogFieldGroupID, groupID,
	)

	item.PlainKey = rawKey
	return item, nil
}

// UpdateOwned 更新当前用户的 API Key。
func (s *Service) UpdateOwned(ctx context.Context, userID, id int, input UpdateInput) (Key, error) {
	logger := sdk.LoggerFromContext(ctx)
	mutation, err := s.buildMutation(ctx, userID, input, true)
	if err != nil {
		return Key{}, err
	}
	updated, err := s.repo.UpdateOwned(ctx, userID, id, mutation)
	if err != nil {
		logger.Error("api_key_update_failed",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldAPIKeyID, id,
			sdk.LogFieldError, err,
		)
		return Key{}, err
	}
	logApiKeyMutationOutcome(logger, userID, id, mutation)
	return updated, nil
}

// UpdateAdmin 管理员更新 API Key。
func (s *Service) UpdateAdmin(ctx context.Context, id int, input UpdateInput) (Key, error) {
	logger := sdk.LoggerFromContext(ctx)
	mutation, err := s.buildMutation(ctx, 0, input, false)
	if err != nil {
		return Key{}, err
	}
	updated, err := s.repo.UpdateAdmin(ctx, id, mutation)
	if err != nil {
		logger.Error("api_key_update_failed",
			sdk.LogFieldAPIKeyID, id,
			sdk.LogFieldReason, "admin",
			sdk.LogFieldError, err,
		)
		return Key{}, err
	}
	logApiKeyMutationOutcome(logger, updated.UserID, id, mutation)
	return updated, nil
}

// DeleteOwned 删除当前用户的 API Key。
func (s *Service) DeleteOwned(ctx context.Context, userID, id int) error {
	logger := sdk.LoggerFromContext(ctx)
	if err := s.repo.DeleteOwned(ctx, userID, id); err != nil {
		logger.Error("api_key_delete_failed",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldAPIKeyID, id,
			sdk.LogFieldError, err,
		)
		return err
	}
	logger.Info("api_key_deleted",
		sdk.LogFieldUserID, userID,
		sdk.LogFieldAPIKeyID, id,
	)
	return nil
}

// RevealOwned 查看当前用户的 API Key 原文。
func (s *Service) RevealOwned(ctx context.Context, userID, id int) (Key, error) {
	logger := sdk.LoggerFromContext(ctx)
	item, err := s.repo.FindOwned(ctx, userID, id)
	if err != nil {
		logger.Error("api_key_lookup_failed",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldAPIKeyID, id,
			sdk.LogFieldReason, "reveal",
			sdk.LogFieldError, err,
		)
		return Key{}, err
	}
	if item.KeyEncrypted == "" {
		logger.Warn("api_key_reveal_rejected",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldAPIKeyID, id,
			sdk.LogFieldReason, "legacy_key",
		)
		return Key{}, ErrLegacyKeyNotReveal
	}
	plainKey, err := auth.DecryptAPIKey(item.KeyEncrypted, s.secret)
	if err != nil {
		logger.Error("api_key_reveal_failed",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldAPIKeyID, id,
			sdk.LogFieldReason, "decrypt",
			sdk.LogFieldError, err,
		)
		return Key{}, ErrKeyDecryptFailed
	}
	item.PlainKey = plainKey
	return item, nil
}

// ProvisionForClient 为用户按应用 get-or-create 一把 sk- key（OAuth provision-key 用）。
//
// 幂等依据 (user, provisioned_by=clientID)：已存在且启用 → 解密返回既有明文；
// 已禁用 → 报错（视为用户暂时封禁该应用，可在密钥管理中重新启用）；
// 用户删除该 key 则下次 provision 自动重建。groupID=0 时选默认分组。
func (s *Service) ProvisionForClient(ctx context.Context, userID int, clientID, keyName string, groupID int) (string, string, bool, error) {
	logger := sdk.LoggerFromContext(ctx)

	plainKey, hint, found, err := s.revealProvisioned(ctx, userID, clientID)
	if err != nil || found {
		return plainKey, hint, false, err
	}

	if groupID == 0 {
		gid, ok, err := s.repo.DefaultGroupID(ctx)
		if err != nil {
			return "", "", false, err
		}
		if !ok {
			return "", "", false, ErrNoDefaultGroup
		}
		groupID = gid
	}
	if err := s.ensureUserCanUseGroup(ctx, userID, groupID); err != nil {
		return "", "", false, err
	}

	rawKey, keyHash, err := auth.GenerateAPIKey()
	if err != nil {
		return "", "", false, err
	}
	encrypted, err := auth.EncryptAPIKey(rawKey, s.secret)
	if err != nil {
		return "", "", false, err
	}
	name := keyName
	if name == "" {
		name = clientID
	}
	keyHint := buildKeyHint(rawKey)
	_, err = s.repo.Create(ctx, Mutation{
		Name:          &name,
		KeyHint:       &keyHint,
		KeyHash:       &keyHash,
		KeyEncrypted:  &encrypted,
		UserID:        &userID,
		GroupID:       &groupID,
		ProvisionedBy: &clientID,
	})
	if err != nil {
		// 并发 provision 撞 (user, provisioned_by) 部分唯一索引：重查一次自愈。
		plainKey, hint, found, retryErr := s.revealProvisioned(ctx, userID, clientID)
		if retryErr == nil && found {
			return plainKey, hint, false, nil
		}
		logger.Error("api_key_provision_failed",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldReason, "create",
			sdk.LogFieldError, err,
		)
		return "", "", false, err
	}
	logger.Info("api_key_provisioned", sdk.LogFieldUserID, userID, "client_id", clientID)
	return rawKey, keyHint, true, nil
}

// revealProvisioned 查找既有 provisioned key 并解回明文；不存在时 found=false 且无错误。
func (s *Service) revealProvisioned(ctx context.Context, userID int, clientID string) (string, string, bool, error) {
	existing, found, err := s.repo.FindProvisioned(ctx, userID, clientID)
	if err != nil {
		return "", "", false, err
	}
	if !found {
		return "", "", false, nil
	}
	if existing.Status != "active" {
		return "", "", true, ErrProvisionedKeyDisabled
	}
	if existing.KeyEncrypted == "" {
		return "", "", true, ErrKeyDecryptFailed
	}
	plainKey, err := auth.DecryptAPIKey(existing.KeyEncrypted, s.secret)
	if err != nil {
		return "", "", true, ErrKeyDecryptFailed
	}
	return plainKey, existing.KeyHint, true, nil
}

// logApiKeyMutationOutcome 根据本次更新涉及的字段，输出对应的成功事件。
// 不打印 key 明文/hash，仅打印 ID 与变更类型。
func logApiKeyMutationOutcome(logger *slog.Logger, userID, keyID int, mutation Mutation) {
	if mutation.Status != nil {
		logger.Info("api_key_status_changed",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldAPIKeyID, keyID,
			sdk.LogFieldStatus, *mutation.Status,
		)
	}
	if mutation.QuotaUSD != nil {
		logger.Info("api_key_quota_updated",
			sdk.LogFieldUserID, userID,
			sdk.LogFieldAPIKeyID, keyID,
		)
	}
}

func (s *Service) buildMutation(ctx context.Context, userID int, input UpdateInput, enforceGroupAccess bool) (Mutation, error) {
	expiresAt, hasExpiresAt, err := parseExpiresAt(input.ExpiresAt)
	if err != nil {
		return Mutation{}, err
	}

	mutation := Mutation{
		Name:           input.Name,
		IPWhitelist:    cloneStringSlice(input.IPWhitelist),
		HasIPWhitelist: input.HasIPWhitelist,
		IPBlacklist:    cloneStringSlice(input.IPBlacklist),
		HasIPBlacklist: input.HasIPBlacklist,
		QuotaUSD:       input.QuotaUSD,
		SellRate:       input.SellRate,
		MaxConcurrency: input.MaxConcurrency,
		ExpiresAt:      expiresAt,
		HasExpiresAt:   hasExpiresAt,
		Status:         input.Status,
	}
	if input.GroupID != nil {
		groupID := int(*input.GroupID)
		if enforceGroupAccess {
			if err := s.ensureUserCanUseGroup(ctx, userID, groupID); err != nil {
				return Mutation{}, err
			}
		}
		mutation.GroupID = &groupID
	}
	return mutation, nil
}

func (s *Service) ensureUserCanUseGroup(ctx context.Context, userID, groupID int) error {
	access, err := s.repo.GetGroupAccess(ctx, userID, groupID)
	if err != nil {
		return err
	}
	if !access.Exists {
		return ErrGroupNotFound
	}
	if !access.Allowed {
		return ErrGroupForbidden
	}
	return nil
}

func parseExpiresAt(raw *string) (*time.Time, bool, error) {
	if raw == nil {
		// 未传该字段：不修改
		return nil, false, nil
	}
	if *raw == "" {
		// 显式传空字符串：清除过期时间
		return nil, true, nil
	}
	parsed, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		return nil, false, ErrInvalidExpiresAt
	}
	return &parsed, true, nil
}

func buildKeyHint(rawKey string) string {
	if len(rawKey) <= 11 {
		return rawKey
	}
	return rawKey[:7] + "..." + rawKey[len(rawKey)-4:]
}

func DisplayKeyPrefix(item Key) string {
	if item.PlainKey != "" {
		if len(item.PlainKey) > 10 {
			return item.PlainKey[:10] + "..."
		}
		return item.PlainKey
	}
	if item.KeyHint != "" {
		return item.KeyHint
	}
	if len(item.KeyHash) > 8 {
		return "sk-" + item.KeyHash[:8] + "..."
	}
	return item.KeyHash
}

func cloneStringSlice(input []string) []string {
	if input == nil {
		return nil
	}
	return append([]string(nil), input...)
}

func stringPtr(value string) *string {
	return &value
}
