import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Layers, ArrowUpDown, X } from 'lucide-react';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { Input, Select } from '../../../shared/components/Input';
import { groupsApi } from '../../../shared/api/groups';
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
  instructionPresets,
}: {
  open: boolean;
  title: string;
  group?: GroupResp;
  onClose: () => void;
  onSubmit: (data: CreateGroupReq | UpdateGroupReq) => void;
  loading: boolean;
  platforms: string[];
  instructionPresets: (platform: string) => string[];
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
    force_instructions: group?.force_instructions ?? '',
    note: group?.note ?? '',
  });

  const [quotas, setQuotas] = useState(
    parseQuotas(group?.quotas as Record<string, unknown> | undefined),
  );

  // 创建模式下：从分组复制账号（同平台，可多选，自动去重）
  const [copyFromGroupIds, setCopyFromGroupIds] = useState<number[]>([]);

  // 加载同平台的全部分组用于下拉（仅创建时需要，且已选定平台）
  const { data: copySourceData } = useQuery({
    queryKey: ['groups-for-copy', form.platform],
    queryFn: () =>
      groupsApi.list({ page: 1, page_size: 1000, platform: form.platform }),
    enabled: !isEdit && !!form.platform && open,
  });
  const copySourceGroups: GroupResp[] = copySourceData?.list ?? [];

  const handleSubmit = () => {
    if (!isEdit && (!form.name || !form.platform)) return;

    onSubmit({
      ...form,
      subscription_type: form.subscription_type as 'standard' | 'subscription',
      quotas: form.subscription_type === 'subscription' ? buildQuotas(quotas) : undefined,
      // 不能用 `|| undefined` —— 那会把空字符串（即"不覆盖"）也吞掉，
      // 导致后端 ForceInstructions 指针为 nil 直接跳过更新，无法从 cc 切回不覆盖。
      // 空串需要真实下发，让后端把字段更新为空字符串。
      force_instructions: form.force_instructions ?? '',
      note: form.note,
      ...(!isEdit && copyFromGroupIds.length > 0
        ? { copy_accounts_from_group_ids: copyFromGroupIds }
        : {}),
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
            onChange={(e) => {
              setForm({ ...form, platform: e.target.value });
              setCopyFromGroupIds([]);
            }}
            options={[
              { value: '', label: t('groups.select_platform') },
              ...platforms.map((p) => ({ value: p, label: p })),
            ]}
          />
        )}

        {/* 从分组复制账号 —— 仅创建时显示 */}
        {!isEdit && (
          <div>
            <label
              className="block text-xs font-medium uppercase tracking-wider mb-1.5"
              style={{ color: 'var(--ag-text-secondary)' }}
            >
              {t('groups.copy_accounts_title')}
            </label>
            {copyFromGroupIds.length > 0 && (
              <div className="flex flex-wrap gap-1.5 mb-2">
                {copyFromGroupIds.map((gid) => {
                  const g = copySourceGroups.find((it) => it.id === gid);
                  return (
                    <span
                      key={gid}
                      className="inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-xs"
                      style={{
                        backgroundColor: 'var(--ag-primary-alpha)',
                        color: 'var(--ag-primary)',
                      }}
                    >
                      {g ? g.name : `#${gid}`}
                      <button
                        type="button"
                        onClick={() =>
                          setCopyFromGroupIds(copyFromGroupIds.filter((id) => id !== gid))
                        }
                        className="inline-flex"
                      >
                        <X className="w-3 h-3" />
                      </button>
                    </span>
                  );
                })}
              </div>
            )}
            <Select
              value=""
              disabled={!form.platform}
              onChange={(e) => {
                const val = Number(e.target.value);
                if (val && !copyFromGroupIds.includes(val)) {
                  setCopyFromGroupIds([...copyFromGroupIds, val]);
                }
              }}
              options={[
                {
                  value: '',
                  label: !form.platform
                    ? t('groups.copy_accounts_select_platform_first')
                    : copySourceGroups.length === 0
                      ? t('groups.copy_accounts_empty')
                      : t('groups.copy_accounts_placeholder'),
                },
                ...copySourceGroups
                  .filter((g) => !copyFromGroupIds.includes(g.id))
                  .map((g) => ({
                    value: String(g.id),
                    label: `${g.name} (${t('groups.copy_accounts_count', { count: g.account_total })})`,
                  })),
              ]}
            />
            <p
              className="text-[11px] mt-1"
              style={{ color: 'var(--ag-text-tertiary)' }}
            >
              {t('groups.copy_accounts_hint')}
            </p>
          </div>
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

        <Input
          label={t('groups.note')}
          value={form.note}
          onChange={(e) => setForm({ ...form, note: e.target.value })}
          hint={t('groups.note_hint')}
          placeholder={t('groups.note_placeholder')}
        />

        {/* 强制 Instructions — 仅插件声明了预设时显示 */}
        {instructionPresets(form.platform).length > 0 && <div>
          <label className="block text-xs font-medium uppercase tracking-wider mb-1.5" style={{ color: 'var(--ag-text-secondary)' }}>
            {t('groups.force_instructions')}
          </label>
          <p className="text-[11px] mb-2" style={{ color: 'var(--ag-text-tertiary)' }}>
            {t('groups.force_instructions_hint')}
          </p>
          <div className="flex gap-2 mb-2 flex-wrap">
            {['', ...instructionPresets(form.platform)].map((preset) => (
              <button
                key={preset}
                type="button"
                onClick={() => setForm({ ...form, force_instructions: preset })}
                className="px-2.5 py-1 text-xs rounded-md transition-colors"
                style={{
                  borderWidth: '1px', borderStyle: 'solid',
                  borderColor: form.force_instructions === preset ? 'var(--ag-primary)' : 'var(--ag-glass-border)',
                  backgroundColor: form.force_instructions === preset ? 'var(--ag-primary-alpha)' : 'transparent',
                  color: form.force_instructions === preset ? 'var(--ag-primary)' : 'var(--ag-text-secondary)',
                }}
              >
                {preset || t('groups.instructions_none')}
              </button>
            ))}
          </div>
          {form.force_instructions && !instructionPresets(form.platform).includes(form.force_instructions) && (
            <textarea
              className="w-full rounded-lg px-3 py-2 text-sm"
              style={{
                borderWidth: '1px', borderStyle: 'solid',
                borderColor: 'var(--ag-glass-border)',
                backgroundColor: 'var(--ag-glass-bg)',
                color: 'var(--ag-text-primary)',
              }}
              rows={4}
              value={form.force_instructions}
              onChange={(e) => setForm({ ...form, force_instructions: e.target.value })}
              placeholder={t('groups.instructions_custom_placeholder')}
            />
          )}
        </div>}

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
