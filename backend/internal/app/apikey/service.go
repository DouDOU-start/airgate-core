package apikey

import (
	"context"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/auth"
)

const (
	defaultPage     = 1
	defaultPageSize = 20
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
func (s *Service) ListByUser(ctx context.Context, userID int, filter ListFilter) (ListResult, error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.ListByUser(ctx, userID, filter)
	if err != nil {
		return ListResult{}, err
	}

	keyIDs := make([]int, 0, len(list))
	for _, item := range list {
		keyIDs = append(keyIDs, item.ID)
	}
	todayMap, thirtyDayMap, err := s.repo.KeyUsage(ctx, keyIDs)
	if err != nil {
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

// CreateOwned 创建当前用户的 API Key。
func (s *Service) CreateOwned(ctx context.Context, userID int, input CreateInput) (Key, error) {
	groupID := int(input.GroupID)
	if err := s.ensureUserCanUseGroup(ctx, userID, groupID); err != nil {
		return Key{}, err
	}

	rawKey, keyHash, err := auth.GenerateAPIKey()
	if err != nil {
		return Key{}, err
	}
	encrypted, err := auth.EncryptAPIKey(rawKey, s.secret)
	if err != nil {
		return Key{}, err
	}
	expiresAt, hasExpiresAt, err := parseExpiresAt(input.ExpiresAt)
	if err != nil {
		return Key{}, err
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
		ExpiresAt:      expiresAt,
		HasExpiresAt:   hasExpiresAt,
	})
	if err != nil {
		return Key{}, err
	}

	item.PlainKey = rawKey
	return item, nil
}

// UpdateOwned 更新当前用户的 API Key。
func (s *Service) UpdateOwned(ctx context.Context, userID, id int, input UpdateInput) (Key, error) {
	mutation, err := s.buildMutation(ctx, userID, input, true)
	if err != nil {
		return Key{}, err
	}
	return s.repo.UpdateOwned(ctx, userID, id, mutation)
}

// UpdateAdmin 管理员更新 API Key。
func (s *Service) UpdateAdmin(ctx context.Context, id int, input UpdateInput) (Key, error) {
	mutation, err := s.buildMutation(ctx, 0, input, false)
	if err != nil {
		return Key{}, err
	}
	return s.repo.UpdateAdmin(ctx, id, mutation)
}

// DeleteOwned 删除当前用户的 API Key。
func (s *Service) DeleteOwned(ctx context.Context, userID, id int) error {
	return s.repo.DeleteOwned(ctx, userID, id)
}

// RevealOwned 查看当前用户的 API Key 原文。
func (s *Service) RevealOwned(ctx context.Context, userID, id int) (Key, error) {
	item, err := s.repo.FindOwned(ctx, userID, id)
	if err != nil {
		return Key{}, err
	}
	if item.KeyEncrypted == "" {
		return Key{}, ErrLegacyKeyNotReveal
	}
	plainKey, err := auth.DecryptAPIKey(item.KeyEncrypted, s.secret)
	if err != nil {
		return Key{}, ErrKeyDecryptFailed
	}
	item.PlainKey = plainKey
	return item, nil
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
		return nil, false, nil
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

func normalizePage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = defaultPage
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	return page, pageSize
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
