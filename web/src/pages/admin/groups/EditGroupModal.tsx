import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Layers, ArrowUpDown } from 'lucide-react';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { Input, Select } from '../../../shared/components/Input';
import type { GroupResp, CreateGroupReq, UpdateGroupReq } from '../../../shared/types';

// 从 quotas 对象解析为结构化值
function parseQuotas(quotas?: Record<string, unknown>): { daily: string; weekly: string; monthly: string } {
  return {
    daily: quotas?.daily ? String(quotas.daily) : '',
    weekly: quotas?.weekly ? String(quotas.weekly) : '',
    monthly: quotas?.monthly ? String(quotas.monthly) : '',
  };
}

// 从结构化值组装回 quotas 对象
function buildQuotas(q: { daily: string; weekly: string; monthly: string }): Record<string, unknown> | undefined {
  const result: Record<string, number> = {};
  if (q.daily && Number(q.daily) > 0) result.daily = Number(q.daily);
  if (q.weekly && Number(q.weekly) > 0) result.weekly = Number(q.weekly);
  if (q.monthly && Number(q.monthly) > 0) result.monthly = Number(q.monthly);
  return Object.keys(result).length > 0 ? result : undefined;
}

export function GroupFormModal({
  open,
  title,
  group,
  onClose,
  onSubmit,
  loading,
  platforms,
}: {
  open: boolean;
  title: string;
  group?: GroupResp;
  onClose: () => void;
  onSubmit: (data: CreateGroupReq | UpdateGroupReq) => void;
  loading: boolean;
  platforms: string[];
}) {
  const { t } = useTranslation();
  const isEdit = !!group;

  const [form, setForm] = useState({
    name: group?.name ?? '',
    platform: group?.platform ?? '',
    rate_multiplier: group?.rate_multiplier ?? 1,
    is_exclusive: group?.is_exclusive ?? false,
    subscription_type: group?.subscription_type ?? 'standard' as const,
    sort_weight: group?.sort_weight ?? 0,
  });

  const [quotas, setQuotas] = useState(
    parseQuotas(group?.quotas as Record<string, unknown> | undefined),
  );

  const handleSubmit = () => {
    if (!isEdit && (!form.name || !form.platform)) return;

    onSubmit({
      ...form,
      subscription_type: form.subscription_type as 'standard' | 'subscription',
      quotas: form.subscription_type === 'subscription' ? buildQuotas(quotas) : undefined,
    });
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={title}
      width="560px"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button onClick={handleSubmit} loading={loading}>
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
          icon={<Layers className="w-4 h-4" />}
        />

        {isEdit ? (
          <Input label={t('groups.platform')} value={form.platform} disabled />
        ) : (
          <Select
            label={t('groups.platform')}
            required
            value={form.platform}
            onChange={(e) => setForm({ ...form, platform: e.target.value })}
            options={[
              { value: '', label: t('groups.select_platform') },
              ...platforms.map((p) => ({ value: p, label: p })),
            ]}
          />
        )}

        <Input
          label={t('groups.rate_multiplier')}
          type="number"
          step="0.1"
          value={String(form.rate_multiplier)}
          onChange={(e) =>
            setForm({ ...form, rate_multiplier: Number(e.target.value) })
          }
        />

        <div className="flex items-center justify-between">
          <span className="text-sm" style={{ color: 'var(--ag-text-secondary)' }}>
            {t('groups.exclusive_hint')}
          </span>
          <button
            type="button"
            role="switch"
            aria-checked={form.is_exclusive}
            onClick={() => setForm({ ...form, is_exclusive: !form.is_exclusive })}
            className="relative inline-flex h-5 w-9 items-center rounded-full transition-colors"
            style={{
              backgroundColor: form.is_exclusive ? 'var(--ag-primary)' : 'var(--ag-glass-border)',
            }}
          >
            <span
              className="inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform"
              style={{
                transform: form.is_exclusive ? 'translateX(18px)' : 'translateX(3px)',
              }}
            />
          </button>
        </div>

        <Select
          label={t('groups.subscription_type')}
          value={form.subscription_type}
          onChange={(e) =>
            setForm({
              ...form,
              subscription_type: e.target.value as 'standard' | 'subscription',
            })
          }
          options={[
            { value: 'standard', label: t('groups.type_standard') },
            { value: 'subscription', label: t('groups.type_subscription') },
          ]}
        />

        <Input
          label={t('groups.sort_weight')}
          type="number"
          value={String(form.sort_weight)}
          onChange={(e) =>
            setForm({ ...form, sort_weight: Number(e.target.value) })
          }
          hint={t('groups.sort_weight_hint')}
          icon={<ArrowUpDown className="w-4 h-4" />}
        />

        {/* 配额限制 -- 仅订阅制显示 */}
        {form.subscription_type === 'subscription' && (
          <div>
            <label className="block text-xs font-medium text-text-secondary uppercase tracking-wider mb-1.5">
              {t('groups.quotas')}
            </label>
            <p className="text-[11px] mb-2" style={{ color: 'var(--ag-text-tertiary)' }}>
              {t('groups.quota_hint')}
            </p>
            <div className="grid grid-cols-3 gap-3">
              <Input
                label={t('groups.quota_daily')}
                type="number"
                min="0"
                value={quotas.daily}
                onChange={(e) => setQuotas({ ...quotas, daily: e.target.value })}
              />
              <Input
                label={t('groups.quota_weekly')}
                type="number"
                min="0"
                value={quotas.weekly}
                onChange={(e) => setQuotas({ ...quotas, weekly: e.target.value })}
              />
              <Input
                label={t('groups.quota_monthly')}
                type="number"
                min="0"
                value={quotas.monthly}
                onChange={(e) => setQuotas({ ...quotas, monthly: e.target.value })}
              />
            </div>
          </div>
        )}
      </div>
    </Modal>
  );
}
