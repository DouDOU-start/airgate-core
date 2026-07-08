import { useEffect, useMemo, useState, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery } from '@tanstack/react-query';
import {
  Button, Checkbox, Input, Label, ListBox, Modal, Select, Spinner,
  TextArea, TextField as HeroTextField, useOverlayState,
} from '@heroui/react';
import { DownloadCloud, X } from 'lucide-react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { channelsApi } from '../../../shared/api/channels';
import { groupsApi } from '../../../shared/api/groups';
import { proxiesApi } from '../../../shared/api/proxies';
import { useCrudMutation } from '../../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../../shared/queryKeys';
import { useToast } from '../../../shared/ui';
import { FETCH_ALL_PARAMS } from '../../../shared/constants';
import type {
  ChannelResp, ChannelType, CreateChannelReq, UpdateChannelReq,
} from '../../../shared/types';

// 渠道类型选项（值与后端 oneof 校验一致）
export const CHANNEL_TYPE_OPTIONS: Array<{ id: ChannelType; label: string }> = [
  { id: 'openai_compatible', label: 'OpenAI Compatible' },
  { id: 'anthropic', label: 'Anthropic' },
  { id: 'gemini', label: 'Gemini' },
  { id: 'custom', label: 'Custom' },
];

// ==================== 标签输入（models / tags 共用） ====================

function TagInput({
  ariaLabel,
  placeholder,
  value,
  onChange,
}: {
  ariaLabel: string;
  placeholder?: string;
  value: string[];
  onChange: (next: string[]) => void;
}) {
  const [draft, setDraft] = useState('');

  function commit(raw: string) {
    // 支持一次粘贴多个（逗号/换行/空白分隔），去重后追加
    const parts = raw.split(/[\n,]+/).map((item) => item.trim()).filter(Boolean);
    if (parts.length === 0) return;
    const next = [...value];
    for (const part of parts) {
      if (!next.includes(part)) next.push(part);
    }
    onChange(next);
    setDraft('');
  }

  function handleKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'Enter' || event.key === ',') {
      event.preventDefault();
      commit(draft);
      return;
    }
    if (event.key === 'Backspace' && draft === '' && value.length > 0) {
      onChange(value.slice(0, -1));
    }
  }

  return (
    <div className="flex min-h-10 flex-wrap items-center gap-1.5 rounded-[var(--field-radius)] border border-border bg-transparent px-2.5 py-1.5">
      {value.map((tag) => (
        <span
          key={tag}
          className="inline-flex items-center gap-1 rounded-md bg-accent-soft px-1.5 py-0.5 font-mono text-xs text-accent-soft-foreground"
        >
          <span className="max-w-[240px] truncate" title={tag}>{tag}</span>
          <button
            aria-label={`remove ${tag}`}
            className="shrink-0 opacity-70 hover:opacity-100"
            type="button"
            onClick={() => onChange(value.filter((item) => item !== tag))}
          >
            <X className="h-3 w-3" />
          </button>
        </span>
      ))}
      <input
        aria-label={ariaLabel}
        className="min-w-[160px] flex-1 bg-transparent py-0.5 text-sm text-text outline-none placeholder:text-text-tertiary"
        placeholder={placeholder}
        value={draft}
        onBlur={() => commit(draft)}
        onChange={(event) => setDraft(event.target.value)}
        onKeyDown={handleKeyDown}
      />
    </div>
  );
}

// ==================== JSON 文本域（提交前 JSON.parse 校验，报错标红） ====================

function JsonField({
  error,
  label,
  placeholder,
  value,
  onChange,
}: {
  error?: string;
  label: string;
  placeholder?: string;
  value: string;
  onChange: (next: string) => void;
}) {
  return (
    <div className="space-y-1">
      <Label>{label}</Label>
      <TextArea
        aria-label={label}
        className={`w-full font-mono text-xs leading-5${error ? ' border-danger' : ''}`}
        placeholder={placeholder}
        rows={3}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
      {error ? <p className="text-xs text-danger">{error}</p> : null}
    </div>
  );
}

// ==================== 表单状态 ====================

interface ChannelForm {
  name: string;
  type: ChannelType;
  base_url: string;
  apiKeysText: string;
  models: string[];
  modelMappingText: string;
  paramOverrideText: string;
  headerOverrideText: string;
  customConfigText: string;
  groupIds: number[];
  proxyId: string;
  priority: string;
  weight: string;
  maxConcurrency: string;
  maxRpm: string;
  costRatio: string;
  testModel: string;
  tags: string[];
}

