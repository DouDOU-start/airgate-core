package store

import (
	"context"

	"github.com/DouDOU-start/airgate-core/ent"
	entsetting "github.com/DouDOU-start/airgate-core/ent/setting"
	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
)

// SettingsStore 使用 Ent 实现设置仓储。
type SettingsStore struct {
	db *ent.Client
}

// NewSettingsStore 创建设置仓储。
func NewSettingsStore(db *ent.Client) *SettingsStore {
	return &SettingsStore{db: db}
}

// List 查询设置列表。
func (s *SettingsStore) List(ctx context.Context, group string) ([]appsettings.Setting, error) {
	query := s.db.Setting.Query().Order(entsetting.ByGroup(), entsetting.ByKey())
	if group != "" {
		query = query.Where(entsetting.GroupEQ(group))
	}

	items, err := query.All(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]appsettings.Setting, 0, len(items))
	for _, item := range items {
		result = append(result, appsettings.Setting{
			Key:   item.Key,
			Value: item.Value,
			Group: item.Group,
		})
	}
	return result, nil
}

// UpsertMany 批量更新或创建设置。
func (s *SettingsStore) UpsertMany(ctx context.Context, items []appsettings.ItemInput) error {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	for _, item := range items {
		existing, err := tx.Setting.Query().
			Where(entsetting.KeyEQ(item.Key)).
			Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				if _, err := tx.Setting.Create().
					SetKey(item.Key).
					SetValue(item.Value).
					Save(ctx); err != nil {
					return err
				}
				continue
			}
			return err
		}

		if _, err := existing.Update().
			SetValue(item.Value).
			Save(ctx); err != nil {
			return err
		}
	}

	return tx.Commit()
}
