import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Key } from 'lucide-react';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { Input, Textarea } from '../../../shared/components/Input';
import { SearchSelect } from '../../../shared/components/SearchSelect';
import { parseIpList } from '../../../shared/utils/ip';
import { useAuth } from '../../../app/providers/AuthProvider';
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
    sell_rate: 0,
    max_concurrency: 0,
    expires_at: '',
  });
  const [ipWhitelist, setIpWhitelist] = useState('');
  const [ipBlacklist, setIpBlacklist] = useState('');

  const handleSubmit = () => {
    if (!form.name || !form.group_id) return;
    onSubmit({
      ...form,
      quota_usd: form.quota_usd || undefined,
      sell_rate: form.sell_rate || undefined,
      // 0 代表"不限制"，依然显式传给后端以便明确语义；留空/undefined 后端也会按 0 处理
      max_concurrency: form.max_concurrency ?? 0,
      expires_at: form.expires_at || undefined,
      ip_whitelist: parseIpList(ipWhitelist),
      ip_blacklist: parseIpList(ipBlacklist),
    });
  };

  const handleClose = () => {
    setForm({ name: '', group_id: 0, quota_usd: 0, sell_rate: 0, max_concurrency: 0, expires_at: '' });
    setIpWhitelist('');
    setIpBlacklist('');
    onClose();
  };

  const { user } = useAuth();
  const userGroupRates = user?.group_rates;
  const groupOptions = groups.map((g) => {
    const override = userGroupRates?.[g.id];
    const hasOverride = override != null && override > 0 && override !== g.rate_multiplier;
    return {
      value: String(g.id),
      label: `${g.name} (${g.platform})`,
      suffix: hasOverride ? (
        <span className="text-text-tertiary">
          <span className="line-through opacity-60">{g.rate_multiplier}x</span>{' '}
          <span className="text-primary font-medium">{override}x</span>
        </span>
      ) : (
        <span className="text-text-tertiary">{g.rate_multiplier}x 倍率</span>
      ),
    };
  });

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

        <SearchSelect
          label={t('api_keys.group')}
          required
          placeholder={t('api_keys.select_group')}
          value={form.group_id ? String(form.group_id) : ''}
          onChange={(v) => setForm({ ...form, group_id: Number(v) })}
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
          label={t('api_keys.sell_rate_label', '销售倍率')}
          type="number"
          step="0.01"
          min="0"
          value={String(form.sell_rate ?? 0)}
          onChange={(e) => setForm({ ...form, sell_rate: Number(e.target.value) })}
          hint={t('api_keys.sell_rate_hint', '留空或 0 表示按平台原价计费')}
        />

        <Input
          label={t('api_keys.max_concurrency_label', '最大并发数')}
          type="number"
          step="1"
          min="0"
          value={String(form.max_concurrency ?? 0)}
          onChange={(e) => setForm({ ...form, max_concurrency: Number(e.target.value) })}
          hint={t('api_keys.max_concurrency_hint', '留空或 0 表示不限制')}
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
