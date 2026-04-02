package store

import (
	"context"

	"github.com/DouDOU-start/airgate-core/ent"
	entproxy "github.com/DouDOU-start/airgate-core/ent/proxy"
	appproxy "github.com/DouDOU-start/airgate-core/internal/app/proxy"
)

// ProxyStore 使用 Ent 实现代理仓储。
type ProxyStore struct {
	db *ent.Client
}

// NewProxyStore 创建代理仓储。
func NewProxyStore(db *ent.Client) *ProxyStore {
	return &ProxyStore{db: db}
}

// List 查询代理列表。
func (s *ProxyStore) List(ctx context.Context, filter appproxy.ListFilter) ([]appproxy.Proxy, int64, error) {
	query := s.db.Proxy.Query()
	if filter.Keyword != "" {
		query = query.Where(entproxy.NameContains(filter.Keyword))
	}
	if filter.Status != "" {
		query = query.Where(entproxy.StatusEQ(entproxy.Status(filter.Status)))
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	items, err := query.
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Order(ent.Desc(entproxy.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	return mapProxyList(items), int64(total), nil
}

// FindByID 按 ID 查询代理。
func (s *ProxyStore) FindByID(ctx context.Context, id int) (appproxy.Proxy, error) {
	item, err := s.db.Proxy.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return appproxy.Proxy{}, appproxy.ErrProxyNotFound
		}
		return appproxy.Proxy{}, err
	}
	return mapProxy(item), nil
}

// Create 创建代理。
func (s *ProxyStore) Create(ctx context.Context, input appproxy.CreateInput) (appproxy.Proxy, error) {
	builder := s.db.Proxy.Create().
		SetName(input.Name).
		SetProtocol(entproxy.Protocol(input.Protocol)).
		SetAddress(input.Address).
		SetPort(input.Port)

	if input.Username != "" {
		builder = builder.SetUsername(input.Username)
	}
	if input.Password != "" {
		builder = builder.SetPassword(input.Password)
	}

	item, err := builder.Save(ctx)
	if err != nil {
		return appproxy.Proxy{}, err
	}
	return mapProxy(item), nil
}

// Update 更新代理。
func (s *ProxyStore) Update(ctx context.Context, id int, input appproxy.UpdateInput) (appproxy.Proxy, error) {
	builder := s.db.Proxy.UpdateOneID(id)

	if input.Name != nil {
		builder = builder.SetName(*input.Name)
	}
	if input.Protocol != nil {
		builder = builder.SetProtocol(entproxy.Protocol(*input.Protocol))
	}
	if input.Address != nil {
		builder = builder.SetAddress(*input.Address)
	}
	if input.Port != nil {
		builder = builder.SetPort(*input.Port)
	}
	if input.Username != nil {
		builder = builder.SetUsername(*input.Username)
	}
	if input.Password != nil {
		builder = builder.SetPassword(*input.Password)
	}
	if input.Status != nil {
		builder = builder.SetStatus(entproxy.Status(*input.Status))
	}

	item, err := builder.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appproxy.Proxy{}, appproxy.ErrProxyNotFound
		}
		return appproxy.Proxy{}, err
	}
	return mapProxy(item), nil
}

// Delete 删除代理。
func (s *ProxyStore) Delete(ctx context.Context, id int) error {
	if err := s.db.Proxy.DeleteOneID(id).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appproxy.ErrProxyNotFound
		}
		return err
	}
	return nil
}

func mapProxyList(items []*ent.Proxy) []appproxy.Proxy {
	result := make([]appproxy.Proxy, 0, len(items))
	for _, item := range items {
		result = append(result, mapProxy(item))
	}
	return result
}

func mapProxy(item *ent.Proxy) appproxy.Proxy {
	return appproxy.Proxy{
		ID:        item.ID,
		Name:      item.Name,
		Protocol:  item.Protocol.String(),
		Address:   item.Address,
		Port:      item.Port,
		Username:  item.Username,
		Password:  item.Password,
		Status:    item.Status.String(),
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
	}
}
