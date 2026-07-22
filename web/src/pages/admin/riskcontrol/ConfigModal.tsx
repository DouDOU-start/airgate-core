import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  Button, Chip, Input, Label, ListBox, Modal, Select, Spinner, TextArea,
  TextField as HeroTextField, useOverlayState,
} from '@heroui/react';
import { FlaskConical, Plus, Trash2 } from 'lucide-react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { NativeSwitch } from '../../../shared/components/NativeSwitch';
import { groupsApi } from '../../../shared/api/groups';
import {
  riskControlApi,
  type KeywordBlockingMode,
  type ModelFilterType,
  type ModerationMode,
  type RiskControlConfig,
  type RiskControlTestAuditResult,
  type UpdateRiskControlConfigReq,
} from '../../../shared/api/riskControl';
import { useCrudMutation } from '../../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../../shared/queryKeys';
import { FETCH_ALL_PARAMS } from '../../../shared/constants';
import { useToast } from '../../../shared/ui';

type TabKey = 'basic' | 'scope' | 'runtime' | 'response' | 'thresholds' | 'keywords' | 'retention';

const TABS: TabKey[] = ['basic', 'scope', 'runtime', 'response', 'thresholds', 'keywords', 'retention'];

interface ConfigForm {
  mode: ModerationMode;
  base_url: string;
  model: string;
  newKeys: string[];
  deleteKeyHashes: string[];
  timeout_ms: string;
  sample_rate: string;
  all_groups: boolean;
  group_ids: number[];
  record_non_hits: boolean;
  thresholds: Record<string, string>;
  worker_count: string;
  queue_size: string;
  block_status: string;
  block_message: string;
  email_on_hit: boolean;
  auto_ban_enabled: boolean;
  ban_threshold: string;
  violation_window_hours: string;
  retry_count: string;
  hit_retention_days: string;
  non_hit_retention_days: string;
  pre_hash_check_enabled: boolean;
  blocked_keywords: string;
  keyword_blocking_mode: KeywordBlockingMode;
  model_filter_type: ModelFilterType;
  model_filter_models: string;
}

function configToForm(cfg: RiskControlConfig): ConfigForm {
  const thresholds: Record<string, string> = {};
  for (const category of cfg.categories) {
    thresholds[category] = String(cfg.thresholds[category] ?? '');
  }
  return {
    mode: cfg.mode,
    base_url: cfg.base_url,
    model: cfg.model,
    newKeys: [''],
    deleteKeyHashes: [],
    timeout_ms: String(cfg.timeout_ms),
    sample_rate: String(cfg.sample_rate),
    all_groups: cfg.all_groups,
    group_ids: cfg.group_ids ?? [],
    record_non_hits: cfg.record_non_hits,
    thresholds,
    worker_count: String(cfg.worker_count),
    queue_size: String(cfg.queue_size),
    block_status: String(cfg.block_status),
    block_message: cfg.block_message,
    email_on_hit: cfg.email_on_hit,
    auto_ban_enabled: cfg.auto_ban_enabled,
    ban_threshold: String(cfg.ban_threshold),
    violation_window_hours: String(cfg.violation_window_hours),
    retry_count: String(cfg.retry_count),
    hit_retention_days: String(cfg.hit_retention_days),
    non_hit_retention_days: String(cfg.non_hit_retention_days),
    pre_hash_check_enabled: cfg.pre_hash_check_enabled,
    blocked_keywords: (cfg.blocked_keywords ?? []).join('\n'),
    keyword_blocking_mode: cfg.keyword_blocking_mode,
    model_filter_type: cfg.model_filter?.type ?? 'all',
    model_filter_models: (cfg.model_filter?.models ?? []).join('\n'),
  };
}

function splitLines(raw: string): string[] {
  return raw.split('\n').map((s) => s.trim()).filter(Boolean);
}

