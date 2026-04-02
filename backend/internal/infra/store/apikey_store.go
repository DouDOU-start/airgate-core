package store

import (
	"context"

	"github.com/DouDOU-start/airgate-core/ent"
	entapikey "github.com/DouDOU-start/airgate-core/ent/apikey"
	entgroup "github.com/DouDOU-start/airgate-core/ent/group"
	entusagelog "github.com/DouDOU-start/airgate-core/ent/usagelog"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
	appapikey "github.com/DouDOU-start/airgate-core/internal/app/apikey"
)

// APIKeyStore 使用 Ent 实现 API Key 仓储。
type APIKeyStore struct {
	db *ent.Client
}

// NewAPIKeyStore 创建 API Key 仓储。
func NewAPIKeyStore(db *ent.Client) *APIKeyStore {
	return &APIKeyStore{db: db}
}

// ListByUser 查询当前用户 API Key 列表。
func (s *APIKeyStore) ListByUser(ctx context.Context, userID int, filter appapikey.ListFilter) ([]appapikey.Key, int64, error) {
	query := s.db.APIKey.Query().
		Where(entapikey.HasUserWith(entuser.IDEQ(userID))).
		WithUser().
		WithGroup()

	if filter.Keyword != "" {
		query = query.Where(entapikey.NameContains(filter.Keyword))
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	keys, err := query.
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Order(ent.Desc(entapikey.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	result := make([]appapikey.Key, 0, len(keys))
	for _, item := range keys {
		result = append(result, mapAPIKey(item))
	}
	return result, int64(total), nil
}

// KeyUsage 查询 API Key 今日与近 30 天用量。
func (s *APIKeyStore) KeyUsage(ctx context.Context, keyIDs []int) (map[int]float64, map[int]float64, error) {
	return queryAPIKeyUsage(ctx, s.db, keyIDs)
}

// GetGroupAccess 校验用户对分组的访问权限。
func (s *APIKeyStore) GetGroupAccess(ctx context.Context, userID, groupID int) (appapikey.GroupAccess, error) {
	exists, err := s.db.Group.Query().
		Where(entgroup.IDEQ(groupID)).
		Exist(ctx)
	if err != nil {
		return appapikey.GroupAccess{}, err
	}
	if !exists {
		return appapikey.GroupAccess{Exists: false}, nil
	}

	allowed, err := s.db.Group.Query().
		Where(
			entgroup.IDEQ(groupID),
			entgroup.Or(
				entgroup.IsExclusiveEQ(false),
				entgroup.And(
					entgroup.IsExclusiveEQ(true),
					entgroup.HasAllowedUsersWith(entuser.IDEQ(userID)),
				),
			),
		).
		Exist(ctx)
	if err != nil {
		return appapikey.GroupAccess{}, err
	}
	return appapikey.GroupAccess{Exists: true, Allowed: allowed}, nil
}

// Create 创建 API Key。
func (s *APIKeyStore) Create(ctx context.Context, mutation appapikey.Mutation) (appapikey.Key, error) {
	builder := s.db.APIKey.Create()
	applyAPIKeyMutationCreate(builder, mutation)

	item, err := builder.Save(ctx)
	if err != nil {
		return appapikey.Key{}, err
	}
	return s.loadByID(ctx, item.ID)
}

// UpdateOwned 更新当前用户的 API Key。
func (s *APIKeyStore) UpdateOwned(ctx context.Context, userID, id int, mutation appapikey.Mutation) (appapikey.Key, error) {
	exists, err := s.db.APIKey.Query().
		Where(entapikey.IDEQ(id), entapikey.HasUserWith(entuser.IDEQ(userID))).
		Exist(ctx)
	if err != nil {
		return appapikey.Key{}, err
	}
	if !exists {
		return appapikey.Key{}, appapikey.ErrKeyNotFound
	}
	return s.updateByID(ctx, id, mutation)
}

// UpdateAdmin 管理员更新 API Key。
func (s *APIKeyStore) UpdateAdmin(ctx context.Context, id int, mutation appapikey.Mutation) (appapikey.Key, error) {
	return s.updateByID(ctx, id, mutation)
}

// DeleteOwned 删除当前用户 API Key。
func (s *APIKeyStore) DeleteOwned(ctx context.Context, userID, id int) error {
	exists, err := s.db.APIKey.Query().
		Where(entapikey.IDEQ(id), entapikey.HasUserWith(entuser.IDEQ(userID))).
		Exist(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return appapikey.ErrKeyNotFound
	}

	tx, err := s.db.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := tx.UsageLog.Update().
		Where(entusagelog.HasAPIKeyWith(entapikey.IDEQ(id))).
		ClearAPIKey().
		Exec(ctx); err != nil {
		return err
	}
	if err := tx.APIKey.DeleteOneID(id).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

// FindOwned 查询当前用户的 API Key。
func (s *APIKeyStore) FindOwned(ctx context.Context, userID, id int) (appapikey.Key, error) {
	item, err := s.db.APIKey.Query().
		Where(entapikey.IDEQ(id), entapikey.HasUserWith(entuser.IDEQ(userID))).
		WithUser().
		WithGroup().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appapikey.Key{}, appapikey.ErrKeyNotFound
		}
		return appapikey.Key{}, err
	}
	return mapAPIKey(item), nil
}

func (s *APIKeyStore) updateByID(ctx context.Context, id int, mutation appapikey.Mutation) (appapikey.Key, error) {
	builder := s.db.APIKey.UpdateOneID(id)
	applyAPIKeyMutationUpdate(builder, mutation)
	if err := builder.Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appapikey.Key{}, appapikey.ErrKeyNotFound
		}
		return appapikey.Key{}, err
	}
	return s.loadByID(ctx, id)
}

