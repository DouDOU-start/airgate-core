import { useState, useEffect, useRef } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Plus,
  Pencil,
  Trash2,
  Zap,
  Shield,
  MoreHorizontal,
  BarChart3,
  RefreshCw,
  ChevronDown,
  Search,
} from 'lucide-react';
import { PageHeader } from '../../shared/components/PageHeader';
import { Button } from '../../shared/components/Button';
import { Input, Select } from '../../shared/components/Input';
import { Table, type Column } from '../../shared/components/Table';
import { ConfirmModal } from '../../shared/components/Modal';
import { StatusBadge } from '../../shared/components/Badge';
import { PlatformIcon } from '../../shared/components/PlatformIcon';
import { accountsApi } from '../../shared/api/accounts';
import { groupsApi } from '../../shared/api/groups';
import { proxiesApi } from '../../shared/api/proxies';
import { AccountTestModal } from './AccountTestModal';
import { AccountStatsModal } from './AccountStatsModal';
import { usePlatforms } from '../../shared/hooks/usePlatforms';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { PAGE_SIZE_OPTIONS, FETCH_ALL_PARAMS } from '../../shared/constants';
import { CreateAccountModal } from './accounts/CreateAccountModal';
import { EditAccountModal } from './accounts/EditAccountModal';
import type {
  AccountResp,
  CreateAccountReq,
  UpdateAccountReq,
} from '../../shared/types';

export default function AccountsPage() {
  const { t } = useTranslation();
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
          queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
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
    queryKey: queryKeys.accounts(page, pageSize, keyword, platformFilter, statusFilter, groupFilter, proxyFilter),
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
    queryKey: queryKeys.groupsAll(),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
  });
  const groupMap = new Map(
    (allGroupsData?.list ?? []).map((g) => [g.id, g.name]),
  );

  // 查询代理列表（用于表格中 ID→名称映射）
  const { data: allProxiesData } = useQuery({
    queryKey: queryKeys.proxiesAll(),
    queryFn: () => proxiesApi.list(FETCH_ALL_PARAMS),
  });
  const proxyMap = new Map(
    (allProxiesData?.list ?? []).map((p) => [p.id, p.name]),
  );

  // 查询用量窗口
  const { data: usageData } = useQuery({
    queryKey: queryKeys.accountUsage(platformFilter),
    queryFn: () => accountsApi.usage(platformFilter || ''),
    refetchInterval: 60_000, // 每分钟刷新
  });

  // 创建账号
  const createMutation = useCrudMutation({
    mutationFn: (data: CreateAccountReq) => accountsApi.create(data),
    successMessage: t('accounts.create_success'),
    queryKey: queryKeys.accounts(),
    onSuccess: () => setShowCreateModal(false),
  });

  // 更新账号
  const updateMutation = useCrudMutation({
    mutationFn: ({ id, data }: { id: number; data: UpdateAccountReq }) =>
      accountsApi.update(id, data),
    successMessage: t('accounts.update_success'),
    queryKey: queryKeys.accounts(),
    onSuccess: () => setEditingAccount(null),
  });

  // 删除账号
  const deleteMutation = useCrudMutation({
    mutationFn: (id: number) => accountsApi.delete(id),
    successMessage: t('accounts.delete_success'),
    queryKey: queryKeys.accounts(),
    onSuccess: () => setDeletingAccount(null),
  });

  // 切换调度状态
  const toggleMutation = useCrudMutation({
    mutationFn: (id: number) => accountsApi.toggleScheduling(id),
    queryKey: queryKeys.accounts(),
  });

  // 刷新令牌
  const refreshQuotaMutation = useCrudMutation({
    mutationFn: (id: number) => accountsApi.refreshQuota(id),
    successMessage: t('accounts.refresh_quota_success'),
    queryKey: queryKeys.accounts(),
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
      render: (row) => <StatusBadge status={row.status} tooltip={row.error_msg || undefined} />,
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
            onClick={() => queryClient.invalidateQueries({ queryKey: queryKeys.accounts() })}
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
        pageSizeOptions={PAGE_SIZE_OPTIONS as unknown as number[]}
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
