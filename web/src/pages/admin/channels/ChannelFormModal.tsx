import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  Button, Checkbox, Input, Label, ListBox, Modal, Select, Spinner,
  TextArea, TextField as HeroTextField, useOverlayState,
} from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { channelsApi } from '../../../shared/api/channels';
import { groupsApi } from '../../../shared/api/groups';
import { useCrudMutation } from '../../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../../shared/queryKeys';
import { useToast } from '../../../shared/ui';
import { FETCH_ALL_PARAMS } from '../../../shared/constants';
import { TagInput, KeyValueEditor, kvRowsToRecord, recordToKVRows, type KVRow } from './editors';
import type {
  ChannelResp, ChannelType, CreateChannelReq, UpdateChannelReq,
} from '../../../shared/types';

// 渠道类型选项（值与后端 oneof 校验一致）
export const CHANNEL_TYPE_OPTIONS: Array<{ id: ChannelType; label: string }> = [
  { id: 'openai_compatible', label: 'OpenAI Compatible' },
  { id: 'anthropic', label: 'Anthropic' },
  { id: 'gemini', label: 'Gemini' },
];

// parseParamValue 参数覆写值智能解析：能按 JSON 解析的（数字/布尔/对象/带引号字符串）
// 用解析结果，否则按原样字符串——管理员填 0.7 得到数字，填 gpt-4o 得到字符串。
function parseParamValue(raw: string): unknown {
  const trimmed = raw.trim();
  if (trimmed === '') return '';
  try {
    return JSON.parse(trimmed) as unknown;
  } catch {
    return raw;
  }
}

// ==================== 表单状态 ====================

// 模型清单 / 模型映射在「模型」弹窗（ChannelTestModal）管理，表单不承载：
// 创建时模型可空（渠道不会被调度命中），建后再配。
interface ChannelForm {
  name: string;
  type: ChannelType;
  base_url: string;
  apiKeysText: string;
  paramSetRows: KVRow[];
  paramRemoveKeys: string[];
  headerRows: KVRow[];
  groupIds: number[];
  priority: string;
  weight: string;
  maxConcurrency: string;
  maxRpm: string;
  costRatio: string;
  tags: string[];
}

// 默认值与后端 ent/schema/channel.go 保持一致
const emptyForm: ChannelForm = {
  name: '',
  type: 'openai_compatible',
  base_url: '',
  apiKeysText: '',
  paramSetRows: [],
  paramRemoveKeys: [],
  headerRows: [],
  groupIds: [],
  priority: '50',
  weight: '10',
  maxConcurrency: '0',
  maxRpm: '0',
  costRatio: '1',
  tags: [],
};