function formToPayload(form: ConfigForm): UpdateRiskControlConfigReq {
  const thresholds: Record<string, number> = {};
  for (const [category, raw] of Object.entries(form.thresholds)) {
    const v = Number(raw);
    if (Number.isFinite(v)) thresholds[category] = v;
  }
  const newKeys = form.newKeys.map((s) => s.trim()).filter(Boolean);
  return {
    mode: form.mode,
    base_url: form.base_url.trim(),
    model: form.model.trim(),
    ...(newKeys.length > 0 ? { api_keys: newKeys, api_keys_mode: 'append' as const } : {}),
    ...(form.deleteKeyHashes.length > 0 ? { delete_api_key_hashes: form.deleteKeyHashes } : {}),
    timeout_ms: Number(form.timeout_ms) || 0,
    sample_rate: Number(form.sample_rate),
    all_groups: form.all_groups,
    group_ids: form.group_ids,
    record_non_hits: form.record_non_hits,
    thresholds,
    worker_count: Number(form.worker_count) || 0,
    queue_size: Number(form.queue_size) || 0,
    block_status: Number(form.block_status) || 0,
    block_message: form.block_message,
    email_on_hit: form.email_on_hit,
    auto_ban_enabled: form.auto_ban_enabled,
    ban_threshold: Number(form.ban_threshold) || 0,
    violation_window_hours: Number(form.violation_window_hours) || 0,
    retry_count: Number(form.retry_count),
    hit_retention_days: Number(form.hit_retention_days) || 0,
    non_hit_retention_days: Number(form.non_hit_retention_days) || 0,
    pre_hash_check_enabled: form.pre_hash_check_enabled,
    blocked_keywords: splitLines(form.blocked_keywords),
    keyword_blocking_mode: form.keyword_blocking_mode,
    model_filter: { type: form.model_filter_type, models: splitLines(form.model_filter_models) },
  };
}

interface ConfigModalProps {
  config: RiskControlConfig | null;
  open: boolean;
  onClose: () => void;
}

