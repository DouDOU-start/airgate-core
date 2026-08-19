import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  Button,
  Checkbox,
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
import { PlatformIcon } from '../../../shared/components/PlatformIcon';
import { FolderTree, KeyRound, Save, SlidersHorizontal, UsersRound } from 'lucide-react';
import { groupsApi } from '../../../shared/api/groups';
import { proxiesApi } from '../../../shared/api/proxies';
import { queryKeys } from '../../../shared/queryKeys';
import { FETCH_ALL_PARAMS } from '../../../shared/constants';
import { OAuthAuthPanel, supportsInteractiveOAuth } from './OAuthAuthPanel';
import { CodexImportPanel } from './CodexImportPanel';
import { AntigravityImportPanel } from './AntigravityImportPanel';
import type {
  AccountPlatform,
  AccountResp,
  AccountType,
  CreateAccountReq,
  StartOAuthReq,
  UpdateAccountReq,
} from '../../../shared/types';

const PLATFORM_OPTIONS: Array<{ id: AccountPlatform; label: string }> = [
  { id: 'codex', label: 'Codex' },
  { id: 'claude', label: 'Claude' },
  { id: 'antigravity', label: 'Antigravity' },
  { id: 'kimi', label: 'Kimi' },
  { id: 'xai', label: 'xAI / Grok' },
  { id: 'cursor', label: 'Cursor' },
  { id: 'gemini', label: 'Gemini' },
  { id: 'aistudio', label: 'AI Studio' },
  { id: 'vertex', label: 'Vertex' },
];

/** 账号类型仅 OAuth / API Key；RT 导入属于 OAuth。 */
const TYPE_OPTIONS: Array<{ id: AccountType; label: string }> = [
  { id: 'oauth', label: 'OAuth' },
  { id: 'api_key', label: 'API Key' },
];

type OAuthMethod = 'authorize' | 'paste';

function platformsWithAPIKey(platform: AccountPlatform): boolean {
  return [
    'codex',
    'claude',
    'kimi',
    'xai',
    'gemini',
    'aistudio',
    'vertex',
  ].includes(platform);
}

function platformsWithOAuth(platform: AccountPlatform): boolean {
  return !['gemini', 'aistudio', 'vertex'].includes(platform);
}

function defaultTypeForPlatform(platform: AccountPlatform): AccountType {
  return platformsWithOAuth(platform) ? 'oauth' : 'api_key';
}

function buildCredentials(fields: {
  type: AccountType;
  platform: AccountPlatform;
  email: string;
  access_token: string;
  refresh_token: string;
  api_key: string;
  service_account_json: string;
  extra_json: string;
}): Record<string, string> | null {
  const credentials: Record<string, string> = {};

  if (fields.email.trim()) credentials.email = fields.email.trim();

  if (fields.type === 'api_key') {
    if (fields.platform === 'vertex') {
      if (fields.service_account_json.trim()) {
        credentials.service_account_json = fields.service_account_json.trim();
      }
    } else if (fields.api_key.trim()) {
      credentials.api_key = fields.api_key.trim();
    }
  } else {
    if (fields.access_token.trim()) credentials.access_token = fields.access_token.trim();
    if (fields.refresh_token.trim()) credentials.refresh_token = fields.refresh_token.trim();
  }

  if (fields.extra_json.trim()) {
    try {
      const parsed = JSON.parse(fields.extra_json) as unknown;
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        return null;
      }
      for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
        if (value == null) continue;
        credentials[key] = typeof value === 'string' ? value : String(value);
      }
    } catch {
      return null;
    }
  }

  return credentials;
}

function normalizeType(raw?: string): AccountType {
  const v = (raw || '').toLowerCase();
  if (v === 'api_key' || v === 'apikey' || v === 'service_account') return 'api_key';
  return 'oauth';
}

