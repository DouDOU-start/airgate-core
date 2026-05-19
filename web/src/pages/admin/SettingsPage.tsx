import { type FormEvent, useState, useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Alert, AlertDialog, Button, Card, Form, Input, Label, Modal, Spinner, Tabs, TextArea, useOverlayState } from '@heroui/react';
import { DialogTriggerShim } from '../../shared/components/DialogTriggerShim';
import { settingsApi } from '../../shared/api/settings';
import { adminApiKeyApi, type AdminAPIKeyResp } from '../../shared/api/adminApiKey';
import { defaultLogoUrl } from '../../app/providers/SiteSettingsProvider';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { useClipboard } from '../../shared/hooks/useClipboard';
import { queryKeys } from '../../shared/queryKeys';
import { useToast } from '../../shared/ui';
import {
  Save, Loader2, Globe, Mail, MailSearch, Send, Upload, X, RotateCcw,
  ShieldCheck, Copy, Trash2, KeyRound, Zap, Download, Database,
} from 'lucide-react';
import type { SettingItem, TestSMTPReq } from '../../shared/types';
import { SystemUpdatePanel } from './SystemUpdatePanel';
import { NativeSwitch } from '../../shared/components/NativeSwitch';
import { CommonModal } from '../../shared/components/CommonModal';

// ==================== 设置 key 定义 ====================

const SITE_KEYS = [
  'site_name', 'site_subtitle', 'site_logo', 'api_base_url',
  'contact_info', 'doc_url',
] as const;

const REG_KEYS = [
  'registration_enabled', 'email_verify_enabled',
  'registration_email_suffix_whitelist',
] as const;

const DEFAULT_KEYS = [
  'default_balance', 'default_concurrency',
] as const;

const SMTP_KEYS = [
  'smtp_host', 'smtp_port', 'smtp_username', 'smtp_password',
  'smtp_from_email', 'smtp_from_name', 'smtp_use_tls',
  'email_template_subject', 'email_template_body',
  'balance_alert_email_subject', 'balance_alert_email_body',
] as const;

const STORAGE_KEYS = [
  's3_endpoint', 's3_bucket', 's3_access_key', 's3_secret_key',
  's3_region', 's3_use_ssl', 's3_public_base_url',
  's3_presign_ttl_minutes', 's3_path_prefix', 'local_storage_dir',
] as const;

// OpenClaw 一键接入相关 setting key。所有 key 统一加 "openclaw." 前缀，便于在 Setting 表中识别。
// 默认值（DEFAULT_OPENCLAW_*）在后端 internal/app/openclaw/defaults.go 中维护了同构的一份，
// 这里只负责前端展示 / 回填。keep in sync。
const OPENCLAW_KEYS = [
  'openclaw.enabled',
  'openclaw.provider_name',
  'openclaw.base_url',
  'openclaw.models_preset',
  'openclaw.memory_search_enabled',
  'openclaw.memory_search_model',
] as const;

const DEFAULT_OPENCLAW_PROVIDER_NAME = 'airgate';
const DEFAULT_OPENCLAW_MEMORY_MODEL = 'text-embedding-3-small';
const DEFAULT_OPENCLAW_MODELS_PRESET = `[
  {
    "id": "gpt-5.4",
    "label": "GPT-5.4 (推荐)",
    "api": "openai-responses",
    "reasoning": true,
    "input": ["text", "image"]
  },
  {
    "id": "claude-sonnet-4-6",
    "label": "Claude Sonnet 4.6",
    "api": "anthropic-messages",
    "reasoning": true,
    "input": ["text", "image"]
  },
  {
    "id": "claude-opus-4-6",
    "label": "Claude Opus 4.6",
    "api": "anthropic-messages",
    "reasoning": true,
    "input": ["text", "image"]
  }
]`;

const DEFAULT_EMAIL_SUBJECT = '{{site_name}} - 邮箱验证码';
const DEFAULT_EMAIL_BODY = `<div style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 420px; margin: 0 auto; background: #ffffff; border-radius: 8px; border: 1px solid #e5e7eb;">
  <div style="padding: 32px 28px;">
    <div style="font-size: 16px; font-weight: 600; color: #111; margin-bottom: 20px;">{{site_name}}</div>
    <p style="color: #555; font-size: 14px; line-height: 1.6; margin: 0 0 24px;">您好，您正在注册账户，请使用以下验证码完成操作：</p>
    <div style="background: #f7f8fa; border: 1px solid #eef0f3; border-radius: 8px; padding: 20px; text-align: center; margin-bottom: 24px;">
      <span style="font-size: 32px; font-weight: 700; letter-spacing: 10px; color: #111;">{{code}}</span>
    </div>
    <p style="color: #999; font-size: 12px; line-height: 1.6; margin: 0;">验证码 10 分钟内有效，请勿泄露给他人。如非本人操作，请忽略此邮件。</p>
  </div>
  <div style="border-top: 1px solid #f0f0f0; padding: 14px 28px;">
    <p style="color: #c0c0c0; font-size: 11px; margin: 0; text-align: center;">此邮件由 {{site_name}} 系统自动发送，请勿直接回复</p>
  </div>
</div>`;

const DEFAULT_BALANCE_ALERT_SUBJECT = '{{site_name}} - 余额预警';
const DEFAULT_BALANCE_ALERT_BODY = `<div style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 420px; margin: 0 auto; background: #ffffff; border-radius: 8px; border: 1px solid #e5e7eb;">
  <div style="padding: 32px 28px;">
    <div style="font-size: 16px; font-weight: 600; color: #111; margin-bottom: 20px;">{{site_name}}</div>
    <p style="color: #555; font-size: 14px; line-height: 1.6; margin: 0 0 16px;">您的账户余额已低于预警阈值：</p>
    <div style="background: #fef3c7; border: 1px solid #fde68a; border-radius: 8px; padding: 16px; margin-bottom: 20px;">
      <div style="display: flex; justify-content: space-between; margin-bottom: 8px;">
        <span style="color: #92400e; font-size: 13px;">当前余额</span>
        <span style="color: #92400e; font-size: 16px; font-weight: 700;">{{balance}}</span>
      </div>
      <div style="display: flex; justify-content: space-between;">
        <span style="color: #92400e; font-size: 13px;">预警阈值</span>
        <span style="color: #92400e; font-size: 13px;">{{threshold}}</span>
      </div>
    </div>
    <p style="color: #999; font-size: 12px; line-height: 1.6; margin: 0;">请及时充值以免影响正常使用。余额回到阈值以上后，预警将自动重置。</p>
  </div>
  <div style="border-top: 1px solid #f0f0f0; padding: 14px 28px;">
    <p style="color: #c0c0c0; font-size: 11px; margin: 0; text-align: center;">此邮件由 {{site_name}} 系统自动发送</p>
  </div>
</div>`;

