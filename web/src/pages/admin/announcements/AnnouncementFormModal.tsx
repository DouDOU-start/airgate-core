import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Description,
  Input,
  Label,
  ListBox,
  Modal,
  Select,
  Spinner,
  TextArea,
  TextField as HeroTextField,
  useOverlayState,
} from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { Megaphone } from 'lucide-react';
import type {
  AnnouncementResp,
  AnnouncementStatus,
  AnnouncementNotifyMode,
  CreateAnnouncementReq,
  UpdateAnnouncementReq,
} from '../../../shared/types';

// RFC3339 → datetime-local 输入值（本地时区，YYYY-MM-DDTHH:mm）
function toDatetimeLocal(value: string | null | undefined): string {
  if (!value) return '';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return '';
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// datetime-local 输入值 → RFC3339；空输入返回空串（后端语义：清空/立即/永久）
function toRFC3339(value: string): string {
  if (!value) return '';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return '';
  return d.toISOString();
}

export function AnnouncementFormModal({
  open,
  title,
  announcement,
  onClose,
  onSubmit,
  loading,
}: {
  open: boolean;
  title: string;
  announcement?: AnnouncementResp;
  onClose: () => void;
  onSubmit: (data: CreateAnnouncementReq | UpdateAnnouncementReq) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const isEdit = !!announcement;

  const [form, setForm] = useState({
    content: announcement?.content ?? '',
    ends_at: toDatetimeLocal(announcement?.ends_at),
    notify_mode: announcement?.notify_mode ?? ('silent' as AnnouncementNotifyMode),
    starts_at: toDatetimeLocal(announcement?.starts_at),
    status: announcement?.status ?? ('draft' as AnnouncementStatus),
    title: announcement?.title ?? '',
  });

  const statusOptions = [
    { id: 'draft', label: t('announcements.status_draft') },
    { id: 'active', label: t('announcements.status_active') },
    { id: 'archived', label: t('announcements.status_archived') },
  ];
  const notifyModeOptions = [
    { id: 'silent', label: t('announcements.notify_silent') },
    { id: 'popup', label: t('announcements.notify_popup') },
  ];
  const selectedStatusLabel =
    statusOptions.find((item) => item.id === form.status)?.label ?? t('announcements.status_draft');
  const selectedNotifyModeLabel =
    notifyModeOptions.find((item) => item.id === form.notify_mode)?.label
    ?? t('announcements.notify_silent');

  const handleSubmit = () => {
    if (!form.title || !form.content) return;

    // 时间字段总是显式提交：空串即"清空"（立即生效/永久展示），与后端约定一致
    onSubmit({
      title: form.title,
      content: form.content,
      status: form.status,
      notify_mode: form.notify_mode,
      starts_at: toRFC3339(form.starts_at),
      ends_at: toRFC3339(form.ends_at),
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
            style={{ maxWidth: '640px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>{title}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                <HeroTextField fullWidth isRequired>
                  <Label>{t('announcements.form_title')}</Label>
                  <div className="relative">
                    <Megaphone className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                    <Input
                      className="pl-9"
                      value={form.title}
                      onChange={(e) => setForm({ ...form, title: e.target.value })}
                      required
                    />
                  </div>
                </HeroTextField>

                <HeroTextField fullWidth isRequired>
                  <Label>{t('announcements.form_content')}</Label>
                  <TextArea
                    rows={8}
                    value={form.content}
                    onChange={(e) => setForm({ ...form, content: e.target.value })}
                    required
                  />
                  <Description>{t('announcements.content_hint')}</Description>
                </HeroTextField>

                <div className="grid grid-cols-2 gap-3">
                  <Select
                    fullWidth
                    selectedKey={form.status}
                    onSelectionChange={(key) =>
                      setForm({ ...form, status: (key ?? 'draft') as AnnouncementStatus })
                    }
                  >
                    <Label>{t('announcements.status')}</Label>
                    <Select.Trigger>
                      <Select.Value>{selectedStatusLabel}</Select.Value>
                      <Select.Indicator />
                    </Select.Trigger>
                    <Select.Popover>
                      <ListBox items={statusOptions}>
                        {(item) => (
                          <ListBox.Item id={item.id} textValue={item.label}>
                            {item.label}
                          </ListBox.Item>
                        )}
                      </ListBox>
                    </Select.Popover>
                  </Select>

                  <Select
                    fullWidth
                    selectedKey={form.notify_mode}
                    onSelectionChange={(key) =>
                      setForm({ ...form, notify_mode: (key ?? 'silent') as AnnouncementNotifyMode })
                    }
                  >
                    <Label>{t('announcements.notify_mode')}</Label>
                    <Select.Trigger>
                      <Select.Value>{selectedNotifyModeLabel}</Select.Value>
                      <Select.Indicator />
                    </Select.Trigger>
                    <Select.Popover>
                      <ListBox items={notifyModeOptions}>
                        {(item) => (
                          <ListBox.Item id={item.id} textValue={item.label}>
                            {item.label}
                          </ListBox.Item>
                        )}
                      </ListBox>
                    </Select.Popover>
                  </Select>
                </div>

                <div className="grid grid-cols-2 gap-3">
                  <HeroTextField fullWidth>
                    <Label>{t('announcements.starts_at')}</Label>
                    <Input
                      type="datetime-local"
                      value={form.starts_at}
                      onChange={(e) => setForm({ ...form, starts_at: e.target.value })}
                    />
                    <Description>{t('announcements.starts_at_hint')}</Description>
                  </HeroTextField>
                  <HeroTextField fullWidth>
                    <Label>{t('announcements.ends_at')}</Label>
                    <Input
                      type="datetime-local"
                      value={form.ends_at}
                      onChange={(e) => setForm({ ...form, ends_at: e.target.value })}
                    />
                    <Description>{t('announcements.ends_at_hint')}</Description>
                  </HeroTextField>
                </div>
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
