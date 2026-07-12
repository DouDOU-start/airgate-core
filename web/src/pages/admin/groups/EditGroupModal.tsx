import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Description, Input, Label, Modal, Spinner, TextField as HeroTextField, useOverlayState } from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { ArrowUpDown, Layers } from 'lucide-react';
import { NativeSwitch } from '../../../shared/components/NativeSwitch';
import type { GroupResp, CreateGroupReq, UpdateGroupReq } from '../../../shared/types';

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

  // 数字字段以字符串存储：受控数字输入若每次 onChange 都 Number() 往返，
  // 会把 "0." / "0.0" 等小数中间态吃成 0（输入 0.01 时光标"跳"），故保留输入原文，提交时再转。
  const buildForm = () => ({
    is_exclusive: group?.is_exclusive ?? false,
    name: group?.name ?? '',
    note: group?.note ?? '',
    rate_multiplier: String(group?.rate_multiplier ?? 1),
    sort_weight: String(group?.sort_weight ?? 0),
    status_visible: group?.status_visible ?? true,
  });

  const [form, setForm] = useState(buildForm);

  // 弹窗常驻挂载：关闭时把表单重置回初始值，避免下次打开残留上次输入
  useEffect(() => {
    if (!open) {
      setForm(buildForm());
    }
  }, [open, group]);

  const handleSubmit = () => {
    if (!isEdit && !form.name) return;
    onSubmit({
      ...form,
      rate_multiplier: form.rate_multiplier === '' ? 1 : Number(form.rate_multiplier),
      sort_weight: form.sort_weight === '' ? 0 : Number(form.sort_weight),
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
            value={form.rate_multiplier}
            onChange={(e) => setForm({ ...form, rate_multiplier: e.target.value })}
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
