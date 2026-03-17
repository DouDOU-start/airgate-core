import { useState, useEffect, useRef, type ComponentType } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  Plus,
  Pencil,
  Trash2,
  Zap,
  Hash,
  Gauge,
  Layers,
  Shield,
  MoreHorizontal,
  BarChart3,
  RefreshCw,
  ChevronDown,
  Search,
} from 'lucide-react';
import { PageHeader } from '../../shared/components/PageHeader';
import { Button } from '../../shared/components/Button';
import { Input, Textarea, Select } from '../../shared/components/Input';
import { Switch } from '../../shared/components/Switch';
import { Table, type Column } from '../../shared/components/Table';
import { Modal, ConfirmModal } from '../../shared/components/Modal';
import { StatusBadge } from '../../shared/components/Badge';
import { PlatformIcon } from '../../shared/components/PlatformIcon';
import { useToast } from '../../shared/components/Toast';
import { accountsApi } from '../../shared/api/accounts';
import { groupsApi } from '../../shared/api/groups';
import { proxiesApi } from '../../shared/api/proxies';
import { pluginsApi } from '../../shared/api/plugins';
import { AccountTestModal } from './AccountTestModal';
import { AccountStatsModal } from './AccountStatsModal';
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

const PAGE_SIZE_OPTIONS = [20, 50, 100];

