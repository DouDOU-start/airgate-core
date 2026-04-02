import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { CalendarDays, User } from 'lucide-react';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { Input, Select } from '../../../shared/components/Input';
import type {
  BulkAssignReq,
  GroupResp,
  UserResp,
} from '../../../shared/types';

export function BulkAssignModal({
  open,
  groups,
  users,
  onClose,
  onSubmit,
  loading,
}: {
  open: boolean;
  groups: GroupResp[];
  users: UserResp[];
  onClose: () => void;
  onSubmit: (data: BulkAssignReq) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const [selectedUserIds, setSelectedUserIds] = useState<number[]>([]);
  const [groupId, setGroupId] = useState(0);
  const [expiresAt, setExpiresAt] = useState('');

  const toggleUser = (userId: number) => {
    setSelectedUserIds((prev) =>
      prev.includes(userId)
        ? prev.filter((id) => id !== userId)
        : [...prev, userId],
    );
  };

  const handleSubmit = () => {
    if (selectedUserIds.length === 0 || !groupId || !expiresAt) return;
    onSubmit({
      user_ids: selectedUserIds,
      group_id: groupId,
      expires_at: expiresAt,
    });
  };

  const handleClose = () => {
    setSelectedUserIds([]);
    setGroupId(0);
    setExpiresAt('');
    onClose();
  };

  const groupOptions = [
    { value: '0', label: t('subscriptions.select_group') },
    ...groups.map((g) => ({
      value: String(g.id),
      label: `${g.name} (${g.platform})`,
    })),
  ];

  return (
    <Modal
      open={open}
      onClose={handleClose}
      title={t('subscriptions.bulk_assign')}
      width="560px"
      footer={
        <>
          <Button variant="secondary" onClick={handleClose}>
            {t('common.cancel')}
          </Button>
          <Button onClick={handleSubmit} loading={loading}>
            {t('subscriptions.bulk_assign_count', { count: selectedUserIds.length })}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {/* 用户多选 */}
        <div className="space-y-1.5">
          <label
            className="block text-xs font-medium uppercase tracking-wider"
            style={{ color: 'var(--ag-text-secondary)' }}
          >
            {t('subscriptions.select_users')} <span style={{ color: 'var(--ag-danger)' }}>*</span>
          </label>
          <div
            className="rounded-md max-h-48 overflow-y-auto p-2 space-y-0.5"
            style={{
              border: '1px solid var(--ag-glass-border)',
              background: 'var(--ag-bg-surface)',
            }}
          >
            {users.map((u) => (
              <label
                key={u.id}
                className="flex items-center gap-2.5 px-2 py-1.5 rounded-sm cursor-pointer transition-colors"
                style={{ color: 'var(--ag-text-secondary)' }}
                onMouseEnter={(e) => {
                  (e.currentTarget as HTMLElement).style.background = 'var(--ag-bg-hover)';
                }}
                onMouseLeave={(e) => {
                  (e.currentTarget as HTMLElement).style.background = 'transparent';
                }}
              >
                <input
                  type="checkbox"
                  checked={selectedUserIds.includes(u.id)}
                  onChange={() => toggleUser(u.id)}
                  className="rounded"
                  style={{
                    borderColor: 'var(--ag-glass-border)',
                    accentColor: 'var(--ag-primary)',
                  }}
                />
                <User className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />
                <span className="text-sm">
                  {u.email} ({u.username || '-'})
                </span>
              </label>
            ))}
          </div>
          <p className="text-xs font-mono" style={{ color: 'var(--ag-text-tertiary)' }}>
            {t('subscriptions.selected_count', { count: selectedUserIds.length })}
          </p>
        </div>

        <Select
          label={t('subscriptions.group')}
          required
          value={String(groupId)}
          onChange={(e) => setGroupId(Number(e.target.value))}
          options={groupOptions}
        />

        <Input
          label={t('subscriptions.expire_time')}
          type="date"
          required
          value={expiresAt ? expiresAt.split('T')[0] : ''}
          onChange={(e) =>
            setExpiresAt(
              e.target.value ? `${e.target.value}T23:59:59Z` : '',
            )
          }
          icon={<CalendarDays className="w-4 h-4" />}
        />
      </div>
    </Modal>
  );
}