// ==================== Tab 定义 ====================

type TabKey = 'site' | 'security' | 'smtp' | 'storage' | 'openclaw' | 'system';

const TABS: { key: TabKey; labelKey: string; icon: typeof Globe }[] = [
  { key: 'site', labelKey: 'settings.tab_site', icon: Globe },
  { key: 'security', labelKey: 'settings.tab_security', icon: ShieldCheck },
  { key: 'smtp', labelKey: 'settings.tab_smtp', icon: Mail },
  { key: 'storage', labelKey: 'settings.tab_storage', icon: Database },
  { key: 'openclaw', labelKey: 'settings.tab_openclaw', icon: Zap },
  { key: 'system', labelKey: 'settings.tab_system', icon: Download },
];

// system tab 通过独立的 upgrade API 管理，不走通用 settings save 流程。
type SaveTabKey = Exclude<TabKey, 'security' | 'system'>;

const TAB_GROUP: Record<SaveTabKey, string> = {
  site: 'site',
  smtp: 'smtp',
  storage: 'storage',
  openclaw: 'openclaw',
};

const TAB_KEYS: Record<SaveTabKey, readonly string[]> = {
  site: SITE_KEYS,
  smtp: SMTP_KEYS,
  storage: STORAGE_KEYS,
  openclaw: OPENCLAW_KEYS,
};

// ==================== Component ====================

