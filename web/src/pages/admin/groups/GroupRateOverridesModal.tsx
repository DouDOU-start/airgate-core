import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Trash2, Plus, Check, X } from 'lucide-react';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { Input } from '../../../shared/components/Input';
import { PlatformIcon } from '../../../shared/components/PlatformIcon';
import { groupsApi } from '../../../shared/api/groups';
import { usersApi } from '../../../shared/api/users';
import { useCrudMutation } from '../../../shared/hooks/useCrudMutation';
import type { GroupResp, GroupRateOverrideResp, UserResp } from '../../../shared/types';

interface GroupRateOverridesModalProps {
  open: boolean;
  group: GroupResp;
  onClose: () => void;
}

export function GroupRateOverridesModal({ open, group, onClose }: GroupRateOverridesModalProps) {
  const { t } = useTranslation();

  // 添加表单
  const [emailQuery, setEmailQuery] = useState('');
  const [pickedUser, setPickedUser] = useState<UserResp | null>(null);
  const [newRate, setNewRate] = useState<string>('1');

  // 单行内联编辑
  const [editingUserId, setEditingUserId] = useState<number | null>(null);
  const [editingRate, setEditingRate] = useState<string>('');

  const overridesKey = ['group-rate-overrides', group.id] as const;
  const { data: overrides = [], isLoading } = useQuery({
    queryKey: overridesKey,
    queryFn: () => groupsApi.listRateOverrides(group.id),
    enabled: open,
  });

  // 用户搜索（只有在输入了关键字且未选中时触发）
  const { data: searchData } = useQuery({
    queryKey: ['users-search', emailQuery],
    queryFn: () => usersApi.list({ page: 1, page_size: 10, keyword: emailQuery }),
    enabled: open && emailQuery.trim().length > 0 && !pickedUser,
  });

  const setMutation = useCrudMutation({
    mutationFn: (payload: { userId: number; rate: number }) =>
      groupsApi.setRateOverride(group.id, payload.userId, payload.rate),
    successMessage: t('groups.rate_override_set_success'),
    queryKey: overridesKey,
    onSuccess: () => {
      // 重置添加表单
      setEmailQuery('');
      setPickedUser(null);
      setNewRate('1');
      // 结束内联编辑
      setEditingUserId(null);
    },
  });

  const deleteMutation = useCrudMutation({
    mutationFn: (userId: number) => groupsApi.deleteRateOverride(group.id, userId),
    successMessage: t('groups.rate_override_delete_success'),
    queryKey: overridesKey,
  });

  const newRateNum = Number(newRate);
  const canAdd = !!pickedUser && Number.isFinite(newRateNum) && newRateNum > 0;

  // 已经在覆盖列表里的 user_id 集合，用于在搜索结果里标记"已存在"
  const existingUserIds = useMemo(
    () => new Set((overrides as GroupRateOverrideResp[]).map((o) => o.user_id)),
    [overrides],
  );

  const searchResults = (searchData?.list ?? []).filter((u) => !existingUserIds.has(u.id));

  const handleAdd = () => {
    if (!canAdd || !pickedUser) return;
    setMutation.mutate({ userId: pickedUser.id, rate: newRateNum });
  };

  const startEdit = (row: GroupRateOverrideResp) => {
    setEditingUserId(row.user_id);
    setEditingRate(String(row.rate));
  };

  const commitEdit = (userId: number) => {
    const v = Number(editingRate);
    if (!Number.isFinite(v) || v <= 0) return;
    setMutation.mutate({ userId, rate: v });
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t('groups.rate_override_title')}
      width="560px"
      footer={<Button variant="secondary" onClick={onClose}>{t('common.close')}</Button>}
    >
      {/* 分组头信息 */}
      <div
        className="flex items-center gap-3 px-3 py-2.5 mb-4 rounded-lg text-sm"
        style={{ borderWidth: '1px', borderStyle: 'solid', borderColor: 'var(--ag-glass-border)' }}
      >
        <PlatformIcon platform={group.platform} className="w-4 h-4" />
        <span className="font-medium" style={{ color: 'var(--ag-text)' }}>{group.name}</span>
        <span style={{ color: 'var(--ag-text-tertiary)' }}>|</span>
        <span style={{ color: 'var(--ag-text-tertiary)' }}>{group.platform}</span>
        <span style={{ color: 'var(--ag-text-tertiary)' }}>|</span>
        <span style={{ color: 'var(--ag-text-tertiary)' }}>
          {t('groups.default_rate')}: <span className="font-mono" style={{ color: 'var(--ag-primary)' }}>{group.rate_multiplier}x</span>
        </span>
      </div>

      {/* 添加用户专属倍率 */}
      <div className="mb-4">
        <p className="text-xs font-medium mb-2 uppercase tracking-wider" style={{ color: 'var(--ag-text-secondary)' }}>
          {t('groups.rate_override_add')}
        </p>
        <div className="flex gap-2 items-start">
          <div className="flex-1 relative">
            <Input
              placeholder={t('groups.rate_override_search_placeholder') ?? ''}
              value={pickedUser ? pickedUser.email : emailQuery}
              disabled={!!pickedUser}
              onChange={(e) => setEmailQuery(e.target.value)}
            />
            {pickedUser && (
              <button
                type="button"
                onClick={() => { setPickedUser(null); setEmailQuery(''); }}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-text-tertiary hover:text-text-primary"
                aria-label="clear"
              >
                <X className="w-3.5 h-3.5" />
              </button>
            )}
            {!pickedUser && emailQuery.trim().length > 0 && searchResults.length > 0 && (
              <div
                className="absolute z-10 left-0 right-0 mt-1 rounded-lg shadow-lg max-h-48 overflow-y-auto"
                style={{ background: 'var(--ag-bg-elevated)', borderWidth: '1px', borderStyle: 'solid', borderColor: 'var(--ag-glass-border)' }}
              >
                {searchResults.map((u) => (
                  <button
                    key={u.id}
                    type="button"
                    onClick={() => { setPickedUser(u); setEmailQuery(''); }}
                    className="flex items-center gap-2 w-full px-3 py-2 text-left text-sm hover:bg-bg-hover transition-colors"
                  >
                    <span style={{ color: 'var(--ag-text)' }}>{u.email}</span>
                    {u.username && (
                      <span className="text-xs" style={{ color: 'var(--ag-text-tertiary)' }}>{u.username}</span>
                    )}
                  </button>
                ))}
              </div>
            )}
          </div>
          <Input
            type="number"
            min="0"
            step="0.01"
            value={newRate}
            onChange={(e) => setNewRate(e.target.value)}
            className="w-24"
          />
          <Button
            icon={<Plus className="w-3.5 h-3.5" />}
            disabled={!canAdd}
            loading={setMutation.isPending && !editingUserId}
            onClick={handleAdd}
          >
            {t('common.add')}
          </Button>
        </div>
      </div>

      {/* 专属倍率列表 */}
      <div>
        <p className="text-xs font-medium mb-2 uppercase tracking-wider" style={{ color: 'var(--ag-text-secondary)' }}>
          {t('groups.rate_override_list', { count: overrides.length })}
        </p>
        {isLoading ? (
          <p className="text-sm text-center py-8" style={{ color: 'var(--ag-text-tertiary)' }}>{t('common.loading')}</p>
        ) : overrides.length === 0 ? (
          <p className="text-sm text-center py-8" style={{ color: 'var(--ag-text-tertiary)' }}>{t('groups.rate_override_empty')}</p>
        ) : (
          <div
            className="rounded-lg overflow-hidden"
            style={{ borderWidth: '1px', borderStyle: 'solid', borderColor: 'var(--ag-glass-border)' }}
          >
            {(overrides as GroupRateOverrideResp[]).map((row, idx) => {
              const isEditing = editingUserId === row.user_id;
              return (
                <div
                  key={row.user_id}
                  className="flex items-center gap-3 px-3 py-2.5 text-sm"
                  style={{
                    borderTopWidth: idx === 0 ? 0 : '1px',
                    borderTopStyle: 'solid',
                    borderTopColor: 'var(--ag-glass-border)',
                  }}
                >
                  <div className="flex-1 min-w-0">
                    <div className="truncate" style={{ color: 'var(--ag-text)' }}>{row.email}</div>
                    {row.username && (
                      <div className="text-[11px] truncate" style={{ color: 'var(--ag-text-tertiary)' }}>{row.username}</div>
                    )}
                  </div>
                  {isEditing ? (
                    <>
                      <input
                        type="number"
                        min="0"
                        step="0.01"
                        value={editingRate}
                        onChange={(e) => setEditingRate(e.target.value)}
                        className="w-20 px-2 py-1 text-xs text-right rounded border bg-transparent focus:outline-none focus:ring-1 focus:ring-primary"
                        style={{ borderColor: 'var(--ag-glass-border)', color: 'var(--ag-text)' }}
                        autoFocus
                      />
                      <Button
                        size="sm"
                        variant="ghost"
                        icon={<Check className="w-3.5 h-3.5" />}
                        onClick={() => commitEdit(row.user_id)}
                        loading={setMutation.isPending}
                      >{t('common.save')}</Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        icon={<X className="w-3.5 h-3.5" />}
                        onClick={() => setEditingUserId(null)}
                      >{t('common.cancel')}</Button>
                    </>
                  ) : (
                    <>
                      <button
                        type="button"
                        onClick={() => startEdit(row)}
                        className="font-mono text-xs px-2 py-1 rounded hover:bg-bg-hover transition-colors"
                        style={{ color: 'var(--ag-primary)' }}
                        title={t('common.edit')}
                      >
                        {row.rate}x
                      </button>
                      <Button
                        size="sm"
                        variant="ghost"
                        icon={<Trash2 className="w-3.5 h-3.5" />}
                        style={{ color: 'var(--ag-danger)' }}
                        onClick={() => deleteMutation.mutate(row.user_id)}
                        loading={deleteMutation.isPending}
                      />
                    </>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </Modal>
  );
}
