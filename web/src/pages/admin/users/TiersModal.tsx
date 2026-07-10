import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Chip, Input, Label, Modal, Spinner, TextField as HeroTextField, useOverlayState } from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { ArrowLeft, Pencil, Plus, Trash2 } from 'lucide-react';
import { tiersApi } from '../../../shared/api/tiers';
import { groupsApi } from '../../../shared/api/groups';
import { useCrudMutation } from '../../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../../shared/queryKeys';
import { ConfirmDialog } from '../../../shared/components/ConfirmDialog';
import type { TierResp, CreateTierReq } from '../../../shared/types';

interface TiersModalProps {
  open: boolean;
  onClose: () => void;
}

interface TierFormState {
  name: string;
  note: string;
  sortWeight: string;
  /** 按 group_id 键的倍率输入值；空字符串表示未设置（继承分组档位） */
  rates: Record<number, string>;
}

const emptyForm: TierFormState = { name: '', note: '', sortWeight: '0', rates: {} };

function formFromTier(tier: TierResp): TierFormState {
  const rates: Record<number, string> = {};
  for (const [groupId, rate] of Object.entries(tier.rates ?? {})) {
    rates[Number(groupId)] = String(rate);
  }
  return { name: tier.name, note: tier.note ?? '', sortWeight: String(tier.sort_weight), rates };
}