export default function SettingsPage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();

  const [activeTab, setActiveTab] = useState<TabKey>('site');
  const [values, setValues] = useState<Record<string, string>>({});
  const [hasChanges, setHasChanges] = useState(false);
  const [emailTplType, setEmailTplType] = useState<'verify' | 'balance_alert'>('verify');
  const [isEmailPreviewOpen, setEmailPreviewOpen] = useState(false);
  const [isSmtpTestOpen, setSmtpTestOpen] = useState(false);

  // 获取所有设置
  const { data: settings, isLoading } = useQuery({
    queryKey: queryKeys.settings(),
    queryFn: () => settingsApi.list(),
  });

  // 初始化
  useEffect(() => {
    if (settings) {
      const map: Record<string, string> = {};
      for (const s of settings) {
        map[s.key] = s.value;
      }
      setValues(map);
      setHasChanges(false);
    }
  }, [settings]);

  // 保存
  const saveMutation = useCrudMutation({
    mutationFn: (items: SettingItem[]) => settingsApi.update({ settings: items }),
    successMessage: t('settings.save_success'),
    queryKey: queryKeys.settings(),
    onSuccess: () => {
      setHasChanges(false);
      queryClient.invalidateQueries({ queryKey: ['site-settings'] });
    },
  });

  // SMTP 测试
  const smtpTestMutation = useMutation({
    mutationFn: (data: TestSMTPReq) => settingsApi.testSMTP(data),
    onSuccess: () => {
      setSmtpTestOpen(false);
      toast('success', t('settings.smtp_test_success'));
    },
    onError: (err: Error) => toast('error', err.message),
  });

  function set(key: string, value: string) {
    setValues((prev) => ({ ...prev, [key]: value }));
    setHasChanges(true);
  }

  function val(key: string): string {
    return values[key] ?? '';
  }

  function boolVal(key: string): boolean {
    return val(key) === 'true';
  }

  function buildSaveItems(): SettingItem[] {
    if (activeTab === 'system') return [];
    if (activeTab === 'security') {
      return [
        ...REG_KEYS.map((key) => ({
          key,
          value: values[key] ?? '',
          group: 'registration',
        })),
        ...DEFAULT_KEYS.map((key) => ({
          key,
          value: values[key] ?? '',
          group: 'defaults',
        })),
      ];
    }

    const tab = activeTab as SaveTabKey;
    const group = TAB_GROUP[tab];
    const keys = TAB_KEYS[tab];
    return keys.map((key) => ({
      key,
      value: values[key] ?? '',
      group,
    }));
  }

  function handleSave() {
    const items = buildSaveItems();
    if (items.length === 0) return;
    saveMutation.mutate(items);
  }

  function handleTestSMTP() {
    setSmtpTestOpen(true);
  }

  function submitSmtpTest(testTo: string) {
    smtpTestMutation.mutate({
      host: val('smtp_host'),
      port: Number(val('smtp_port')) || 587,
      username: val('smtp_username'),
      password: val('smtp_password'),
      use_tls: boolVal('smtp_use_tls'),
      from: val('smtp_from_email'),
      to: testTo,
    });
  }

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-16">
        <Loader2 className="w-5 h-5 animate-spin text-primary" />
        <span className="ml-2 text-sm text-text-tertiary">{t('common.loading')}</span>
      </div>
    );
  }

  function renderSaveAction(left?: React.ReactNode) {
    if (activeTab === 'system') return null;
    return (
      <div className="ag-settings-card-footer">
        {left ? <div className="ag-settings-card-footer-left">{left}</div> : null}
        <Button
          onPress={handleSave}
          isDisabled={!hasChanges || saveMutation.isPending}
          aria-busy={saveMutation.isPending}
        >
          <Save className="w-4 h-4" />
          {t('common.save')}
        </Button>
      </div>
    );
  }

  const saveAction = renderSaveAction();
  const smtpSaveAction = renderSaveAction(
    <>
      <Button
        size="sm"
        variant="ghost"
        onPress={() => setEmailPreviewOpen(true)}
      >
        <MailSearch className="w-3.5 h-3.5" />
        {t('settings.template_preview')}
      </Button>
      <Button
        size="sm"
        variant="ghost"
        onPress={() => {
          if (emailTplType === 'verify') {
            set('email_template_subject', DEFAULT_EMAIL_SUBJECT);
            set('email_template_body', DEFAULT_EMAIL_BODY);
            return;
          }
          set('balance_alert_email_subject', DEFAULT_BALANCE_ALERT_SUBJECT);
          set('balance_alert_email_body', DEFAULT_BALANCE_ALERT_BODY);
        }}
      >
        <RotateCcw className="w-3.5 h-3.5" />
        {t('settings.template_reset')}
      </Button>
    </>,
  );

  return (
    <div className="max-w-6xl mx-auto w-full px-4 sm:px-6 lg:px-8 py-6 md:py-8 flex flex-col gap-6 min-h-screen">
      <div className="mx-auto w-full max-w-full overflow-x-auto hide-scrollbar pb-1">
        <Tabs
          className="ag-page-tabs ag-settings-tabs whitespace-nowrap"
          selectedKey={activeTab}
          onSelectionChange={(key) => setActiveTab(key as TabKey)}
        >
          <Tabs.List>
            {TABS.map((tab, index) => {
              const Icon = tab.icon;
              return (
                <Tabs.Tab key={tab.key} id={tab.key}>
                  {index > 0 ? <Tabs.Separator /> : null}
                  <Tabs.Indicator />
                  <Icon className="w-4 h-4" />
                  <span>{t(tab.labelKey)}</span>
                </Tabs.Tab>
              );
            })}
          </Tabs.List>
        </Tabs>
      </div>

      {/* Content */}
      <div className="flex-1 w-full flex flex-col gap-6">
        {activeTab === 'site' && (
          <Card>
            <Card.Header>
              <Card.Title>{t('settings.site_branding')}</Card.Title>
            </Card.Header>
            <Card.Content>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                <Field label={t('settings.site_name')} hint={t('settings.site_name_hint')}>
                  <Input value={val('site_name')} onChange={(e) => set('site_name', e.target.value)} placeholder="AirGate" />
                </Field>
                <Field label={t('settings.site_subtitle')}>
                  <Input value={val('site_subtitle')} onChange={(e) => set('site_subtitle', e.target.value)} placeholder="AI API Gateway" />
                </Field>
                <Field className="col-span-1 md:col-span-2" label={t('settings.api_base_url')} hint={t('settings.api_base_url_hint')}>
                  <Input value={val('api_base_url')} onChange={(e) => set('api_base_url', e.target.value)} placeholder="https://api.example.com" />
                </Field>
                <Field label={t('settings.contact_info')}>
                  <Input value={val('contact_info')} onChange={(e) => set('contact_info', e.target.value)} />
                </Field>
                <Field label={t('settings.doc_url')}>
                  <Input value={val('doc_url')} onChange={(e) => set('doc_url', e.target.value)} placeholder="https://docs.example.com" />
                </Field>
                <Field className="col-span-1 md:col-span-2" label={t('settings.site_logo')} hint={t('settings.site_logo_hint')}>
                  <LogoUpload value={val('site_logo')} onChange={(url) => set('site_logo', url)} />
                </Field>
              </div>
              {saveAction}
            </Card.Content>
          </Card>
        )}

        {activeTab === 'security' && (
          <Card>
            <Card.Header>
              <Card.Title>{t('settings.tab_security')}</Card.Title>
            </Card.Header>
            <Card.Content>
              <div className="ag-settings-section-stack">
                <SecurityPanel />

                <SettingsSection title={t('settings.registration_auth')}>
                  <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                    <div className="space-y-6">
                      <NativeSwitch
                        isSelected={boolVal('registration_enabled')}
                        label={(
                          <>
                            <span className="text-sm font-medium text-text">{t('settings.registration_enabled')}</span>
                            <span className="block text-xs text-text-tertiary">{t('settings.registration_enabled_desc')}</span>
                          </>
                        )}
                        onChange={(v) => set('registration_enabled', String(v))}
                      />
                      <NativeSwitch
                        isDisabled={!val('smtp_host')}
                        isSelected={boolVal('email_verify_enabled')}
                        label={(
                          <>
                            <span className="text-sm font-medium text-text">{t('settings.email_verify_enabled')}</span>
                            <span className="block text-xs text-text-tertiary">
                              {val('smtp_host') ? t('settings.email_verify_enabled_desc') : t('settings.email_verify_no_smtp')}
                            </span>
                          </>
                        )}
                        onChange={(v) => {
                          if (v && !val('smtp_host')) return;
                          set('email_verify_enabled', String(v));
                        }}
                      />
                    </div>
                    <Field className="col-span-1" label={t('settings.email_suffix_whitelist')} hint={t('settings.email_suffix_whitelist_hint')}>
                    <TextArea
                        value={val('registration_email_suffix_whitelist')}
                        onChange={(e) => set('registration_email_suffix_whitelist', e.target.value)}
                        rows={3}
                        placeholder="gmail.com&#10;outlook.com"
                      />
                    </Field>
                  </div>
                </SettingsSection>

                <SettingsSection title={t('settings.new_user_defaults')}>
                  <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                    <Field label={t('settings.default_balance')} hint={t('settings.default_balance_hint')}>
                      <Input
                        type="number"
                        value={val('default_balance')}
                        onChange={(e) => set('default_balance', e.target.value)}
                        placeholder="0"
                      />
                    </Field>
                    <Field label={t('settings.default_concurrency')} hint={t('settings.default_concurrency_hint')}>
                      <Input
                        type="number"
                        value={val('default_concurrency')}
                        onChange={(e) => set('default_concurrency', e.target.value)}
                        placeholder="5"
                      />
                    </Field>
                  </div>
                </SettingsSection>
              </div>
              {saveAction}
            </Card.Content>
          </Card>
        )}

        {activeTab === 'smtp' && (
          <Card>
            <Card.Header className="justify-between gap-3">
              <Card.Title>{t('settings.smtp_config')}</Card.Title>
              <Button
                size="sm"
                variant="secondary"
                onPress={handleTestSMTP}
                isDisabled={!val('smtp_host') || smtpTestMutation.isPending}
                aria-busy={smtpTestMutation.isPending}
              >
                <Send className="w-3.5 h-3.5" />
                {t('settings.smtp_test')}
              </Button>
            </Card.Header>
            <Card.Content>
              <div className="ag-settings-section-stack">
                <SettingsSection title={t('settings.smtp_config')}>
                  <Form className="space-y-4" onSubmit={(e) => e.preventDefault()}>
                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                      <Field label={t('settings.smtp_host')}>
                        <Input value={val('smtp_host')} onChange={(e) => set('smtp_host', e.target.value)} placeholder="smtp.gmail.com" />
                      </Field>
                      <Field label={t('settings.smtp_port')}>
                        <Input type="number" value={val('smtp_port')} onChange={(e) => set('smtp_port', e.target.value)} placeholder="587" />
                      </Field>
                    </div>
                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                      <Field label={t('settings.smtp_username')}>
                        <Input value={val('smtp_username')} onChange={(e) => set('smtp_username', e.target.value)} />
                      </Field>
                      <Field label={t('settings.smtp_password')}>
                        <Input name="smtp_password" type="password" value={val('smtp_password')} onChange={(e) => set('smtp_password', e.target.value)} autoComplete="off" />
                      </Field>
                    </div>
                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                      <Field label={t('settings.smtp_from_email')}>
                        <Input value={val('smtp_from_email')} onChange={(e) => set('smtp_from_email', e.target.value)} placeholder="noreply@example.com" />
                      </Field>
                      <Field label={t('settings.smtp_from_name')}>
                        <Input value={val('smtp_from_name')} onChange={(e) => set('smtp_from_name', e.target.value)} placeholder="AirGate" />
                      </Field>
                    </div>
                    <NativeSwitch
                      isSelected={boolVal('smtp_use_tls')}
                      label={(
                        <>
                          <span className="text-sm font-medium text-text">{t('settings.smtp_use_tls')}</span>
                          <span className="block text-xs text-text-tertiary">{t('settings.smtp_use_tls_desc')}</span>
                        </>
                      )}
                      onChange={(v) => set('smtp_use_tls', String(v))}
                    />
                  </Form>
                </SettingsSection>

                <SettingsSection
                  action={(
                    <Tabs
                      className="ag-page-tabs ag-page-tabs-compact"
                      selectedKey={emailTplType}
                      onSelectionChange={(key) => setEmailTplType(key as 'verify' | 'balance_alert')}
                    >
                      <Tabs.List>
                        <Tabs.Tab id="verify">
                          <Tabs.Indicator />
                          <span>{t('settings.email_template')}</span>
                        </Tabs.Tab>
                        <Tabs.Tab id="balance_alert">
                          <Tabs.Separator />
                          <Tabs.Indicator />
                          <span>{t('settings.balance_alert_email_template')}</span>
                        </Tabs.Tab>
                      </Tabs.List>
                    </Tabs>
                  )}
                  title={emailTplType === 'verify' ? t('settings.email_template') : t('settings.balance_alert_email_template')}
                >
                  {emailTplType === 'verify' ? (
                    <EmailTemplateEditor
                      subject={val('email_template_subject') || DEFAULT_EMAIL_SUBJECT}
                      body={val('email_template_body') || DEFAULT_EMAIL_BODY}
                      onSubjectChange={(v) => set('email_template_subject', v)}
                      onBodyChange={(v) => set('email_template_body', v)}
                      siteName={val('site_name') || 'AirGate'}
                      variables={[
                        { name: 'site_name', sample: val('site_name') || 'AirGate' },
                        { name: 'code', sample: '888888' },
                        { name: 'email', sample: 'user@example.com' },
                      ]}
                      isPreviewOpen={isEmailPreviewOpen}
                      onPreviewOpenChange={setEmailPreviewOpen}
                    />
                  ) : (
                    <EmailTemplateEditor
                      subject={val('balance_alert_email_subject') || DEFAULT_BALANCE_ALERT_SUBJECT}
                      body={val('balance_alert_email_body') || DEFAULT_BALANCE_ALERT_BODY}
                      onSubjectChange={(v) => set('balance_alert_email_subject', v)}
                      onBodyChange={(v) => set('balance_alert_email_body', v)}
                      siteName={val('site_name') || 'AirGate'}
                      variables={[
                        { name: 'site_name', sample: val('site_name') || 'AirGate' },
                        { name: 'balance', sample: '$1.2345' },
                        { name: 'threshold', sample: '$5.00' },
                      ]}
                      isPreviewOpen={isEmailPreviewOpen}
                      onPreviewOpenChange={setEmailPreviewOpen}
                    />
                  )}
                </SettingsSection>
              </div>
              {smtpSaveAction}
            </Card.Content>
          </Card>
        )}

        {activeTab === 'storage' && (
          <StoragePanel set={set} boolVal={boolVal} val={val} footer={saveAction} />
        )}

        {activeTab === 'openclaw' && (
          <OpenClawPanel
            values={values}
            set={set}
            boolVal={boolVal}
            val={val}
            footer={saveAction}
          />
        )}

        {activeTab === 'system' && <SystemUpdatePanel />}
      </div>

      <SmtpTestModal
        isPending={smtpTestMutation.isPending}
        open={isSmtpTestOpen}
        onClose={() => setSmtpTestOpen(false)}
        onSubmit={submitSmtpTest}
      />
    </div>
  );
}

