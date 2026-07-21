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
				creator := tx.Setting.Create().
					SetKey(item.Key).
					SetValue(item.Value)
				if item.Group != "" {
					creator = creator.SetGroup(item.Group)
				}
				if _, err := creator.Save(ctx); err != nil {
					return err
				}
				continue
			}
			return err
		}

		updater := existing.Update().SetValue(item.Value)
		if item.Group != "" {
			updater = updater.SetGroup(item.Group)
		}
		if _, err := updater.Save(ctx); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GroupValues 返回某组全部设置的 key→value 映射（风控中心等专用通道使用，
// 绕开 app/settings 的敏感组屏蔽——屏蔽只针对管理端通用读写路径）。
func (s *SettingsStore) GroupValues(ctx context.Context, group string) (map[string]string, error) {
	items, err := s.db.Setting.Query().
		Where(entsetting.GroupEQ(group)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(items))
	for _, item := range items {
		out[item.Key] = item.Value
	}
	return out, nil
}

// UpsertValue 写单条设置（按 key 冲突合并，与 UpsertMany 同口径）。
func (s *SettingsStore) UpsertValue(ctx context.Context, group, key, value string) error {
	return s.UpsertMany(ctx, []appsettings.ItemInput{{Key: key, Value: value, Group: group}})
}
