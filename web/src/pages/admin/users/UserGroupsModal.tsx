import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { usersApi } from '../../../shared/api/users';
import { groupsApi } from '../../../shared/api/groups';
import { useCrudMutation } from '../../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../../shared/queryKeys';
import { FETCH_ALL_PARAMS } from '../../../shared/constants';
import type { UserResp, GroupResp } from '../../../shared/types';

interface UserGroupsModalProps {
  open: boolean;
  user: UserResp;
  onClose: () => void;
  onSaved: () => void;
}

export function UserGroupsModal({ open, user, onClose, onSaved }: UserGroupsModalProps) {
  const { t } = useTranslation();
  const [selectedIds, setSelectedIds] = useState<number[]>(user.allowed_group_ids ?? []);

  const { data: groupsData, isLoading: groupsLoading } = useQuery({
    queryKey: queryKeys.groupsAll(),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
    enabled: open,
  });

  const updateMutation = useCrudMutation({
    mutationFn: (_?: void) => usersApi.update(user.id, { allowed_group_ids: selectedIds }),
    successMessage: t('users.update_success'),
    queryKey: queryKeys.users(),
    onSuccess: () => onSaved(),
  });

  const allGroups = groupsData?.list ?? [];
  const exclusiveGroups = allGroups.filter((g: GroupResp) => g.is_exclusive);
  const normalGroups = allGroups.filter((g: GroupResp) => !g.is_exclusive);

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={`${t('users.groups')} - ${user.email}`}
      width="480px"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>{t('common.cancel')}</Button>
          <Button onClick={() => updateMutation.mutate()} loading={updateMutation.isPending}>{t('common.save')}</Button>
        </>
      }
    >
      {groupsLoading ? (
        <p className="text-sm text-text-tertiary text-center py-8">{t('common.loading')}</p>
      ) : allGroups.length === 0 ? (
        <p className="text-sm text-text-tertiary text-center py-8">{t('common.no_data')}</p>
      ) : (
        <div className="space-y-4 max-h-80 overflow-y-auto">
          {normalGroups.length > 0 && (
            <div>
              <p className="text-xs text-text-tertiary mb-2 font-medium uppercase tracking-wider">{t('users.normal_groups')}</p>
              <div className="space-y-0.5">
                {normalGroups.map((g: GroupResp) => (
                  <div
                    key={g.id}
                    className="flex items-center gap-2.5 w-full px-3 py-2 rounded-lg text-sm"
                    style={{ color: 'var(--ag-text-secondary)' }}
                  >
                    <span
                      className="flex items-center justify-center w-4 h-4 rounded border flex-shrink-0"
                      style={{ borderColor: 'var(--ag-glass-border)', background: 'var(--ag-primary)', opacity: 0.5 }}
                    >
                      <svg className="w-3 h-3 text-white" viewBox="0 0 12 12" fill="none"><path d="M2.5 6l2.5 2.5 4.5-5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>
                    </span>
                    <span>{g.name}</span>
                    <span className="text-[10px]" style={{ color: 'var(--ag-text-tertiary)' }}>{g.platform}</span>
                    <span className="ml-auto text-[10px]" style={{ color: 'var(--ag-text-tertiary)' }}>{t('users.all_users_accessible')}</span>
                  </div>
                ))}
              </div>
            </div>
          )}

          {exclusiveGroups.length > 0 && (
            <div>
              <p className="text-xs text-text-tertiary mb-2 font-medium uppercase tracking-wider">{t('users.exclusive_groups')}</p>
              <div className="space-y-0.5">
                {exclusiveGroups.map((g: GroupResp) => (
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
                      className="flex items-center justify-center w-4 h-4 rounded border flex-shrink-0 transition-colors"
                      style={{
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