// ==================== SMTP Test Modal ====================

function SmtpTestModal({
  isPending,
  onClose,
  onSubmit,
  open,
}: {
  isPending: boolean;
  onClose: () => void;
  onSubmit: (email: string) => void;
  open: boolean;
}) {
  const { t } = useTranslation();
  const [email, setEmail] = useState('');
  const trimmedEmail = email.trim();

  useEffect(() => {
    if (open) setEmail('');
  }, [open]);

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen && !isPending) onClose();
    },
  });

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!trimmedEmail || isPending) return;
    onSubmit(trimmedEmail);
  }

  return (
    <CommonModal
      description={t('settings.smtp_test_prompt')}
      footer={(
        <div className="flex w-full justify-end gap-2">
          <Button variant="secondary" onPress={onClose} isDisabled={isPending}>
            {t('common.cancel')}
          </Button>
          <Button
            aria-busy={isPending}
            form="smtp-test-form"
            isDisabled={isPending || !trimmedEmail}
            type="submit"
            variant="primary"
          >
            {isPending ? <Spinner size="sm" /> : null}
            {t('settings.smtp_test')}
          </Button>
        </div>
      )}
      icon={<Send className="w-4 h-4" />}
      showCloseTrigger={!isPending}
      size="sm"
      state={modalState}
      surface={false}
      title={t('settings.smtp_test')}
    >
      <Form id="smtp-test-form" className="space-y-4" onSubmit={handleSubmit}>
        <Field label={t('settings.smtp_test_recipient')}>
          <Input
            autoFocus
            autoComplete="email"
            disabled={isPending}
            onChange={(event) => setEmail(event.target.value)}
            placeholder="user@example.com"
            type="text"
            value={email}
          />
        </Field>
      </Form>
    </CommonModal>
  );
}

