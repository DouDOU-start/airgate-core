package store

import (
	"context"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entapikey "github.com/DouDOU-start/airgate-core/ent/apikey"
	entbalancelog "github.com/DouDOU-start/airgate-core/ent/balancelog"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
	appuser "github.com/DouDOU-start/airgate-core/internal/app/user"
)

// UserStore 使用 Ent 实现用户仓储。
type UserStore struct {
	db *ent.Client
}

// NewUserStore 创建用户仓储。
func NewUserStore(db *ent.Client) *UserStore {
	return &UserStore{db: db}
}

// FindByID 查询用户。
func (s *UserStore) FindByID(ctx context.Context, id int, withAllowedGroups bool) (appuser.User, error) {
	query := s.db.User.Query().Where(entuser.IDEQ(id))
	if withAllowedGroups {
		query = query.WithAllowedGroups()
	}
	item, err := query.Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appuser.User{}, appuser.ErrUserNotFound
		}
		return appuser.User{}, err
	}
	return mapUser(item), nil
}

// List 查询用户列表。
func (s *UserStore) List(ctx context.Context, filter appuser.ListFilter) ([]appuser.User, int64, error) {
	query := s.db.User.Query()
	if filter.Keyword != "" {
		query = query.Where(
			entuser.Or(
				entuser.EmailContains(filter.Keyword),
				entuser.UsernameContains(filter.Keyword),
			),
		)
	}
	if filter.Status != "" {
		query = query.Where(entuser.StatusEQ(entuser.Status(filter.Status)))
	}
	if filter.Role != "" {
		query = query.Where(entuser.RoleEQ(entuser.Role(filter.Role)))
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	users, err := query.
		WithAllowedGroups().
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Order(ent.Desc(entuser.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	result := make([]appuser.User, 0, len(users))
	for _, item := range users {
		result = append(result, mapUser(item))
	}
	return result, int64(total), nil
}

// EmailExists 检查邮箱是否已存在。
func (s *UserStore) EmailExists(ctx context.Context, email string) (bool, error) {
	return s.db.User.Query().Where(entuser.EmailEQ(email)).Exist(ctx)
}

// Create 创建用户。
func (s *UserStore) Create(ctx context.Context, mutation appuser.Mutation) (appuser.User, error) {
	builder := s.db.User.Create()
	applyUserMutationCreate(builder, mutation)
	item, err := builder.Save(ctx)
	if err != nil {
		return appuser.User{}, err
	}
	return s.FindByID(ctx, item.ID, true)
}

// Update 更新用户。
func (s *UserStore) Update(ctx context.Context, id int, mutation appuser.Mutation) (appuser.User, error) {
	builder := s.db.User.UpdateOneID(id)
	applyUserMutationUpdate(builder, mutation)
	if _, err := builder.Save(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appuser.User{}, appuser.ErrUserNotFound
		}
		return appuser.User{}, err
	}
	return s.FindByID(ctx, id, true)
}

// UpdateBalance 更新用户余额并写日志。
func (s *UserStore) UpdateBalance(ctx context.Context, id int, update appuser.BalanceUpdate) (appuser.User, error) {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return appuser.User{}, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	item, err := tx.User.UpdateOneID(id).
		SetBalance(update.AfterBalance).
		Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appuser.User{}, appuser.ErrUserNotFound
		}
		return appuser.User{}, err
	}

	if _, err := tx.BalanceLog.Create().
		SetAction(entbalancelog.Action(update.Action)).
		SetAmount(update.Amount).
		SetBeforeBalance(update.BeforeBalance).
		SetAfterBalance(update.AfterBalance).
		SetRemark(update.Remark).
		SetUserID(id).
		Save(ctx); err != nil {
		return appuser.User{}, err
	}

	if err := tx.Commit(); err != nil {
		return appuser.User{}, err
	}
	return s.FindByID(ctx, item.ID, true)
}