export default function AccountsPage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();
  const { platforms, platformName } = usePlatforms();

  const PLATFORM_OPTIONS = [
    { value: '', label: t('accounts.all_platforms') },
    ...platforms.map((p) => ({ value: p, label: platformName(p) })),
  ];

  const STATUS_OPTIONS = [
    { value: '', label: t('users.all_status') },
    { value: 'active', label: t('status.active') },
    { value: 'error', label: t('status.error') },
    { value: 'disabled', label: t('status.disabled') },
  ];

  // 筛选状态
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [keyword, setKeyword] = useState('');
  const [platformFilter, setPlatformFilter] = useState('');
  const [statusFilter, setStatusFilter] = useState('');
  const [groupFilter, setGroupFilter] = useState('');
  const [proxyFilter, setProxyFilter] = useState('');

  // 自动刷新
  const AUTO_REFRESH_OPTIONS = [0, 5, 10, 15, 30];
  const [autoRefresh, setAutoRefresh] = useState(0); // 秒，0=关闭
  const [showRefreshMenu, setShowRefreshMenu] = useState(false);
  const refreshMenuRef = useRef<HTMLDivElement>(null);
  const [countdown, setCountdown] = useState(0);

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (refreshMenuRef.current && !refreshMenuRef.current.contains(e.target as Node)) setShowRefreshMenu(false);
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, []);

  useEffect(() => {
    if (!autoRefresh) { setCountdown(0); return; }
    setCountdown(autoRefresh);
    const timer = setInterval(() => {
      setCountdown((prev) => {
        if (prev <= 1) {
          queryClient.invalidateQueries({ queryKey: ['accounts'] });
          return autoRefresh;
        }
        return prev - 1;
      });
    }, 1000);
    return () => clearInterval(timer);
  }, [autoRefresh, queryClient]);

  // 弹窗状态
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingAccount, setEditingAccount] = useState<AccountResp | null>(null);
  const [deletingAccount, setDeletingAccount] = useState<AccountResp | null>(null);
  const [testingAccount, setTestingAccount] = useState<AccountResp | null>(null);

  // 查询账号列表
  const { data, isLoading } = useQuery({
    queryKey: ['accounts', page, pageSize, keyword, platformFilter, statusFilter, groupFilter, proxyFilter],
    queryFn: () =>
      accountsApi.list({
        page,
        page_size: pageSize,
        keyword: keyword || undefined,
        platform: platformFilter || undefined,
        status: statusFilter || undefined,
        group_id: groupFilter ? Number(groupFilter) : undefined,
        proxy_id: proxyFilter ? Number(proxyFilter) : undefined,
      }),
  });

  // 查询分组列表（用于表格中 ID→名称映射）
  const { data: allGroupsData } = useQuery({
    queryKey: ['groups-all'],
    queryFn: () => groupsApi.list({ page: 1, page_size: 100 }),
  });
  const groupMap = new Map(
    (allGroupsData?.list ?? []).map((g) => [g.id, g.name]),
  );

  // 查询代理列表（用于表格中 ID→名称映射）
  const { data: allProxiesData } = useQuery({
    queryKey: ['proxies-all'],
    queryFn: () => proxiesApi.list({ page: 1, page_size: 100 }),
  });
  const proxyMap = new Map(
    (allProxiesData?.list ?? []).map((p) => [p.id, p.name]),
  );

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

  // 刷新令牌
  const refreshQuotaMutation = useMutation({
    mutationFn: (id: number) => accountsApi.refreshQuota(id),
    onSuccess: () => {
      toast('success', t('accounts.refresh_quota_success'));
      queryClient.invalidateQueries({ queryKey: ['accounts'] });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 更多菜单状态（合并 id 和位置为单一状态，避免分步更新导致闪跳）
  const [moreMenu, setMoreMenu] = useState<{ id: number; top: number; left: number } | null>(null);
  const moreMenuRef = useRef<HTMLDivElement>(null);
  const moreMenuBtnRef = useRef<HTMLButtonElement>(null);

  // 统计弹窗
  const [statsAccountId, setStatsAccountId] = useState<number | null>(null);

  // 点击外部关闭更多菜单
  useEffect(() => {
    if (!moreMenu) return;
    const handler = (e: MouseEvent) => {
      if (
        moreMenuRef.current && !moreMenuRef.current.contains(e.target as Node) &&
        moreMenuBtnRef.current && !moreMenuBtnRef.current.contains(e.target as Node)
      ) {
        setMoreMenu(null);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [moreMenu]);

  // 表格列定义
  const columns: Column<AccountResp>[] = [
    {
      key: 'name',
      title: t('common.name'),
      width: '150px',
      fixed: 'left',
      render: (row) => {
        const email = row.credentials?.email;
        return (
          <div className="flex flex-col">
            <span style={{ color: 'var(--ag-text)' }} className="font-medium">
              {row.name}
            </span>
            {email && (
              <span className="text-[11px]" style={{ color: 'var(--ag-text-tertiary)' }}>
                {email}
              </span>
            )}
          </div>
        );
      },
    },
    {
      key: 'platform',
      title: t('accounts.platform_type'),
      render: (row) => {
        const planType = row.credentials?.plan_type;
        return (
          <div className="flex flex-col items-center gap-1.5">
            <span className="inline-flex items-center gap-1">
              <PlatformIcon platform={row.platform} className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />
              <span>{platformName(row.platform)}</span>
            </span>
            <div className="flex items-center gap-1">
              {row.type && (
                <span className="text-[10px] px-1 py-0 rounded" style={{ background: 'var(--ag-bg-surface)', border: '1px solid var(--ag-glass-border)', color: 'var(--ag-text-secondary)' }}>
                  {row.type.charAt(0).toUpperCase() + row.type.slice(1)}
                </span>
              )}
              {planType && (
                <span className="text-[10px] px-1 py-0 rounded font-medium" style={{ background: 'var(--ag-primary)', color: 'var(--ag-text-inverse)', opacity: 0.85 }}>
                  {planType.charAt(0).toUpperCase() + planType.slice(1)}
                </span>
              )}
            </div>
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
          <span className="inline-flex items-center gap-1">
            <Shield className="w-3 h-3" style={{ color: 'var(--ag-text-tertiary)' }} />
            {proxyMap.get(row.proxy_id) ?? `#${row.proxy_id}`}
          </span>
        ) : (
          <span style={{ color: 'var(--ag-text-tertiary)' }}>-</span>
        ),
    },
    {
      key: 'groups',
      title: t('accounts.groups'),
      render: (row) => {
        if (!row.group_ids || row.group_ids.length === 0) {
          return <span style={{ color: 'var(--ag-text-tertiary)' }}>-</span>;
        }
        return (
          <div className="flex flex-wrap gap-1">
            {row.group_ids.map((gid) => (
              <span
                key={gid}
                className="text-[10px] px-1.5 py-0 rounded"
                style={{ background: 'var(--ag-bg-surface)', border: '1px solid var(--ag-glass-border)', color: 'var(--ag-text-secondary)' }}
              >
                {groupMap.get(gid) ?? `#${gid}`}
              </span>
            ))}
          </div>
        );
      },
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

        // 简化 label：取最后一段（如 "GPT-5.3-Codex-Spark" → "Spark"）
        const shortLabel = (label: string) => {
          const parts = label.split(/[\s]+/);
          // 第一部分是时间窗口（如 "5h"、"7d"），后面是模型名
          const timePart = parts[0];
          if (parts.length <= 1) return timePart;
          const modelPart = parts.slice(1).join(' ');
          const segments = modelPart.split('-');
          return `${timePart} ${segments[segments.length - 1]}`;
        };

        const badgeStyle = { background: 'var(--ag-bg-surface)', border: '1px solid var(--ag-glass-border)', minWidth: 24 };

        return (
          <div className="flex flex-col gap-1.5 text-[11px]" style={{ fontFamily: 'var(--ag-font-mono)', minWidth: 160 }}>
            {windows.map((w, i) => (
              <div key={i} className="flex items-center gap-1.5">
                <span className="inline-flex items-center justify-center px-1 py-0 rounded text-[10px] font-medium shrink-0" style={badgeStyle}>
                  {shortLabel(w.label)}
                </span>
                <div className="flex-1 h-1.5 rounded-full overflow-hidden" style={{ background: 'var(--ag-glass-border)', minWidth: 40 }}>
                  <div
                    className="h-full rounded-full transition-all"
                    style={{ width: `${Math.min(100, Math.round(w.used_percent))}%`, background: usageColor(w.used_percent) }}
                  />
                </div>
                <span className="shrink-0" style={{ color: usageColor(w.used_percent), fontSize: 10 }}>
                  {Math.round(w.used_percent)}%
                </span>
                <span className="shrink-0" style={{ color: 'var(--ag-text-tertiary)', fontSize: 10 }}>
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
      key: 'last_used_at',
      title: t('accounts.last_used'),
      width: '120px',
      render: (row) => {
        if (!row.last_used_at) {
          return <span style={{ color: 'var(--ag-text-tertiary)' }}>-</span>;
        }
        const diff = Date.now() - new Date(row.last_used_at).getTime();
        const seconds = Math.floor(diff / 1000);
        const minutes = Math.floor(seconds / 60);
        const hours = Math.floor(minutes / 60);
        const days = Math.floor(hours / 24);
        let relative: string;
        if (seconds < 60) relative = t('accounts.just_now');
        else if (minutes < 60) relative = t('accounts.minutes_ago', { n: minutes });
        else if (hours < 24) relative = t('accounts.hours_ago', { n: hours });
        else relative = t('accounts.days_ago', { n: days });
        return (
          <span className="text-xs" style={{ color: 'var(--ag-text-secondary)' }} title={new Date(row.last_used_at).toLocaleString()}>
            {relative}
          </span>
        );
      },
    },
    {
      key: 'expires_at',
      title: t('accounts.expires_at'),
      width: '120px',
      render: (row) => {
        const subUntil = row.credentials?.subscription_active_until;
        if (!subUntil) {
          return <span style={{ color: 'var(--ag-text-tertiary)' }}>-</span>;
        }
        const isExpired = new Date(subUntil) < new Date();
        return (
          <span
            className="text-xs"
            style={{ color: isExpired ? 'var(--ag-danger)' : 'var(--ag-text-secondary)' }}
          >
            {isExpired ? t('accounts.subscription_expired') : new Date(subUntil).toLocaleDateString()}
          </span>
        );
      },
    },
    {
      key: 'actions',
      title: t('common.actions'),
      fixed: 'right',
      render: (row) => (
        <div className="flex items-center justify-center gap-0.5">
          <button
            className="p-1.5 rounded hover:bg-bg-hover transition-colors"
            style={{ color: 'var(--ag-text-secondary)' }}
            title={t('common.edit')}
            onClick={() => setEditingAccount(row)}
          >
            <Pencil className="w-3.5 h-3.5" />
          </button>
          <button
            className="p-1.5 rounded hover:bg-bg-hover transition-colors"
            style={{ color: 'var(--ag-danger)' }}
            title={t('common.delete')}
            onClick={() => setDeletingAccount(row)}
          >
            <Trash2 className="w-3.5 h-3.5" />
          </button>
          <button
            ref={moreMenu?.id === row.id ? moreMenuBtnRef : undefined}
            className="p-1.5 rounded hover:bg-bg-hover transition-colors"
            style={{ color: 'var(--ag-text-secondary)' }}
            title={t('common.more')}
            onClick={(e) => {
              e.stopPropagation();
              if (moreMenu?.id === row.id) {
                setMoreMenu(null);
              } else {
                const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
                setMoreMenu({ id: row.id, top: rect.bottom + 4, left: rect.right });
              }
            }}
          >
            <MoreHorizontal className="w-3.5 h-3.5" />
          </button>
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
      <div className="flex items-end gap-3 mb-5 flex-wrap">
        <Input
          value={keyword}
          onChange={(e) => { setKeyword(e.target.value); setPage(1); }}
          placeholder={t('common.search')}
          icon={<Search className="w-4 h-4" />}
          style={{ width: 200 }}
        />
        <Select
          value={platformFilter}
          onChange={(e) => { setPlatformFilter(e.target.value); setPage(1); }}
          options={PLATFORM_OPTIONS}
        />
        <Select
          value={statusFilter}
          onChange={(e) => { setStatusFilter(e.target.value); setPage(1); }}
          options={STATUS_OPTIONS}
        />
        <Select
          value={groupFilter}
          onChange={(e) => { setGroupFilter(e.target.value); setPage(1); }}
          options={[
            { value: '', label: t('accounts.all_groups') },
            ...(allGroupsData?.list ?? []).map((g) => ({ value: String(g.id), label: g.name })),
          ]}
        />
        <Select
          value={proxyFilter}
          onChange={(e) => { setProxyFilter(e.target.value); setPage(1); }}
          options={[
            { value: '', label: t('accounts.all_proxies') },
            ...(allProxiesData?.list ?? []).map((p) => ({ value: String(p.id), label: p.name })),
          ]}
        />

        {/* 刷新 & 自动刷新 */}
        <div className="flex items-center gap-1.5 ml-auto">
          <button
            onClick={() => queryClient.invalidateQueries({ queryKey: ['accounts'] })}
            className="flex items-center justify-center w-8 h-8 rounded-lg text-text-tertiary hover:text-text-secondary hover:bg-bg-hover transition-colors"
            title={t('common.refresh')}
          >
            <RefreshCw className="w-4 h-4" />
          </button>
          <div className="relative" ref={refreshMenuRef}>
            <button
              onClick={() => setShowRefreshMenu(!showRefreshMenu)}
              className={`flex items-center gap-1.5 h-8 px-2.5 rounded-lg text-xs transition-colors ${
                autoRefresh ? 'text-primary bg-primary-subtle' : 'text-text-tertiary hover:text-text-secondary hover:bg-bg-hover'
              }`}
            >
              {autoRefresh ? (
                <span>{t('accounts.auto_refresh')}{countdown}s</span>
              ) : (
                <span>{t('accounts.auto_refresh_off')}</span>
              )}
              <ChevronDown className={`w-3 h-3 transition-transform ${showRefreshMenu ? 'rotate-180' : ''}`} />
            </button>
            {showRefreshMenu && (
              <div
                className="absolute right-0 mt-1 w-40 rounded-lg border shadow-lg py-1 z-50"
                style={{ background: 'var(--ag-bg-elevated)', borderColor: 'var(--ag-glass-border)' }}
              >
                {AUTO_REFRESH_OPTIONS.map((sec) => (
                  <button
                    key={sec}
                    onClick={() => { setAutoRefresh(sec); setShowRefreshMenu(false); }}
                    className="flex items-center justify-between w-full px-3 py-2 text-sm hover:bg-bg-hover transition-colors text-left"
                    style={{ color: 'var(--ag-text)' }}
                  >
                    <span>{sec === 0 ? t('accounts.auto_refresh_off') : `${sec}s`}</span>
                    {autoRefresh === sec && <span className="text-primary">✓</span>}
                  </button>
                ))}
              </div>
            )}
          </div>
        </div>
      </div>

      {/* 表格 */}
      <Table<AccountResp>
        columns={columns}
        data={data?.list ?? []}
        loading={isLoading}
        rowKey={(row) => row.id}
        page={page}
        pageSize={pageSize}
        total={data?.total ?? 0}
        onPageChange={setPage}
        pageSizeOptions={PAGE_SIZE_OPTIONS}
        onPageSizeChange={(size) => { setPageSize(size); setPage(1); }}
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

      {/* 更多操作下拉菜单 (Portal) */}
      {moreMenu && createPortal(
        <div
          ref={moreMenuRef}
          className="fixed py-1 rounded-lg shadow-lg min-w-[140px]"
          style={{
            top: moreMenu.top,
            left: moreMenu.left,
            transform: 'translateX(-100%)',
            zIndex: 9999,
            background: 'var(--ag-bg-elevated)',
            border: '1px solid var(--ag-glass-border)',
          }}
        >
          {(() => {
            const row = data?.list?.find((a) => a.id === moreMenu.id);
            if (!row) return null;
            return (
              <>
                <button
                  className="flex items-center gap-2 w-full px-3 py-1.5 text-xs hover:bg-bg-hover transition-colors text-left"
                  style={{ color: 'var(--ag-text-secondary)' }}
                  onClick={() => { setTestingAccount(row); setMoreMenu(null); }}
                >
                  <Zap className="w-3.5 h-3.5" style={{ color: 'var(--ag-warning)' }} />
                  {t('accounts.test_connection')}
                </button>
                <button
                  className="flex items-center gap-2 w-full px-3 py-1.5 text-xs hover:bg-bg-hover transition-colors text-left"
                  style={{ color: 'var(--ag-text-secondary)' }}
                  onClick={() => { setStatsAccountId(row.id); setMoreMenu(null); }}
                >
                  <BarChart3 className="w-3.5 h-3.5" style={{ color: 'var(--ag-primary)' }} />
                  {t('accounts.view_stats')}
                </button>
                {row.type === 'oauth' && (
                  <button
                    className="flex items-center gap-2 w-full px-3 py-1.5 text-xs hover:bg-bg-hover transition-colors text-left"
                    style={{ color: 'var(--ag-text-secondary)' }}
                    onClick={() => { refreshQuotaMutation.mutate(row.id); setMoreMenu(null); }}
                  >
                    <RefreshCw className="w-3.5 h-3.5" style={{ color: 'var(--ag-success)' }} />
                    {t('accounts.refresh_quota')}
                  </button>
                )}
              </>
            );
          })()}
        </div>,
        document.body,
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

      {/* 账号统计 */}
      {statsAccountId !== null && (
        <AccountStatsModal
          accountId={statsAccountId}
          onClose={() => setStatsAccountId(null)}
        />
      )}
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
  const { platformName: pName } = usePlatforms();
  const [platform, setPlatform] = useState('');
  const [accountType, setAccountType] = useState('');
  const [form, setForm] = useState<Omit<CreateAccountReq, 'platform' | 'credentials' | 'type'>>({
    name: '',
    priority: 0,
    max_concurrency: 5,
    rate_multiplier: 1,
  });
  const [credentials, setCredentials] = useState<Record<string, string>>({});
  const [groupIds, setGroupIds] = useState<number[]>([]);

  // 根据平台获取凭证字段定义
  const { data: schema } = useQuery({
    queryKey: ['credentials-schema', platform],
    queryFn: () => accountsApi.credentialsSchema(platform),
    enabled: !!platform,
  });

  // 查询分组列表
  const { data: groupsData } = useQuery({
    queryKey: ['groups-all'],
    queryFn: () => groupsApi.list({ page: 1, page_size: 100 }),
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
      group_ids: groupIds,
    });
  };

  const handleClose = () => {
    setPlatform('');
    setAccountType('');
    setForm({ name: '', priority: 0, max_concurrency: 5, rate_multiplier: 1 });
    setCredentials({});
    setGroupIds([]);
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
            ...platforms.map((p) => ({ value: p, label: pName(p) })),
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
            className="ag-plugin-scope pt-4"
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
          label={t('accounts.priority_hint')}
          type="number"
          min={0}
          max={999}
          step={1}
          value={String(form.priority ?? 50)}
          onChange={(e) => {
            const v = Math.round(Number(e.target.value));
            setForm({ ...form, priority: Math.max(0, Math.min(999, v)) });
          }}
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

        {/* 分组选择 */}
        <GroupCheckboxList
          groups={groupsData?.list ?? []}
          selectedIds={groupIds}
          onChange={setGroupIds}
        />
      </div>
    </Modal>
  );
}

// ==================== 分组多选 ====================

function GroupCheckboxList({
  groups,
  selectedIds,
  onChange,
}: {
  groups: { id: number; name: string; platform: string }[];
  selectedIds: number[];
  onChange: (ids: number[]) => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, []);

  if (groups.length === 0) return null;

  const toggle = (id: number) => {
    onChange(
      selectedIds.includes(id)
        ? selectedIds.filter((v) => v !== id)
        : [...selectedIds, id],
    );
  };

  const selectedGroups = groups.filter((g) => selectedIds.includes(g.id));

  return (
    <div ref={ref} className="relative">
      <label
        className="block text-xs font-medium mb-1.5"
        style={{ color: 'var(--ag-text-secondary)' }}
      >
        {t('accounts.groups')}
      </label>
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="flex items-center justify-between w-full px-3 py-2 rounded-lg border text-sm text-left transition-colors"
        style={{ borderColor: 'var(--ag-glass-border)', background: 'var(--ag-bg-surface)', color: 'var(--ag-text)' }}
      >
        <span className="truncate" style={selectedGroups.length === 0 ? { color: 'var(--ag-text-tertiary)' } : undefined}>
          {selectedGroups.length === 0
            ? t('accounts.select_groups')
            : selectedGroups.map((g) => g.name).join('、')}
        </span>
        <ChevronDown className={`w-4 h-4 flex-shrink-0 ml-2 transition-transform duration-200 ${open ? 'rotate-180' : ''}`} style={{ color: 'var(--ag-text-tertiary)' }} />
      </button>
      {open && (
        <div
          className="absolute z-50 mt-1 w-full rounded-lg border shadow-lg max-h-48 overflow-y-auto py-1"
          style={{ borderColor: 'var(--ag-glass-border)', background: 'var(--ag-bg-elevated)' }}
        >
          {groups.map((g) => (
            <button
              key={g.id}
              type="button"
              onClick={() => toggle(g.id)}
              className="flex items-center gap-2 w-full px-3 py-2 text-sm hover:bg-bg-hover transition-colors text-left"
              style={{ color: 'var(--ag-text)' }}
            >
              <span
                className="flex items-center justify-center w-4 h-4 rounded border flex-shrink-0 transition-colors"
                style={{
                  borderColor: selectedIds.includes(g.id) ? 'var(--ag-primary)' : 'var(--ag-glass-border)',
                  background: selectedIds.includes(g.id) ? 'var(--ag-primary)' : 'transparent',
                }}
              >
                {selectedIds.includes(g.id) && (
                  <svg className="w-3 h-3 text-white" viewBox="0 0 12 12" fill="none"><path d="M2.5 6l2.5 2.5 4.5-5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>
                )}
              </span>
              <span>{g.name}</span>
              <span className="text-[10px]" style={{ color: 'var(--ag-text-tertiary)' }}>{g.platform}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

// ==================== 凭证字段渲染 ====================

function CredentialFieldInput({
  field,
  value,
  onChange,
  disabled,
  placeholder,
}: {
  field: CredentialField;
  value: string;
  onChange: (val: string) => void;
  disabled?: boolean;
  placeholder?: string;
}) {
  const hint = placeholder ?? field.placeholder;

  if (field.type === 'textarea') {
    return (
      <Textarea
        label={field.label}
        required={field.required}
        placeholder={hint}
        value={value}
        rows={3}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
      />
    );
  }

  // text 和 password 都使用 Input
  // 密码字段使用 type="text" + CSS 遮蔽，避免浏览器检测到 password 字段自动填充
  const isPassword = field.type === 'password';
  return (
    <Input
      label={field.label}
      type="text"
      required={field.required}
      placeholder={hint}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      disabled={disabled}
      autoComplete="off"
      style={isPassword ? { WebkitTextSecurity: 'disc', textSecurity: 'disc' } as React.CSSProperties : undefined}
    />
  );
}

function SchemaCredentialsForm({
  schema,
  accountType,
  onAccountTypeChange,
  credentials,
  onCredentialsChange,
  mode = 'create',
}: {
  schema: CredentialSchemaResp;
  accountType: string;
  onAccountTypeChange: (type: string) => void;
  credentials: Record<string, string>;
  onCredentialsChange: (credentials: Record<string, string>) => void;
  mode?: 'create' | 'edit';
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

      {accountTypes.length > 0 && mode === 'create' && (
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

      {visibleFields
        .filter((field) => !(mode === 'edit' && field.edit_disabled))
        .map((field) => (
          <CredentialFieldInput
            key={field.key}
            field={field}
            value={credentials[field.key] ?? ''}
            onChange={(val) =>
              onCredentialsChange({ ...credentials, [field.key]: val })
            }
            placeholder={mode === 'edit' && field.type === 'password' ? t('accounts.leave_empty_to_keep') : undefined}
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
  const { platformName: pName } = usePlatforms();
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

  // 保留原始凭证，用于提交时回填未修改的密码字段
  const origCredentials = useRef(account.credentials);
  const [credentials, setCredentials] = useState<Record<string, string>>(
    account.credentials,
  );

  // schema 加载后，清空密码字段的显示值（避免回填）
  const passwordFieldsCleared = useRef(false);
  useEffect(() => {
    if (!schema || passwordFieldsCleared.current) return;
    const passwordKeys = getSchemaVisibleFields(schema, accountType)
      .filter((f) => f.type === 'password')
      .map((f) => f.key);
    if (passwordKeys.length === 0) return;
    passwordFieldsCleared.current = true;
    setCredentials((prev) => {
      const next = { ...prev };
      for (const key of passwordKeys) next[key] = '';
      return next;
    });
  }, [schema, accountType]);
  const [groupIds, setGroupIds] = useState<number[]>(account.group_ids ?? []);

  // 查询分组列表
  const { data: groupsData } = useQuery({
    queryKey: ['groups-all'],
    queryFn: () => groupsApi.list({ page: 1, page_size: 100 }),
  });

  // 查询代理列表
  const { data: proxiesData } = useQuery({
    queryKey: ['proxies-all'],
    queryFn: () => proxiesApi.list({ page: 1, page_size: 100 }),
  });

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
            onClick={() => {
              // 提交时：用户未修改的密码字段回填原值
              const merged = { ...credentials };
              for (const [k, v] of Object.entries(origCredentials.current)) {
                if (merged[k] === '' && v) merged[k] = v;
              }
              onSubmit({ ...form, type: accountType || undefined, credentials: merged, group_ids: groupIds });
            }}
            loading={loading}
          >
            {t('common.save')}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Input label={t('accounts.platform')} value={pName(account.platform)} disabled />
        <Input
          label={t('common.name')}
          value={form.name ?? ''}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
          icon={<Layers className="w-4 h-4" />}
        />

        {/* 凭证编辑：插件自定义表单 or 默认 schema 驱动 */}
        {PluginAccountForm ? (
          <div
            className="ag-plugin-scope pt-4"
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
            mode="edit"
          />
        ) : null}

        <Switch
          label={t('accounts.enable_dispatch')}
          checked={form.status !== 'disabled'}
          onChange={(on) => setForm({ ...form, status: on ? 'active' : 'disabled' })}
        />
        <Input
          label={t('accounts.priority_hint')}
          type="number"
          min={0}
          max={999}
          step={1}
          value={String(form.priority ?? 50)}
          onChange={(e) => {
            const v = Math.round(Number(e.target.value));
            setForm({ ...form, priority: Math.max(0, Math.min(999, v)) });
          }}
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
        <Select
          label={t('accounts.proxy')}
          value={String(form.proxy_id ?? '')}
          onChange={(e) =>
            setForm({
              ...form,
              proxy_id: e.target.value ? Number(e.target.value) : undefined,
            })
          }
          options={[
            { value: '', label: t('accounts.no_proxy') },
            ...(proxiesData?.list ?? []).map((p) => ({
              value: String(p.id),
              label: `${p.name} (${p.protocol}://${p.address}:${p.port})`,
            })),
          ]}
        />

        {/* 分组选择 */}
        <GroupCheckboxList
          groups={groupsData?.list ?? []}
          selectedIds={groupIds}
          onChange={setGroupIds}
        />
      </div>
    </Modal>
  );
}