// ==================== Security Panel ====================

function SecurityPanel() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();
  const copy = useClipboard();

  const [showKeyModal, setShowKeyModal] = useState(false);
  const [plainKey, setPlainKey] = useState('');
  const [confirmRegen, setConfirmRegen] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const { data, isLoading } = useQuery({
    queryKey: queryKeys.adminApiKey(),
    queryFn: () => adminApiKeyApi.get(),
  });

  const hasKey = !!data?.hint;

  const generateMutation = useMutation({
    mutationFn: () => adminApiKeyApi.generate(),
    onSuccess: (resp: AdminAPIKeyResp) => {
      queryClient.setQueryData(queryKeys.adminApiKey(), { hint: resp.hint });
      queryClient.invalidateQueries({ queryKey: queryKeys.adminApiKey() });
      setPlainKey(resp.key ?? '');
      setShowKeyModal(true);
      setConfirmRegen(false);
      toast(
        'success',
        hasKey
          ? t('settings.security_admin_key_regenerated')
          : t('settings.security_admin_key_generated'),
      );
    },
    onError: (err: Error) => toast('error', err.message),
  });

  const deleteMutation = useMutation({
    mutationFn: () => adminApiKeyApi.remove(),
    onSuccess: () => {
      queryClient.setQueryData(queryKeys.adminApiKey(), null);
      queryClient.invalidateQueries({ queryKey: queryKeys.adminApiKey() });
      setConfirmDelete(false);
      toast('success', t('settings.security_admin_key_deleted'));
    },
    onError: (err: Error) => toast('error', err.message),
  });
  const showKeyModalState = useOverlayState({
    isOpen: showKeyModal,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) {
        setShowKeyModal(false);
        setPlainKey('');
      }
    },
  });

  return (
    <>
      <SettingsSection
        description={t('settings.security_admin_key_desc')}
        title={t('settings.security_admin_key')}
      >
        <div className="mb-4">
          <Alert status="warning">
            <Alert.Content>
              <Alert.Description>{t('settings.security_admin_key_warning')}</Alert.Description>
            </Alert.Content>
          </Alert>
        </div>

        {isLoading ? (
          <div className="flex items-center py-4 text-text-tertiary text-sm">
            <Loader2 className="w-4 h-4 animate-spin mr-2" />
            {t('common.loading')}
          </div>
        ) : (
          <div className="flex flex-col sm:flex-row sm:items-end sm:justify-between gap-3">
            <div className="min-w-0 flex-1">
              <div className="text-[12px] text-text-tertiary mb-1.5">
                {t('settings.security_admin_key_current')}
              </div>
              {hasKey ? (
                <code className="inline-block px-2.5 py-1.5 rounded-md bg-surface border border-glass-border text-[13px] font-mono text-text break-all">
                  {data!.hint}
                </code>
              ) : (
                <span className="text-[13px] text-text-tertiary">
                  {t('settings.security_admin_key_none')}
                </span>
              )}
            </div>

            <div className="flex items-center gap-2 shrink-0">
              {hasKey ? (
                <>
                  <Button
                    size="sm"
                    variant="secondary"
                    onPress={() => setConfirmRegen(true)}
                    isDisabled={generateMutation.isPending}
                    aria-busy={generateMutation.isPending}
                  >
                    <RotateCcw className="w-3.5 h-3.5" />
                    {t('settings.security_admin_key_regenerate')}
                  </Button>
                  <Button
                    size="sm"
                    variant="danger"
                    onPress={() => setConfirmDelete(true)}
                    isDisabled={deleteMutation.isPending}
                    aria-busy={deleteMutation.isPending}
                  >
                    <Trash2 className="w-3.5 h-3.5" />
                    {t('settings.security_admin_key_delete')}
                  </Button>
                </>
              ) : (
                <Button
                  size="sm"
                  onPress={() => generateMutation.mutate()}
                  isDisabled={generateMutation.isPending}
                  aria-busy={generateMutation.isPending}
                >
                  <KeyRound className="w-3.5 h-3.5" />
                  {t('settings.security_admin_key_generate')}
                </Button>
              )}
            </div>
          </div>
        )}
      </SettingsSection>

      <Modal state={showKeyModalState}>
        <DialogTriggerShim />
        <Modal.Backdrop>
          <Modal.Container placement="center" scroll="inside" size="md">
            <Modal.Dialog
              className="ag-elevation-modal"
              style={{ maxWidth: '520px', width: 'min(100%, calc(100vw - 2rem))' }}
            >
              <Modal.Header>
                <Modal.Heading>{t('settings.security_admin_key_show_title')}</Modal.Heading>
                <Modal.CloseTrigger />
              </Modal.Header>
              <Modal.Body>
                <div className="space-y-3">
                  <Alert status="warning">
                    <Alert.Content>
                      <Alert.Description>{t('settings.security_admin_key_show_hint')}</Alert.Description>
                    </Alert.Content>
                  </Alert>
                  <div className="flex items-center gap-2">
                    <code className="flex-1 min-w-0 px-3 py-2 rounded-md bg-surface border border-glass-border text-[13px] font-mono text-text break-all">
                      {plainKey}
                    </code>
                    <Button
                      size="sm"
                      variant="secondary"
                      onPress={() => copy(plainKey)}
                    >
                      <Copy className="w-3.5 h-3.5" />
                      {t('settings.security_admin_key_copy')}
                    </Button>
                  </div>
                </div>
              </Modal.Body>
              <Modal.Footer>
                <Button
                  onPress={() => {
                    setShowKeyModal(false);
                    setPlainKey('');
                  }}
                >
                  {t('common.confirm')}
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      <AlertDialog isOpen={confirmRegen} onOpenChange={setConfirmRegen}>
        <DialogTriggerShim />
        <AlertDialog.Backdrop>
          <AlertDialog.Container placement="center" size="sm">
            <AlertDialog.Dialog className="ag-elevation-modal">
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t('settings.security_admin_key_regenerate_confirm_title')}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>{t('settings.security_admin_key_regenerate_confirm_msg')}</AlertDialog.Body>
              <AlertDialog.Footer>
                <Button variant="secondary" onPress={() => setConfirmRegen(false)}>
                  {t('common.cancel')}
                </Button>
                <Button
                  aria-busy={generateMutation.isPending}
                  isDisabled={generateMutation.isPending}
                  variant="danger"
                  onPress={() => generateMutation.mutate()}
                >
                  {generateMutation.isPending ? <Spinner size="sm" /> : null}
                  {t('common.confirm')}
                </Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>

      <AlertDialog isOpen={confirmDelete} onOpenChange={setConfirmDelete}>
        <DialogTriggerShim />
        <AlertDialog.Backdrop>
          <AlertDialog.Container placement="center" size="sm">
            <AlertDialog.Dialog className="ag-elevation-modal">
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t('settings.security_admin_key_delete_confirm_title')}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>{t('settings.security_admin_key_delete_confirm_msg')}</AlertDialog.Body>
              <AlertDialog.Footer>
                <Button variant="secondary" onPress={() => setConfirmDelete(false)}>
                  {t('common.cancel')}
                </Button>
                <Button
                  aria-busy={deleteMutation.isPending}
                  isDisabled={deleteMutation.isPending}
                  variant="danger"
                  onPress={() => deleteMutation.mutate()}
                >
                  {deleteMutation.isPending ? <Spinner size="sm" /> : null}
                  {t('common.confirm')}
                </Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>
    </>
  );
}

