import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Button, Input, Label, ListBox, Modal, Select, Spinner, TextField as HeroTextField, useOverlayState } from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { Eye, EyeOff } from 'lucide-react';
import { NativeSwitch } from '../../../shared/components/NativeSwitch';
import { tiersApi } from '../../../shared/api/tiers';
import { queryKeys } from '../../../shared/queryKeys';
import type { UserResp, UpdateUserReq } from '../../../shared/types';

interface EditUserModalProps {
  open: boolean;
  user: UserResp;
  onClose: () => void;
  onSubmit: (data: UpdateUserReq) => void;
  loading: boolean;
}

export function EditUserModal({ open, user, onClose, onSubmit, loading }: EditUserModalProps) {
  const { t } = useTranslation();
  const editableRole = user.role === 'admin' ? 'admin' : 'user';
  const [form, setForm] = useState<UpdateUserReq>({
    max_concurrency: user.max_concurrency,
    role: editableRole,
    status: user.status as 'active' | 'disabled',
    tier_id: user.tier_id ?? 0,
    username: user.username,
  });
  const [showPassword, setShowPassword] = useState(false);

  const { data: tiersData } = useQuery({
    queryKey: queryKeys.tiers(),
    queryFn: () => tiersApi.list({ page: 1, page_size: 100 }),
    enabled: open,
  });
  const tierOptions = [
    { id: '0', label: t('tiers.none') },
    ...(tiersData?.list ?? []).map((tier) => ({ id: String(tier.id), label: tier.name })),
  ];
  const selectedTierLabel =
    tierOptions.find((option) => option.id === String(form.tier_id ?? 0))?.label ?? t('tiers.none');
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
          <Modal.Dialog className="ag-elevation-modal">
            <Modal.Header>
              <Modal.Heading>{t('users.edit')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                <HeroTextField fullWidth isDisabled>
                  <Label>{t('users.email')}</Label>
                  <Input name="email" value={user.email} disabled />
                </HeroTextField>
                <HeroTextField fullWidth>
                  <Label>{t('users.username')}</Label>
                  <Input
                    name="username"
                    value={form.username ?? ''}
                    onChange={(e) => setForm({ ...form, username: e.target.value })}
                    autoComplete="username"
                  />
                </HeroTextField>
                <HeroTextField fullWidth>
                  <Label>{t('users.password')}</Label>
                  <div className="relative">
                    <Input
                      className="pr-10"
                      name="new-password"
                      type={showPassword ? 'text' : 'password'}
                      placeholder={t('users.leave_empty_to_keep')}
                      value={form.password ?? ''}
                      onChange={(e) => setForm({ ...form, password: e.target.value || undefined })}
                      autoComplete="new-password"
                    />
                    <Button
                      isIconOnly
                      aria-label={showPassword ? 'Hide password' : 'Show password'}
                      className="absolute right-1 top-1/2 z-10 -translate-y-1/2"
                      size="sm"
                      type="button"
                      variant="ghost"
                      onPress={() => setShowPassword((value) => !value)}
                    >
                      {showPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                    </Button>
                  </div>
                </HeroTextField>
                <HeroTextField fullWidth>
                  <Label>{t('users.max_concurrency')}</Label>
                  <Input
                    type="number"
                    min="0"
                    value={String(form.max_concurrency ?? 0)}
                    onChange={(e) => setForm({ ...form, max_concurrency: Number(e.target.value) })}
                  />
                </HeroTextField>
                <Select
                  fullWidth
                  selectedKey={String(form.tier_id ?? 0)}
                  onSelectionChange={(key) =>
                    setForm({ ...form, tier_id: key == null ? 0 : Number(key) })
                  }
                >
                  <Label>{t('users.tier')}</Label>
                  <Select.Trigger>
                    <Select.Value>{selectedTierLabel}</Select.Value>
                    <Select.Indicator />
                  </Select.Trigger>
                  <Select.Popover>
                    <ListBox items={tierOptions}>
                      {(item) => (
                        <ListBox.Item id={item.id} textValue={item.label}>
                          {item.label}
                        </ListBox.Item>
                      )}
                    </ListBox>
                  </Select.Popover>
                </Select>
                <NativeSwitch
                  isSelected={form.status === 'active'}
                  contentClassName="text-xs"
                  contentStyle={{ color: form.status === 'active' ? 'var(--ag-success)' : 'var(--ag-text-tertiary)' }}
                  label={form.status === 'active' ? t('status.enabled') : t('status.disabled')}
                  onChange={(isSelected) =>
                    setForm({ ...form, status: isSelected ? 'active' : 'disabled' })
                  }
                />
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              <Button variant="primary" isDisabled={loading} onPress={() => onSubmit(form)}>
                {loading ? <Spinner size="sm" /> : null}
                {t('common.save')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
