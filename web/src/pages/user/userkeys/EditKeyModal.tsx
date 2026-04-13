import { useTranslation } from 'react-i18next';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { Input } from '../../../shared/components/Input';
import { SearchSelect, type SearchSelectOption } from '../../../shared/components/SearchSelect';
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
  groupOptions: SearchSelectOption[];
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
        <SearchSelect
          label={t('user_keys.group')}
          required
          placeholder={t('user_keys.select_group')}
          value={form.group_id}
          onChange={(v) => setForm({ ...form, group_id: v })}
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
        <Input
          label={t('user_keys.sell_rate_label', '销售倍率（对外售价）')}
          type="number"
          value={form.sell_rate}
          onChange={(e) => setForm({ ...form, sell_rate: e.target.value })}
          placeholder="0"
          hint={t('user_keys.sell_rate_hint', '留空或 0 表示按平台原价计费')}
        />
        <Input
          label={t('user_keys.max_concurrency_label', '最大并发数')}
          type="number"
          value={form.max_concurrency}
          onChange={(e) => setForm({ ...form, max_concurrency: e.target.value })}
          placeholder="0"
          hint={t('user_keys.max_concurrency_hint', '留空或 0 表示不限制')}
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