// ==================== Email Template Editor ====================

function EmailTemplateEditor({
  subject,
  body,
  onSubjectChange,
  onBodyChange,
  siteName,
  variables,
  isPreviewOpen,
  onPreviewOpenChange,
}: {
  subject: string;
  body: string;
  onSubjectChange: (v: string) => void;
  onBodyChange: (v: string) => void;
  siteName: string;
  variables: { name: string; sample: string }[];
  isPreviewOpen: boolean;
  onPreviewOpenChange: (isOpen: boolean) => void;
}) {
  const { t } = useTranslation();

  // 模板变量替换预览
  function replaceVars(text: string) {
    let result = text;
    for (const v of variables) {
      result = result.replace(new RegExp(`\\{\\{${v.name}\\}\\}`, 'g'), v.sample);
    }
    return result;
  }

  const previewHtml = replaceVars(body);
  const previewModalState = useOverlayState({
    isOpen: isPreviewOpen,
    onOpenChange: onPreviewOpenChange,
  });

  return (
    <>
      <div className="space-y-4">
        <div className="text-[11px] text-text-tertiary space-x-3">
          <span>{t('settings.template_vars')}:</span>
          {variables.map((v) => (
            <code key={v.name} className="px-1.5 py-0.5 rounded bg-surface border border-glass-border text-primary">{`{{${v.name}}}`}</code>
          ))}
        </div>
        <Field label={t('settings.template_subject')}>
          <Input value={subject} onChange={(e) => onSubjectChange(e.target.value)} />
        </Field>
        <Field label={t('settings.template_body')} hint={t('settings.template_body_hint')}>
          <TextArea
            aria-label={t('settings.template_body')}
            value={body}
            onChange={(e) => onBodyChange(e.target.value)}
            className="h-80 w-full font-mono text-xs leading-5"
          />
        </Field>
      </div>
      {isPreviewOpen ? (
        <Modal state={previewModalState}>
          <DialogTriggerShim />
          <Modal.Backdrop>
            <Modal.Container placement="center" scroll="inside" size="lg">
              <Modal.Dialog
                className="ag-elevation-modal"
                style={{ maxWidth: '820px', width: 'min(100%, calc(100vw - 2rem))' }}
              >
                <Modal.Header>
                  <Modal.Heading>{t('settings.template_preview')}</Modal.Heading>
                  <Modal.CloseTrigger />
                </Modal.Header>
                <Modal.Body>
                  <div className="overflow-hidden rounded-xl border border-glass-border bg-overlay shadow-sm">
                    <div className="space-y-0.5 border-b border-glass-border bg-bg-hover/50 px-4 py-2.5 text-[11px]">
                      <div className="flex gap-2">
                        <span className="w-8 shrink-0 text-text-tertiary">From</span>
                        <span className="text-text-secondary">{siteName}</span>
                      </div>
                      <div className="flex gap-2">
                        <span className="w-8 shrink-0 text-text-tertiary">To</span>
                        <span className="text-text-secondary">user@example.com</span>
                      </div>
                      <div className="flex gap-2">
                        <span className="w-8 shrink-0 text-text-tertiary">Sub</span>
                        <span className="font-medium text-text">{replaceVars(subject)}</span>
                      </div>
                    </div>
                    <div className="max-h-[60vh] overflow-y-auto bg-[#f8f9fa] p-5">
                      <div dangerouslySetInnerHTML={{ __html: previewHtml }} />
                    </div>
                  </div>
                </Modal.Body>
                <Modal.Footer>
                  <Button onPress={() => onPreviewOpenChange(false)}>{t('common.close')}</Button>
                </Modal.Footer>
              </Modal.Dialog>
            </Modal.Container>
          </Modal.Backdrop>
        </Modal>
      ) : null}
    </>
  );
}

// ==================== Storage Panel ====================

