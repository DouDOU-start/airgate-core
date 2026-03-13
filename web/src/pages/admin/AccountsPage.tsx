import { useState, useEffect, useRef, type ComponentType } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  Plus,
  Pencil,
  Trash2,
  Zap,
  Server,
  Hash,
  Gauge,
  Layers,
  Shield,
} from 'lucide-react';
import { PageHeader } from '../../shared/components/PageHeader';
import { Button } from '../../shared/components/Button';
import { Input, Textarea, Select } from '../../shared/components/Input';
import { Table, type Column } from '../../shared/components/Table';
import { Modal, ConfirmModal } from '../../shared/components/Modal';
import { StatusBadge } from '../../shared/components/Badge';
import { useToast } from '../../shared/components/Toast';
import { accountsApi } from '../../shared/api/accounts';
import { pluginsApi } from '../../shared/api/plugins';
import { AccountTestModal } from './AccountTestModal';
import { usePlatforms } from '../../shared/hooks/usePlatforms';
import {
  loadPluginFrontend,
  type AccountFormProps,
  type PluginOAuthBridge,
} from '../../app/plugin-loader';
import type {
  AccountResp,
  CreateAccountReq,
  UpdateAccountReq,
  CredentialField,
  AccountTypeResp,
  CredentialSchemaResp,
} from '../../shared/types';

/** 平台 → 插件名称映射缓存 */
let platformPluginMap: Map<string, string> | null = null;

async function getPlatformPluginMap(): Promise<Map<string, string>> {
  if (platformPluginMap) return platformPluginMap;
  const resp = await pluginsApi.list({ page: 1, page_size: 100 });
  const map = new Map<string, string>();
  for (const p of resp.list) {
    if (p.platform) map.set(p.platform, p.name);
  }
  platformPluginMap = map;
  return map;
}

function detectCredentialAccountType(credentials: Record<string, string>): string {
  if (credentials.provider === 'sub2api') return 'sub2api';
  if (credentials.api_key) return 'apikey';
  if (credentials.access_token) return 'oauth';
  return '';
}

function getSchemaAccountTypes(schema?: CredentialSchemaResp): AccountTypeResp[] {
  return schema?.account_types ?? [];
}

function getSchemaSelectedAccountType(
  schema: CredentialSchemaResp | undefined,
  accountType: string,
): AccountTypeResp | undefined {
  const accountTypes = getSchemaAccountTypes(schema);
  if (!accountTypes.length) return undefined;
  return accountTypes.find((item) => item.key === accountType) ?? accountTypes[0];
}

function getSchemaVisibleFields(
  schema: CredentialSchemaResp | undefined,
  accountType: string,
): CredentialField[] {
  const selectedType = getSchemaSelectedAccountType(schema, accountType);
  if (selectedType) return selectedType.fields;
  return schema?.fields ?? [];
}

function filterCredentialsForAccountType(
  credentials: Record<string, string>,
  accountType?: AccountTypeResp,
): Record<string, string> {
  if (!accountType) return credentials;

  const allowedKeys = new Set(accountType.fields.map((field) => field.key));
  const next: Record<string, string> = {};
  for (const [key, value] of Object.entries(credentials)) {
    if (allowedKeys.has(key)) {
      next[key] = value;
    }
  }
  return next;
}

const pluginFormCache = new Map<string, ComponentType<AccountFormProps> | null>();
function usePluginAccountForm(platform: string) {
  const [Form, setForm] = useState<ComponentType<AccountFormProps> | null>(null);
  const [pluginId, setPluginId] = useState('');
  const loadedRef = useRef('');

  useEffect(() => {
    if (!platform) {
      setForm(null);
      setPluginId('');
      loadedRef.current = '';
      return;
    }
    if (loadedRef.current === platform) return;
    loadedRef.current = platform;
    let cancelled = false;

    getPlatformPluginMap().then((map) => {
      const resolvedPluginId = map.get(platform) ?? '';
      if (cancelled) return;

      setPluginId(resolvedPluginId);

      if (!resolvedPluginId) {
        setForm(null);
        return;
      }
      if (pluginFormCache.has(resolvedPluginId)) {
        const cachedForm = pluginFormCache.get(resolvedPluginId) ?? null;
        setForm(() => cachedForm);
        return;
      }
      loadPluginFrontend(resolvedPluginId).then((mod) => {
        if (cancelled) return;
        const form = mod?.accountForm ?? null;
        pluginFormCache.set(resolvedPluginId, form);
        setForm(() => form);
      });
    });

    return () => {
      cancelled = true;
    };
  }, [platform]);

  return { Form, pluginId };
}

