import { useEffect, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  Button, Checkbox, Input, Label, ListBox, Modal, Select, Spinner,
  TextArea, TextField as HeroTextField, useOverlayState,
} from '@heroui/react';
import {
  Activity, Braces, ChevronDown, FolderTree, KeyRound, Save, SlidersHorizontal,
} from 'lucide-react';
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
  types: ChannelType[];
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
  types: ['openai_compatible'],
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
    types: key.credential_protocols?.length > 0 ? key.credential_protocols : [key.type],
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
    enabled: key.credential_status === 'enabled',
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

function FormSectionHeader({
  icon,
  title,
  hint,
  aside,
}: {
  icon: ReactNode;
  title: string;
  hint?: string;
  aside?: ReactNode;
}) {
  return (
    <div className="ag-key-form-section__header">
      <div className="ag-key-form-section__heading">
        <span className="ag-key-form-section__icon" aria-hidden="true">{icon}</span>
        <div className="ag-key-form-section__copy">
          <h3>{title}</h3>
          {hint ? <p>{hint}</p> : null}
        </div>
      </div>
      {aside ? <div className="ag-key-form-section__aside">{aside}</div> : null}
    </div>
  );
}

export function KeyFormModal({ channelId, channelKey, open, onClose }: KeyFormModalProps) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const [form, setForm] = useState<KeyForm>(emptyForm);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const isEdit = !!channelKey;

  useEffect(() => {
    if (!open) return;
    const nextForm = channelKey ? formFromKey(channelKey) : emptyForm;
    setForm(nextForm);
    setAdvancedOpen(
      nextForm.paramSetRows.length > 0
      || nextForm.paramRemoveKeys.length > 0
      || nextForm.headerRows.length > 0,
    );
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

  function toggleProtocol(type: ChannelType, selected: boolean) {
    setForm((prev) => {
      if (!selected) {
        return { ...prev, types: prev.types.filter((item) => item !== type) };
      }
      // 异步任务协议生命周期独立，保持单选；同步协议之间可任意组合。
      if (type === 'openai_video' || type === 'suno') {
        return { ...prev, types: [type] };
      }
      const withoutTaskProtocols = prev.types.filter((item) => item !== 'openai_video' && item !== 'suno');
      return { ...prev, types: [...new Set([...withoutTaskProtocols, type])] };
    });
  }

  function handleSubmit() {
    const apiKey = form.apiKey.trim();
    if (!isEdit && !apiKey) {
      toast('error', t('channels.api_key_required'));
      return;
    }
    if (form.types.length === 0) {
      toast('error', t('channels.protocol_required'));
      return;
    }

    const paramSet = kvRowsToRecord(form.paramSetRows, parseParamValue);
    const paramRemove = form.paramRemoveKeys.map((k) => k.trim()).filter(Boolean);
    const paramOverride: Record<string, unknown> = {};
    if (Object.keys(paramSet).length > 0) paramOverride.set = paramSet;
    if (paramRemove.length > 0) paramOverride.remove = paramRemove;

    const payload: ChannelKeyReq = {
      name: form.name.trim(),
      types: form.types,
      api_key: apiKey, // 编辑时留空 = 保持原密钥
      param_override: paramOverride,
      header_override: kvRowsToRecord(form.headerRows) as Record<string, string>,
      group_ids: form.groupIds,
      credential_status: form.enabled ? 'enabled' : 'disabled_manual',
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
  const upstreamRatePlatformOptions: Array<{ id: string; label: string }> = UPSTREAM_RATE_PLATFORM_PRESETS.map((item) => ({
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
  const overrideCount = form.paramSetRows.length + form.paramRemoveKeys.length + form.headerRows.length;

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="lg">
          <Modal.Dialog className="ag-elevation-modal ag-key-form-modal">
            <Modal.Header className="ag-key-form-modal__header">
              <div className="ag-key-form-modal__title">
                <Modal.Heading>{isEdit ? t('channels.edit_key') : t('channels.add_key')}</Modal.Heading>
                {isEdit && channelKey ? (
                  <div className="ag-key-form-modal__meta">
                    <span>{channelKey.channel_name}</span>
                    <i aria-hidden="true" />
                    <code>{channelKey.api_key_hint || '-'}</code>
                  </div>
                ) : null}
              </div>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body className="ag-key-form-modal__body">
              <div className="ag-key-form-modal__content">
                <section className="ag-key-form-section ag-key-form-section--identity">
                  <FormSectionHeader
                    icon={<KeyRound size={16} />}
                    title={t('channels.form_credentials_title')}
                    hint={t('channels.form_credentials_hint')}
                    aside={(
                      <NativeSwitch
                        ariaLabel={t('channels.key_enabled')}
                        isSelected={form.enabled}
                        label={t('channels.key_enabled')}
                        onChange={(selected) => setForm((p) => ({ ...p, enabled: selected }))}
                      />
                    )}
                  />
                <div className="ag-key-form-credentials-grid">
                  <div className="ag-key-form-credentials-fields">
                  <HeroTextField fullWidth>
                    <Label>{t('channels.key_name')}</Label>
                    <Input
                      autoComplete="off"
                      placeholder={t('channels.key_name_placeholder')}
                      value={form.name}
                      onChange={(event) => setForm((prev) => ({ ...prev, name: event.target.value }))}
                    />
                  </HeroTextField>
                  <HeroTextField fullWidth isRequired={!isEdit}>
                    <Label>{t('channels.api_key')}</Label>
                    <TextArea
                      className="ag-key-form-secret-field"
                      autoComplete="off"
                      placeholder={isEdit ? t('channels.api_key_edit_placeholder', { hint: channelKey?.api_key_hint }) : t('channels.api_key_placeholder')}
                      rows={2}
                      value={form.apiKey}
                      onChange={(event) => setForm((prev) => ({ ...prev, apiKey: event.target.value }))}
                    />
                  </HeroTextField>
                  </div>
                  <div className="ag-key-form-protocol-panel">
                    <div className="ag-key-form-protocol-panel__head">
                      <Label>{t('channels.supported_protocols')}</Label>
                      <span>{t('channels.protocol_shared_hint')}</span>
                    </div>
                    <div className="ag-key-form-protocol-grid">
                      {CHANNEL_TYPE_OPTIONS.map((item) => (
                        <Checkbox
                          key={item.id}
                          isSelected={form.types.includes(item.id)}
                          onChange={(selected) => toggleProtocol(item.id, selected)}
                        >
                          <Checkbox.Control>
                            <Checkbox.Indicator />
                          </Checkbox.Control>
                          <span className="text-xs">{item.label}</span>
                        </Checkbox>
                      ))}
                    </div>
                    <p className="ag-key-form-protocol-panel__foot">{t('channels.models_in_key_modal_hint')}</p>
                  </div>
                </div>
                </section>

                <section className="ag-key-form-section">
                  <FormSectionHeader
                    icon={<SlidersHorizontal size={16} />}
                    title={t('channels.form_scheduling_title')}
                    hint={t('channels.form_scheduling_hint')}
                  />
                <div className="ag-key-form-metric-grid">
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
                </section>

                <details
                  className="ag-key-form-advanced"
                  open={advancedOpen}
                  onToggle={(event) => setAdvancedOpen(event.currentTarget.open)}
                >
                  <summary>
                    <span className="ag-key-form-section__icon" aria-hidden="true"><Braces size={16} /></span>
                    <span className="ag-key-form-advanced__copy">
                      <strong>{t('channels.form_advanced_title')}</strong>
                      <small>{t('channels.form_advanced_hint')}</small>
                    </span>
                    {overrideCount > 0 ? <span className="ag-key-form-advanced__count">{overrideCount}</span> : null}
                    <ChevronDown className="ag-key-form-advanced__chevron" size={16} aria-hidden="true" />
                  </summary>
                  <div className="ag-key-form-advanced__content">
                <div className="ag-key-form-transform-card">
                  <Label>{t('channels.param_override')}</Label>
                  <p>{t('channels.param_override_hint')}</p>
                  <KeyValueEditor
                    ariaLabel={t('channels.param_override')}
                    keyPlaceholder={t('channels.param_name_placeholder')}
                    valuePlaceholder={t('channels.param_value_placeholder')}
                    rows={form.paramSetRows}
                    onChange={(rows) => setForm((p) => ({ ...p, paramSetRows: rows }))}
                  />
                </div>
                <div className="ag-key-form-transform-card ag-key-form-transform-card--wide">
                  <Label>{t('channels.param_remove')}</Label>
                  <p>{t('channels.param_remove_hint')}</p>
                  <TagInput
                    ariaLabel={t('channels.param_remove')}
                    placeholder={t('channels.param_remove_placeholder')}
                    value={form.paramRemoveKeys}
                    onChange={(tags) => setForm((p) => ({ ...p, paramRemoveKeys: tags }))}
                  />
                </div>
                <div className="ag-key-form-transform-card">
                  <Label>{t('channels.header_override')}</Label>
                  <p>{t('channels.header_override_hint')}</p>
                  <KeyValueEditor
                    ariaLabel={t('channels.header_override')}
                    keyPlaceholder={t('channels.header_name_placeholder')}
                    valuePlaceholder={t('channels.header_value_placeholder')}
                    rows={form.headerRows}
                    onChange={(rows) => setForm((p) => ({ ...p, headerRows: rows }))}
                  />
                </div>
                  </div>
                </details>

                <section className="ag-key-form-section">
                  <FormSectionHeader
                    icon={<FolderTree size={16} />}
                    title={t('channels.form_routing_title')}
                    hint={t('channels.form_routing_hint')}
                  />
                <div className="ag-key-form-routing-grid">
                <div className="ag-key-form-field-group">
                  <Label className="mb-1 block text-sm">{t('channels.groups')}</Label>
                  <p className="mb-1.5 text-xs text-text-tertiary">{t('channels.groups_hint')}</p>
                  {groups.length === 0 ? (
                    <div className="ag-key-form-empty">{t('common.no_data')}</div>
                  ) : (
                    <div className="ag-key-form-group-list">
                      {groups.map((group) => (
                        <Checkbox
                          key={group.id}
                          isSelected={form.groupIds.includes(group.id)}
                          onChange={(selected) => toggleGroup(group.id, selected)}
                        >
                          <Checkbox.Control>
                            <Checkbox.Indicator />
                          </Checkbox.Control>
                          <span title={group.name}>{group.name}</span>
                        </Checkbox>
                      ))}
                    </div>
                  )}
                </div>

                <div className="ag-key-form-field-group">
                  <Label className="mb-1 block text-sm">{t('channels.tags')}</Label>
                  <p className="mb-1.5 text-xs text-text-tertiary">{t('channels.tags_placeholder')}</p>
                  <TagInput
                    ariaLabel={t('channels.tags')}
                    placeholder={t('channels.tags_placeholder')}
                    value={form.tags}
                    onChange={(tags) => setForm((p) => ({ ...p, tags }))}
                  />
                </div>
                </div>
                </section>

                <section className="ag-key-form-section ag-key-form-section--automation">
                  <FormSectionHeader
                    icon={<Activity size={16} />}
                    title={t('channels.form_automation_title')}
                    hint={t('channels.form_automation_hint')}
                  />
                <div className="ag-key-form-toggle-grid">
                  <div className="ag-key-form-toggle-card" data-selected={form.balanceCheckEnabled}>
                    <span title={t('channels.balance_check_enabled_hint')}>
                      <NativeSwitch
                        ariaLabel={t('channels.balance_check_enabled')}
                        isSelected={form.balanceCheckEnabled}
                        label={t('channels.balance_check_enabled')}
                        onChange={(selected) => setForm((p) => ({ ...p, balanceCheckEnabled: selected }))}
                      />
                    </span>
                    <p>{t('channels.form_balance_hint')}</p>
                  </div>
                  <div className="ag-key-form-toggle-card" data-selected={form.probeEnabled}>
                    <NativeSwitch
                      ariaLabel={t('channels.probe_enabled')}
                      isSelected={form.probeEnabled}
                      label={t('channels.probe_enabled')}
                      onChange={(selected) => setForm((p) => ({ ...p, probeEnabled: selected }))}
                    />
                    <p>{t('channels.form_probe_hint')}</p>
                  </div>
                  <div className="ag-key-form-toggle-card" data-selected={form.upstreamRateEnabled}>
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
                    <p>{t('channels.form_rate_probe_hint')}</p>
                  </div>
                </div>
                {form.probeEnabled || form.upstreamRateEnabled ? (
                  <div className="ag-key-form-automation-fields">
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
                  <div className="ag-key-form-rate-controls">
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
                    <div className="ag-key-form-rate-switch" data-selected={form.useUpstreamRateForCost}>
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
                ) : null}
                  </div>
                ) : null}
                </section>
              </div>
            </Modal.Body>
            <Modal.Footer className="ag-key-form-modal__footer">
              <Button variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              <Button isDisabled={saving} variant="primary" onPress={handleSubmit}>
                {saving ? <Spinner size="sm" /> : <Save size={15} />}
                {isEdit ? t('common.save') : t('common.create')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