func (s *APIKeyStore) loadByID(ctx context.Context, id int) (appapikey.Key, error) {
	item, err := s.db.APIKey.Query().
		Where(entapikey.IDEQ(id)).
		WithUser().
		WithGroup().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appapikey.Key{}, appapikey.ErrKeyNotFound
		}
		return appapikey.Key{}, err
	}
	return mapAPIKey(item), nil
}

func applyAPIKeyMutationCreate(builder *ent.APIKeyCreate, mutation appapikey.Mutation) {
	if mutation.Name != nil {
		builder.SetName(*mutation.Name)
	}
	if mutation.KeyHint != nil {
		builder.SetKeyHint(*mutation.KeyHint)
	}
	if mutation.KeyHash != nil {
		builder.SetKeyHash(*mutation.KeyHash)
	}
	if mutation.KeyEncrypted != nil {
		builder.SetKeyEncrypted(*mutation.KeyEncrypted)
	}
	if mutation.UserID != nil {
		builder.SetUserID(*mutation.UserID)
	}
	if mutation.GroupID != nil {
		builder.SetGroupID(*mutation.GroupID)
	}
	if mutation.HasIPWhitelist {
		builder.SetIPWhitelist(cloneStringSlice(mutation.IPWhitelist))
	}
	if mutation.HasIPBlacklist {
		builder.SetIPBlacklist(cloneStringSlice(mutation.IPBlacklist))
	}
	if mutation.QuotaUSD != nil {
		builder.SetQuotaUsd(*mutation.QuotaUSD)
	}
	if mutation.HasExpiresAt && mutation.ExpiresAt != nil {
		builder.SetExpiresAt(*mutation.ExpiresAt)
	}
	if mutation.Status != nil {
		builder.SetStatus(entapikey.Status(*mutation.Status))
	}
}

func applyAPIKeyMutationUpdate(builder *ent.APIKeyUpdateOne, mutation appapikey.Mutation) {
	if mutation.Name != nil {
		builder.SetName(*mutation.Name)
	}
	if mutation.GroupID != nil {
		builder.SetGroupID(*mutation.GroupID)
	}
	if mutation.HasIPWhitelist {
		builder.SetIPWhitelist(cloneStringSlice(mutation.IPWhitelist))
	}
	if mutation.HasIPBlacklist {
		builder.SetIPBlacklist(cloneStringSlice(mutation.IPBlacklist))
	}
	if mutation.QuotaUSD != nil {
		builder.SetQuotaUsd(*mutation.QuotaUSD)
	}
	if mutation.HasExpiresAt && mutation.ExpiresAt != nil {
		builder.SetExpiresAt(*mutation.ExpiresAt)
	}
	if mutation.Status != nil {
		builder.SetStatus(entapikey.Status(*mutation.Status))
	}
}

func mapAPIKey(item *ent.APIKey) appapikey.Key {
	result := appapikey.Key{
		ID:           item.ID,
		Name:         item.Name,
		KeyHint:      item.KeyHint,
		KeyHash:      item.KeyHash,
		KeyEncrypted: item.KeyEncrypted,
		IPWhitelist:  cloneStringSlice(item.IPWhitelist),
		IPBlacklist:  cloneStringSlice(item.IPBlacklist),
		QuotaUSD:     item.QuotaUsd,
		UsedQuota:    item.UsedQuota,
		Status:       item.Status.String(),
		CreatedAt:    item.CreatedAt,
		UpdatedAt:    item.UpdatedAt,
	}
	if item.ExpiresAt != nil {
		value := *item.ExpiresAt
		result.ExpiresAt = &value
	}
	if item.Edges.User != nil {
		result.UserID = item.Edges.User.ID
	}
	if item.Edges.Group != nil {
		groupID := item.Edges.Group.ID
		result.GroupID = &groupID
	}
	return result
}

func cloneStringSlice(input []string) []string {
	if input == nil {
		return nil
	}
	return append([]string(nil), input...)
}

var _ appapikey.Repository = (*APIKeyStore)(nil)
