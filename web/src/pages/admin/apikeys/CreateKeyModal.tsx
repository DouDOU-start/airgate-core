import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Key } from 'lucide-react';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { Input, Textarea, Select } from '../../../shared/components/Input';
import { parseIpList } from '../../../shared/utils/ip';
import type { CreateAPIKeyReq, GroupResp } from '../../../shared/types';

interface CreateKeyModalProps {
  open: boolean;
  groups: GroupResp[];
  onClose: () => void;
  onSubmit: (data: CreateAPIKeyReq) => void;
  loading: boolean;
}

export function CreateKeyModal({ open, groups, onClose, onSubmit, loading }: CreateKeyModalProps) {
  const { t } = useTranslation();
  const [form, setForm] = useState<CreateAPIKeyReq>({
    name: '',
    group_id: 0,
    quota_usd: 0,
    expires_at: '',
  });
  const [ipWhitelist, setIpWhitelist] = useState('');
  const [ipBlacklist, setIpBlacklist] = useState('');

  const handleSubmit = () => {
    if (!form.name || !form.group_id) return;
    onSubmit({
      ...form,
      quota_usd: form.quota_usd || undefined,
      expires_at: form.expires_at || undefined,
      ip_whitelist: parseIpList(ipWhitelist),
      ip_blacklist: parseIpList(ipBlacklist),
    });
  };

  const handleClose = () => {
    setForm({ name: '', group_id: 0, quota_usd: 0, expires_at: '' });
    setIpWhitelist('');
    setIpBlacklist('');
    onClose();
  };

  const groupOptions = [
    { value: '0', label: t('api_keys.select_group') },
    ...groups.map((g) => ({ value: String(g.id), label: `${g.name} (${g.platform})` })),
  ];

  return (
    <Modal
      open={open}
      onClose={handleClose}
      title={t('api_keys.create')}
      width="560px"
      footer={
        <>
          <Button variant="secondary" onClick={handleClose}>
            {t('common.cancel')}
          </Button>
          <Button onClick={handleSubmit} loading={loading}>
            {t('common.create')}
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
          placeholder={t('api_keys.name_placeholder')}
          icon={<Key className="w-4 h-4" />}
        />

        <Select
          label={t('api_keys.group')}
          required
          value={String(form.group_id)}
          onChange={(e) => setForm({ ...form, group_id: Number(e.target.value) })}
          options={groupOptions}
        />

        <Input
          label={t('api_keys.quota_label')}
          type="number"
          step="0.01"
          min="0"
          value={String(form.quota_usd ?? 0)}
          onChange={(e) => setForm({ ...form, quota_usd: Number(e.target.value) })}
          hint={t('api_keys.quota_hint')}
        />

        <Input
          label={t('api_keys.expire_time')}
          type="date"
          value={form.expires_at ? form.expires_at.split('T')[0] : ''}
          onChange={(e) =>
            setForm({
              ...form,
              expires_at: e.target.value ? `${e.target.value}T23:59:59Z` : '',
            })
          }
          hint={t('api_keys.expire_hint')}
        />

        <Textarea
          label={t('api_keys.ip_whitelist')}
          placeholder={t('api_keys.ip_placeholder')}
          value={ipWhitelist}
          onChange={(e) => setIpWhitelist(e.target.value)}
          className="font-mono"
          rows={2}
        />

        <Textarea
          label={t('api_keys.ip_blacklist')}
          placeholder={t('api_keys.ip_placeholder')}
          value={ipBlacklist}
          onChange={(e) => setIpBlacklist(e.target.value)}
          className="font-mono"
          rows={2}
        />
      </div>
    </Modal>
  );
}
