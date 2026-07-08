import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Description, Input, Label, ListBox, Modal, Select, Spinner, TextField as HeroTextField, useOverlayState } from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { ArrowUpDown, Layers } from 'lucide-react';
import { NativeSwitch } from '../../../shared/components/NativeSwitch';
import type { GroupResp, CreateGroupReq, UpdateGroupReq } from '../../../shared/types';

function parseQuotas(quotas?: Record<string, unknown>): { daily: string; weekly: string; monthly: string } {
  return {
    daily: quotas?.daily ? String(quotas.daily) : '',
    monthly: quotas?.monthly ? String(quotas.monthly) : '',
    weekly: quotas?.weekly ? String(quotas.weekly) : '',
  };
}

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
}: {
  open: boolean;
  title: string;
  group?: GroupResp;
  onClose: () => void;
  onSubmit: (data: CreateGroupReq | UpdateGroupReq) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const isEdit = !!group;

  const [form, setForm] = useState({
    force_instructions: group?.force_instructions ?? '',
    is_exclusive: group?.is_exclusive ?? false,
    name: group?.name ?? '',
    note: group?.note ?? '',
    rate_multiplier: group?.rate_multiplier ?? 1,
    sort_weight: group?.sort_weight ?? 0,
    status_visible: group?.status_visible ?? true,
    subscription_type: group?.subscription_type ?? 'standard' as const,
  });
  const [quotas, setQuotas] = useState(parseQuotas(group?.quotas as Record<string, unknown> | undefined));

  const subscriptionTypeOptions = [
    { id: 'standard', label: t('groups.type_standard') },
    { id: 'subscription', label: t('groups.type_subscription') },
  ];
  const selectedSubscriptionTypeLabel =
    subscriptionTypeOptions.find((item) => item.id === form.subscription_type)?.label ?? t('groups.type_standard');

  const handleSubmit = () => {
    if (!isEdit && !form.name) return;

    onSubmit({
      ...form,
      force_instructions: form.force_instructions ?? '',
      note: form.note,
      quotas: form.subscription_type === 'subscription' ? buildQuotas(quotas) : undefined,
      subscription_type: form.subscription_type as 'standard' | 'subscription',
    });
  };

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog
            className="ag-elevation-modal"
            style={{ maxWidth: '560px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>{title}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
      <div className="space-y-4">
        <HeroTextField fullWidth isRequired>
          <Label>{t('common.name')}</Label>
          <div className="relative">
            <Layers className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
            <Input
              className="pl-9"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              required
            />
          </div>
        </HeroTextField>

        <HeroTextField fullWidth>
          <Label>{t('groups.rate_multiplier')}</Label>
          <Input
            type="number"
            step="0.1"
            value={String(form.rate_multiplier)}
            onChange={(e) => setForm({ ...form, rate_multiplier: Number(e.target.value) })}
          />
        </HeroTextField>

        <div className="grid grid-cols-2 gap-3">
          <NativeSwitch
            isSelected={form.is_exclusive}
            label={<span className="text-sm text-text">{t('groups.exclusive_hint')}</span>}
            onChange={(selected) => setForm({ ...form, is_exclusive: selected })}
          />

          <NativeSwitch
            isSelected={form.status_visible}
            label={<span className="text-sm text-text">{t('groups.status_visible_hint')}</span>}
            onChange={(selected) => setForm({ ...form, status_visible: selected })}
          />
        </div>

        <Select
          fullWidth
          selectedKey={form.subscription_type}
          onSelectionChange={(key) =>
            setForm({ ...form, subscription_type: (key ?? 'standard') as 'standard' | 'subscription' })
          }
        >
          <Label>{t('groups.subscription_type')}</Label>
          <Select.Trigger>
            <Select.Value>{selectedSubscriptionTypeLabel}</Select.Value>
            <Select.Indicator />
          </Select.Trigger>
          <Select.Popover>
            <ListBox items={subscriptionTypeOptions}>
              {(item) => (
                <ListBox.Item id={item.id} textValue={item.label}>
                  {item.label}
                </ListBox.Item>
              )}
            </ListBox>
          </Select.Popover>
        </Select>

        <HeroTextField fullWidth>
          <Label>{t('groups.sort_weight')}</Label>
          <div className="relative">
            <ArrowUpDown className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
            <Input
              className="pl-9"
              type="number"
              value={String(form.sort_weight)}
              onChange={(e) => setForm({ ...form, sort_weight: Number(e.target.value) })}
            />
          </div>
          <Description>{t('groups.sort_weight_hint')}</Description>
        </HeroTextField>

        <HeroTextField fullWidth>
          <Label>{t('groups.note')}</Label>
          <Input
            value={form.note}
            onChange={(e) => setForm({ ...form, note: e.target.value })}
            placeholder={t('groups.note_placeholder')}
          />
          <Description>{t('groups.note_hint')}</Description>
        </HeroTextField>

        {form.subscription_type === 'subscription' ? (
          <div>
            <p className="mb-1.5 text-xs font-medium uppercaser text-text-secondary">
              {t('groups.quotas')}
            </p>
            <p className="mb-2 text-[11px] text-text-tertiary">{t('groups.quota_hint')}</p>
            <div className="grid grid-cols-3 gap-3">
              <HeroTextField fullWidth>
                <Label>{t('groups.quota_daily')}</Label>
                <Input
                  type="number"
                  min="0"
                  value={quotas.daily}
                  onChange={(e) => setQuotas({ ...quotas, daily: e.target.value })}
                />
              </HeroTextField>
              <HeroTextField fullWidth>
                <Label>{t('groups.quota_weekly')}</Label>
                <Input
                  type="number"
                  min="0"
                  value={quotas.weekly}
                  onChange={(e) => setQuotas({ ...quotas, weekly: e.target.value })}
                />
              </HeroTextField>
              <HeroTextField fullWidth>
                <Label>{t('groups.quota_monthly')}</Label>
                <Input
                  type="number"
                  min="0"
                  value={quotas.monthly}
                  onChange={(e) => setQuotas({ ...quotas, monthly: e.target.value })}
                />
              </HeroTextField>
            </div>
          </div>
        ) : null}
      </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              <Button variant="primary" isDisabled={loading} onPress={handleSubmit}>
                {loading ? <Spinner size="sm" /> : null}
                {isEdit ? t('common.save') : t('common.create')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
