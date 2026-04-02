import { useState, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { settingsApi } from '../../shared/api/settings';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { PageHeader } from '../../shared/components/PageHeader';
import { Button } from '../../shared/components/Button';
import { Input } from '../../shared/components/Input';
import { Card } from '../../shared/components/Card';
import { Save, Loader2 } from 'lucide-react';
import type { SettingResp, SettingItem } from '../../shared/types';

export default function SettingsPage() {
  const { t } = useTranslation();

  // 本地编辑状态：{ key: value }
  const [editedValues, setEditedValues] = useState<Record<string, string>>({});
  const [hasChanges, setHasChanges] = useState(false);

  // 获取设置列表
  const { data: settings, isLoading } = useQuery({
    queryKey: queryKeys.settings(),
    queryFn: () => settingsApi.list(),
  });

  // 当设置数据加载后，初始化编辑状态
  useEffect(() => {
    if (settings) {
      const values: Record<string, string> = {};
      for (const s of settings) {
        values[s.key] = s.value;
      }
      setEditedValues(values);
      setHasChanges(false);
    }
  }, [settings]);

  // 保存设置
  const saveMutation = useCrudMutation({
    mutationFn: (items: SettingItem[]) => settingsApi.update({ settings: items }),
    successMessage: t('settings.save_success'),
    queryKey: queryKeys.settings(),
    onSuccess: () => setHasChanges(false),
  });

  // 更新某个设置值
  function handleChange(key: string, value: string) {
    setEditedValues((prev) => ({ ...prev, [key]: value }));
    setHasChanges(true);
  }

  // 提交保存
  function handleSave() {
    const items: SettingItem[] = Object.entries(editedValues).map(([key, value]) => ({
      key,
      value,
    }));
    saveMutation.mutate(items);
  }

  // 按 group 分组
  function groupSettings(list: SettingResp[]): Record<string, SettingResp[]> {
    const groups: Record<string, SettingResp[]> = {};
    for (const s of list) {
      const group = s.group || '通用';
      if (!groups[group]) groups[group] = [];
      groups[group].push(s);
    }
    return groups;
  }

  if (isLoading) {
    return (
      <div>
        <div className="flex items-center justify-center py-16">
          <Loader2 className="w-5 h-5 animate-spin text-primary" />
          <span className="ml-2 text-sm text-text-tertiary">{t('common.loading')}</span>
        </div>
      </div>
    );
  }

  const groups = groupSettings(settings ?? []);

  return (
    <div>
      <PageHeader
        title={t('settings.title')}
        description={t('settings.description')}
        actions={
          <Button
            icon={<Save className="w-4 h-4" />}
            onClick={handleSave}
            loading={saveMutation.isPending}
            disabled={!hasChanges}
          >
            {t('common.save')}
          </Button>
        }
      />

      {Object.keys(groups).length === 0 ? (
        <div className="text-center py-16 text-text-tertiary">
          {t('settings.no_settings')}
        </div>
      ) : (
        <div className="space-y-5">
          {Object.entries(groups).map(([group, items]) => (
            <Card key={group} title={group}>
              <div className="space-y-4">
                {items.map((setting) => (
                  <div key={setting.key} className="flex items-center gap-4">
                    <label
                      className="w-52 shrink-0 text-xs font-medium text-text-secondary uppercase tracking-wider font-mono"
                    >
                      {setting.key}
                    </label>
                    <div className="flex-1">
                      <Input
                        value={editedValues[setting.key] ?? setting.value}
                        onChange={(e) => handleChange(setting.key, e.target.value)}
                      />
                    </div>
                  </div>
                ))}
              </div>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