function formFromChannel(channel: ChannelResp): ChannelForm {
  // param_override 语义：值为 null 表示转发时删除该参数，其余为覆盖写入。
  const paramEntries = Object.entries(channel.param_override ?? {});
  return {
    name: channel.name,
    type: channel.type,
    base_url: channel.base_url,
    apiKeysText: '',
    paramSetRows: recordToKVRows(Object.fromEntries(paramEntries.filter(([, v]) => v !== null))),
    paramRemoveKeys: paramEntries.filter(([, v]) => v === null).map(([k]) => k),
    headerRows: recordToKVRows(channel.header_override),
    groupIds: channel.group_ids ?? [],
    priority: String(channel.priority),
    weight: String(channel.weight),
    maxConcurrency: String(channel.max_concurrency),
    maxRpm: String(channel.max_rpm),
    costRatio: String(channel.cost_ratio),
    tags: channel.tags ?? [],
  };
}

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

  // 打开时按 创建/编辑 初始化表单
  useEffect(() => {
    if (!open) return;
    setForm(channel ? formFromChannel(channel) : emptyForm);
  }, [open, channel]);

  const { data: groupsData } = useQuery({
    queryKey: queryKeys.groupsAll(),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
    enabled: open,
  });
  const groups = groupsData?.list ?? [];

  const createMutation = useCrudMutation({
    mutationFn: (data: CreateChannelReq) => channelsApi.create(data),
    successMessage: t('channels.create_success'),
    queryKey: queryKeys.channels(),
    onSuccess: () => onClose(),
  });
  const updateMutation = useCrudMutation({
    mutationFn: ({ id, data }: { id: number; data: UpdateChannelReq }) => channelsApi.update(id, data),
    successMessage: t('channels.update_success'),
    queryKey: queryKeys.channels(),
    onSuccess: () => onClose(),
  });

  function set<K extends keyof ChannelForm>(key: K, value: ChannelForm[K]) {
    setForm((prev) => ({ ...prev, [key]: value }));
  }

  function toggleGroup(groupId: number, selected: boolean) {
    setForm((prev) => ({
      ...prev,
      groupIds: selected
        ? [...new Set([...prev.groupIds, groupId])]
        : prev.groupIds.filter((id) => id !== groupId),
    }));
  }

  function handleSubmit() {
    // 行编辑器 → 后端对象：删除参数按 value=null 语义合并进 param_override。
    const paramOverride: Record<string, unknown> = kvRowsToRecord(form.paramSetRows, parseParamValue);
    for (const key of form.paramRemoveKeys) {
      const trimmed = key.trim();
      if (trimmed) paramOverride[trimmed] = null;
    }
    const headerOverride = kvRowsToRecord(form.headerRows) as Record<string, string>;

    const apiKeys = form.apiKeysText.split('\n').map((line) => line.trim()).filter(Boolean);
    if (!form.name.trim() || !form.base_url.trim()) {
      toast('error', t('common.fill_required'));
      return;
    }
    if (!isEdit && apiKeys.length === 0) {
      toast('error', t('channels.api_keys_required'));
      return;
    }

    const numbers = {
      priority: Number(form.priority) || 0,
      weight: Number(form.weight) || 0,
      max_concurrency: Number(form.maxConcurrency) || 0,
      max_rpm: Number(form.maxRpm) || 0,
      cost_ratio: Number(form.costRatio) || 0,
    };

    // models / model_mapping 由「模型与测试」弹窗维护，
    // 编辑载荷不携带（后端 partial 语义：缺省 = 不改），避免互相覆盖。
    const shared = {
      name: form.name.trim(),
      type: form.type,
      base_url: form.base_url.trim(),
      param_override: paramOverride,
      header_override: headerOverride,
      tags: form.tags,
      group_ids: form.groupIds,
      ...numbers,
    };

    if (isEdit) {
      const payload: UpdateChannelReq = {
        ...shared,
        // api_keys 留空 = 不改；非空 = 整组替换
        ...(apiKeys.length > 0 ? { api_keys: apiKeys } : {}),
      };
      updateMutation.mutate({ id: channel.id, data: payload });
    } else {
      const payload: CreateChannelReq = {
        ...shared,
        api_keys: apiKeys,
      };
      createMutation.mutate(payload);
    }
  }

  const saving = createMutation.isPending || updateMutation.isPending;
  const selectedTypeLabel = CHANNEL_TYPE_OPTIONS.find((item) => item.id === form.type)?.label ?? '';
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
        <Modal.Container placement="center" scroll="inside" size="lg">
          <Modal.Dialog
            className="ag-elevation-modal"
            style={{ maxWidth: '880px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>{isEdit ? t('channels.edit') : t('channels.create')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                  <HeroTextField fullWidth isRequired>
                    <Label>{t('common.name')}</Label>
                    <Input
                      autoComplete="off"
                      placeholder={t('channels.name_placeholder')}
                      value={form.name}
                      onChange={(event) => set('name', event.target.value)}
                    />
                  </HeroTextField>
                  <Select
                    fullWidth
                    isRequired
                    selectedKey={form.type}
                    onSelectionChange={(key) => set('type', (key ?? 'openai_compatible') as ChannelType)}
                  >
                    <Label>{t('common.type')}</Label>
                    <Select.Trigger>
                      <Select.Value>{selectedTypeLabel}</Select.Value>
                      <Select.Indicator />
                    </Select.Trigger>
                    <Select.Popover>
                      <ListBox items={CHANNEL_TYPE_OPTIONS}>
                        {(item) => (
                          <ListBox.Item id={item.id} textValue={item.label}>
                            {item.label}
                          </ListBox.Item>
                        )}
                      </ListBox>
                    </Select.Popover>
                  </Select>
                </div>

                <HeroTextField fullWidth isRequired>
                  <Label>Base URL</Label>
                  <Input
                    autoComplete="off"
                    placeholder={t('channels.base_url_placeholder')}
                    value={form.base_url}
                    onChange={(event) => set('base_url', event.target.value)}
                  />
                </HeroTextField>

                <div className="space-y-1">
                  <Label>{t('channels.api_keys')}</Label>
                  <TextArea
                    aria-label={t('channels.api_keys')}
                    className="w-full font-mono text-xs leading-5"
                    placeholder={
                      isEdit
                        ? t('channels.api_keys_edit_placeholder', {
                            count: channel.api_keys_count,
                            hints: (channel.api_key_hints ?? []).join(', '),
                          })
                        : t('channels.api_keys_placeholder')
                    }
                    rows={3}
                    value={form.apiKeysText}
                    onChange={(event) => set('apiKeysText', event.target.value)}
                  />
                  <p className="text-xs text-text-tertiary">{t('channels.api_keys_hint')}</p>
                </div>

                {/* 模型清单/映射不在表单承载（创建可空），指引到渠道列表「模型」弹窗 */}
                <p className="text-xs text-text-tertiary">{t('channels.models_in_test_modal_hint')}</p>

                <div className="space-y-1">
                  <Label>{t('channels.param_override')}</Label>
                  <p className="text-xs text-text-tertiary">{t('channels.param_override_hint')}</p>
                  <KeyValueEditor
                    ariaLabel={t('channels.param_override')}
                    keyPlaceholder={t('channels.param_name_placeholder')}
                    valuePlaceholder={t('channels.param_value_placeholder')}
                    rows={form.paramSetRows}
                    onChange={(rows) => set('paramSetRows', rows)}
                  />
                </div>

                <div className="space-y-1">
                  <Label>{t('channels.param_remove')}</Label>
                  <p className="text-xs text-text-tertiary">{t('channels.param_remove_hint')}</p>
                  <TagInput
                    ariaLabel={t('channels.param_remove')}
                    placeholder={t('channels.param_remove_placeholder')}
                    value={form.paramRemoveKeys}
                    onChange={(keys) => set('paramRemoveKeys', keys)}
                  />
                </div>

                <div className="space-y-1">
                  <Label>{t('channels.header_override')}</Label>
                  <p className="text-xs text-text-tertiary">{t('channels.header_override_hint')}</p>
                  <KeyValueEditor
                    ariaLabel={t('channels.header_override')}
                    keyPlaceholder={t('channels.header_name_placeholder')}
                    valuePlaceholder={t('channels.header_value_placeholder')}
                    rows={form.headerRows}
                    onChange={(rows) => set('headerRows', rows)}
                  />
                </div>

                <div className="space-y-1">
                  <Label>{t('channels.groups')}</Label>
                  <p className="text-xs text-text-tertiary">{t('channels.groups_hint')}</p>
                  {groups.length === 0 ? (
                    <p className="py-2 text-sm text-text-tertiary">{t('common.no_data')}</p>
                  ) : (
                    <div className="grid max-h-40 grid-cols-1 gap-x-4 gap-y-1 overflow-y-auto sm:grid-cols-2 md:grid-cols-3">
                      {groups.map((group) => (
                        <Checkbox
                          key={group.id}
                          isSelected={form.groupIds.includes(group.id)}
                          onChange={(selected) => toggleGroup(group.id, selected)}
                        >
                          <Checkbox.Control>
                            <Checkbox.Indicator />
                          </Checkbox.Control>
                          <span className="truncate text-sm text-text">{group.name}</span>
                        </Checkbox>
                      ))}
                    </div>
                  )}
                </div>

                <div className="grid grid-cols-2 gap-4 md:grid-cols-5">
                  <HeroTextField fullWidth>
                    <Label>{t('channels.priority')}</Label>
                    <Input
                      min={0}
                      max={999}
                      type="number"
                      value={form.priority}
                      onChange={(event) => set('priority', event.target.value)}
                    />
                  </HeroTextField>
                  <HeroTextField fullWidth>
                    <Label>{t('channels.weight')}</Label>
                    <Input
                      min={0}
                      type="number"
                      value={form.weight}
                      onChange={(event) => set('weight', event.target.value)}
                    />
                  </HeroTextField>
                  <HeroTextField fullWidth>
                    <Label>{t('channels.max_concurrency')}</Label>
                    <Input
                      min={0}
                      type="number"
                      value={form.maxConcurrency}
                      onChange={(event) => set('maxConcurrency', event.target.value)}
                    />
                  </HeroTextField>
                  <HeroTextField fullWidth>
                    <Label>{t('channels.max_rpm')}</Label>
                    <Input
                      min={0}
                      type="number"
                      value={form.maxRpm}
                      onChange={(event) => set('maxRpm', event.target.value)}
                    />
                  </HeroTextField>
                  <HeroTextField fullWidth>
                    <Label>{t('channels.cost_ratio')}</Label>
                    <Input
                      min={0}
                      step={0.01}
                      type="number"
                      value={form.costRatio}
                      onChange={(event) => set('costRatio', event.target.value)}
                    />
                  </HeroTextField>
                </div>

                <div className="space-y-1">
                  <Label>{t('channels.tags')}</Label>
                  <TagInput
                    ariaLabel={t('channels.tags')}
                    placeholder={t('channels.tags_placeholder')}
                    value={form.tags}
                    onChange={(tags) => set('tags', tags)}
                  />
                </div>
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
