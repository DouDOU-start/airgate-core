import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  Button, Checkbox, Input, Label, ListBox, Modal, Select, Spinner,
  TextArea, TextField as HeroTextField, useOverlayState,
} from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { NativeSwitch } from '../../../shared/components/NativeSwitch';
import { channelsApi } from '../../../shared/api/channels';
import { groupsApi } from '../../../shared/api/groups';
import { useCrudMutation } from '../../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../../shared/queryKeys';
import { FETCH_ALL_PARAMS } from '../../../shared/constants';
import { useToast } from '../../../shared/ui';
import { CHANNEL_TYPE_OPTIONS } from './ChannelFormModal';
import {
  KeyValueEditor, TagInput, kvRowsToRecord, recordToKVRows, type KVRow,
} from './editors';
import type { ChannelKeyReq, ChannelKeyResp, ChannelType } from '../../../shared/types';

// parseParamValue 智能识别 param_override 值：数字 / 布尔 / JSON 对象，否则原样字符串。
function parseParamValue(raw: string): unknown {
  const trimmed = raw.trim();
  if (trimmed === '') return '';
  if (trimmed === 'true') return true;
  if (trimmed === 'false') return false;
  const num = Number(trimmed);
  if (!Number.isNaN(num) && /^-?\d+(\.\d+)?$/.test(trimmed)) return num;
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
    try {
      return JSON.parse(trimmed);
    } catch {
      return raw;
    }
  }
  return raw;
}

interface KeyForm {
  name: string;
  type: ChannelType;
  apiKey: string;
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
  enabled: boolean;
  // 是否参与主动余额刷新（进页自动/一键批量）；官方直连等无余额接口的上游关掉，省得反复打无效请求。
  balanceCheckEnabled: boolean;
  probeEnabled: boolean;
  probeModel: string;
  upstreamRateEnabled: boolean;
  upstreamRatePath: string;
  useUpstreamRateForCost: boolean;
}

const AIRGATE_RATE_PATH = '/v1/airgate/billing';
const UPSTREAM_RATE_PLATFORM_PRESETS = [
  { id: AIRGATE_RATE_PATH, labelKey: 'channels.upstream_rate_platform_airgate' },
  { id: '/v1/sub2api/billing', labelKey: 'channels.upstream_rate_platform_sub2api' },
] as const;

const emptyForm: KeyForm = {
  name: '',
  type: 'openai_compatible',
  apiKey: '',
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
  enabled: true,
  balanceCheckEnabled: true,
  probeEnabled: false,
  probeModel: '',
  upstreamRateEnabled: false,
  upstreamRatePath: AIRGATE_RATE_PATH,
  useUpstreamRateForCost: false,
};

function formFromKey(key: ChannelKeyResp): KeyForm {
  const override = key.param_override ?? {};
  const set = (override.set ?? {}) as Record<string, unknown>;
  const remove = (override.remove ?? []) as string[];
  return {
    name: key.name,
    type: key.type,
    apiKey: '', // 明文不回显，留空 = 保持原密钥
    paramSetRows: recordToKVRows(set),
    paramRemoveKeys: Array.isArray(remove) ? remove : [],
    headerRows: recordToKVRows(key.header_override),
    groupIds: key.group_ids ?? [],
    priority: String(key.priority),
    weight: String(key.weight),
    maxConcurrency: String(key.max_concurrency),
    maxRpm: String(key.max_rpm),
    costRatio: String(key.cost_ratio),
    tags: key.tags ?? [],
    enabled: key.status !== 'disabled_manual',
    balanceCheckEnabled: key.balance_check_enabled,
    probeEnabled: key.probe_enabled,
    probeModel: key.probe_model ?? '',
    upstreamRateEnabled: key.upstream_rate_enabled,
    upstreamRatePath: key.upstream_rate_path || AIRGATE_RATE_PATH,
    useUpstreamRateForCost: key.use_upstream_rate_for_cost,
  };
}

interface KeyFormModalProps {
  // channelId 走新增模式；channelKey 走编辑模式（二选一）。
  channelId: number | null;
  channelKey: ChannelKeyResp | null;
  open: boolean;
  onClose: () => void;
}