export function TiersModal({ open, onClose }: TiersModalProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  // list 视图 ↔ form 视图（editingTier 为 null 时是新建）
  const [view, setView] = useState<'list' | 'form'>('list');
  const [editingTier, setEditingTier] = useState<TierResp | null>(null);
  const [deletingTier, setDeletingTier] = useState<TierResp | null>(null);
  const [form, setForm] = useState<TierFormState>(emptyForm);

  const { data, isLoading } = useQuery({
    queryKey: queryKeys.tiers(),
    queryFn: () => tiersApi.list({ page: 1, page_size: 100 }),
    enabled: open,
  });
  const { data: groupsData } = useQuery({
    queryKey: queryKeys.groups('tiers-modal'),
    queryFn: () => groupsApi.list({ page: 1, page_size: 100 }),
    enabled: open && view === 'form',
  });

  const tiers = data?.list ?? [];
  const groups = groupsData?.list ?? [];

  const backToList = () => {
    setView('list');
    setEditingTier(null);
    setForm(emptyForm);
  };

  const saveMutation = useCrudMutation({
    mutationFn: (payload: CreateTierReq) =>
      editingTier ? tiersApi.update(editingTier.id, payload) : tiersApi.create(payload),
    successMessage: editingTier ? t('tiers.update_success') : t('tiers.create_success'),
    queryKey: queryKeys.tiers(),
    onSuccess: () => {
      // 等级改名会影响用户列表里的等级徽标
      queryClient.invalidateQueries({ queryKey: queryKeys.users() });
      backToList();
    },
  });

  const deleteMutation = useCrudMutation({
    mutationFn: (id: number) => tiersApi.delete(id),
    successMessage: t('tiers.delete_success'),
    queryKey: queryKeys.tiers(),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.users() });
      setDeletingTier(null);
    },
  });

  const parsedRates = useMemo(() => {
    const rates: Record<number, number> = {};
    let invalid = false;
    for (const [groupId, raw] of Object.entries(form.rates)) {
      const trimmed = raw.trim();
      if (!trimmed) continue;
      const value = Number(trimmed);
      if (!Number.isFinite(value) || value <= 0) {
        invalid = true;
        continue;
      }
      rates[Number(groupId)] = value;
    }
    return { rates, invalid };
  }, [form.rates]);

  const canSave = form.name.trim() !== '' && !parsedRates.invalid;

  const handleSave = () => {
    if (!canSave) return;
    saveMutation.mutate({
      name: form.name.trim(),
      note: form.note.trim(),
      sort_weight: Number(form.sortWeight) || 0,
      rates: parsedRates.rates,
    });
  };

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) {
        backToList();
        onClose();
      }
    },
  });

  return (
    <>
      <Modal state={modalState}>
        <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog
            className="ag-elevation-modal"
            style={{ maxWidth: '640px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>
                {view === 'list'
                  ? t('tiers.title')
                  : editingTier
                    ? t('tiers.edit')
                    : t('tiers.create')}
              </Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              {view === 'list' ? (
                <div>
                  <p className="mb-3 text-xs text-text-tertiary">{t('tiers.description')}</p>
                  {isLoading ? (
                    <p className="py-8 text-center text-sm text-text-tertiary">{t('common.loading')}</p>
                  ) : tiers.length === 0 ? (
                    <p className="py-8 text-center text-sm text-text-tertiary">{t('tiers.empty')}</p>
                  ) : (
                    <div className="overflow-hidden rounded-lg border border-glass-border">
                      {tiers.map((tier, index) => (
                        <div
                          key={tier.id}
                          className={`flex items-center gap-3 px-3 py-2.5 text-sm ${index === 0 ? '' : 'border-t border-glass-border'}`}
                        >
                          <div className="min-w-0 flex-1">
                            <div className="flex items-center gap-2">
                              <span className="truncate font-medium text-text">{tier.name}</span>
                              <Chip color="accent" size="sm" variant="soft">
                                {t('tiers.user_count', { count: tier.user_count })}
                              </Chip>
                            </div>
                            <div className="mt-0.5 truncate text-[11px] text-text-tertiary">
                              {Object.keys(tier.rates ?? {}).length > 0
                                ? t('tiers.rates_count', { count: Object.keys(tier.rates ?? {}).length })
                                : t('tiers.no_rates')}
                              {tier.note ? ` · ${tier.note}` : ''}
                            </div>
                          </div>
                          <Button
                            isIconOnly
                            size="sm"
                            variant="ghost"
                            aria-label={t('common.edit')}
                            onPress={() => {
                              setEditingTier(tier);
                              setForm(formFromTier(tier));
                              setView('form');
                            }}
                          >
                            <Pencil className="h-3.5 w-3.5" />
                          </Button>
                          <Button
                            isIconOnly
                            size="sm"
                            variant="ghost"
                            className="text-danger"
                            aria-label={t('common.delete')}
                            onPress={() => setDeletingTier(tier)}
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              ) : (
                <div className="space-y-4">
                  <div className="flex gap-3">
                    <HeroTextField className="flex-1" fullWidth isRequired>
                      <Label>{t('common.name')}</Label>
                      <Input
                        value={form.name}
                        placeholder={t('tiers.name_placeholder')}
                        onChange={(e) => setForm({ ...form, name: e.target.value })}
                      />
                    </HeroTextField>
                    <HeroTextField className="w-28" fullWidth>
                      <Label>{t('tiers.sort_weight')}</Label>
                      <Input
                        type="number"
                        value={form.sortWeight}
                        onChange={(e) => setForm({ ...form, sortWeight: e.target.value })}
                      />
                    </HeroTextField>
                  </div>
                  <HeroTextField fullWidth>
                    <Label>{t('tiers.note')}</Label>
                    <Input
                      value={form.note}
                      onChange={(e) => setForm({ ...form, note: e.target.value })}
                    />
                  </HeroTextField>
                  <div>
                    <p className="mb-2 text-xs font-medium uppercase text-text-secondary">
                      {t('tiers.rates_title')}
                    </p>
                    <p className="mb-2 text-xs text-text-tertiary">{t('tiers.rates_hint')}</p>
                    <div className="overflow-hidden rounded-lg border border-glass-border">
                      {groups.length === 0 ? (
                        <p className="py-6 text-center text-sm text-text-tertiary">{t('common.no_data')}</p>
                      ) : (
                        groups.map((group, index) => (
                          <div
                            key={group.id}
                            className={`flex items-center gap-3 px-3 py-2 text-sm ${index === 0 ? '' : 'border-t border-glass-border'}`}
                          >
                            <div className="min-w-0 flex-1">
                              <span className="truncate text-text">{group.name}</span>
                            </div>
                            <span className="font-mono text-xs text-text-tertiary">
                              {t('tiers.group_default_rate', { rate: group.rate_multiplier })}
                            </span>
                            <HeroTextField className="w-24" fullWidth>
                              <Input
                                aria-label={group.name}
                                type="number"
                                min="0"
                                step="0.01"
                                placeholder={t('tiers.inherit')}
                                value={form.rates[group.id] ?? ''}
                                onChange={(e) =>
                                  setForm({ ...form, rates: { ...form.rates, [group.id]: e.target.value } })
                                }
                              />
                            </HeroTextField>
                          </div>
                        ))
                      )}
                    </div>
                  </div>
                </div>
              )}
            </Modal.Body>
            <Modal.Footer>
              {view === 'list' ? (
                <>
                  <Button variant="secondary" onPress={onClose}>
                    {t('common.close')}
                  </Button>
                  <Button
                    variant="primary"
                    onPress={() => {
                      setEditingTier(null);
                      setForm(emptyForm);
                      setView('form');
                    }}
                  >
                    <Plus className="h-4 w-4" />
                    {t('tiers.create')}
                  </Button>
                </>
              ) : (
                <>
                  <Button variant="secondary" onPress={backToList}>
                    <ArrowLeft className="h-3.5 w-3.5" />
                    {t('common.back')}
                  </Button>
                  <Button variant="primary" isDisabled={!canSave || saveMutation.isPending} onPress={handleSave}>
                    {saveMutation.isPending ? <Spinner size="sm" /> : null}
                    {t('common.save')}
                  </Button>
                </>
              )}
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      <ConfirmDialog
        open={!!deletingTier}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setDeletingTier(null);
        }}
        title={t('tiers.delete_title')}
        description={t('tiers.delete_confirm', { name: deletingTier?.name })}
        loading={deleteMutation.isPending}
        onConfirm={() => deletingTier && deleteMutation.mutate(deletingTier.id)}
      />
    </>
  );
}
