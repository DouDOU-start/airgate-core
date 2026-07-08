import { useEffect, useState, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery } from '@tanstack/react-query';
import {
  Button, Checkbox, Input, Label, ListBox, Modal, Select, Spinner,
  TextArea, TextField as HeroTextField, useOverlayState,
} from '@heroui/react';
import { DownloadCloud, Plus, X } from 'lucide-react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { channelsApi } from '../../../shared/api/channels';
import { groupsApi } from '../../../shared/api/groups';
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

// ==================== 键值对行编辑器（模型映射 / 参数覆写 / Header 覆写共用） ====================

interface KVRow {
  key: string;
  value: string;
}

// kvRowsToRecord 行 → 对象：键去首尾空白，空键行忽略；重复键后者覆盖前者。
function kvRowsToRecord(rows: KVRow[], mapValue: (raw: string) => unknown = (raw) => raw): Record<string, unknown> {
  const result: Record<string, unknown> = {};
  for (const row of rows) {
    const key = row.key.trim();
    if (!key) continue;
    result[key] = mapValue(row.value);
  }
  return result;
}

// recordToKVRows 对象 → 行：非字符串值序列化为 JSON 文本回显。
function recordToKVRows(record: Record<string, unknown> | null | undefined): KVRow[] {
  return Object.entries(record ?? {}).map(([key, value]) => ({
    key,
    value: typeof value === 'string' ? value : JSON.stringify(value),
  }));
}

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

function KeyValueEditor({
  ariaLabel,
  keyPlaceholder,
  valuePlaceholder,
  rows,
  onChange,
}: {
  ariaLabel: string;
  keyPlaceholder: string;
  valuePlaceholder: string;
  rows: KVRow[];
  onChange: (next: KVRow[]) => void;
}) {
  const { t } = useTranslation();

  function updateRow(index: number, patch: Partial<KVRow>) {
    onChange(rows.map((row, i) => (i === index ? { ...row, ...patch } : row)));
  }

  return (
    <div className="space-y-1.5">
      {rows.map((row, index) => (
        // 行无稳定业务主键，索引即身份（增删只在尾部/原位），用 index 作 key 可接受
        <div key={index} className="flex items-center gap-2">
          <Input
            aria-label={`${ariaLabel} key`}
            className="flex-1 font-mono text-xs"
            placeholder={keyPlaceholder}
            value={row.key}
            onChange={(event) => updateRow(index, { key: event.target.value })}
          />
          <Input
            aria-label={`${ariaLabel} value`}
            className="flex-1 font-mono text-xs"
            placeholder={valuePlaceholder}
            value={row.value}
            onChange={(event) => updateRow(index, { value: event.target.value })}
          />
          <Button
            isIconOnly
            aria-label={t('common.delete')}
            size="sm"
            variant="ghost"
            onPress={() => onChange(rows.filter((_, i) => i !== index))}
          >
            <X className="h-3.5 w-3.5" />
          </Button>
        </div>
      ))}
      <Button size="sm" variant="secondary" onPress={() => onChange([...rows, { key: '', value: '' }])}>
        <Plus className="h-3.5 w-3.5" />
        {t('channels.add_row')}
      </Button>
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
  mappingRows: KVRow[];
  paramSetRows: KVRow[];
  paramRemoveKeys: string[];
  headerRows: KVRow[];
  customConfigText: string;
  groupIds: number[];
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
  mappingRows: [],
  paramSetRows: [],
  paramRemoveKeys: [],
  headerRows: [],
  customConfigText: '',
  groupIds: [],
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
  // param_override 语义：值为 null 表示转发时删除该参数，其余为覆盖写入。
  const paramEntries = Object.entries(channel.param_override ?? {});
  return {
    name: channel.name,
    type: channel.type,
    base_url: channel.base_url,
    apiKeysText: '',
    models: channel.models ?? [],
    mappingRows: recordToKVRows(channel.model_mapping),
    paramSetRows: recordToKVRows(Object.fromEntries(paramEntries.filter(([, v]) => v !== null))),
    paramRemoveKeys: paramEntries.filter(([, v]) => v === null).map(([k]) => k),
    headerRows: recordToKVRows(channel.header_override),
    customConfigText: stringifyJson(channel.custom_config),
    groupIds: channel.group_ids ?? [],
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

  // 拉取上游模型列表回填。表单里填了新 key（或创建态）时按表单连接参数预览拉取，
  // 无需先保存渠道；编辑态未填新 key 则按渠道 ID 用库里已存密钥拉取。
  const firstTypedKey = form.apiKeysText.split('\n').map((line) => line.trim()).find(Boolean) ?? '';
  const canFetchModels = form.base_url.trim() !== '' && (isEdit || firstTypedKey !== '');
  const fetchModelsMutation = useMutation({
    mutationFn: () => (firstTypedKey
      ? channelsApi.fetchModelsPreview({
          type: form.type,
          base_url: form.base_url.trim(),
          api_key: firstTypedKey,
        })
      : channelsApi.fetchModels(channel!.id)),
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
    // 仅剩 custom_config 是 JSON 域（custom 类型的声明式接入，面向高级用户）。
    const nextErrors: Record<string, string> = {};
    let customConfig: Record<string, unknown> = {};
    try {
      customConfig = parseJsonObject(form.customConfigText, false);
    } catch {
      nextErrors.custom_config = t('channels.json_invalid');
    }
    setJsonErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;

    // 行编辑器 → 后端对象：删除参数按 value=null 语义合并进 param_override。
    const modelMapping = kvRowsToRecord(form.mappingRows) as Record<string, string>;
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
      model_mapping: modelMapping,
      param_override: paramOverride,
      header_override: headerOverride,
      custom_config: customConfig,
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

                <div className="space-y-1">
                  <div className="flex items-center justify-between gap-2">
                    <Label>{t('channels.models')}</Label>
                    <Button
                      isDisabled={!canFetchModels || fetchModelsMutation.isPending}
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
                  {!isEdit && !canFetchModels ? (
                    <p className="text-xs text-text-tertiary">{t('channels.fetch_models_create_hint')}</p>
                  ) : null}
                </div>

                <div className="space-y-1">
                  <Label>{t('channels.model_mapping')}</Label>
                  <p className="text-xs text-text-tertiary">{t('channels.model_mapping_hint')}</p>
                  <KeyValueEditor
                    ariaLabel={t('channels.model_mapping')}
                    keyPlaceholder={t('channels.mapping_from_placeholder')}
                    valuePlaceholder={t('channels.mapping_to_placeholder')}
                    rows={form.mappingRows}
                    onChange={(rows) => set('mappingRows', rows)}
                  />
                </div>

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

                {form.type === 'custom' ? (
                  <JsonField
                    error={jsonErrors.custom_config}
                    label={t('channels.custom_config')}
                    placeholder={'{ }'}
                    value={form.customConfigText}
                    onChange={(text) => set('customConfigText', text)}
                  />
                ) : null}

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
