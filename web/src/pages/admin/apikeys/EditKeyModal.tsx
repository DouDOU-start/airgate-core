import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Key } from 'lucide-react';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { Input, Textarea, Select } from '../../../shared/components/Input';
import { parseIpList, formatIpList } from '../../../shared/utils/ip';
import type { APIKeyResp, UpdateAPIKeyReq, GroupResp } from '../../../shared/types';

interface EditKeyModalProps {
  open: boolean;
  apiKey: APIKeyResp;
  groups: GroupResp[];
  onClose: () => void;
  onSubmit: (data: UpdateAPIKeyReq) => void;
  loading: boolean;
}

export function EditKeyModal({ open, apiKey, groups, onClose, onSubmit, loading }: EditKeyModalProps) {
  const { t } = useTranslation();
  const [groupId, setGroupId] = useState<number>(apiKey.group_id ?? 0);
  const [form, setForm] = useState<UpdateAPIKeyReq>({
    name: apiKey.name,
    quota_usd: apiKey.quota_usd,
    expires_at: apiKey.expires_at,
    status: apiKey.status as 'active' | 'disabled',
  });
  const [ipWhitelist, setIpWhitelist] = useState(formatIpList(apiKey.ip_whitelist));
  const [ipBlacklist, setIpBlacklist] = useState(formatIpList(apiKey.ip_blacklist));

  const handleSubmit = () => {
    onSubmit({
      ...form,
      group_id: groupId !== apiKey.group_id ? groupId : undefined,
      ip_whitelist: parseIpList(ipWhitelist),
      ip_blacklist: parseIpList(ipBlacklist),
    });
  };

  const groupOptions = [
    { value: '0', label: t('api_keys.group_unbound') },
    ...groups.map((g) => ({
      value: String(g.id),
      label: `${g.name} (${g.platform})`,
    })),
  ];

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t('api_keys.edit')}
      width="560px"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button onClick={handleSubmit} loading={loading}>
            {t('common.save')}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Input
          label={t('common.name')}
          value={form.name ?? ''}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
          icon={<Key className="w-4 h-4" />}
        />

        <Select
          label={t('api_keys.group')}
          value={String(groupId)}
          onChange={(e) => setGroupId(Number(e.target.value))}
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
              expires_at: e.target.value ? `${e.target.value}T23:59:59Z` : undefined,
            })
          }
          hint={t('api_keys.expire_hint')}
        />

        <Select
          label={t('common.status')}
          value={form.status ?? 'active'}
          onChange={(e) =>
            setForm({
              ...form,
              status: e.target.value as 'active' | 'disabled',
            })
          }
          options={[
            { value: 'active', label: t('status.active') },
            { value: 'disabled', label: t('status.disabled') },
          ]}
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