export function ConfigModal({ config, open, onClose }: ConfigModalProps) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const [activeTab, setActiveTab] = useState<TabKey>('basic');
  const [form, setForm] = useState<ConfigForm | null>(null);
  const [testing, setTesting] = useState(false);
  const [testPrompt, setTestPrompt] = useState('I want to kill everyone in the building');
  const [auditResult, setAuditResult] = useState<RiskControlTestAuditResult | null>(null);

  useEffect(() => {
    if (!open || !config) return;
    setForm(configToForm(config));
    setActiveTab('basic');
    setAuditResult(null);
  }, [open, config]);

  const { data: groupsData } = useQuery({
    queryKey: queryKeys.groupsAll(),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
    enabled: open,
  });
  const groups = groupsData?.list ?? [];

  const saveMutation = useCrudMutation({
    mutationFn: (payload: UpdateRiskControlConfigReq) => riskControlApi.updateConfig(payload),
    successMessage: t('risk_control.save_success'),
    queryKey: queryKeys.riskControlConfig(),
    extraQueryKeys: [queryKeys.riskControlStatus()],
    onSuccess: () => onClose(),
  });

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  const set = <K extends keyof ConfigForm>(key: K, value: ConfigForm[K]) =>
    setForm((prev) => (prev ? { ...prev, [key]: value } : prev));

  const remainingKeyStatuses = useMemo(
    () => (config?.api_key_statuses ?? []).filter((s) => !form?.deleteKeyHashes.includes(s.key_hash)),
    [config, form?.deleteKeyHashes],
  );

  async function handleTestKeys() {
    if (!form) return;
    setTesting(true);
    setAuditResult(null);
    try {
      const resp = await riskControlApi.testKeys({
        api_keys: form.newKeys.map((s) => s.trim()).filter(Boolean),
        base_url: form.base_url.trim(),
        model: form.model.trim(),
        prompt: testPrompt.trim() || undefined,
      });
      const okCount = resp.items.filter((i) => i.status === 'ok').length;
      toast('success', t('risk_control.test_done', { ok: okCount, total: resp.items.length }));
      if (resp.audit_result) setAuditResult(resp.audit_result);
    } catch (err) {
      toast('error', err instanceof Error ? err.message : String(err));
    } finally {
      setTesting(false);
    }
  }

  if (!form) return null;

  const numberField = (labelKey: string, key: keyof ConfigForm, hintKey?: string) => (
    <HeroTextField fullWidth>
      <Label>{t(labelKey)}</Label>
      <Input
        type="number"
        value={String(form[key])}
        onChange={(e) => set(key, e.target.value as ConfigForm[typeof key])}
      />
      {hintKey ? <div className="text-xs text-default-400 mt-1">{t(hintKey)}</div> : null}
    </HeroTextField>
  );

  const modeOptions: { id: ModerationMode; label: string }[] = [
    { id: 'pre_block', label: t('risk_control.mode_pre_block') },
    { id: 'observe', label: t('risk_control.mode_observe') },
    { id: 'off', label: t('risk_control.mode_off') },
  ];
  const keywordModeOptions: { id: KeywordBlockingMode; label: string }[] = [
    { id: 'keyword_and_api', label: t('risk_control.kw_mode_keyword_and_api') },
    { id: 'keyword_only', label: t('risk_control.kw_mode_keyword_only') },
    { id: 'api_only', label: t('risk_control.kw_mode_api_only') },
  ];
  const filterTypeOptions: { id: ModelFilterType; label: string }[] = [
    { id: 'all', label: t('risk_control.filter_all') },
    { id: 'include', label: t('risk_control.filter_include') },
    { id: 'exclude', label: t('risk_control.filter_exclude') },
  ];

  const selectField = <V extends string>(
    labelKey: string,
    value: V,
    options: { id: V; label: string }[],
    onChange: (v: V) => void,
  ) => (
    <div>
      <Label className="mb-1 block">{t(labelKey)}</Label>
      <Select fullWidth selectedKey={value} onSelectionChange={(key) => key != null && onChange(String(key) as V)}>
        <Select.Trigger>
          <Select.Value>{options.find((o) => o.id === value)?.label}</Select.Value>
          <Select.Indicator />
        </Select.Trigger>
        <Select.Popover>
          <ListBox items={options}>
            {(item) => <ListBox.Item id={item.id} textValue={item.label}>{item.label}</ListBox.Item>}
          </ListBox>
        </Select.Popover>
      </Select>
    </div>
  );

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" size="lg">
          <Modal.Dialog className="ag-elevation-modal">
            <Modal.Header>
              <Modal.Heading>{t('risk_control.config_title')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="flex flex-wrap gap-2 mb-4">
                {TABS.map((tab) => (
                  <Button
                    key={tab}
                    size="sm"
                    variant={activeTab === tab ? 'primary' : 'ghost'}
                    onPress={() => setActiveTab(tab)}
                  >
                    {t(`risk_control.tab_${tab}`)}
                  </Button>
                ))}
              </div>

              <div className="space-y-4 max-h-[60vh] overflow-y-auto pr-1">
                {activeTab === 'basic' && (
                  <>
                    {selectField('risk_control.mode', form.mode, modeOptions, (v) => set('mode', v))}
                    <HeroTextField fullWidth>
                      <Label>{t('risk_control.base_url')}</Label>
                      <Input
                        placeholder="https://api.openai.com"
                        value={form.base_url}
                        onChange={(e) => set('base_url', e.target.value)}
                      />
                    </HeroTextField>
                    <HeroTextField fullWidth>
                      <Label>{t('risk_control.model')}</Label>
                      <Input
                        placeholder="omni-moderation-latest"
                        value={form.model}
                        onChange={(e) => set('model', e.target.value)}
                      />
                    </HeroTextField>
                    <div>
                      <Label className="mb-1 block">{t('risk_control.existing_keys')}</Label>
                      {remainingKeyStatuses.length === 0 ? (
                        <div className="text-sm text-default-400">{t('risk_control.no_keys')}</div>
                      ) : (
                        <div className="space-y-1">
                          {remainingKeyStatuses.map((s) => (
                            <div key={s.key_hash} className="flex items-center gap-2 text-sm">
                              <code className="font-mono">{s.masked}</code>
                              <Chip
                                color={s.status === 'ok' ? 'success' : s.status === 'frozen' ? 'warning' : s.status === 'error' ? 'danger' : 'default'}
                                size="sm"
                              >
                                {t(`risk_control.key_status_${s.status}`)}
                              </Chip>
                              {s.last_error ? (
                                <span className="text-xs text-default-400 truncate max-w-56" title={s.last_error}>{s.last_error}</span>
                              ) : null}
                              <Button
                                isIconOnly
                                aria-label={t('common.delete')}
                                className="ml-auto"
                                size="sm"
                                variant="ghost"
                                onPress={() => set('deleteKeyHashes', [...form.deleteKeyHashes, s.key_hash])}
                              >
                                <Trash2 className="w-3.5 h-3.5" />
                              </Button>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                    <div>
                      <Label className="mb-1 block">{t('risk_control.new_keys')}</Label>
                      <div className="space-y-2">
                        {form.newKeys.map((key, i) => (
                          <div key={i} className="flex items-center gap-2">
                            <HeroTextField fullWidth>
                              <Input
                                placeholder={t('risk_control.new_key_placeholder')}
                                value={key}
                                onChange={(e) => {
                                  const next = [...form.newKeys];
                                  next[i] = e.target.value;
                                  set('newKeys', next);
                                }}
                              />
                            </HeroTextField>
                            {form.newKeys.length > 1 && (
                              <Button
                                isIconOnly
                                aria-label={t('common.delete')}
                                size="sm"
                                variant="ghost"
                                onPress={() => set('newKeys', form.newKeys.filter((_, j) => j !== i))}
                              >
                                <Trash2 className="w-3.5 h-3.5" />
                              </Button>
                            )}
                          </div>
                        ))}
                        <Button
                          size="sm"
                          variant="ghost"
                          onPress={() => set('newKeys', [...form.newKeys, ''])}
                        >
                          <Plus className="w-4 h-4" />
                          {t('risk_control.add_key')}
                        </Button>
                      </div>
                    </div>
                    <div className="flex items-end gap-2">
                      <HeroTextField fullWidth>
                        <Label>{t('risk_control.test_prompt')}</Label>
                        <Input
                          placeholder={t('risk_control.test_prompt_placeholder')}
                          value={testPrompt}
                          onChange={(e) => setTestPrompt(e.target.value)}
                        />
                      </HeroTextField>
                      <Button isDisabled={testing} variant="secondary" onPress={handleTestKeys}>
                        {testing ? <Spinner size="sm" /> : <FlaskConical className="w-4 h-4" />}
                        {t('risk_control.test_keys')}
                      </Button>
                    </div>
                    {auditResult ? (
                      <div className="text-sm rounded-lg border border-default-200 p-3">
                        <div>
                          {t('risk_control.audit_flagged')}：
                          <Chip color={auditResult.flagged ? 'danger' : 'success'} size="sm">
                            {auditResult.flagged ? t('risk_control.flagged') : t('risk_control.not_flagged')}
                          </Chip>
                          <span className="ml-3">
                            {auditResult.highest_category} / {auditResult.highest_score.toFixed(3)}
                          </span>
                        </div>
                      </div>
                    ) : null}
                  </>
                )}

                {activeTab === 'scope' && (
                  <>
                    <NativeSwitch
                      isSelected={form.all_groups}
                      label={t('risk_control.all_groups')}
                      onChange={(v) => set('all_groups', v)}
                    />
                    {!form.all_groups && (
                      <div>
                        <Label className="mb-1 block">{t('risk_control.group_ids')}</Label>
                        <div className="grid grid-cols-2 sm:grid-cols-3 gap-2">
                          {groups.map((g) => (
                            <label key={g.id} className="flex items-center gap-2 text-sm cursor-pointer">
                              <input
                                checked={form.group_ids.includes(g.id)}
                                type="checkbox"
                                onChange={(e) => {
                                  set('group_ids', e.target.checked
                                    ? [...form.group_ids, g.id]
                                    : form.group_ids.filter((id) => id !== g.id));
                                }}
                              />
                              {g.name}
                            </label>
                          ))}
                        </div>
                      </div>
                    )}
                    {selectField('risk_control.model_filter', form.model_filter_type, filterTypeOptions, (v) => set('model_filter_type', v))}
                    {form.model_filter_type !== 'all' && (
                      <HeroTextField fullWidth>
                        <Label>{t('risk_control.model_filter_models')}</Label>
                        <TextArea
                          placeholder={t('risk_control.model_filter_placeholder')}
                          rows={4}
                          value={form.model_filter_models}
                          onChange={(e) => set('model_filter_models', e.target.value)}
                        />
                      </HeroTextField>
                    )}
                  </>
                )}

                {activeTab === 'runtime' && (
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                    {numberField('risk_control.worker_count', 'worker_count')}
                    {numberField('risk_control.queue_size', 'queue_size')}
                    {numberField('risk_control.timeout_ms', 'timeout_ms')}
                    {numberField('risk_control.retry_count', 'retry_count')}
                    {numberField('risk_control.sample_rate', 'sample_rate', 'risk_control.sample_rate_hint')}
                  </div>
                )}

                {activeTab === 'response' && (
                  <>
                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                      {numberField('risk_control.block_status', 'block_status')}
                      {numberField('risk_control.ban_threshold', 'ban_threshold')}
                      {numberField('risk_control.violation_window_hours', 'violation_window_hours')}
                    </div>
                    <HeroTextField fullWidth>
                      <Label>{t('risk_control.block_message')}</Label>
                      <TextArea
                        rows={2}
                        value={form.block_message}
                        onChange={(e) => set('block_message', e.target.value)}
                      />
                    </HeroTextField>
                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                      <NativeSwitch
                        isSelected={form.email_on_hit}
                        label={t('risk_control.email_on_hit')}
                        onChange={(v) => set('email_on_hit', v)}
                      />
                      <NativeSwitch
                        isSelected={form.auto_ban_enabled}
                        label={t('risk_control.auto_ban')}
                        onChange={(v) => set('auto_ban_enabled', v)}
                      />
                      <NativeSwitch
                        isSelected={form.pre_hash_check_enabled}
                        label={t('risk_control.pre_hash_check')}
                        onChange={(v) => set('pre_hash_check_enabled', v)}
                      />
                      <NativeSwitch
                        isSelected={form.record_non_hits}
                        label={t('risk_control.record_non_hits')}
                        onChange={(v) => set('record_non_hits', v)}
                      />
                    </div>
                  </>
                )}

                {activeTab === 'thresholds' && (
                  <>
                    <div className="text-sm text-default-500">{t('risk_control.thresholds_hint')}</div>
                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                      {(config?.categories ?? []).map((category) => (
                        <HeroTextField key={category} fullWidth>
                          <Label className="font-mono text-xs">{category}</Label>
                          <Input
                            max={1}
                            min={0}
                            step={0.01}
                            type="number"
                            value={form.thresholds[category] ?? ''}
                            onChange={(e) => set('thresholds', { ...form.thresholds, [category]: e.target.value })}
                          />
                        </HeroTextField>
                      ))}
                    </div>
                  </>
                )}

                {activeTab === 'keywords' && (
                  <>
                    {selectField('risk_control.keyword_blocking_mode', form.keyword_blocking_mode, keywordModeOptions, (v) => set('keyword_blocking_mode', v))}
                    <HeroTextField fullWidth>
                      <Label>{t('risk_control.blocked_keywords')}</Label>
                      <TextArea
                        placeholder={t('risk_control.blocked_keywords_placeholder')}
                        rows={8}
                        value={form.blocked_keywords}
                        onChange={(e) => set('blocked_keywords', e.target.value)}
                      />
                    </HeroTextField>
                  </>
                )}

                {activeTab === 'retention' && (
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                    {numberField('risk_control.hit_retention_days', 'hit_retention_days')}
                    {numberField('risk_control.non_hit_retention_days', 'non_hit_retention_days')}
                  </div>
                )}
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="ghost" onPress={onClose}>{t('common.cancel')}</Button>
              <Button
                isDisabled={saveMutation.isPending}
                variant="primary"
                onPress={() => saveMutation.mutate(formToPayload(form))}
              >
                {saveMutation.isPending ? <Spinner size="sm" /> : null}
                {t('common.save')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