// 默认值与后端 ent/schema/channel.go 保持一致
const emptyForm: ChannelForm = {
  name: '',
  type: 'openai_compatible',
  base_url: '',
  apiKeysText: '',
  models: [],
  modelMappingText: '',
  paramOverrideText: '',
  headerOverrideText: '',
  customConfigText: '',
  groupIds: [],
  proxyId: '',
  priority: '50',
  weight: '10',
  maxConcurrency: '0',
  maxRpm: '0',
  costRatio: '1',
  testModel: '',
  tags: [],
};

function stringifyJson(value: Record<string, unknown> | Record<string, string> | null | undefined): string {
  if (!value || Object.keys(value).length === 0) return '';
  return JSON.stringify(value, null, 2);
}

function formFromChannel(channel: ChannelResp): ChannelForm {
  return {
    name: channel.name,
    type: channel.type,
    base_url: channel.base_url,
    apiKeysText: '',
    models: channel.models ?? [],
    modelMappingText: stringifyJson(channel.model_mapping),
    paramOverrideText: stringifyJson(channel.param_override),
    headerOverrideText: stringifyJson(channel.header_override),
    customConfigText: stringifyJson(channel.custom_config),
    groupIds: channel.group_ids ?? [],
    proxyId: channel.proxy_id ? String(channel.proxy_id) : '',
    priority: String(channel.priority),
    weight: String(channel.weight),
    maxConcurrency: String(channel.max_concurrency),
    maxRpm: String(channel.max_rpm),
    costRatio: String(channel.cost_ratio),
    testModel: channel.test_model,
    tags: channel.tags ?? [],
  };
}

