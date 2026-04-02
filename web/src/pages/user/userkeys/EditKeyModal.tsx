import { useTranslation } from 'react-i18next';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { Input, Select } from '../../../shared/components/Input';
import { DatePicker } from '../../../shared/components/DatePicker';
import type { KeyForm } from './types';

export function EditKeyModal({
  open,
  isEdit,
  form,
  setForm,
  groupOptions,
  onClose,
  onSubmit,
  loading,
}: {
  open: boolean;
  isEdit: boolean;
  form: KeyForm;
  setForm: (form: KeyForm) => void;
  groupOptions: Array<{ value: string; label: string }>;
  onClose: () => void;
  onSubmit: () => void;
  loading: boolean;
}) {
  const { t } = useTranslation();

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={isEdit ? t('user_keys.edit') : t('user_keys.create')}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button onClick={onSubmit} loading={loading}>
            {isEdit ? t('common.save') : t('common.create')}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Input
          label={t('common.name')}
          required
          value={form.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
          placeholder={t('user_keys.name_placeholder')}
        />
        <Select
          label={t('user_keys.group')}
          required
          value={form.group_id}
          onChange={(e) => setForm({ ...form, group_id: e.target.value })}
          options={groupOptions}
        />
        <Input
          label={t('user_keys.quota_label')}
          type="number"
          value={form.quota_usd}
          onChange={(e) => setForm({ ...form, quota_usd: e.target.value })}
          placeholder={t('user_keys.quota_unlimited_hint')}
          hint={t('user_keys.quota_hint')}
        />
        <DatePicker
          label={t('user_keys.expires_at')}
          value={form.expires_at}
          onChange={(v) => setForm({ ...form, expires_at: v })}
          hint={t('user_keys.expire_hint')}
        />
      </div>
    </Modal>
  );
}