function StoragePanel({
  set,
  boolVal,
  val,
  footer,
}: {
  set: (key: string, value: string) => void;
  boolVal: (key: string) => boolean;
  val: (key: string) => string;
  footer?: React.ReactNode;
}) {
  const { t } = useTranslation();

  return (
    <Card>
      <Card.Header>
        <Card.Title>{t('settings.storage_config')}</Card.Title>
      </Card.Header>
      <Card.Content>
        <Form className="grid grid-cols-1 md:grid-cols-2 gap-6" onSubmit={(e) => e.preventDefault()}>
          <Field label={t('settings.s3_endpoint')} hint={t('settings.s3_endpoint_hint')}>
            <Input
              value={val('s3_endpoint')}
              onChange={(e) => set('s3_endpoint', e.target.value)}
              placeholder="http://minio:9000"
            />
          </Field>
          <Field label={t('settings.s3_bucket')} hint={t('settings.s3_bucket_hint')}>
            <Input
              value={val('s3_bucket')}
              onChange={(e) => set('s3_bucket', e.target.value)}
              placeholder="airgate"
            />
          </Field>
          <Field label={t('settings.s3_access_key')}>
            <Input
              value={val('s3_access_key')}
              onChange={(e) => set('s3_access_key', e.target.value)}
              autoComplete="off"
            />
          </Field>
          <Field label={t('settings.s3_secret_key')}>
            <Input
              name="s3_secret_key"
              type="password"
              value={val('s3_secret_key')}
              onChange={(e) => set('s3_secret_key', e.target.value)}
              autoComplete="off"
            />
          </Field>
          <Field label={t('settings.s3_region')} hint={t('settings.s3_region_hint')}>
            <Input
              value={val('s3_region')}
              onChange={(e) => set('s3_region', e.target.value)}
              placeholder="us-east-1"
            />
          </Field>
          <Field label={t('settings.s3_presign_ttl_minutes')} hint={t('settings.s3_presign_ttl_minutes_hint')}>
            <Input
              type="number"
              value={val('s3_presign_ttl_minutes')}
              onChange={(e) => set('s3_presign_ttl_minutes', e.target.value)}
              placeholder="360"
            />
          </Field>
          <Field className="col-span-1 md:col-span-2" label={t('settings.s3_public_base_url')} hint={t('settings.s3_public_base_url_hint')}>
            <Input
              value={val('s3_public_base_url')}
              onChange={(e) => set('s3_public_base_url', e.target.value)}
              placeholder="https://cdn.example.com/airgate"
            />
          </Field>
          <Field label={t('settings.s3_path_prefix')} hint={t('settings.s3_path_prefix_hint')}>
            <Input
              value={val('s3_path_prefix')}
              onChange={(e) => set('s3_path_prefix', e.target.value)}
              placeholder="airgate"
            />
          </Field>
          <Field label={t('settings.local_storage_dir')} hint={t('settings.local_storage_dir_hint')}>
            <Input
              value={val('local_storage_dir')}
              onChange={(e) => set('local_storage_dir', e.target.value)}
              placeholder="data/assets"
            />
          </Field>
          <NativeSwitch
            className="col-span-1 md:col-span-2"
            isSelected={boolVal('s3_use_ssl')}
            label={(
              <>
                <span className="text-sm font-medium text-text">{t('settings.s3_use_ssl')}</span>
                <span className="block text-xs text-text-tertiary">{t('settings.s3_use_ssl_desc')}</span>
              </>
            )}
            onChange={(v) => set('s3_use_ssl', String(v))}
          />
        </Form>
        {footer}
      </Card.Content>
    </Card>
  );
}

// ==================== OpenClaw Panel ====================

function OpenClawPanel({
  values,
  set,
  boolVal,
  val,
  footer,
}: {
  values: Record<string, string>;
  set: (key: string, value: string) => void;
  boolVal: (key: string) => boolean;
  val: (key: string) => string;
  footer?: React.ReactNode;
}) {
  const { t } = useTranslation();
  const copy = useClipboard();

  // 未设置时按钮态显示"启用"，即默认启用。
  const enabled = (values['openclaw.enabled'] ?? 'true') === 'true';

  // 管理员可能没填 site.api_base_url，这里只做展示预览，真正的 URL 推导在后端。
  // 都为空时回退到当前页面 origin（与 DocsPage 的处理一致），避免出现尴尬的 <站点地址> 占位符。
  const fallbackOrigin = typeof window !== 'undefined' ? window.location.origin : '';
  const usingFallbackOrigin = !val('openclaw.base_url') && !val('api_base_url');
  const previewBase = (val('openclaw.base_url') || val('api_base_url') || fallbackOrigin || '').replace(/\/$/, '');

  // 两个平台对应两份命令：Unix 用 bash + curl，Windows 用 PowerShell iwr|iex。
  // 后端 HandleInfo 同时返回 install_command_bash / install_command_powershell 两个字段，
  // 这里也分开展示，通过 tab 切换。
  const baseForCmd = previewBase || '<站点地址>';
  const installCommandBash = `curl -fsSL ${baseForCmd}/openclaw/install.sh -o openclaw-install.sh && bash openclaw-install.sh`;
  const installCommandPowerShell = `iwr -useb ${baseForCmd}/openclaw/install.ps1 | iex`;

  // 模型预设 JSON 的客户端校验：不阻塞保存，只给提示，让管理员自己决定。
  const modelsRaw = values['openclaw.models_preset'] ?? '';
  let modelsError = '';
  if (modelsRaw.trim() !== '') {
    try {
      const parsed = JSON.parse(modelsRaw);
      if (!Array.isArray(parsed)) {
        modelsError = t('settings.openclaw_models_not_array');
      }
    } catch (e) {
      modelsError = (e as Error).message;
    }
  }

  return (
    <Card>
      <Card.Header>
        <Card.Title>{t('settings.tab_openclaw')}</Card.Title>
      </Card.Header>
      <Card.Content>
        <div className="ag-settings-section-stack">
          <SettingsSection
            description={t('settings.openclaw_quickstart_desc')}
            title={t('settings.openclaw_quickstart')}
          >
            <div className="space-y-4">
              <div className="space-y-2">
                <div className="text-sm font-medium text-text">
                  {t('settings.openclaw_install_tab_unix')}
                </div>
                <div className="flex items-center gap-2">
                  <code className="flex-1 min-w-0 px-3 py-2 rounded-md bg-surface border border-glass-border text-[12px] font-mono text-text break-all">
                    {installCommandBash}
                  </code>
                  <Button
                    size="sm"
                    variant="secondary"
                    onPress={() => copy(installCommandBash)}
                    isDisabled={!previewBase}
                  >
                    <Copy className="w-3.5 h-3.5" />
                    {t('settings.openclaw_copy_command')}
                  </Button>
                </div>
              </div>

              <div className="space-y-2">
                <div className="text-sm font-medium text-text">
                  {t('settings.openclaw_install_tab_windows')}
                </div>
                <div className="flex items-center gap-2">
                  <code className="flex-1 min-w-0 px-3 py-2 rounded-md bg-surface border border-glass-border text-[12px] font-mono text-text break-all">
                    {installCommandPowerShell}
                  </code>
                  <Button
                    size="sm"
                    variant="secondary"
                    onPress={() => copy(installCommandPowerShell)}
                    isDisabled={!previewBase}
                  >
                    <Copy className="w-3.5 h-3.5" />
                    {t('settings.openclaw_copy_command')}
                  </Button>
                </div>
              </div>
            </div>
            {usingFallbackOrigin && (
              <p className="text-[11px] text-text-tertiary mt-2">
                {t('settings.openclaw_base_url_missing_hint')}
              </p>
            )}
          </SettingsSection>

          <SettingsSection title={t('settings.openclaw_basic')}>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
              <NativeSwitch
                className="col-span-1 md:col-span-2"
                isSelected={enabled}
                label={(
                  <>
                    <span className="text-sm font-medium text-text">{t('settings.openclaw_enabled')}</span>
                    <span className="block text-xs text-text-tertiary">{t('settings.openclaw_enabled_desc')}</span>
                  </>
                )}
                onChange={(v) => set('openclaw.enabled', String(v))}
              />
              <Field label={t('settings.openclaw_provider_name')} hint={t('settings.openclaw_provider_name_hint')}>
                <Input
                  value={val('openclaw.provider_name')}
                  onChange={(e) => set('openclaw.provider_name', e.target.value)}
                  placeholder={DEFAULT_OPENCLAW_PROVIDER_NAME}
                />
              </Field>
              <Field label={t('settings.openclaw_base_url')} hint={t('settings.openclaw_base_url_hint')}>
                <Input
                  value={val('openclaw.base_url')}
                  onChange={(e) => set('openclaw.base_url', e.target.value)}
                  placeholder="https://api.example.com"
                />
              </Field>
            </div>
          </SettingsSection>

          <SettingsSection title={t('settings.openclaw_memory_search')}>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
              <NativeSwitch
                className="col-span-1 md:col-span-2"
                isSelected={boolVal('openclaw.memory_search_enabled')}
                label={(
                  <>
                    <span className="text-sm font-medium text-text">{t('settings.openclaw_memory_search_enabled')}</span>
                    <span className="block text-xs text-text-tertiary">{t('settings.openclaw_memory_search_enabled_desc')}</span>
                  </>
                )}
                onChange={(v) => set('openclaw.memory_search_enabled', String(v))}
              />
              <Field label={t('settings.openclaw_memory_search_model')} hint={t('settings.openclaw_memory_search_model_hint')}>
                <Input
                  value={val('openclaw.memory_search_model')}
                  onChange={(e) => set('openclaw.memory_search_model', e.target.value)}
                  placeholder={DEFAULT_OPENCLAW_MEMORY_MODEL}
                />
              </Field>
            </div>
          </SettingsSection>

          <SettingsSection
            action={(
              <Button
                size="sm"
                variant="ghost"
                onPress={() => set('openclaw.models_preset', DEFAULT_OPENCLAW_MODELS_PRESET)}
              >
                <RotateCcw className="w-3.5 h-3.5" />
                {t('settings.template_reset')}
              </Button>
            )}
            description={t('settings.openclaw_models_preset_desc')}
            title={t('settings.openclaw_models_preset')}
          >
            <TextArea
              aria-label={t('settings.openclaw_models_preset')}
              value={modelsRaw || DEFAULT_OPENCLAW_MODELS_PRESET}
              onChange={(e) => set('openclaw.models_preset', e.target.value)}
              className="h-80 w-full font-mono text-xs leading-5"
              placeholder={DEFAULT_OPENCLAW_MODELS_PRESET}
            />
            {modelsError && (
              <p className="text-[11px] text-danger mt-1.5">{modelsError}</p>
            )}
          </SettingsSection>
        </div>
        {footer}
      </Card.Content>
    </Card>
  );
}