// 解析 JSON 对象字段；空串返回 {}（更新语义：提供空集合 = 整组替换清空）
function parseJsonObject(text: string, stringValues: boolean): Record<string, unknown> {
  const trimmed = text.trim();
  if (!trimmed) return {};
  const parsed: unknown = JSON.parse(trimmed);
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('not an object');
  }
  if (stringValues) {
    for (const value of Object.values(parsed as Record<string, unknown>)) {
      if (typeof value !== 'string') throw new Error('values must be strings');
    }
  }
  return parsed as Record<string, unknown>;
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
  const [jsonErrors, setJsonErrors] = useState<Record<string, string>>({});
  const isEdit = !!channel;

  // 打开时按 创建/编辑 初始化表单
  useEffect(() => {
    if (!open) return;
    setForm(channel ? formFromChannel(channel) : emptyForm);
    setJsonErrors({});
  }, [open, channel]);

  const { data: groupsData } = useQuery({
    queryKey: queryKeys.groupsAll(),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
    enabled: open,
  });
  const { data: proxiesData } = useQuery({
    queryKey: queryKeys.proxiesAll(),
    queryFn: () => proxiesApi.list(FETCH_ALL_PARAMS),
    enabled: open,
  });

  const groups = groupsData?.list ?? [];
  const proxyOptions = useMemo(() => ([
    { id: '', label: t('channels.no_proxy') },
    ...(proxiesData?.list ?? []).map((proxy) => ({
      id: String(proxy.id),
      label: `${proxy.name} (${proxy.protocol}://${proxy.address}:${proxy.port})`,
    })),
  ]), [proxiesData?.list, t]);

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

  // 拉取上游模型列表回填（仅编辑态；创建时尚无渠道 ID）
  const fetchModelsMutation = useMutation({
    mutationFn: () => channelsApi.fetchModels(channel!.id),
    onSuccess: (resp) => {
      setForm((prev) => ({ ...prev, models: resp.models }));
      toast('success', t('channels.fetch_models_success', { count: resp.models.length }));
    },
    onError: (err: Error) => toast('error', err.message),
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
    // JSON 四字段校验
    const jsonFields: Array<{ key: string; text: string; stringValues: boolean }> = [
      { key: 'model_mapping', text: form.modelMappingText, stringValues: true },
      { key: 'param_override', text: form.paramOverrideText, stringValues: false },
      { key: 'header_override', text: form.headerOverrideText, stringValues: true },
      { key: 'custom_config', text: form.customConfigText, stringValues: false },
    ];
    const parsedJson: Record<string, Record<string, unknown>> = {};
    const nextErrors: Record<string, string> = {};
    for (const field of jsonFields) {
      try {
        parsedJson[field.key] = parseJsonObject(field.text, field.stringValues);
      } catch {
        nextErrors[field.key] = field.stringValues
          ? t('channels.json_invalid_string_map')
          : t('channels.json_invalid');
      }
    }
    setJsonErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;

    const apiKeys = form.apiKeysText.split('\n').map((line) => line.trim()).filter(Boolean);
    if (!form.name.trim() || !form.base_url.trim()) {
      toast('error', t('common.fill_required'));
      return;
    }
    if (form.models.length === 0) {
      toast('error', t('channels.models_required'));
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

    const shared = {
      name: form.name.trim(),
      type: form.type,
      base_url: form.base_url.trim(),
      models: form.models,
      model_mapping: parsedJson.model_mapping as Record<string, string>,
      param_override: parsedJson.param_override,
      header_override: parsedJson.header_override as Record<string, string>,
      custom_config: parsedJson.custom_config,
      tags: form.tags,
      test_model: form.testModel.trim(),
      group_ids: form.groupIds,
      ...numbers,
    };

    if (isEdit) {
      const payload: UpdateChannelReq = {
        ...shared,
        // api_keys 留空 = 不改；非空 = 整组替换
        ...(apiKeys.length > 0 ? { api_keys: apiKeys } : {}),
        // proxy_id 传 0 = 解绑代理
        proxy_id: form.proxyId ? Number(form.proxyId) : 0,
      };
      updateMutation.mutate({ id: channel.id, data: payload });
    } else {
      const payload: CreateChannelReq = {
        ...shared,
        api_keys: apiKeys,
        ...(form.proxyId ? { proxy_id: Number(form.proxyId) } : {}),
      };
      createMutation.mutate(payload);
    }
  }

  const saving = createMutation.isPending || updateMutation.isPending;
  const selectedTypeLabel = CHANNEL_TYPE_OPTIONS.find((item) => item.id === form.type)?.label ?? '';
  const selectedProxyLabel = proxyOptions.find((item) => item.id === form.proxyId)?.label ?? t('channels.no_proxy');
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

                <div className="space-y-1">
                  <div className="flex items-center justify-between gap-2">
                    <Label>{t('channels.models')}</Label>
                    <Button
                      isDisabled={!isEdit || fetchModelsMutation.isPending}
                      size="sm"
                      variant="secondary"
                      onPress={() => fetchModelsMutation.mutate()}
                    >
                      {fetchModelsMutation.isPending ? <Spinner size="sm" /> : <DownloadCloud className="h-3.5 w-3.5" />}
                      {t('channels.fetch_models')}
                    </Button>
                  </div>
                  <TagInput
                    ariaLabel={t('channels.models')}
                    placeholder={t('channels.models_placeholder')}
                    value={form.models}
                    onChange={(models) => set('models', models)}
                  />
                  {!isEdit ? (
                    <p className="text-xs text-text-tertiary">{t('channels.fetch_models_create_hint')}</p>
                  ) : null}
                </div>

                <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                  <JsonField
                    error={jsonErrors.model_mapping}
                    label={t('channels.model_mapping')}
                    placeholder={'{\n  "gpt-4o": "gpt-4o-2024-11-20"\n}'}
                    value={form.modelMappingText}
                    onChange={(text) => set('modelMappingText', text)}
                  />
                  <JsonField
                    error={jsonErrors.param_override}
                    label={t('channels.param_override')}
                    placeholder={'{\n  "temperature": 0.7\n}'}
                    value={form.paramOverrideText}
                    onChange={(text) => set('paramOverrideText', text)}
                  />
                  <JsonField
                    error={jsonErrors.header_override}
                    label={t('channels.header_override')}
                    placeholder={'{\n  "X-Custom-Header": "value"\n}'}
                    value={form.headerOverrideText}
                    onChange={(text) => set('headerOverrideText', text)}
                  />
                  <JsonField
                    error={jsonErrors.custom_config}
                    label={t('channels.custom_config')}
                    placeholder={'{ }'}
                    value={form.customConfigText}
                    onChange={(text) => set('customConfigText', text)}
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

                <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                  <Select
                    fullWidth
                    selectedKey={form.proxyId}
                    onSelectionChange={(key) => set('proxyId', key == null ? '' : String(key))}
                  >
                    <Label>{t('channels.proxy')}</Label>
                    <Select.Trigger>
                      <Select.Value>{selectedProxyLabel}</Select.Value>
                      <Select.Indicator />
                    </Select.Trigger>
                    <Select.Popover>
                      <ListBox items={proxyOptions}>
                        {(item) => (
                          <ListBox.Item id={item.id} textValue={item.label}>
                            {item.label}
                          </ListBox.Item>
                        )}
                      </ListBox>
                    </Select.Popover>
                  </Select>
                  <HeroTextField fullWidth>
                    <Label>{t('channels.test_model')}</Label>
                    <Input
                      autoComplete="off"
                      placeholder={t('channels.test_model_placeholder')}
                      value={form.testModel}
                      onChange={(event) => set('testModel', event.target.value)}
                    />
                  </HeroTextField>
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