// Delete 删除用户。
func (s *UserStore) Delete(ctx context.Context, id int) error {
	if err := s.db.User.DeleteOneID(id).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appuser.ErrUserNotFound
		}
		return err
	}
	return nil
}

// ListBalanceLogs 查询余额日志。
func (s *UserStore) ListBalanceLogs(ctx context.Context, userID, page, pageSize int) ([]appuser.BalanceLog, int64, error) {
	query := s.db.BalanceLog.Query().
		Where(entbalancelog.HasUserWith(entuser.IDEQ(userID)))

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	logs, err := query.
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Order(ent.Desc(entbalancelog.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	result := make([]appuser.BalanceLog, 0, len(logs))
	for _, item := range logs {
		result = append(result, appuser.BalanceLog{
			ID:            int64(item.ID),
			Action:        item.Action.String(),
			Amount:        item.Amount,
			BeforeBalance: item.BeforeBalance,
			AfterBalance:  item.AfterBalance,
			Remark:        item.Remark,
			CreatedAt:     item.CreatedAt.Format(time.RFC3339),
		})
	}
	return result, int64(total), nil
}

// GetAPIKeyName 获取 API Key 名称。
func (s *UserStore) GetAPIKeyName(ctx context.Context, keyID int) (string, error) {
	ak, err := s.db.APIKey.Get(ctx, keyID)
	if err != nil {
		return "", err
	}
	return ak.Name, nil
}

// GetAPIKeyInfo 获取 API Key 基本信息（名称、额度、到期时间）。
func (s *UserStore) GetAPIKeyInfo(ctx context.Context, keyID int) (appuser.APIKeyBrief, error) {
	ak, err := s.db.APIKey.Get(ctx, keyID)
	if err != nil {
		return appuser.APIKeyBrief{}, err
	}
	return appuser.APIKeyBrief{
		Name:      ak.Name,
		QuotaUSD:  ak.QuotaUsd,
		UsedQuota: ak.UsedQuota,
		ExpiresAt: ak.ExpiresAt,
	}, nil
}

// ListAPIKeys 查询指定用户的 API Key 列表。
func (s *UserStore) ListAPIKeys(ctx context.Context, userID, page, pageSize int) ([]appuser.APIKey, int64, error) {
	query := s.db.APIKey.Query().
		Where(entapikey.HasUserWith(entuser.IDEQ(userID))).
		WithGroup()

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	items, err := query.
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Order(ent.Desc(entapikey.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	keyIDs := make([]int, 0, len(items))
	for _, item := range items {
		keyIDs = append(keyIDs, item.ID)
	}
	todayUsage, thirtyDayUsage, err := queryAPIKeyUsage(ctx, s.db, keyIDs)
	if err != nil {
		return nil, 0, err
	}

	result := make([]appuser.APIKey, 0, len(items))
	for _, item := range items {
		result = append(result, mapUserAPIKey(item, userID, todayUsage[item.ID], thirtyDayUsage[item.ID]))
	}
	return result, int64(total), nil
}

func applyUserMutationCreate(builder *ent.UserCreate, mutation appuser.Mutation) {
	if mutation.Email != nil {
		builder.SetEmail(*mutation.Email)
	}
	if mutation.Username != nil {
		builder.SetUsername(*mutation.Username)
	}
	if mutation.PasswordHash != nil {
		builder.SetPasswordHash(*mutation.PasswordHash)
	}
	if mutation.Role != nil {
		builder.SetRole(entuser.Role(*mutation.Role))
	}
	if mutation.MaxConcurrency != nil {
		builder.SetMaxConcurrency(*mutation.MaxConcurrency)
	}
	if mutation.HasGroupRates {
		builder.SetGroupRates(cloneUserGroupRates(mutation.GroupRates))
	}
}

func applyUserMutationUpdate(builder *ent.UserUpdateOne, mutation appuser.Mutation) {
	if mutation.Username != nil {
		builder.SetUsername(*mutation.Username)
	}
	if mutation.PasswordHash != nil {
		builder.SetPasswordHash(*mutation.PasswordHash)
	}
	if mutation.Role != nil {
		builder.SetRole(entuser.Role(*mutation.Role))
	}
	if mutation.MaxConcurrency != nil {
		builder.SetMaxConcurrency(*mutation.MaxConcurrency)
	}
	if mutation.HasGroupRates {
		builder.SetGroupRates(cloneUserGroupRates(mutation.GroupRates))
	}
	if mutation.HasAllowedGroupIDs {
		builder.ClearAllowedGroups()
		if len(mutation.AllowedGroupIDs) > 0 {
			ids := make([]int, 0, len(mutation.AllowedGroupIDs))
			for _, value := range mutation.AllowedGroupIDs {
				ids = append(ids, int(value))
			}
			builder.AddAllowedGroupIDs(ids...)
		}
	}
	if mutation.Status != nil {
		builder.SetStatus(entuser.Status(*mutation.Status))
	}
}

// UpdateBalanceAlert 更新余额预警阈值。
func (s *UserStore) UpdateBalanceAlert(ctx context.Context, userID int, threshold float64) error {
	return s.db.User.UpdateOneID(userID).
		SetBalanceAlertThreshold(threshold).
		SetBalanceAlertNotified(false). // 改阈值时重置通知状态
		Exec(ctx)
}

// SetBalanceAlertNotified 设置余额预警通知状态。
func (s *UserStore) SetBalanceAlertNotified(ctx context.Context, userID int, notified bool) error {
	return s.db.User.UpdateOneID(userID).
		SetBalanceAlertNotified(notified).
		Exec(ctx)
}

func mapUser(item *ent.User) appuser.User {
	result := appuser.User{
		ID:                    item.ID,
		Email:                 item.Email,
		Username:              item.Username,
		PasswordHash:          item.PasswordHash,
		Balance:               item.Balance,
		Role:                  item.Role.String(),
		MaxConcurrency:        item.MaxConcurrency,
		GroupRates:            cloneUserGroupRates(item.GroupRates),
		BalanceAlertThreshold: item.BalanceAlertThreshold,
		BalanceAlertNotified:  item.BalanceAlertNotified,
		Status:                item.Status.String(),
		CreatedAt:             item.CreatedAt,
		UpdatedAt:             item.UpdatedAt,
	}
	if item.Edges.AllowedGroups != nil {
		result.AllowedGroupIDs = make([]int64, 0, len(item.Edges.AllowedGroups))
		for _, group := range item.Edges.AllowedGroups {
			result.AllowedGroupIDs = append(result.AllowedGroupIDs, int64(group.ID))
		}
	}
	return result
}

func cloneUserGroupRates(input map[int64]float64) map[int64]float64 {
	if input == nil {
		return nil
	}
	cloned := make(map[int64]float64, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func mapUserAPIKey(item *ent.APIKey, userID int, todayCost, thirtyDayCost float64) appuser.APIKey {
	var groupID *int
	if item.Edges.Group != nil {
		value := item.Edges.Group.ID
		groupID = &value
	}

	return appuser.APIKey{
		ID:            item.ID,
		Name:          item.Name,
		KeyHint:       item.KeyHint,
		KeyHash:       item.KeyHash,
		UserID:        userID,
		GroupID:       groupID,
		IPWhitelist:   append([]string(nil), item.IPWhitelist...),
		IPBlacklist:   append([]string(nil), item.IPBlacklist...),
		QuotaUSD:      item.QuotaUsd,
		UsedQuota:     item.UsedQuota,
		TodayCost:     todayCost,
		ThirtyDayCost: thirtyDayCost,
		ExpiresAt:     item.ExpiresAt,
		Status:        item.Status.String(),
		CreatedAt:     item.CreatedAt,
		UpdatedAt:     item.UpdatedAt,
	}
}

var _ appuser.Repository = (*UserStore)(nil)
