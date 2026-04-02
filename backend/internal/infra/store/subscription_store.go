package store

import (
	"context"

	"github.com/DouDOU-start/airgate-core/ent"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
	entusersubscription "github.com/DouDOU-start/airgate-core/ent/usersubscription"
	appsubscription "github.com/DouDOU-start/airgate-core/internal/app/subscription"
)

// SubscriptionStore 使用 Ent 实现订阅仓储。
type SubscriptionStore struct {
	db *ent.Client
}

// NewSubscriptionStore 创建订阅仓储。
func NewSubscriptionStore(db *ent.Client) *SubscriptionStore {
	return &SubscriptionStore{db: db}
}

// ListByUser 查询用户订阅列表。
func (s *SubscriptionStore) ListByUser(ctx context.Context, filter appsubscription.UserListFilter) ([]appsubscription.Subscription, int64, error) {
	query := s.db.UserSubscription.Query().
		Where(entusersubscription.HasUserWith(entuser.IDEQ(filter.UserID))).
		WithGroup()

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	list, err := query.
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Order(ent.Desc(entusersubscription.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	items := mapSubscriptions(list)
	for i := range items {
		items[i].UserID = filter.UserID
	}
	return items, int64(total), nil
}

// ListActiveByUser 查询用户活跃订阅。
func (s *SubscriptionStore) ListActiveByUser(ctx context.Context, userID int) ([]appsubscription.Subscription, error) {
	list, err := s.db.UserSubscription.Query().
		Where(
			entusersubscription.HasUserWith(entuser.IDEQ(userID)),
			entusersubscription.StatusEQ(entusersubscription.StatusActive),
		).
		WithGroup().
		Order(ent.Desc(entusersubscription.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	items := mapSubscriptions(list)
	for i := range items {
		items[i].UserID = userID
	}
	return items, nil
}

// ListAdmin 查询管理员订阅列表。
func (s *SubscriptionStore) ListAdmin(ctx context.Context, filter appsubscription.AdminListFilter) ([]appsubscription.Subscription, int64, error) {
	query := s.db.UserSubscription.Query().
		WithUser().
		WithGroup()

	if filter.Status != "" {
		query = query.Where(entusersubscription.StatusEQ(entusersubscription.Status(filter.Status)))
	}
	if filter.UserID != nil {
		query = query.Where(entusersubscription.HasUserWith(entuser.IDEQ(*filter.UserID)))
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	list, err := query.
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Order(ent.Desc(entusersubscription.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	return mapSubscriptions(list), int64(total), nil
}

// Create 创建订阅并返回包含关联信息的数据。
func (s *SubscriptionStore) Create(ctx context.Context, input appsubscription.CreateInput) (appsubscription.Subscription, error) {
	sub, err := s.db.UserSubscription.Create().
		SetUserID(input.UserID).
		SetGroupID(input.GroupID).
		SetEffectiveAt(input.EffectiveAt).
		SetExpiresAt(input.ExpiresAt).
		SetStatus(entusersubscription.Status(input.Status)).
		Save(ctx)
	if err != nil {
		return appsubscription.Subscription{}, err
	}

	return s.findOneWithEdges(ctx, sub.ID)
}

// BulkCreate 批量创建订阅。
func (s *SubscriptionStore) BulkCreate(ctx context.Context, input appsubscription.BulkCreateInput) (int, error) {
	builders := make([]*ent.UserSubscriptionCreate, 0, len(input.UserIDs))
	for _, userID := range input.UserIDs {
		builder := s.db.UserSubscription.Create().
			SetUserID(userID).
			SetGroupID(input.GroupID).
			SetEffectiveAt(input.EffectiveAt).
			SetExpiresAt(input.ExpiresAt).
			SetStatus(entusersubscription.Status(input.Status))
		builders = append(builders, builder)
	}

	subs, err := s.db.UserSubscription.CreateBulk(builders...).Save(ctx)
	if err != nil {
		return 0, err
	}
	return len(subs), nil
}

// Update 更新订阅并返回包含关联信息的数据。
func (s *SubscriptionStore) Update(ctx context.Context, id int, input appsubscription.UpdateInput) (appsubscription.Subscription, error) {
	builder := s.db.UserSubscription.UpdateOneID(id)

	if input.ExpiresAt != nil {
		builder = builder.SetExpiresAt(*input.ExpiresAt)
	}
	if input.Status != nil {
		builder = builder.SetStatus(entusersubscription.Status(*input.Status))
	}

	if _, err := builder.Save(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appsubscription.Subscription{}, appsubscription.ErrSubscriptionNotFound
		}
		return appsubscription.Subscription{}, err
	}

	return s.findOneWithEdges(ctx, id)
}

func (s *SubscriptionStore) findOneWithEdges(ctx context.Context, id int) (appsubscription.Subscription, error) {
	item, err := s.db.UserSubscription.Query().
		Where(entusersubscription.IDEQ(id)).
		WithUser().
		WithGroup().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appsubscription.Subscription{}, appsubscription.ErrSubscriptionNotFound
		}
		return appsubscription.Subscription{}, err
	}
	return mapSubscription(item), nil
}

func mapSubscriptions(items []*ent.UserSubscription) []appsubscription.Subscription {
	result := make([]appsubscription.Subscription, 0, len(items))
	for _, item := range items {
		result = append(result, mapSubscription(item))
	}
	return result
}

func mapSubscription(item *ent.UserSubscription) appsubscription.Subscription {
	result := appsubscription.Subscription{
		ID:          item.ID,
		EffectiveAt: item.EffectiveAt,
		ExpiresAt:   item.ExpiresAt,
		Usage:       mapSubscriptionUsage(item.Usage),
		Status:      string(item.Status),
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.UpdatedAt,
	}

	if edgeUser := item.Edges.User; edgeUser != nil {
		result.UserID = edgeUser.ID
	}
	if edgeGroup := item.Edges.Group; edgeGroup != nil {
		result.GroupID = edgeGroup.ID
		result.GroupName = edgeGroup.Name
	}

	return result
}

func mapSubscriptionUsage(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}
