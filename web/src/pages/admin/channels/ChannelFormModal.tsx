import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button, Input, Label, Modal, Spinner, TextField as HeroTextField, useOverlayState,
} from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { channelsApi } from '../../../shared/api/channels';
import { useCrudMutation } from '../../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../../shared/queryKeys';
import { useToast } from '../../../shared/ui';
import type {
  ChannelResp, ChannelType, CreateChannelReq, UpdateChannelReq,
} from '../../../shared/types';

// 渠道类型选项（值与后端 oneof 校验一致）；custom 也纳入（每把 key 可独立选类型）。
// 定义在此供渠道列表页与 KeyFormModal 复用。
export const CHANNEL_TYPE_OPTIONS: Array<{ id: ChannelType; label: string }> = [
  { id: 'openai_compatible', label: 'OpenAI Compatible' },
  { id: 'anthropic', label: 'Anthropic' },
  { id: 'gemini', label: 'Gemini' },
  { id: 'custom', label: 'Custom' },
  // 任务类渠道（异步任务子系统）：视频（Sora 形态 /v1/videos）与 Suno 音乐
  { id: 'openai_video', label: 'OpenAI Video' },
  { id: 'suno', label: 'Suno Music' },
];

// 渠道退化为容器，表单只管 name / base_url；key 的增删改在 KeyFormModal 里做。
interface ChannelForm {
  name: string;
  base_url: string;
}

const emptyForm: ChannelForm = { name: '', base_url: '' };

interface ChannelFormModalProps {
  channel: ChannelResp | null;
  open: boolean;
  onClose: () => void;
}

export function ChannelFormModal({ channel, open, onClose }: ChannelFormModalProps) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const [form, setForm] = useState<ChannelForm>(emptyForm);
  const isEdit = !!channel;

  useEffect(() => {
    if (!open) return;
    setForm(channel ? { name: channel.name, base_url: channel.base_url } : emptyForm);
  }, [open, channel]);

  const createMutation = useCrudMutation({
    mutationFn: (data: CreateChannelReq) => channelsApi.create(data),
    successMessage: t('channels.create_success'),
    queryKey: queryKeys.channels(),
    extraQueryKeys: [queryKeys.channelKeys()],
    onSuccess: () => onClose(),
  });
  const updateMutation = useCrudMutation({
    mutationFn: ({ id, data }: { id: number; data: UpdateChannelReq }) => channelsApi.update(id, data),
    successMessage: t('channels.update_success'),
    queryKey: queryKeys.channels(),
    extraQueryKeys: [queryKeys.channelKeys()],
    onSuccess: () => onClose(),
  });

  function handleSubmit() {
    if (!form.name.trim() || !form.base_url.trim()) {
      toast('error', t('common.fill_required'));
      return;
    }
    const payload = { name: form.name.trim(), base_url: form.base_url.trim() };
    if (isEdit) {
      updateMutation.mutate({ id: channel.id, data: payload });
    } else {
      createMutation.mutate(payload);
    }
  }

  const saving = createMutation.isPending || updateMutation.isPending;
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
        <Modal.Container placement="center" size="md">
          <Modal.Dialog className="ag-elevation-modal">
            <Modal.Header>
              <Modal.Heading>{isEdit ? t('channels.edit') : t('channels.create')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                <HeroTextField fullWidth isRequired>
                  <Label>{t('common.name')}</Label>
                  <Input
                    autoComplete="off"
                    placeholder={t('channels.name_placeholder')}
                    value={form.name}
                    onChange={(event) => setForm((prev) => ({ ...prev, name: event.target.value }))}
                  />
                </HeroTextField>
                <HeroTextField fullWidth isRequired>
                  <Label>Base URL</Label>
                  <Input
                    autoComplete="off"
                    placeholder={t('channels.base_url_placeholder')}
                    value={form.base_url}
                    onChange={(event) => setForm((prev) => ({ ...prev, base_url: event.target.value }))}
                  />
                </HeroTextField>
                <p className="text-xs text-text-tertiary">{t('channels.keys_managed_in_list_hint')}</p>
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              <Button isDisabled={saving} variant="primary" onPress={handleSubmit}>
                {saving ? <Spinner size="sm" /> : null}
                {isEdit ? t('common.save') : t('common.create')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
