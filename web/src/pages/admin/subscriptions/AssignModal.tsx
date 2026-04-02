import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { CalendarDays } from 'lucide-react';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { Input, Select } from '../../../shared/components/Input';
import type {
  AssignSubscriptionReq,
  GroupResp,
  UserResp,
} from '../../../shared/types';

export function AssignModal({
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
  onSubmit: (data: AssignSubscriptionReq) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const [form, setForm] = useState<AssignSubscriptionReq>({
    user_id: 0,
    group_id: 0,
    expires_at: '',
  });

  const handleSubmit = () => {
    if (!form.user_id || !form.group_id || !form.expires_at) return;
    onSubmit(form);
  };

  const handleClose = () => {
    setForm({ user_id: 0, group_id: 0, expires_at: '' });
    onClose();
  };

  const userOptions = [
    { value: '0', label: t('subscriptions.select_user') },
    ...users.map((u) => ({
      value: String(u.id),
      label: `${u.email} (${u.username || '-'})`,
    })),
  ];

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
      title={t('subscriptions.assign')}
      footer={
        <>
          <Button variant="secondary" onClick={handleClose}>
            {t('common.cancel')}
          </Button>
          <Button onClick={handleSubmit} loading={loading}>
            {t('subscriptions.assign')}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Select
          label={t('subscriptions.user')}
          required
          value={String(form.user_id)}
          onChange={(e) =>
            setForm({ ...form, user_id: Number(e.target.value) })
          }
          options={userOptions}
        />

        <Select
          label={t('subscriptions.group')}
          required
          value={String(form.group_id)}
          onChange={(e) =>
            setForm({ ...form, group_id: Number(e.target.value) })
          }
          options={groupOptions}
        />

        <Input
          label={t('subscriptions.expire_time')}
          type="date"
          required
          value={form.expires_at ? form.expires_at.split('T')[0] : ''}
          onChange={(e) =>
            setForm({
              ...form,
              expires_at: e.target.value
                ? `${e.target.value}T23:59:59Z`
                : '',
            })
          }
          icon={<CalendarDays className="w-4 h-4" />}
        />
      </div>
    </Modal>
  );
}