function createPluginOAuthBridge(pluginId: string): PluginOAuthBridge | undefined {
  if (!pluginId) return undefined;

  return {
    start: async () => {
      const result = await pluginsApi.rpc<{ authorize_url: string; state: string }>(
        pluginId, 'oauth/start',
      );
      return {
        authorizeURL: result.authorize_url,
        state: result.state,
      };
    },
    exchange: async (callbackURL: string) => {
      const result = await pluginsApi.rpc<{
        account_type: string; account_name: string; credentials: Record<string, string>;
      }>(pluginId, 'oauth/exchange', { callback_url: callbackURL });
      return {
        accountType: result.account_type,
        accountName: result.account_name,
        credentials: result.credentials,
      };
    },
  };
}

const PAGE_SIZE = 20;

export default function AccountsPage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();
  const { platforms } = usePlatforms();

  const PLATFORM_OPTIONS = [
    { value: '', label: t('accounts.all_platforms') },
    ...platforms.map((p) => ({ value: p, label: p })),
  ];

  const STATUS_OPTIONS = [
    { value: '', label: t('users.all_status') },
    { value: 'active', label: t('status.active') },
    { value: 'error', label: t('status.error') },
    { value: 'disabled', label: t('status.disabled') },
  ];

  // 筛选状态
  const [page, setPage] = useState(1);
  const [platformFilter, setPlatformFilter] = useState('');
  const [statusFilter, setStatusFilter] = useState('');

  // 弹窗状态
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingAccount, setEditingAccount] = useState<AccountResp | null>(null);
  const [deletingAccount, setDeletingAccount] = useState<AccountResp | null>(null);
  const [testingAccount, setTestingAccount] = useState<AccountResp | null>(null);

  // 查询账号列表
  const { data, isLoading } = useQuery({
    queryKey: ['accounts', page, platformFilter, statusFilter],
    queryFn: () =>
      accountsApi.list({
        page,
        page_size: PAGE_SIZE,
        platform: platformFilter || undefined,
        status: statusFilter || undefined,
      }),
  });

  // 查询用量窗口
  const { data: usageData } = useQuery({
    queryKey: ['account-usage', platformFilter],
    queryFn: () => accountsApi.usage(platformFilter || ''),
    refetchInterval: 60_000, // 每分钟刷新
  });

  // 创建账号
  const createMutation = useMutation({
    mutationFn: (data: CreateAccountReq) => accountsApi.create(data),
    onSuccess: () => {
      toast('success', t('accounts.create_success'));
      setShowCreateModal(false);
      queryClient.invalidateQueries({ queryKey: ['accounts'] });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 更新账号
  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: number; data: UpdateAccountReq }) =>
      accountsApi.update(id, data),
    onSuccess: () => {
      toast('success', t('accounts.update_success'));
      setEditingAccount(null);
      queryClient.invalidateQueries({ queryKey: ['accounts'] });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 删除账号
  const deleteMutation = useMutation({
    mutationFn: (id: number) => accountsApi.delete(id),
    onSuccess: () => {
      toast('success', t('accounts.delete_success'));
      setDeletingAccount(null);
      queryClient.invalidateQueries({ queryKey: ['accounts'] });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 切换调度状态
  const toggleMutation = useMutation({
    mutationFn: (id: number) => accountsApi.toggleScheduling(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['accounts'] }),
    onError: (err: Error) => toast('error', err.message),
  });

  // 表格列定义
  const columns: Column<AccountResp>[] = [
    {
      key: 'id',
      title: t('common.id'),
      width: '60px',
      render: (row) => (
        <span className="font-mono">
          {row.id}
        </span>
      ),
    },
    {
      key: 'name',
      title: t('common.name'),
      render: (row) => (
        <span style={{ color: 'var(--ag-text)' }} className="font-medium">
          {row.name}
        </span>
      ),
    },
    {
      key: 'platform',
      title: t('accounts.platform'),
      render: (row) => {
        const planType = row.credentials?.plan_type;
        const subUntil = row.credentials?.subscription_active_until;
        const isExpired = subUntil ? new Date(subUntil) < new Date() : false;

        return (
          <div className="flex flex-col gap-0.5">
            <span className="inline-flex items-center gap-1.5">
              <Server className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />
              <span>{row.platform.toUpperCase()}</span>
              {row.type && (
                <span className="text-[10px] px-1 py-0 rounded" style={{ background: 'var(--ag-bg-surface)', border: '1px solid var(--ag-glass-border)', color: 'var(--ag-text-secondary)' }}>
                  {row.type}
                </span>
              )}
            </span>
            {planType && (
              <span className="inline-flex items-center gap-1 text-[11px]" style={{ fontFamily: 'var(--ag-font-mono)' }}>
                <span className="px-1 py-0 rounded text-[10px]" style={{ background: 'var(--ag-bg-surface)', border: '1px solid var(--ag-glass-border)', color: 'var(--ag-text-secondary)' }}>
                  {planType.charAt(0).toUpperCase() + planType.slice(1)}
                </span>
                {subUntil && (
                  <span style={{ color: isExpired ? 'var(--ag-danger)' : 'var(--ag-text-tertiary)', fontSize: 10 }}>
                    {isExpired
                      ? t('accounts.subscription_expired')
                      : new Date(subUntil).toLocaleDateString()
                    }
                  </span>
                )}
              </span>
            )}
          </div>
        );
      },
    },
    {
      key: 'capacity',
      title: t('accounts.capacity'),
      width: '100px',
      render: (row) => {
        const current = row.current_concurrency || 0;
        const max = row.max_concurrency;
        const loadPct = max > 0 ? (current / max) * 100 : 0;
        const color = loadPct < 50 ? 'var(--ag-success)' : loadPct < 80 ? 'var(--ag-warning)' : 'var(--ag-danger)';
        return (
          <span style={{ fontFamily: 'var(--ag-font-mono)' }}>
            <span style={{ color }}>{current}</span>
            <span style={{ color: 'var(--ag-text-tertiary)' }}> / {max}</span>
          </span>
        );
      },
    },
    {
      key: 'status',
      title: t('common.status'),
      render: (row) => <StatusBadge status={row.status} />,
    },
    {
      key: 'scheduling',
      title: t('accounts.scheduling'),
      width: '80px',
      render: (row) => (
        <button
          onClick={(e) => {
            e.stopPropagation();
            toggleMutation.mutate(row.id);
          }}
          disabled={toggleMutation.isPending}
          className="relative inline-flex h-5 w-9 items-center rounded-full transition-colors duration-200 focus:outline-none"
          style={{
            backgroundColor: row.status === 'active' ? 'var(--ag-primary)' : 'var(--ag-glass-border)',
          }}
        >
          <span
            className="inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform duration-200"
            style={{ transform: row.status === 'active' ? 'translateX(17px)' : 'translateX(3px)' }}
          />
        </button>
      ),
    },
    {
      key: 'rate_multiplier',
      title: t('accounts.rate_multiplier'),
      width: '80px',
      render: (row) => (
        <span className="font-mono" style={{ color: 'var(--ag-primary)' }}>
          {row.rate_multiplier}x
        </span>
      ),
    },
    {
      key: 'proxy_id',
      title: t('accounts.proxy'),
      width: '80px',
      render: (row) =>
        row.proxy_id ? (
          <span className="inline-flex items-center gap-1 font-mono">
            <Shield className="w-3 h-3" style={{ color: 'var(--ag-text-tertiary)' }} />
            #{row.proxy_id}
          </span>
        ) : (
          <span style={{ color: 'var(--ag-text-tertiary)' }}>-</span>
        ),
    },
    // 用量窗口
    ...(usageData?.accounts && Object.keys(usageData.accounts).length > 0 ? [{
      key: 'usage_window',
      title: t('accounts.usage_window'),
      width: '200px',
      render: (row: AccountResp) => {
        const usage = usageData?.accounts?.[String(row.id)];
        if (!usage) return <span style={{ color: 'var(--ag-text-tertiary)' }}>-</span>;

        const windows: Array<{ label: string; used_percent: number; reset_seconds: number }> = usage.windows || [];
        const credits: { balance: number; unlimited: boolean } | null = usage.credits || null;

        if (windows.length === 0 && !credits) {
          return <span style={{ color: 'var(--ag-text-tertiary)' }}>-</span>;
        }

        const formatReset = (seconds: number) => {
          if (!seconds || seconds <= 0) return '-';
          const d = Math.floor(seconds / 86400);
          const h = Math.floor((seconds % 86400) / 3600);
          const m = Math.floor((seconds % 3600) / 60);
          if (d > 0) return `${d}d ${h}h`;
          if (h > 0) return `${h}h ${m}m`;
          return `${m}m`;
        };

        const usageColor = (pct: number) => {
          if (pct < 50) return 'var(--ag-success)';
          if (pct < 80) return 'var(--ag-warning)';
          return 'var(--ag-danger)';
        };

        const badgeStyle = { background: 'var(--ag-bg-surface)', border: '1px solid var(--ag-glass-border)', minWidth: 24 };

        return (
          <div className="flex flex-col gap-0.5 text-[11px]" style={{ fontFamily: 'var(--ag-font-mono)' }}>
            {windows.map((w, i) => (
              <div key={i} className="flex items-center gap-1">
                <span className="inline-flex items-center justify-center px-1 py-0 rounded text-[10px] font-medium" style={badgeStyle}>
                  {w.label}
                </span>
                <span style={{ color: usageColor(w.used_percent) }}>
                  {Math.round(w.used_percent)}%
                </span>
                <span style={{ color: 'var(--ag-text-tertiary)', fontSize: 10 }}>
                  {formatReset(w.reset_seconds)}
                </span>
              </div>
            ))}
            {credits && (
              <div className="flex items-center gap-1">
                <span className="inline-flex items-center justify-center px-1 py-0 rounded text-[10px] font-medium" style={badgeStyle}>
                  $
                </span>
                <span style={{ color: credits.unlimited ? 'var(--ag-success)' : credits.balance > 0 ? 'var(--ag-text)' : 'var(--ag-danger)' }}>
                  {credits.unlimited ? '∞' : `$${Number(credits.balance).toFixed(2)}`}
                </span>
              </div>
            )}
          </div>
        );
      },
    } as Column<AccountResp>] : []),
    {
      key: 'actions',
      title: t('common.actions'),
      render: (row) => (
        <div className="flex gap-1">
          <Button
            size="sm"
            variant="ghost"
            icon={<Pencil className="w-3.5 h-3.5" />}
            onClick={() => setEditingAccount(row)}
          >
            {t('common.edit')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            icon={<Zap className="w-3.5 h-3.5" />}
            onClick={() => setTestingAccount(row)}
          >
            {t('accounts.test_connection')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            icon={<Trash2 className="w-3.5 h-3.5" />}
            style={{ color: 'var(--ag-danger)' }}
            onClick={() => setDeletingAccount(row)}
          >
            {t('common.delete')}
          </Button>
        </div>
      ),
    },
  ];

  return (
    <div>
      <PageHeader
        title={t('accounts.title')}
        description={t('accounts.description')}
        actions={
          <Button icon={<Plus className="w-4 h-4" />} onClick={() => setShowCreateModal(true)}>
            {t('accounts.create')}
          </Button>
        }
      />

      {/* 筛选 */}
      <div className="flex items-center gap-3 mb-5">
        <Select
          value={platformFilter}
          onChange={(e) => {
            setPlatformFilter(e.target.value);
            setPage(1);
          }}
          options={PLATFORM_OPTIONS}
          label={t('accounts.platform')}
        />
        <Select
          value={statusFilter}
          onChange={(e) => {
            setStatusFilter(e.target.value);
            setPage(1);
          }}
          options={STATUS_OPTIONS}
          label={t('common.status')}
        />
      </div>

      {/* 表格 */}
      <Table<AccountResp>
        columns={columns}
        data={data?.list ?? []}
        loading={isLoading}
        rowKey={(row) => row.id}
        page={page}
        pageSize={PAGE_SIZE}
        total={data?.total ?? 0}
        onPageChange={setPage}
      />

      {/* 创建弹窗 */}
      <CreateAccountModal
        open={showCreateModal}
        onClose={() => setShowCreateModal(false)}
        onSubmit={(data) => createMutation.mutate(data)}
        loading={createMutation.isPending}
        platforms={platforms}
      />

      {/* 编辑弹窗 */}
      {editingAccount && (
        <EditAccountModal
          open
          account={editingAccount}
          onClose={() => setEditingAccount(null)}
          onSubmit={(data) =>
            updateMutation.mutate({ id: editingAccount.id, data })
          }
          loading={updateMutation.isPending}
        />
      )}

      {/* 删除确认 */}
      <ConfirmModal
        open={!!deletingAccount}
        onClose={() => setDeletingAccount(null)}
        onConfirm={() => deletingAccount && deleteMutation.mutate(deletingAccount.id)}
        title={t('accounts.delete_title')}
        message={t('accounts.delete_confirm', { name: deletingAccount?.name })}
        loading={deleteMutation.isPending}
        danger
      />

      {/* 测试连接 */}
      <AccountTestModal
        open={!!testingAccount}
        account={testingAccount}
        onClose={() => setTestingAccount(null)}
      />
    </div>
  );
}

// ==================== 创建账号弹窗 ====================

function CreateAccountModal({
  open,
  onClose,
  onSubmit,
  loading,
  platforms,
}: {
  open: boolean;
  onClose: () => void;
  onSubmit: (data: CreateAccountReq) => void;
  loading: boolean;
  platforms: string[];
}) {
  const { t } = useTranslation();
  const [platform, setPlatform] = useState('');
  const [accountType, setAccountType] = useState('');
  const [form, setForm] = useState<Omit<CreateAccountReq, 'platform' | 'credentials' | 'type'>>({
    name: '',
    priority: 0,
    max_concurrency: 5,
    rate_multiplier: 1,
  });
  const [credentials, setCredentials] = useState<Record<string, string>>({});

  // 根据平台获取凭证字段定义
  const { data: schema } = useQuery({
    queryKey: ['credentials-schema', platform],
    queryFn: () => accountsApi.credentialsSchema(platform),
    enabled: !!platform,
  });

  // 加载插件自定义表单组件
  const { Form: PluginAccountForm, pluginId } = usePluginAccountForm(platform);
  const pluginOAuth = createPluginOAuthBridge(pluginId);

  useEffect(() => {
    const selectedType = getSchemaSelectedAccountType(schema, accountType);
    if (!selectedType || selectedType.key === accountType) return;
    setAccountType(selectedType.key);
  }, [schema, accountType]);

  // 平台变化时重置凭证和账号类型
  const handlePlatformChange = (newPlatform: string) => {
    setPlatform(newPlatform);
    setCredentials({});
    setAccountType('');
  };

  const handleSchemaAccountTypeChange = (type: string) => {
    const selectedType = getSchemaSelectedAccountType(schema, type);
    setAccountType(type);
    setCredentials((prev) => filterCredentialsForAccountType(prev, selectedType));
  };

  const handleSubmit = () => {
    if (!platform || !form.name) return;
    onSubmit({
      ...form,
      platform,
      type: accountType || undefined,
      credentials,
    });
  };

  const handleClose = () => {
    setPlatform('');
    setAccountType('');
    setForm({ name: '', priority: 0, max_concurrency: 5, rate_multiplier: 1 });
    setCredentials({});
    onClose();
  };

  return (
    <Modal
      open={open}
      onClose={handleClose}
      title={t('accounts.create')}
      width="560px"
      footer={
        <>
          <Button variant="secondary" onClick={handleClose}>
            {t('common.cancel')}
          </Button>
          <Button onClick={handleSubmit} loading={loading} disabled={!platform}>
            {t('common.create')}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Select
          label={t('accounts.platform')}
          required
          value={platform}
          onChange={(e) => handlePlatformChange(e.target.value)}
          options={[
            { value: '', label: t('accounts.select_platform') },
            ...platforms.map((p) => ({ value: p, label: p })),
          ]}
        />

        <Input
          label={t('common.name')}
          required
          value={form.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
          icon={<Layers className="w-4 h-4" />}
        />

        {/* 凭证区域：插件自定义表单 or 默认 schema 驱动 */}
        {PluginAccountForm ? (
          <div
            className="pt-4"
            style={{ borderTop: '1px solid var(--ag-border)' }}
          >
            <PluginAccountForm
              credentials={credentials}
              onChange={setCredentials}
              mode="create"
              accountType={accountType}
              onAccountTypeChange={setAccountType}
              onSuggestedName={(name) =>
                setForm((prev) => (prev.name ? prev : { ...prev, name }))
              }
              oauth={pluginOAuth}
            />
          </div>
        ) : schema && getSchemaVisibleFields(schema, accountType).length > 0 ? (
          <SchemaCredentialsForm
            schema={schema}
            accountType={accountType}
            onAccountTypeChange={handleSchemaAccountTypeChange}
            credentials={credentials}
            onCredentialsChange={setCredentials}
          />
        ) : null}

        <Input
          label={t('accounts.priority')}
          type="number"
          value={String(form.priority ?? 0)}
          onChange={(e) =>
            setForm({ ...form, priority: Number(e.target.value) })
          }
          icon={<Hash className="w-4 h-4" />}
        />
        <Input
          label={t('accounts.concurrency')}
          type="number"
          value={String(form.max_concurrency ?? 5)}
          onChange={(e) =>
            setForm({ ...form, max_concurrency: Number(e.target.value) })
          }
          icon={<Gauge className="w-4 h-4" />}
        />
        <Input
          label={t('accounts.rate_multiplier')}
          type="number"
          step="0.1"
          value={String(form.rate_multiplier ?? 1)}
          onChange={(e) =>
            setForm({ ...form, rate_multiplier: Number(e.target.value) })
          }
        />
      </div>
    </Modal>
  );
}

// ==================== 凭证字段渲染 ====================

function CredentialFieldInput({
  field,
  value,
  onChange,
}: {
  field: CredentialField;
  value: string;
  onChange: (val: string) => void;
}) {
  if (field.type === 'textarea') {
    return (
      <Textarea
        label={field.label}
        required={field.required}
        placeholder={field.placeholder}
        value={value}
        rows={3}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }

  // text 和 password 都使用 Input
  return (
    <Input
      label={field.label}
      type={field.type === 'password' ? 'password' : 'text'}
      required={field.required}
      placeholder={field.placeholder}
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}

function SchemaCredentialsForm({
  schema,
  accountType,
  onAccountTypeChange,
  credentials,
  onCredentialsChange,
}: {
  schema: CredentialSchemaResp;
  accountType: string;
  onAccountTypeChange: (type: string) => void;
  credentials: Record<string, string>;
  onCredentialsChange: (credentials: Record<string, string>) => void;
}) {
  const { t } = useTranslation();
  const accountTypes = getSchemaAccountTypes(schema);
  const selectedType = getSchemaSelectedAccountType(schema, accountType);
  const visibleFields = getSchemaVisibleFields(schema, accountType);

  return (
    <div
      className="space-y-4 pt-4"
      style={{ borderTop: '1px solid var(--ag-border)' }}
    >
      <p
        className="text-xs font-medium uppercase tracking-wider"
        style={{ color: 'var(--ag-text-secondary)' }}
      >
        {t('accounts.credentials')}
      </p>

      {accountTypes.length > 0 && (
        <>
          <Select
            label={t('common.type')}
            value={selectedType?.key ?? ''}
            onChange={(e) => onAccountTypeChange(e.target.value)}
            options={accountTypes.map((item) => ({
              value: item.key,
              label: item.label,
            }))}
          />
          {selectedType?.description && (
            <p className="text-xs text-text-tertiary -mt-2">
              {selectedType.description}
            </p>
          )}
        </>
      )}

      {visibleFields.map((field) => (
        <CredentialFieldInput
          key={field.key}
          field={field}
          value={credentials[field.key] ?? ''}
          onChange={(val) =>
            onCredentialsChange({ ...credentials, [field.key]: val })
          }
        />
      ))}
    </div>
  );
}

// ==================== 编辑账号弹窗 ====================

function EditAccountModal({
  open,
  account,
  onClose,
  onSubmit,
  loading,
}: {
  open: boolean;
  account: AccountResp;
  onClose: () => void;
  onSubmit: (data: UpdateAccountReq) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const initialAccountType = account.type || detectCredentialAccountType(account.credentials);
  const [accountType, setAccountType] = useState(initialAccountType);
  const [form, setForm] = useState<UpdateAccountReq>({
    name: account.name,
    type: initialAccountType || undefined,
    status: account.status === 'error' ? 'active' : (account.status as 'active' | 'disabled'),
    priority: account.priority,
    max_concurrency: account.max_concurrency,
    rate_multiplier: account.rate_multiplier,
    proxy_id: account.proxy_id,
  });

  // 获取凭证字段定义，用于编辑凭证
  const { data: schema } = useQuery({
    queryKey: ['credentials-schema', account.platform],
    queryFn: () => accountsApi.credentialsSchema(account.platform),
  });

  // 加载插件自定义表单组件
  const { Form: PluginAccountForm, pluginId } = usePluginAccountForm(account.platform);
  const pluginOAuth = createPluginOAuthBridge(pluginId);

  const [credentials, setCredentials] = useState<Record<string, string>>(
    account.credentials,
  );

  useEffect(() => {
    const selectedType = getSchemaSelectedAccountType(schema, accountType);
    if (!selectedType || selectedType.key === accountType) return;
    setAccountType(selectedType.key);
    setForm((prev) => ({ ...prev, type: selectedType.key || undefined }));
  }, [schema, accountType]);

  const handleAccountTypeChange = (type: string) => {
    setAccountType(type);
    setForm({ ...form, type: type || undefined });
  };

  const handleSchemaAccountTypeChange = (type: string) => {
    const selectedType = getSchemaSelectedAccountType(schema, type);
    handleAccountTypeChange(type);
    setCredentials((prev) => filterCredentialsForAccountType(prev, selectedType));
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t('accounts.edit')}
      width="560px"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button
            onClick={() => onSubmit({ ...form, type: accountType || undefined, credentials })}
            loading={loading}
          >
            {t('common.save')}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Input label={t('accounts.platform')} value={account.platform} disabled />
        <Input
          label={t('common.name')}
          value={form.name ?? ''}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
          icon={<Layers className="w-4 h-4" />}
        />

        {/* 凭证编辑：插件自定义表单 or 默认 schema 驱动 */}
        {PluginAccountForm ? (
          <div
            className="pt-4"
            style={{ borderTop: '1px solid var(--ag-border)' }}
          >
            <PluginAccountForm
              credentials={credentials}
              onChange={setCredentials}
              mode="edit"
              accountType={accountType}
              onAccountTypeChange={handleAccountTypeChange}
              oauth={pluginOAuth}
            />
          </div>
        ) : schema && getSchemaVisibleFields(schema, accountType).length > 0 ? (
          <SchemaCredentialsForm
            schema={schema}
            accountType={accountType}
            onAccountTypeChange={handleSchemaAccountTypeChange}
            credentials={credentials}
            onCredentialsChange={setCredentials}
          />
        ) : null}

        <Select
          label={t('common.status')}
          value={form.status ?? 'active'}
          onChange={(e) =>
            setForm({
              ...form,
              status: e.target.value as 'active' | 'disabled',
            })
          }
          options={[
            { value: 'active', label: t('status.active') },
            { value: 'disabled', label: t('status.disabled') },
          ]}
        />
        <Input
          label={t('accounts.priority')}
          type="number"
          value={String(form.priority ?? 0)}
          onChange={(e) =>
            setForm({ ...form, priority: Number(e.target.value) })
          }
          icon={<Hash className="w-4 h-4" />}
        />
        <Input
          label={t('accounts.concurrency')}
          type="number"
          value={String(form.max_concurrency ?? 5)}
          onChange={(e) =>
            setForm({ ...form, max_concurrency: Number(e.target.value) })
          }
          icon={<Gauge className="w-4 h-4" />}
        />
        <Input
          label={t('accounts.rate_multiplier')}
          type="number"
          step="0.1"
          value={String(form.rate_multiplier ?? 1)}
          onChange={(e) =>
            setForm({ ...form, rate_multiplier: Number(e.target.value) })
          }
        />
        <Input
          label={t('accounts.proxy_id')}
          type="number"
          value={String(form.proxy_id ?? '')}
          onChange={(e) =>
            setForm({
              ...form,
              proxy_id: e.target.value ? Number(e.target.value) : undefined,
            })
          }
          hint={t('accounts.proxy_hint')}
          icon={<Shield className="w-4 h-4" />}
        />
      </div>
    </Modal>
  );
}
