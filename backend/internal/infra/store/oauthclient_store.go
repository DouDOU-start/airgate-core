package store

import (
	"context"

	"github.com/DouDOU-start/airgate-core/ent"
	entoauthclient "github.com/DouDOU-start/airgate-core/ent/oauthclient"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
	appoauth "github.com/DouDOU-start/airgate-core/internal/app/oauth"
)

// OAuthClientStore 使用 Ent 实现 OAuth 客户端仓储与用户信息读取。
type OAuthClientStore struct {
	db *ent.Client
}

// NewOAuthClientStore 创建 OAuth 客户端仓储。
func NewOAuthClientStore(db *ent.Client) *OAuthClientStore {
	return &OAuthClientStore{db: db}
}

// List 列出全部客户端（管理面）。
func (s *OAuthClientStore) List(ctx context.Context) ([]appoauth.Client, error) {
	items, err := s.db.OAuthClient.Query().
		Order(ent.Asc(entoauthclient.FieldSortOrder), ent.Desc(entoauthclient.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]appoauth.Client, 0, len(items))
	for _, item := range items {
		result = append(result, mapOAuthClient(item))
	}
	return result, nil
}

// FindByID 按主键查客户端。
func (s *OAuthClientStore) FindByID(ctx context.Context, id int) (appoauth.Client, error) {
	item, err := s.db.OAuthClient.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return appoauth.Client{}, appoauth.ErrClientNotFound
		}
		return appoauth.Client{}, err
	}
	return mapOAuthClient(item), nil
}

// FindByClientID 按对外 client_id 查客户端。
func (s *OAuthClientStore) FindByClientID(ctx context.Context, clientID string) (appoauth.Client, error) {
	item, err := s.db.OAuthClient.Query().
		Where(entoauthclient.ClientIDEQ(clientID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appoauth.Client{}, appoauth.ErrClientNotFound
		}
		return appoauth.Client{}, err
	}
	return mapOAuthClient(item), nil
}

// Create 创建客户端。
func (s *OAuthClientStore) Create(ctx context.Context, clientID, secretHash, secretHint string, m appoauth.ClientMutation) (appoauth.Client, error) {
	item, err := s.db.OAuthClient.Create().
		SetClientID(clientID).
		SetSecretHash(secretHash).
		SetSecretHint(secretHint).
		SetName(m.Name).
		SetDescription(m.Description).
		SetRedirectUris(m.RedirectURIs).
		SetFirstParty(m.FirstParty).
		SetEnabled(m.Enabled).
		SetShowInNav(m.ShowInNav).
		SetLaunchURL(m.LaunchURL).
		SetIcon(m.Icon).
		SetSortOrder(m.SortOrder).
		Save(ctx)
	if err != nil {
		return appoauth.Client{}, err
	}
	return mapOAuthClient(item), nil
}

// Update 全量更新客户端可写字段。
func (s *OAuthClientStore) Update(ctx context.Context, id int, m appoauth.ClientMutation) (appoauth.Client, error) {
	item, err := s.db.OAuthClient.UpdateOneID(id).
		SetName(m.Name).
		SetDescription(m.Description).
		SetRedirectUris(m.RedirectURIs).
		SetFirstParty(m.FirstParty).
		SetEnabled(m.Enabled).
		SetShowInNav(m.ShowInNav).
		SetLaunchURL(m.LaunchURL).
		SetIcon(m.Icon).
		SetSortOrder(m.SortOrder).
		Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appoauth.Client{}, appoauth.ErrClientNotFound
		}
		return appoauth.Client{}, err
	}
	return mapOAuthClient(item), nil
}

// UpdateSecret 重置 secret 哈希与提示。
func (s *OAuthClientStore) UpdateSecret(ctx context.Context, id int, secretHash, secretHint string) (appoauth.Client, error) {
	item, err := s.db.OAuthClient.UpdateOneID(id).
		SetSecretHash(secretHash).
		SetSecretHint(secretHint).
		Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appoauth.Client{}, appoauth.ErrClientNotFound
		}
		return appoauth.Client{}, err
	}
	return mapOAuthClient(item), nil
}

// Delete 删除客户端。
func (s *OAuthClientStore) Delete(ctx context.Context, id int) error {
	err := s.db.OAuthClient.DeleteOneID(id).Exec(ctx)
	if ent.IsNotFound(err) {
		return appoauth.ErrClientNotFound
	}
	return err
}

// ListNav 启用且展示在导航的客户端，按 sort_order 升序。
func (s *OAuthClientStore) ListNav(ctx context.Context) ([]appoauth.Client, error) {
	items, err := s.db.OAuthClient.Query().
		Where(entoauthclient.EnabledEQ(true), entoauthclient.ShowInNavEQ(true)).
		Order(ent.Asc(entoauthclient.FieldSortOrder), ent.Asc(entoauthclient.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]appoauth.Client, 0, len(items))
	for _, item := range items {
		result = append(result, mapOAuthClient(item))
	}
	return result, nil
}

// BasicInfo 读取用户基本信息（userinfo 端点用）。
func (s *OAuthClientStore) BasicInfo(ctx context.Context, id int) (appoauth.UserInfo, error) {
	item, err := s.db.User.Query().
		Where(entuser.IDEQ(id)).
		Select(entuser.FieldID, entuser.FieldEmail, entuser.FieldUsername, entuser.FieldRole, entuser.FieldStatus).
		Only(ctx)
	if err != nil {
		return appoauth.UserInfo{}, err
	}
	return appoauth.UserInfo{
		ID:       item.ID,
		Email:    item.Email,
		Username: item.Username,
		Role:     item.Role.String(),
		Status:   item.Status.String(),
	}, nil
}

func mapOAuthClient(item *ent.OAuthClient) appoauth.Client {
	return appoauth.Client{
		ID:           item.ID,
		ClientID:     item.ClientID,
		SecretHash:   item.SecretHash,
		SecretHint:   item.SecretHint,
		Name:         item.Name,
		Description:  item.Description,
		RedirectURIs: cloneStringSlice(item.RedirectUris),
		FirstParty:   item.FirstParty,
		Enabled:      item.Enabled,
		ShowInNav:    item.ShowInNav,
		LaunchURL:    item.LaunchURL,
		Icon:         item.Icon,
		SortOrder:    item.SortOrder,
		CreatedAt:    item.CreatedAt,
		UpdatedAt:    item.UpdatedAt,
	}
}

var (
	_ appoauth.Repository = (*OAuthClientStore)(nil)
	_ appoauth.UserReader = (*OAuthClientStore)(nil)
)
