package store

import (
	"context"
	"fmt"

	"github.com/DouDOU-start/airgate-core/ent"
	entproxy "github.com/DouDOU-start/airgate-core/ent/proxy"
	appproxy "github.com/DouDOU-start/airgate-core/internal/app/proxy"
	"github.com/DouDOU-start/airgate-core/internal/auth"
)

// ProxyStore 使用 Ent 实现代理仓储。
type ProxyStore struct {
	db     *ent.Client
	secret string
}

// NewProxyStore 创建代理仓储。
func NewProxyStore(db *ent.Client, secret string) *ProxyStore {
	return &ProxyStore{db: db, secret: secret}
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

	result, err := s.mapProxyList(items)
	return result, int64(total), err
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
	return s.mapProxy(item)
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
		encrypted, err := auth.EncryptSecretValue(input.Password, s.secret)
		if err != nil {
			return appproxy.Proxy{}, fmt.Errorf("加密代理密码失败: %w", err)
		}
		builder = builder.SetPassword(encrypted)
	}

	item, err := builder.Save(ctx)
	if err != nil {
		return appproxy.Proxy{}, err
	}
	return s.mapProxy(item)
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
		encrypted, err := auth.EncryptSecretValue(*input.Password, s.secret)
		if err != nil {
			return appproxy.Proxy{}, fmt.Errorf("加密代理密码失败: %w", err)
		}
		builder = builder.SetPassword(encrypted)
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
	return s.mapProxy(item)
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

func (s *ProxyStore) mapProxyList(items []*ent.Proxy) ([]appproxy.Proxy, error) {
	result := make([]appproxy.Proxy, 0, len(items))
	for _, item := range items {
		mapped, err := s.mapProxy(item)
		if err != nil {
			return nil, err
		}
		result = append(result, mapped)
	}
	return result, nil
}

func (s *ProxyStore) mapProxy(item *ent.Proxy) (appproxy.Proxy, error) {
	password, err := auth.DecryptSecretValue(item.Password, s.secret)
	if err != nil {
		return appproxy.Proxy{}, fmt.Errorf("解密代理 %d 的密码失败: %w", item.ID, err)
	}
	return appproxy.Proxy{
		ID:        item.ID,
		Name:      item.Name,
		Protocol:  item.Protocol.String(),
		Address:   item.Address,
		Port:      item.Port,
		Username:  item.Username,
		Password:  password,
		Status:    item.Status.String(),
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
	}, nil
}
