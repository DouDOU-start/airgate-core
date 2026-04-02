import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { CalendarDays } from 'lucide-react';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { Input, Select } from '../../../shared/components/Input';
import type {
  SubscriptionResp,
  AdjustSubscriptionReq,
} from '../../../shared/types';

export function AdjustModal({
  open,
  subscription,
  onClose,
  onSubmit,
  loading,
}: {
  open: boolean;
  subscription: SubscriptionResp;
  onClose: () => void;
  onSubmit: (data: AdjustSubscriptionReq) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const [form, setForm] = useState<AdjustSubscriptionReq>({
    expires_at: subscription.expires_at,
    status: subscription.status as 'active' | 'suspended',
  });

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t('subscriptions.adjust_title', { name: subscription.group_name })}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button onClick={() => onSubmit(form)} loading={loading}>
            {t('common.save')}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Input
          label={t('subscriptions.expire_time')}
          type="date"
          value={
            form.expires_at ? form.expires_at.split('T')[0] : ''
          }
          onChange={(e) =>
            setForm({
              ...form,
              expires_at: e.target.value
                ? `${e.target.value}T23:59:59Z`
                : undefined,
            })
          }
          icon={<CalendarDays className="w-4 h-4" />}
        />

        <Select
          label={t('common.status')}
          value={form.status ?? 'active'}
          onChange={(e) =>
            setForm({
              ...form,
              status: e.target.value as 'active' | 'suspended',
            })
          }
          options={[
            { value: 'active', label: t('subscriptions.status_active') },
            { value: 'suspended', label: t('subscriptions.status_suspended') },
          ]}
        />
      </div>
    </Modal>
  );
}