export function AccountFormModal({
  open,
  title,
  account,
  onClose,
  onSubmit,
  onOAuthSuccess,
  loading,
}: {
  open: boolean;
  title: string;
  account?: AccountResp;
  onClose: () => void;
  onSubmit: (data: CreateAccountReq | UpdateAccountReq) => void;
  /** 交互式 OAuth 在服务端建号成功后回调（刷新列表等）。 */
  onOAuthSuccess?: () => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const isEdit = !!account;

  const buildForm = () => {
    const platform = (account?.platform || 'codex') as AccountPlatform;
    return {
      name: account?.name ?? '',
      platform,
      type: normalizeType(account?.type) || defaultTypeForPlatform(platform),
      email: account?.email ?? '',
      access_token: '',
      refresh_token: '',
      api_key: '',
      service_account_json: '',
      extra_json: '',
      priority: String(account?.priority ?? 50),
      weight: String(account?.weight ?? 10),
      max_concurrency: String(account?.max_concurrency ?? 10),
      rate_multiplier: String(account?.rate_multiplier ?? 1),
      proxy_id: account?.proxy_id != null ? String(account.proxy_id) : '',
      group_ids: account?.group_ids ?? [],
    };
  };

  const [form, setForm] = useState(buildForm);
  const [credentialsError, setCredentialsError] = useState('');
  /**
   * 新增 OAuth 方式：
   * - Codex：走专用授权 / RT / Session 面板
   * - Antigravity：走专用授权 / RT 面板
   * - 其它平台：授权或粘贴完整凭证
   */
  const [oauthMethod, setOauthMethod] = useState<OAuthMethod>('authorize');

  useEffect(() => {
    if (!open) {
      setForm(buildForm());
      setCredentialsError('');
      setOauthMethod('authorize');
    }
  }, [open, account]);

  const { data: proxiesData } = useQuery({
    queryKey: queryKeys.proxies('form-options'),
    queryFn: () => proxiesApi.list({ page: 1, page_size: 100 }),
    enabled: open,
    staleTime: 30_000,
  });

  const { data: groupsData } = useQuery({
    queryKey: queryKeys.groupsAll(),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
    enabled: open,
    staleTime: 60_000,
  });
  const groups = groupsData?.list ?? [];

  const proxyOptions = useMemo(() => {
    const list = (proxiesData?.list ?? []).map((p) => ({
      id: String(p.id),
      label: `${p.name} (${p.address}:${p.port})`,
    }));
    return [{ id: '', label: t('accounts.proxy_none') }, ...list];
  }, [proxiesData?.list, t]);

  const toggleGroup = (id: number, selected: boolean) => {
    setForm((prev) => ({
      ...prev,
      group_ids: selected
        ? [...new Set([...prev.group_ids, id])]
        : prev.group_ids.filter((g) => g !== id),
    }));
  };

  const typeOptions = useMemo(() => {
    return TYPE_OPTIONS.filter((opt) => {
      if (opt.id === 'oauth') return platformsWithOAuth(form.platform);
      if (opt.id === 'api_key') return platformsWithAPIKey(form.platform);
      return true;
    });
  }, [form.platform]);

  const selectedPlatformLabel =
    PLATFORM_OPTIONS.find((item) => item.id === form.platform)?.label ?? form.platform;
  const selectedTypeLabel =
    typeOptions.find((item) => item.id === form.type)?.label ?? form.type;
  const selectedProxyLabel =
    proxyOptions.find((item) => item.id === form.proxy_id)?.label ?? t('accounts.proxy_none');

  // 编辑 OAuth：只走重新授权，不提供手动改 token
  const canInteractiveReauth =
    isEdit && form.type === 'oauth' && supportsInteractiveOAuth(form.platform);

  const isCodexOAuthCreate = !isEdit && form.platform === 'codex' && form.type === 'oauth';
  const isCodexOAuthReauth = canInteractiveReauth && form.platform === 'codex';
  const showCodexOAuthPanel = isCodexOAuthCreate || isCodexOAuthReauth;

  const isAntigravityOAuthCreate =
    !isEdit && form.platform === 'antigravity' && form.type === 'oauth';
  const isAntigravityOAuthReauth =
    canInteractiveReauth && form.platform === 'antigravity';
  const showAntigravityOAuthPanel =
    isAntigravityOAuthCreate || isAntigravityOAuthReauth;

  const showOAuthAuthorize =
    form.type === 'oauth'
    && !['codex', 'antigravity'].includes(form.platform)
    && supportsInteractiveOAuth(form.platform)
    && (
      (!isEdit && oauthMethod === 'authorize')
      || canInteractiveReauth
    );

  // 新建可粘贴凭证；编辑仅 api_key 允许改密钥（OAuth 一律重新授权）
  const showManualCredentials =
    form.type === 'api_key'
    || (!isEdit && form.type === 'oauth' && !['codex', 'antigravity'].includes(form.platform) && (
      !supportsInteractiveOAuth(form.platform) || oauthMethod === 'paste'
    ));

  // 新建 + 交互式 OAuth 时名称可空（服务端用邮箱/自动名兜底）
  const isNameOptional =
    !isEdit && (showOAuthAuthorize || showCodexOAuthPanel || showAntigravityOAuthPanel);

  const typeHint = (() => {
    if (form.type === 'api_key') {
      if (form.platform === 'codex') return t('accounts.type_hint_codex_apikey');
      if (form.platform === 'claude') return t('accounts.type_hint_claude_apikey');
      if (form.platform === 'vertex') return t('accounts.type_hint_vertex');
      return t('accounts.type_hint_apikey');
    }
    if (form.platform === 'claude') return t('accounts.type_hint_claude_oauth');
    return '';
  })();

  const oauthStartOptions = useMemo((): StartOAuthReq => {
    const priority = Number(form.priority);
    const weight = Number(form.weight);
    const maxConcurrency = Number(form.max_concurrency);
    const rateMultiplier = Number(form.rate_multiplier);
    const proxyId = form.proxy_id ? Number(form.proxy_id) : undefined;
    return {
      name: form.name.trim() || undefined,
      priority: Number.isFinite(priority) ? priority : undefined,
      weight: Number.isFinite(weight) ? weight : undefined,
      max_concurrency: Number.isFinite(maxConcurrency) ? maxConcurrency : undefined,
      rate_multiplier: Number.isFinite(rateMultiplier) ? rateMultiplier : undefined,
      proxy_id: proxyId ?? null,
      group_ids: form.group_ids,
      // 编辑态带上目标账号：OAuth/导入完成后更新凭证而非新建
      account_id: isEdit && account?.id ? account.id : undefined,
    };
  }, [form, isEdit, account?.id]);

  const handleSubmit = () => {
    // 新建时的交互式 OAuth 在面板内完成；编辑态重新授权也在面板内写凭证，
    // 但编辑仍可提交名称/分组等非凭证字段。
    if (!isEdit && (showOAuthAuthorize || showCodexOAuthPanel || showAntigravityOAuthPanel)) return;

    if (!form.name.trim()) return;

    const credentials = buildCredentials({
      type: form.type,
      platform: form.platform,
      email: form.email,
      access_token: form.access_token,
      refresh_token: form.refresh_token,
      api_key: form.api_key,
      service_account_json: form.service_account_json,
      extra_json: form.extra_json,
    });
    if (credentials === null) {
      setCredentialsError(t('accounts.credentials_json_invalid'));
      return;
    }
    setCredentialsError('');

    const priority = Number(form.priority);
    const weight = Number(form.weight);
    const maxConcurrency = Number(form.max_concurrency);
    const rateMultiplier = Number(form.rate_multiplier);
    const proxyId = form.proxy_id ? Number(form.proxy_id) : null;

    if (isEdit) {
      const payload: UpdateAccountReq = {
        name: form.name.trim(),
        platform: form.platform,
        type: form.type,
        priority: Number.isFinite(priority) ? priority : undefined,
        weight: Number.isFinite(weight) ? weight : undefined,
        max_concurrency: Number.isFinite(maxConcurrency) ? maxConcurrency : undefined,
        rate_multiplier: Number.isFinite(rateMultiplier) ? rateMultiplier : undefined,
        proxy_id: proxyId,
        // 始终提交（含空数组），以便清空绑定
        group_ids: form.group_ids,
      };
      // OAuth 凭证只走重新授权面板；仅 api_key 编辑时允许更新密钥
      if (form.type === 'api_key') {
        const hasSecretUpdate =
          !!form.api_key.trim() ||
          !!form.service_account_json.trim() ||
          !!form.extra_json.trim();
        if (hasSecretUpdate) {
          payload.credentials = credentials;
        }
      }
      onSubmit(payload);
      return;
    }

    if (Object.keys(credentials).length === 0) {
      setCredentialsError(t('accounts.credentials_required'));
      return;
    }
    if (form.type === 'oauth' && form.platform === 'codex') {
      if (!credentials.refresh_token && !credentials.access_token) {
        setCredentialsError(t('accounts.credentials_codex_oauth_required'));
        return;
      }
    }
    if (form.type === 'api_key' && form.platform !== 'vertex' && !credentials.api_key) {
      setCredentialsError(t('accounts.credentials_apikey_required'));
      return;
    }
    if (form.type === 'api_key' && form.platform === 'vertex' && !credentials.service_account_json) {
      setCredentialsError(t('accounts.credentials_apikey_required'));
      return;
    }

    onSubmit({
      name: form.name.trim(),
      platform: form.platform,
      type: form.type,
      credentials,
      priority: Number.isFinite(priority) ? priority : 50,
      weight: Number.isFinite(weight) ? weight : 10,
      max_concurrency: Number.isFinite(maxConcurrency) ? maxConcurrency : 10,
      rate_multiplier: Number.isFinite(rateMultiplier) ? rateMultiplier : 1,
      proxy_id: proxyId,
      group_ids: form.group_ids,
    } satisfies CreateAccountReq);
  };

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  const methodTabs: Array<{ id: OAuthMethod; label: string }> = [
    { id: 'authorize', label: t('accounts.oauth_method_authorize') },
    { id: 'paste', label: t('accounts.oauth_method_paste') },
  ];

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="lg">
          <Modal.Dialog
            className="ag-elevation-modal ag-account-form-modal"
            style={{ maxWidth: '640px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header className="ag-account-form-modal__header">
              <Modal.Heading>{title}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body className="ag-account-form-modal__body">
              <div className="ag-account-form-modal__content">
                <section className="ag-account-form-section ag-account-form-section--identity">
                  <div className={isEdit ? 'ag-account-form-identity' : undefined}>
                    {isEdit ? (
                      <div className="ag-account-form-identity__icon" aria-hidden="true">
                        <PlatformIcon platform={form.platform} size={22} withBadge={false} />
                      </div>
                    ) : null}
                    <HeroTextField
                      fullWidth
                      isRequired={!isNameOptional}
                      className={isEdit ? 'ag-account-form-identity__field' : undefined}
                    >
                      <Label className={isEdit ? 'sr-only' : undefined}>
                        {isNameOptional ? t('accounts.name_optional') : t('accounts.name')}
                      </Label>
                      <div className="relative">
                        {!isEdit ? (
                          <UsersRound className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                        ) : null}
                        <Input
                          className={!isEdit ? 'pl-9' : undefined}
                          value={form.name}
                          onChange={(e) => setForm({ ...form, name: e.target.value })}
                          placeholder={isNameOptional ? t('accounts.name_placeholder') : undefined}
                          required={!isNameOptional}
                        />
                      </div>
                    </HeroTextField>
                    {isEdit ? (
                      <div
                        className="ag-account-form-identity__meta"
                        aria-label={`${t('accounts.platform')} ${selectedPlatformLabel}，${t('accounts.type')} ${selectedTypeLabel}`}
                      >
                        <span>{selectedPlatformLabel}</span>
                        <i aria-hidden="true" />
                        <span>{selectedTypeLabel}</span>
                      </div>
                    ) : null}
                  </div>

                  {!isEdit ? (
                    <div className="ag-account-form-platform-grid">
                  <div>
                    <Label className="mb-1.5 block">{t('accounts.platform')}</Label>
                    <Select
                      aria-label={t('accounts.platform')}
                      fullWidth
                      selectedKey={form.platform}
                      isDisabled={isEdit}
                      onSelectionChange={(key) => {
                        if (key == null) return;
                        const platform = String(key) as AccountPlatform;
                        const nextType = platformsWithOAuth(platform)
                          ? (form.type === 'api_key' && platformsWithAPIKey(platform)
                            ? form.type
                            : defaultTypeForPlatform(platform))
                          : 'api_key';
                        setForm({ ...form, platform, type: nextType });
                        if (supportsInteractiveOAuth(platform) && nextType === 'oauth') {
                          setOauthMethod('authorize');
                        }
                      }}
                    >
                      <Select.Trigger>
                        <Select.Value>
                          <span className="inline-flex items-center gap-2">
                            <PlatformIcon platform={form.platform} size={14} withBadge={false} />
                            {selectedPlatformLabel}
                          </span>
                        </Select.Value>
                        <Select.Indicator />
                      </Select.Trigger>
                      <Select.Popover>
                        <ListBox items={PLATFORM_OPTIONS}>
                          {(item) => (
                            <ListBox.Item id={item.id} textValue={item.label}>
                              <div className="flex items-center gap-2">
                                <PlatformIcon platform={item.id} size={14} withBadge={false} />
                                <span>{item.label}</span>
                              </div>
                            </ListBox.Item>
                          )}
                        </ListBox>
                      </Select.Popover>
                    </Select>
                  </div>
                  <div>
                    <Label className="mb-1.5 block">{t('accounts.type')}</Label>
                    <Select
                      aria-label={t('accounts.type')}
                      fullWidth
                      selectedKey={form.type}
                      isDisabled={isEdit}
                      onSelectionChange={(key) => {
                        if (key == null) return;
                        const nextType = String(key) as AccountType;
                        setForm({ ...form, type: nextType });
                        if (nextType === 'oauth' && supportsInteractiveOAuth(form.platform)) {
                          setOauthMethod('authorize');
                        }
                      }}
                    >
                      <Select.Trigger>
                        <Select.Value>{selectedTypeLabel}</Select.Value>
                        <Select.Indicator />
                      </Select.Trigger>
                      <Select.Popover>
                        <ListBox items={typeOptions}>
                          {(item) => (
                            <ListBox.Item id={item.id} textValue={item.label}>
                              {item.label}
                            </ListBox.Item>
                          )}
                        </ListBox>
                      </Select.Popover>
                    </Select>
                    {typeHint ? (
                      <Description className="mt-1 text-xs text-text-tertiary">
                        {typeHint}
                      </Description>
                    ) : null}
                  </div>
                    </div>
                  ) : null}
                </section>

                <section className="ag-account-form-section ag-account-form-section--credentials">
                  <div className="ag-account-form-section__header">
                    <div className="ag-account-form-section__heading">
                      <span className="ag-account-form-section__icon" aria-hidden="true">
                        <KeyRound size={16} />
                      </span>
                      <div className="ag-account-form-section__heading-copy">
                        <h3>{t('accounts.credentials_section_title')}</h3>
                        {isEdit && form.type === 'api_key' ? (
                          <p>{t('accounts.credentials_edit_hint')}</p>
                        ) : null}
                      </div>
                    </div>

                    {!isEdit
                      && form.type === 'oauth'
                      && !['codex', 'antigravity'].includes(form.platform)
                      && supportsInteractiveOAuth(form.platform) ? (
                      <div className="ag-account-form-segmented">
                        {methodTabs.map((tab) => (
                          <button
                            key={tab.id}
                            type="button"
                            data-active={oauthMethod === tab.id}
                            onClick={() => setOauthMethod(tab.id)}
                          >
                            {tab.label}
                          </button>
                        ))}
                      </div>
                    ) : null}
                  </div>

                  <div className="ag-account-form-section__content">
                    {showCodexOAuthPanel ? (
                      <CodexImportPanel
                        startOptions={oauthStartOptions}
                        onSuccess={() => {
                          onOAuthSuccess?.();
                          onClose();
                        }}
                      />
                    ) : null}

                    {showAntigravityOAuthPanel ? (
                      <AntigravityImportPanel
                        startOptions={oauthStartOptions}
                        onSuccess={() => {
                          onOAuthSuccess?.();
                          onClose();
                        }}
                      />
                    ) : null}

                    {showOAuthAuthorize ? (
                      <OAuthAuthPanel
                        platform={form.platform}
                        startOptions={oauthStartOptions}
                        onSuccess={() => {
                          onOAuthSuccess?.();
                          onClose();
                        }}
                      />
                    ) : null}

                    {showManualCredentials ? (
                      <div className="ag-account-form-credentials-grid">
                        {form.type === 'oauth' ? (
                          <>
                            <HeroTextField fullWidth className="ag-account-form-credential-field">
                              <Label>{t('accounts.email')}</Label>
                              <Input
                                value={form.email}
                                onChange={(e) => setForm({ ...form, email: e.target.value })}
                                placeholder="user@example.com"
                                autoComplete="off"
                              />
                            </HeroTextField>
                            <HeroTextField fullWidth className="ag-account-form-credential-field">
                              <Label>
                                {form.platform === 'codex'
                                  ? t('accounts.refresh_token')
                                  : t('accounts.access_token')}
                              </Label>
                              {form.platform === 'codex' ? (
                                <Input
                                  type="password"
                                  value={form.refresh_token}
                                  onChange={(e) => setForm({ ...form, refresh_token: e.target.value })}
                                  autoComplete="new-password"
                                  placeholder={t('accounts.refresh_token_placeholder')}
                                />
                              ) : (
                                <Input
                                  type="password"
                                  value={form.access_token}
                                  onChange={(e) => setForm({ ...form, access_token: e.target.value })}
                                  autoComplete="new-password"
                                />
                              )}
                            </HeroTextField>
                            {form.platform === 'codex' ? (
                              <HeroTextField fullWidth className="ag-account-form-credential-field">
                                <Label>{t('accounts.access_token_optional')}</Label>
                                <Input
                                  type="password"
                                  value={form.access_token}
                                  onChange={(e) => setForm({ ...form, access_token: e.target.value })}
                                  autoComplete="new-password"
                                />
                              </HeroTextField>
                            ) : (
                              <HeroTextField fullWidth className="ag-account-form-credential-field">
                                <Label>{t('accounts.refresh_token')}</Label>
                                <Input
                                  type="password"
                                  value={form.refresh_token}
                                  onChange={(e) => setForm({ ...form, refresh_token: e.target.value })}
                                  autoComplete="new-password"
                                />
                              </HeroTextField>
                            )}
                          </>
                        ) : form.platform === 'vertex' ? (
                          <HeroTextField fullWidth className="ag-account-form-credential-field">
                            <Label>{t('accounts.service_account_json')}</Label>
                            <TextArea
                              rows={5}
                              value={form.service_account_json}
                              onChange={(e) => setForm({ ...form, service_account_json: e.target.value })}
                              placeholder='{"type":"service_account",...}'
                            />
                          </HeroTextField>
                        ) : (
                          <HeroTextField fullWidth className="ag-account-form-credential-field">
                            <Label>API Key</Label>
                            <Input
                              type="password"
                              value={form.api_key}
                              onChange={(e) => setForm({ ...form, api_key: e.target.value })}
                              autoComplete="new-password"
                              placeholder={form.platform === 'claude' ? 'sk-ant-...' : undefined}
                            />
                          </HeroTextField>
                        )}

                        <HeroTextField
                          fullWidth
                          className="ag-account-form-credential-field ag-account-form-credential-field--wide"
                        >
                          <Label>{t('accounts.credentials_json')}</Label>
                          <TextArea
                            rows={3}
                            value={form.extra_json}
                            onChange={(e) => setForm({ ...form, extra_json: e.target.value })}
                            placeholder='{"id_token":"...","account_id":"..."}'
                          />
                          <Description className="mt-1 text-xs text-text-tertiary">
                            {t('accounts.credentials_json_hint')}
                          </Description>
                        </HeroTextField>
                        {credentialsError ? (
                          <div className="ag-account-form-credentials-error text-xs text-danger">
                            {credentialsError}
                          </div>
                        ) : null}
                      </div>
                    ) : null}
                  </div>
                </section>

                <section className="ag-account-form-section ag-account-form-section--scheduling">
                  <div className="ag-account-form-section__header">
                    <div className="ag-account-form-section__heading">
                      <span className="ag-account-form-section__icon" aria-hidden="true">
                        <SlidersHorizontal size={16} />
                      </span>
                      <div className="ag-account-form-section__heading-copy">
                        <h3>{t('accounts.scheduling_section_title')}</h3>
                      </div>
                    </div>
                  </div>

                  <div className="ag-account-form-metric-grid">
                    <HeroTextField fullWidth>
                      <Label>{t('accounts.priority')}</Label>
                      <Input
                        inputMode="numeric"
                        value={form.priority}
                        onChange={(e) => setForm({ ...form, priority: e.target.value })}
                      />
                    </HeroTextField>
                    <HeroTextField fullWidth>
                      <Label>{t('accounts.weight')}</Label>
                      <Input
                        inputMode="numeric"
                        value={form.weight}
                        onChange={(e) => setForm({ ...form, weight: e.target.value })}
                      />
                    </HeroTextField>
                    <HeroTextField fullWidth>
                      <Label>{t('accounts.max_concurrency')}</Label>
                      <Input
                        inputMode="numeric"
                        value={form.max_concurrency}
                        onChange={(e) => setForm({ ...form, max_concurrency: e.target.value })}
                      />
                    </HeroTextField>
                    <HeroTextField fullWidth>
                      <Label>{t('accounts.rate_multiplier')}</Label>
                      <Input
                        inputMode="decimal"
                        value={form.rate_multiplier}
                        onChange={(e) => setForm({ ...form, rate_multiplier: e.target.value })}
                      />
                    </HeroTextField>
                  </div>

                  <div className="ag-account-form-proxy-field">
                    <Label className="mb-1.5 block">{t('accounts.proxy')}</Label>
                    <Select
                      aria-label={t('accounts.proxy')}
                      fullWidth
                      selectedKey={form.proxy_id}
                      onSelectionChange={(key) => {
                        setForm({ ...form, proxy_id: key == null ? '' : String(key) });
                      }}
                    >
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
                  </div>
                </section>

                <section className="ag-account-form-section ag-account-form-section--groups">
                  <div className="ag-account-form-section__header">
                    <div className="ag-account-form-section__heading">
                      <span className="ag-account-form-section__icon" aria-hidden="true">
                        <FolderTree size={16} />
                      </span>
                      <div className="ag-account-form-section__heading-copy">
                        <h3>{t('accounts.groups')}</h3>
                      </div>
                    </div>
                  </div>
                  {groups.length === 0 ? (
                    <p className="ag-account-form-groups-empty">{t('accounts.groups_empty')}</p>
                  ) : (
                    <div className="ag-account-form-group-list">
                      {groups.map((group) => (
                        <div
                          key={group.id}
                          className="ag-account-form-group-item"
                          data-selected={form.group_ids.includes(group.id)}
                        >
                          <Checkbox
                            isSelected={form.group_ids.includes(group.id)}
                            onChange={(selected) => toggleGroup(group.id, selected)}
                          >
                            <Checkbox.Control>
                              <Checkbox.Indicator />
                            </Checkbox.Control>
                            <span title={group.name}>{group.name}</span>
                          </Checkbox>
                        </div>
                      ))}
                    </div>
                  )}
                </section>
              </div>
            </Modal.Body>
            <Modal.Footer className="ag-account-form-modal__footer">
              <Button variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              {!isEdit && (
                showOAuthAuthorize
                || showCodexOAuthPanel
                || showAntigravityOAuthPanel
              ) ? null : (
                <Button variant="primary" isDisabled={loading} onPress={handleSubmit}>
                  {loading ? <Spinner size="sm" /> : null}
                  {!loading && isEdit ? <Save size={15} /> : null}
                  {isEdit ? t('common.save') : t('common.create')}
                </Button>
              )}
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