export function KeyFormModal({ channelId, channelKey, open, onClose }: KeyFormModalProps) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const [form, setForm] = useState<KeyForm>(emptyForm);
  const isEdit = !!channelKey;

  useEffect(() => {
    if (!open) return;
    setForm(channelKey ? formFromKey(channelKey) : emptyForm);
  }, [open, channelKey]);

  const { data: groupsData } = useQuery({
    queryKey: queryKeys.groups(FETCH_ALL_PARAMS),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
    enabled: open,
  });
  const groups = groupsData?.list ?? [];

  const addMutation = useCrudMutation({
    mutationFn: ({ id, data }: { id: number; data: ChannelKeyReq }) => channelsApi.addKey(id, data),
    successMessage: t('channels.add_key_success'),
    queryKey: queryKeys.channels(),
    extraQueryKeys: [queryKeys.channelKeys()],
    onSuccess: () => onClose(),
  });
  const updateMutation = useCrudMutation({
    mutationFn: ({ id, data }: { id: number; data: ChannelKeyReq }) => channelsApi.updateKey(id, data),
    successMessage: t('channels.update_key_success'),
    queryKey: queryKeys.channels(),
    extraQueryKeys: [queryKeys.channelKeys()],
    onSuccess: () => onClose(),
  });

  function toggleGroup(id: number, selected: boolean) {
    setForm((prev) => ({
      ...prev,
      groupIds: selected ? [...new Set([...prev.groupIds, id])] : prev.groupIds.filter((g) => g !== id),
    }));
  }

  function handleSubmit() {
    const apiKey = form.apiKey.trim();
    if (!isEdit && !apiKey) {
      toast('error', t('channels.api_key_required'));
      return;
    }

    const paramSet = kvRowsToRecord(form.paramSetRows, parseParamValue);
    const paramRemove = form.paramRemoveKeys.map((k) => k.trim()).filter(Boolean);
    const paramOverride: Record<string, unknown> = {};
    if (Object.keys(paramSet).length > 0) paramOverride.set = paramSet;
    if (paramRemove.length > 0) paramOverride.remove = paramRemove;

    const payload: ChannelKeyReq = {
      name: form.name.trim(),
      type: form.type,
      api_key: apiKey, // 编辑时留空 = 保持原密钥
      param_override: paramOverride,
      header_override: kvRowsToRecord(form.headerRows) as Record<string, string>,
      group_ids: form.groupIds,
      status: form.enabled ? 'enabled' : 'disabled_manual',
      priority: Number(form.priority) || 0,
      weight: Number(form.weight) || 0,
      max_concurrency: Number(form.maxConcurrency) || 0,
      max_rpm: Number(form.maxRpm) || 0,
      cost_ratio: Number(form.costRatio) || 0,
      tags: form.tags,
      balance_check_enabled: form.balanceCheckEnabled,
      probe_enabled: form.probeEnabled,
      probe_model: form.probeModel.trim() || undefined,
      upstream_rate_enabled: form.upstreamRateEnabled,
      upstream_rate_path: form.upstreamRatePath.trim(),
      use_upstream_rate_for_cost: form.upstreamRateEnabled && form.useUpstreamRateForCost,
    };

    if (isEdit && channelKey) {
      updateMutation.mutate({ id: channelKey.id, data: payload });
    } else if (channelId != null) {
      addMutation.mutate({ id: channelId, data: payload });
    }
  }

  const saving = addMutation.isPending || updateMutation.isPending;
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });
  const selectedTypeLabel = CHANNEL_TYPE_OPTIONS.find((item) => item.id === form.type)?.label ?? form.type;
  const upstreamRatePlatformOptions = UPSTREAM_RATE_PLATFORM_PRESETS.map((item) => ({
    id: item.id,
    label: t(item.labelKey),
  }));
  if (!UPSTREAM_RATE_PLATFORM_PRESETS.some((item) => item.id === form.upstreamRatePath)) {
    upstreamRatePlatformOptions.push({
      id: form.upstreamRatePath,
      label: t('channels.upstream_rate_platform_existing'),
    });
  }
  const selectedUpstreamRatePlatformLabel = upstreamRatePlatformOptions
    .find((item) => item.id === form.upstreamRatePath)?.label ?? form.upstreamRatePath;

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" size="lg">
          <Modal.Dialog className="ag-elevation-modal">
            <Modal.Header>
              <Modal.Heading>{isEdit ? t('channels.edit_key') : t('channels.add_key')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <HeroTextField fullWidth>
                    <Label>{t('channels.key_name')}</Label>
                    <Input
                      autoComplete="off"
                      placeholder={t('channels.key_name_placeholder')}
                      value={form.name}
                      onChange={(event) => setForm((prev) => ({ ...prev, name: event.target.value }))}
                    />
                  </HeroTextField>
                  <div>
                    <Label className="mb-1.5 block text-sm">{t('common.type')}</Label>
                    <Select
                      aria-label={t('common.type')}
                      fullWidth
                      selectedKey={form.type}
                      onSelectionChange={(key) => setForm((prev) => ({ ...prev, type: String(key) as ChannelType }))}
                    >
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
                </div>

                <HeroTextField fullWidth isRequired={!isEdit}>
                  <Label>{t('channels.api_key')}</Label>
                  <TextArea
                    autoComplete="off"
                    placeholder={isEdit ? t('channels.api_key_edit_placeholder', { hint: channelKey?.api_key_hint }) : t('channels.api_key_placeholder')}
                    rows={2}
                    value={form.apiKey}
                    onChange={(event) => setForm((prev) => ({ ...prev, apiKey: event.target.value }))}
                  />
                </HeroTextField>
                <p className="text-xs text-text-tertiary">{t('channels.models_in_key_modal_hint')}</p>

                <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
                  <HeroTextField>
                    <Label>{t('channels.priority')}</Label>
                    <Input min={0} max={999} type="number" value={form.priority} onChange={(e) => setForm((p) => ({ ...p, priority: e.target.value }))} />
                  </HeroTextField>
                  <HeroTextField>
                    <Label>{t('channels.weight')}</Label>
                    <Input min={0} type="number" value={form.weight} onChange={(e) => setForm((p) => ({ ...p, weight: e.target.value }))} />
                  </HeroTextField>
                  <HeroTextField>
                    <Label>{t('channels.max_concurrency')}</Label>
                    <Input min={0} type="number" value={form.maxConcurrency} onChange={(e) => setForm((p) => ({ ...p, maxConcurrency: e.target.value }))} />
                  </HeroTextField>
                  <HeroTextField>
                    <Label>{t('channels.max_rpm')}</Label>
                    <Input min={0} type="number" value={form.maxRpm} onChange={(e) => setForm((p) => ({ ...p, maxRpm: e.target.value }))} />
                  </HeroTextField>
                  <HeroTextField>
                    <Label>{t('channels.cost_ratio')}</Label>
                    <Input min={0} step="0.01" type="number" value={form.costRatio} onChange={(e) => setForm((p) => ({ ...p, costRatio: e.target.value }))} />
                  </HeroTextField>
                </div>

                <div>
                  <Label className="mb-1 block text-sm">{t('channels.param_override')}</Label>
                  <p className="mb-1.5 text-xs text-text-tertiary">{t('channels.param_override_hint')}</p>
                  <KeyValueEditor
                    ariaLabel={t('channels.param_override')}
                    keyPlaceholder={t('channels.param_name_placeholder')}
                    valuePlaceholder={t('channels.param_value_placeholder')}
                    rows={form.paramSetRows}
                    onChange={(rows) => setForm((p) => ({ ...p, paramSetRows: rows }))}
                  />
                </div>
                <div>
                  <Label className="mb-1 block text-sm">{t('channels.param_remove')}</Label>
                  <p className="mb-1.5 text-xs text-text-tertiary">{t('channels.param_remove_hint')}</p>
                  <TagInput
                    ariaLabel={t('channels.param_remove')}
                    placeholder={t('channels.param_remove_placeholder')}
                    value={form.paramRemoveKeys}
                    onChange={(tags) => setForm((p) => ({ ...p, paramRemoveKeys: tags }))}
                  />
                </div>
                <div>
                  <Label className="mb-1 block text-sm">{t('channels.header_override')}</Label>
                  <p className="mb-1.5 text-xs text-text-tertiary">{t('channels.header_override_hint')}</p>
                  <KeyValueEditor
                    ariaLabel={t('channels.header_override')}
                    keyPlaceholder={t('channels.header_name_placeholder')}
                    valuePlaceholder={t('channels.header_value_placeholder')}
                    rows={form.headerRows}
                    onChange={(rows) => setForm((p) => ({ ...p, headerRows: rows }))}
                  />
                </div>

                <div>
                  <Label className="mb-1 block text-sm">{t('channels.groups')}</Label>
                  <p className="mb-1.5 text-xs text-text-tertiary">{t('channels.groups_hint')}</p>
                  <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                    {groups.map((group) => (
                      <Checkbox
                        key={group.id}
                        isSelected={form.groupIds.includes(group.id)}
                        onChange={(selected) => toggleGroup(group.id, selected)}
                      >
                        <Checkbox.Control>
                          <Checkbox.Indicator />
                        </Checkbox.Control>
                        <span className="text-sm">{group.name}</span>
                      </Checkbox>
                    ))}
                  </div>
                </div>

                <div>
                  <Label className="mb-1 block text-sm">{t('channels.tags')}</Label>
                  <TagInput
                    ariaLabel={t('channels.tags')}
                    placeholder={t('channels.tags_placeholder')}
                    value={form.tags}
                    onChange={(tags) => setForm((p) => ({ ...p, tags }))}
                  />
                </div>

                <div className="flex flex-wrap items-center gap-x-8 gap-y-3">
                  <NativeSwitch
                    ariaLabel={t('channels.key_enabled')}
                    isSelected={form.enabled}
                    label={t('channels.key_enabled')}
                    onChange={(selected) => setForm((p) => ({ ...p, enabled: selected }))}
                  />
                  <span title={t('channels.balance_check_enabled_hint')}>
                    <NativeSwitch
                      ariaLabel={t('channels.balance_check_enabled')}
                      isSelected={form.balanceCheckEnabled}
                      label={t('channels.balance_check_enabled')}
                      onChange={(selected) => setForm((p) => ({ ...p, balanceCheckEnabled: selected }))}
                    />
                  </span>
                  <NativeSwitch
                    ariaLabel={t('channels.probe_enabled')}
                    isSelected={form.probeEnabled}
                    label={t('channels.probe_enabled')}
                    onChange={(selected) => setForm((p) => ({ ...p, probeEnabled: selected }))}
                  />
                  <NativeSwitch
                    ariaLabel={t('channels.upstream_rate_enabled')}
                    isSelected={form.upstreamRateEnabled}
                    label={t('channels.upstream_rate_enabled')}
                    onChange={(selected) => setForm((p) => ({
                      ...p,
                      upstreamRateEnabled: selected,
                      useUpstreamRateForCost: selected ? p.useUpstreamRateForCost : false,
                    }))}
                  />
                </div>
                {form.probeEnabled ? (
                  <HeroTextField fullWidth>
                    <Label>{t('channels.probe_model')}</Label>
                    <Input
                      autoComplete="off"
                      placeholder={t('channels.probe_model_placeholder')}
                      value={form.probeModel}
                      onChange={(e) => setForm((p) => ({ ...p, probeModel: e.target.value }))}
                    />
                  </HeroTextField>
                ) : null}
                {form.upstreamRateEnabled ? (
                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 sm:items-stretch sm:gap-x-6">
                    <div>
                      <Label className="mb-1.5 block text-sm">{t('channels.upstream_rate_platform')}</Label>
                      <Select
                        aria-label={t('channels.upstream_rate_platform')}
                        fullWidth
                        selectedKey={form.upstreamRatePath}
                        onSelectionChange={(key) => setForm((p) => ({ ...p, upstreamRatePath: String(key) }))}
                      >
                        <Select.Trigger>
                          <Select.Value>{selectedUpstreamRatePlatformLabel}</Select.Value>
                          <Select.Indicator />
                        </Select.Trigger>
                        <Select.Popover>
                          <ListBox items={upstreamRatePlatformOptions}>
                            {(item) => (
                              <ListBox.Item id={item.id} textValue={item.label}>
                                {item.label}
                              </ListBox.Item>
                            )}
                          </ListBox>
                        </Select.Popover>
                      </Select>
                    </div>
                    <div className="flex flex-col">
                      {/* 与左列 Label 等高的隐形占位：撑出标题行，使开关中线对齐下拉框中线 */}
                      <span aria-hidden className="mb-1.5 hidden select-none text-sm sm:block sm:invisible">
                        {t('channels.upstream_rate_platform')}
                      </span>
                      <div className="flex flex-1 items-center">
                        <span title={t('channels.use_upstream_rate_for_cost_hint')}>
                          <NativeSwitch
                            ariaLabel={t('channels.use_upstream_rate_for_cost')}
                            isSelected={form.useUpstreamRateForCost}
                            label={t('channels.use_upstream_rate_for_cost')}
                            onChange={(selected) => setForm((p) => ({ ...p, useUpstreamRateForCost: selected }))}
                          />
                        </span>
                      </div>
                    </div>
                  </div>
                ) : null}
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