// ==================== Logo Upload ====================

function LogoUpload({ value, onChange }: { value: string; onChange: (url: string) => void }) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const fileInputRef = useRef<HTMLInputElement>(null);

  const handleFile = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    if (file.size > 512 * 1024) {
      toast('error', t('settings.logo_too_large'));
      return;
    }
    const reader = new FileReader();
    reader.onload = () => {
      onChange(reader.result as string);
    };
    reader.readAsDataURL(file);
    e.target.value = '';
  };

  return (
    <div className="flex items-center gap-3">
      <div className="relative group">
        <img src={value || defaultLogoUrl} alt="Logo" className="w-14 h-14 rounded-sm object-cover" />
        {value && (
          <Button
            aria-label={t('settings.restore_default_logo')}
            className="absolute -top-1.5 -right-1.5 opacity-0 group-hover:opacity-100 transition-opacity"
            isIconOnly
            size="sm"
            variant="danger"
            onPress={() => onChange('')}
          >
            <X className="w-3 h-3" />
          </Button>
        )}
      </div>
      <div className="flex flex-col gap-1.5">
        <input
          ref={fileInputRef}
          type="file"
          accept="image/png,image/jpeg,image/svg+xml,image/x-icon,image/webp"
          onChange={handleFile}
          className="hidden"
        />
        <Button
          size="sm"
          variant="secondary"
          onPress={() => fileInputRef.current?.click()}
        >
          <Upload className="w-3.5 h-3.5" />
          {value ? t('settings.change_logo') : t('settings.upload_logo')}
        </Button>
        {value && (
          <Button
            size="sm"
            variant="ghost"
            onPress={() => onChange('')}
          >
            <RotateCcw className="w-3.5 h-3.5" />
            {t('settings.restore_default_logo')}
          </Button>
        )}
      </div>
    </div>
  );
}

// ==================== Field wrapper ====================

function SettingsSection({
  action,
  children,
  description,
  title,
}: {
  action?: React.ReactNode;
  children: React.ReactNode;
  description?: React.ReactNode;
  title: React.ReactNode;
}) {
  return (
    <section className="ag-settings-section">
      <div className="ag-settings-section-heading">
        <div className="min-w-0">
          <h3 className="text-sm font-semibold text-text">{title}</h3>
          {description ? (
            <p className="mt-1 text-[12px] leading-5 text-text-tertiary">{description}</p>
          ) : null}
        </div>
        {action ? <div className="shrink-0">{action}</div> : null}
      </div>
      <div className="ag-settings-section-body">{children}</div>
    </section>
  );
}

function Field({
  className = '',
  label,
  hint,
  children,
}: {
  className?: string;
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div className={`flex flex-col ${className}`}>
      <Label className="block text-[13px] font-medium text-text-secondary mb-1.5">
        {label}
      </Label>
      {children}
      {hint && <p className="text-[11px] text-text-tertiary mt-1">{hint}</p>}
    </div>
  );
}
