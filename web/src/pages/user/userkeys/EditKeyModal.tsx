import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, ComboBox, Description, Input, Label, ListBox, Modal, Spinner, TextField as HeroTextField, useOverlayState } from '@heroui/react';
import { AirGateDatePicker } from '../../../shared/components/AirGateDatePicker';
import type { KeyForm } from './types';

export interface KeyGroupOption {
  value: string;
  label: string;
  suffix?: ReactNode;
}

export function EditKeyModal({
  open,
  isEdit,
  form,
  setForm,
  groupOptions,
  onClose,
  onSubmit,
  loading,
}: {
  open: boolean;
  isEdit: boolean;
  form: KeyForm;
  setForm: (form: KeyForm) => void;
  groupOptions: KeyGroupOption[];
  onClose: () => void;
  onSubmit: () => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const selectedGroup = groupOptions.find((option) => option.value === form.group_id);
  const groupItems = groupOptions.map((option) => ({
    id: option.value,
    label: (
      <div className="flex min-w-0 items-center justify-between gap-2">
        <span className="truncate">{option.label}</span>
        {option.suffix ? <span className="shrink-0 text-xs">{option.suffix}</span> : null}
      </div>
    ),
    textValue: option.label,
  }));
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  return (
    <Modal state={modalState}>
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog className="ag-elevation-modal">
            <Modal.Header>
              <Modal.Heading>{isEdit ? t('user_keys.edit') : t('user_keys.create')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
      <div className="space-y-4">
        <HeroTextField fullWidth isRequired>
          <Label>{t('common.name')}</Label>
          <Input
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
            placeholder={t('user_keys.name_placeholder')}
            required
          />
        </HeroTextField>
        <ComboBox
          allowsCustomValue
          fullWidth
          inputValue={selectedGroup?.label ?? ''}
          isRequired
          items={groupItems}
          menuTrigger="focus"
          selectedKey={form.group_id || null}
          onSelectionChange={(key) => setForm({ ...form, group_id: key == null ? '' : String(key) })}
        >
          <Label>{t('user_keys.group')}</Label>
          <ComboBox.InputGroup>
            <Input placeholder={t('user_keys.select_group')} required />
            <ComboBox.Trigger />
          </ComboBox.InputGroup>
          <ComboBox.Popover>
            <ListBox items={groupItems}>
              {(item) => (
                <ListBox.Item id={item.id} textValue={item.textValue}>
                  {item.label}
                </ListBox.Item>
              )}
            </ListBox>
          </ComboBox.Popover>
        </ComboBox>
        <HeroTextField fullWidth>
          <Label>{t('user_keys.quota_label')}</Label>
          <Input
            type="number"
            value={form.quota_usd}
            onChange={(e) => setForm({ ...form, quota_usd: e.target.value })}
            placeholder={t('user_keys.quota_unlimited_hint')}
          />
          <Description>{t('user_keys.quota_hint')}</Description>
        </HeroTextField>
        <HeroTextField fullWidth>
          <Label>{t('user_keys.sell_rate_label', '销售倍率（对外售价）')}</Label>
          <Input
            type="number"
            value={form.sell_rate}
            onChange={(e) => setForm({ ...form, sell_rate: e.target.value })}
            placeholder="0"
          />
          <Description>{t('user_keys.sell_rate_hint', '留空或 0 表示按平台原价计费')}</Description>
        </HeroTextField>
        <HeroTextField fullWidth>
          <Label>{t('user_keys.max_concurrency_label', '最大并发数')}</Label>
          <Input
            type="number"
            value={form.max_concurrency}
            onChange={(e) => setForm({ ...form, max_concurrency: e.target.value })}
            placeholder="0"
          />
          <Description>{t('user_keys.max_concurrency_hint', '留空或 0 表示不限制')}</Description>
        </HeroTextField>
        <AirGateDatePicker
          description={t('user_keys.expire_hint')}
          label={t('user_keys.expires_at')}
          value={form.expires_at}
          onChange={(value) => setForm({ ...form, expires_at: value })}
        />
      </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              <Button variant="primary" isDisabled={loading} onPress={onSubmit}>
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
