import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { usersApi } from '../../../shared/api/users';
import { groupsApi } from '../../../shared/api/groups';
import { useCrudMutation } from '../../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../../shared/queryKeys';
import { FETCH_ALL_PARAMS } from '../../../shared/constants';
import type { UserResp, GroupResp, UpdateUserReq } from '../../../shared/types';

interface UserGroupsModalProps {
  open: boolean;
  user: UserResp;
  onClose: () => void;
  onSaved: () => void;
}

// 把 user.group_rates 中的数字键转成内部字符串状态（便于受控 <input>）
function initialRateState(groupRates?: Record<number, number>): Record<number, string> {
  const out: Record<number, string> = {};
  if (!groupRates) return out;
  for (const [k, v] of Object.entries(groupRates)) {
    if (typeof v === 'number' && v > 0) {
      out[Number(k)] = String(v);
    }
  }
  return out;
}

export function UserGroupsModal({ open, user, onClose, onSaved }: UserGroupsModalProps) {
  const { t } = useTranslation();
  const [selectedIds, setSelectedIds] = useState<number[]>(user.allowed_group_ids ?? []);
  // groupId -> 用户侧的倍率输入值（字符串，空串 = 使用分组默认倍率）
  const [customRates, setCustomRates] = useState<Record<number, string>>(() => initialRateState(user.group_rates));

  const { data: groupsData, isLoading: groupsLoading } = useQuery({
    queryKey: queryKeys.groupsAll(),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
    enabled: open,
  });

  const allGroups: GroupResp[] = groupsData?.list ?? [];
  const exclusiveGroups = allGroups.filter((g) => g.is_exclusive);
  const normalGroups = allGroups.filter((g) => !g.is_exclusive);

  // 构造提交 payload：只发送合法的自定义倍率（数值 > 0），把整个 map 作为替换语义提交
  const buildPayload = (): UpdateUserReq => {
    const group_rates: Record<number, number> = {};
    for (const [k, raw] of Object.entries(customRates)) {
      if (raw === '' || raw == null) continue;
      const v = Number(raw);
      if (!Number.isFinite(v) || v <= 0) continue;
      group_rates[Number(k)] = v;
    }
    return {
      allowed_group_ids: selectedIds,
      group_rates,
    };
  };

  const updateMutation = useCrudMutation({
    mutationFn: (_?: void) => usersApi.update(user.id, buildPayload()),
    successMessage: t('users.update_success'),
    queryKey: queryKeys.users(),
    onSuccess: () => onSaved(),
  });

  const setRate = (groupId: number, value: string) => {
    setCustomRates((prev) => ({ ...prev, [groupId]: value }));
  };

  // 一个分组是否允许编辑倍率：
  //  - 普通分组：所有用户都可访问，任何时候都能覆盖
  //  - 专属分组：只有勾选后才允许填倍率（未勾选时倍率无意义）
  const canEditRate = (g: GroupResp) => !g.is_exclusive || selectedIds.includes(g.id);

  const renderRateInput = (g: GroupResp) => {
    const enabled = canEditRate(g);
    const value = customRates[g.id] ?? '';
    return (
      <div className="flex items-center gap-1.5 ml-auto flex-shrink-0" onClick={(e) => e.stopPropagation()}>
        <input
          type="number"
          min="0"
          step="0.01"
          disabled={!enabled}
          value={value}
          placeholder={String(g.rate_multiplier ?? 1)}
          onClick={(e) => e.stopPropagation()}
          onChange={(e) => setRate(g.id, e.target.value)}
          className="w-16 px-2 py-1 text-xs text-right rounded border bg-transparent focus:outline-none focus:ring-1 focus:ring-primary disabled:opacity-40"
          style={{ borderColor: 'var(--ag-glass-border)', color: 'var(--ag-text)' }}
        />
        <span className="text-[10px]" style={{ color: 'var(--ag-text-tertiary)' }}>×</span>
      </div>
    );
  };

  // memo 是否有任何非法输入（负数），用于禁用保存按钮
  const hasInvalidRate = useMemo(() => {
    for (const raw of Object.values(customRates)) {
      if (raw === '' || raw == null) continue;
      const v = Number(raw);
      if (!Number.isFinite(v) || v < 0) return true;
    }
    return false;
  }, [customRates]);

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={`${t('users.groups')} - ${user.email}`}
      width="540px"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>{t('common.cancel')}</Button>
          <Button
            onClick={() => updateMutation.mutate()}
            loading={updateMutation.isPending}
            disabled={hasInvalidRate}
          >
            {t('common.save')}
          </Button>
        </>
      }
    >
      {groupsLoading ? (
        <p className="text-sm text-text-tertiary text-center py-8">{t('common.loading')}</p>
      ) : allGroups.length === 0 ? (
        <p className="text-sm text-text-tertiary text-center py-8">{t('common.no_data')}</p>
      ) : (
        <div className="space-y-4 max-h-[26rem] overflow-y-auto">
          <p className="text-[11px]" style={{ color: 'var(--ag-text-tertiary)' }}>{t('users.group_rate_hint')}</p>

          {normalGroups.length > 0 && (
            <div>
              <p className="text-xs text-text-tertiary mb-2 font-medium uppercase tracking-wider">{t('users.normal_groups')}</p>
              <div className="space-y-0.5">
                {normalGroups.map((g) => (
                  <div
                    key={g.id}
                    className="flex items-center gap-2.5 w-full px-3 py-2 rounded-lg text-sm"
                    style={{ color: 'var(--ag-text-secondary)' }}
                  >
                    <span
                      className="flex items-center justify-center w-4 h-4 rounded flex-shrink-0"
                      style={{ borderWidth: '1px', borderStyle: 'solid', borderColor: 'var(--ag-glass-border)', background: 'var(--ag-primary)', opacity: 0.5 }}
                    >
                      <svg className="w-3 h-3 text-white" viewBox="0 0 12 12" fill="none"><path d="M2.5 6l2.5 2.5 4.5-5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>
                    </span>
                    <span>{g.name}</span>
                    <span className="text-[10px]" style={{ color: 'var(--ag-text-tertiary)' }}>{g.platform}</span>
                    {renderRateInput(g)}
                  </div>
                ))}
              </div>
            </div>
          )}

          {exclusiveGroups.length > 0 && (
            <div>
              <p className="text-xs text-text-tertiary mb-2 font-medium uppercase tracking-wider">{t('users.exclusive_groups')}</p>
              <div className="space-y-0.5">
                {exclusiveGroups.map((g) => (
                  <button
                    key={g.id}
                    type="button"
                    onClick={() => {
                      setSelectedIds(
                        selectedIds.includes(g.id)
                          ? selectedIds.filter((v) => v !== g.id)
                          : [...selectedIds, g.id],
                      );
                    }}
                    className="flex items-center gap-2.5 w-full px-3 py-2 rounded-lg text-sm hover:bg-bg-hover transition-colors text-left cursor-pointer"
                    style={{ color: 'var(--ag-text)' }}
                  >
                    <span
                      className="flex items-center justify-center w-4 h-4 rounded flex-shrink-0 transition-colors"
                      style={{
                        borderWidth: '1px',
                        borderStyle: 'solid',
                        borderColor: selectedIds.includes(g.id) ? 'var(--ag-primary)' : 'var(--ag-glass-border)',
                        background: selectedIds.includes(g.id) ? 'var(--ag-primary)' : 'transparent',
                      }}
                    >
                      {selectedIds.includes(g.id) && (
                        <svg className="w-3 h-3 text-white" viewBox="0 0 12 12" fill="none"><path d="M2.5 6l2.5 2.5 4.5-5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>
                      )}
                    </span>
                    <span>{g.name}</span>
                    <span className="text-[10px]" style={{ color: 'var(--ag-text-tertiary)' }}>{g.platform}</span>
                    {renderRateInput(g)}
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </Modal>
  );
}
