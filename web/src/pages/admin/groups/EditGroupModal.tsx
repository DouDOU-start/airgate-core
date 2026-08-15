import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Description, Input, Label, Modal, Spinner, TextField as HeroTextField, useOverlayState } from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { ArrowUpDown, Layers, Search } from 'lucide-react';
import { NativeSwitch } from '../../../shared/components/NativeSwitch';
import type { GroupResp, CreateGroupReq, UpdateGroupReq } from '../../../shared/types';

const CLIENT_TYPES = ['claude_code', 'codex'] as const;

export function GroupFormModal({
  open,
  title,
  group,
  groups,
  onClose,
  onSubmit,
  loading,
}: {
  open: boolean;
  title: string;
  group?: GroupResp;
  groups?: GroupResp[];
  onClose: () => void;
  onSubmit: (data: CreateGroupReq | UpdateGroupReq) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const isEdit = !!group;

  const buildForm = () => ({
    is_exclusive: group?.is_exclusive ?? false,
    name: group?.name ?? '',
    note: group?.note ?? '',
    rate_multiplier: String(group?.rate_multiplier ?? 1),
    alpha_search_price: group?.alpha_search_price != null ? String(group.alpha_search_price) : '',
    sort_weight: String(group?.sort_weight ?? 0),
    status_visible: group?.status_visible ?? true,
    allowed_clients: group?.allowed_clients ?? [] as string[],
    fallback_group_id: group?.fallback_group_id ?? null as number | null,
  });

  const [form, setForm] = useState(buildForm);

  useEffect(() => {
    if (!open) {
      setForm(buildForm());
    }
  }, [open, group]);

  const toggleClient = (client: string) => {
    setForm((prev) => {
      const next = prev.allowed_clients.includes(client)
        ? prev.allowed_clients.filter((c) => c !== client)
        : [...prev.allowed_clients, client];
      return {
        ...prev,
        allowed_clients: next,
        fallback_group_id: next.length === 0 ? null : prev.fallback_group_id,
      };
    });
  };

  const handleSubmit = () => {
    if (!isEdit && !form.name) return;
    onSubmit({
      ...form,
      rate_multiplier: form.rate_multiplier === '' ? 1 : Number(form.rate_multiplier),
      alpha_search_price: form.alpha_search_price === '' ? null : Number(form.alpha_search_price),
      sort_weight: form.sort_weight === '' ? 0 : Number(form.sort_weight),
      allowed_clients: form.allowed_clients.length > 0 ? form.allowed_clients : [],
      fallback_group_id: form.allowed_clients.length > 0 ? form.fallback_group_id : null,
    });
  };

  const fallbackOptions = (groups ?? []).filter((g) => g.id !== group?.id);

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
            value={form.rate_multiplier}
            onChange={(e) => setForm({ ...form, rate_multiplier: e.target.value })}
          />
        </HeroTextField>

        <HeroTextField fullWidth>
          <Label>{t('groups.alpha_search_price')}</Label>
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
            <Input
              className="pl-9"
              type="number"
              step="0.001"
              min="0"
              value={form.alpha_search_price}
              onChange={(e) => setForm({ ...form, alpha_search_price: e.target.value })}
              placeholder={t('groups.alpha_search_price_placeholder')}
            />
          </div>
        </HeroTextField>

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
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

        {/* 客户端限制 */}
        <div className="rounded-lg border border-border-secondary p-3 space-y-3">
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium text-text">{t('groups.allowed_clients')}</span>
            <span className="text-xs text-text-tertiary">
              {form.allowed_clients.length === 0 ? t('groups.allowed_clients_none') : ''}
            </span>
          </div>
          <div className="flex gap-2">
            {CLIENT_TYPES.map((ct) => {
              const active = form.allowed_clients.includes(ct);
              return (
                <button
                  key={ct}
                  type="button"
                  onClick={() => toggleClient(ct)}
                  className={`inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium transition-colors ${
                    active
                      ? 'border-primary-500 bg-primary-500 text-white'
                      : 'border-border-secondary bg-transparent text-text-secondary hover:border-text-tertiary'
                  }`}
                >
                  {active && (
                    <svg className="h-3.5 w-3.5" viewBox="0 0 16 16" fill="currentColor">
                      <path d="M13.78 4.22a.75.75 0 0 1 0 1.06l-7.25 7.25a.75.75 0 0 1-1.06 0L2.22 9.28a.75.75 0 0 1 1.06-1.06L6 10.94l6.72-6.72a.75.75 0 0 1 1.06 0Z" />
                    </svg>
                  )}
                  {t(`groups.client_${ct}`)}
                </button>
              );
            })}
          </div>
          <p className="text-xs text-text-tertiary">{t('groups.allowed_clients_hint')}</p>

          {form.allowed_clients.length > 0 && (
            <div className="space-y-1.5 border-t border-border-secondary pt-3">
              <span className="text-sm font-medium text-text">{t('groups.fallback_group')}</span>
              <select
                className="block w-full rounded-md border border-border-secondary bg-surface-primary px-3 py-1.5 text-sm text-text shadow-sm focus:border-primary-500 focus:outline-none focus:ring-1 focus:ring-primary-500"
                value={form.fallback_group_id ?? ''}
                onChange={(e) =>
                  setForm({ ...form, fallback_group_id: e.target.value ? Number(e.target.value) : null })
                }
              >
                <option value="">{t('groups.fallback_group_none')}</option>
                {fallbackOptions.map((g) => (
                  <option key={g.id} value={g.id}>{g.name}</option>
                ))}
              </select>
              <p className="text-xs text-text-tertiary">{t('groups.fallback_group_hint')}</p>
            </div>
          )}
        </div>

        <HeroTextField fullWidth>
          <Label>{t('groups.sort_weight')}</Label>
          <div className="relative">
            <ArrowUpDown className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
            <Input
              className="pl-9"
              type="number"
              value={form.sort_weight}
              onChange={(e) => setForm({ ...form, sort_weight: e.target.value })}
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
