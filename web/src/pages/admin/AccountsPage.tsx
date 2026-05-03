import { useState, useEffect, useRef, type ReactElement, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertDialog, Button, Checkbox, Chip, Dropdown, EmptyState, Input, Label, ListBox, Select, Spinner, Switch, Table as HeroTable, TextField as HeroTextField, Tooltip } from '@heroui/react';
import {
  Plus,
  Pencil,
  Trash2,
  Zap,
  MoreHorizontal,
  BarChart3,
  RefreshCw,
  ChevronDown,
  Search,
  Download,
  Upload,
  Eraser,
} from 'lucide-react';
import { useToast } from '../../shared/ui';
import { PlatformIcon } from '../../shared/ui';
import { accountsApi } from '../../shared/api/accounts';
import { groupsApi } from '../../shared/api/groups';
import { proxiesApi } from '../../shared/api/proxies';
import { AccountTestModal } from './AccountTestModal';
import { AccountStatsModal } from './AccountStatsModal';
import { usePlatforms } from '../../shared/hooks/usePlatforms';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { PAGE_SIZE_OPTIONS, FETCH_ALL_PARAMS } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { CreateAccountModal } from './accounts/CreateAccountModal';
import { EditAccountModal } from './accounts/EditAccountModal';
import { BulkActionsBar } from './accounts/BulkActionsBar';
import { BulkEditAccountModal } from './accounts/BulkEditAccountModal';
import { BulkRefreshProgressModal } from './accounts/BulkRefreshProgressModal';
import type {
  AccountResp,
  CreateAccountReq,
  UpdateAccountReq,
  BulkUpdateAccountsReq,
  BulkOpResp,
  AccountExportFile,
  AccountExportItem,
  PagedData,
} from '../../shared/types';

interface AccountTableColumn {
  key: string;
  title: ReactNode;
  width?: string;
  align?: 'left' | 'center' | 'right';
  hideOnMobile?: boolean;
  render: (row: AccountResp) => ReactNode;
}

function StatusPill({ status, tooltip }: { status: 'active' | 'disabled'; tooltip?: string }) {
  const { t } = useTranslation();
  const chip = (
    <Chip color={status === 'active' ? 'success' : 'default'} size="sm" variant="soft">
      {status === 'active' ? t('status.active') : t('status.disabled')}
    </Chip>
  );

  if (!tooltip) return chip;
  return (
    <Tooltip>
      <Tooltip.Trigger>{chip}</Tooltip.Trigger>
      <Tooltip.Content className="max-w-[360px] whitespace-pre-wrap">{tooltip}</Tooltip.Content>
    </Tooltip>
  );
}

// formatCountdown 把剩余毫秒格式化成 "Xd Yh"/"Xh Ym"/"Ym" 样式，
// 与 sub2api 的"限流中 10h 16m 自动恢复"徽标一致。
function formatCountdown(ms: number): string {
  if (ms <= 0) return '';
  const s = Math.floor(ms / 1000);
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m`;
  return `${sec}s`;
}

/**
 * AccountStatusCell 渲染账号状态徽标，按 state + state_until 动态展示：
 *   active       → 绿色 "活跃"
 *   rate_limited → 橙色 "限流中 Xh Ym"（state_until 倒计时）
 *   degraded     → 黄色 "降级 Xm"（池账号软降级，倒计时）
 *   disabled     → 红色 "已禁用"（tooltip 显示 error_msg）
 * 到期的 rate_limited / degraded 视作 active（后端 lazy 回收，前端可先显示 active）。
 *
 * 同一行还会叠加家族级冷却（family_cooldowns）：账号 state 可能仍是 active，
 * 但某个 family（如 gpt-image）在 Redis 上仍处冷却中。用一个橙色小 pill
 * 标出"限流家族数"，hover tooltip 列出每个家族剩余时间。
 */
function AccountStatusCell({ row }: { row: AccountResp }) {
  const { t } = useTranslation();
  const untilMs = row.state_until ? Date.parse(row.state_until) : 0;
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    if (!untilMs || untilMs <= now) {
      // 即使账号 state 不需要倒计时，也可能有家族冷却需要刷新显示
      if (!row.family_cooldowns || row.family_cooldowns.length === 0) return;
    }
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [untilMs, now, row.family_cooldowns]);

  const remainingMs = untilMs - now;
  const hasCountdown = untilMs > 0 && remainingMs > 0;

  // 过滤出仍生效的家族冷却（后端可能返回刚到期的）。
  const liveFamilyCooldowns = (row.family_cooldowns || []).filter(
    (fc) => Date.parse(fc.until) > now,
  );

  const pill = (label: string, bg: string, fg: string, tooltip?: string) => (
    <span
      className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-[11px] font-semibold border whitespace-nowrap"
      style={{ background: bg, color: fg, borderColor: bg }}
      title={tooltip}
    >
      <span className="w-1.5 h-1.5 rounded-full" style={{ background: fg }} />
      {label}
    </span>
  );

  // 主 state 徽标
  let mainBadge: ReactElement;
  if (row.state === 'rate_limited' && hasCountdown) {
    mainBadge = pill(
      `${t('accounts.rate_limited_label', '限流中')} ${formatCountdown(remainingMs)}`,
      'var(--ag-warning-subtle)',
      'var(--ag-warning)',
      t('accounts.rate_limited_tooltip', '上游限流，到期自动恢复，不影响调度开关'),
    );
  } else if (row.state === 'degraded' && hasCountdown) {
    mainBadge = pill(
      `${t('accounts.degraded_label', '降级')} ${formatCountdown(remainingMs)}`,
      'var(--ag-warning-subtle)',
      'var(--ag-warning)',
      t('accounts.degraded_tooltip', '上游池抖动，软降级仅做兜底，到期自动恢复'),
    );
  } else if (row.state === 'disabled') {
    mainBadge = <StatusPill status="disabled" tooltip={row.error_msg || undefined} />;
  } else {
    // active，或 rate_limited/degraded 已到期（lazy 恢复）
    mainBadge = <StatusPill status="active" />;
  }

  if (liveFamilyCooldowns.length === 0) {
    return mainBadge;
  }

  // tooltip 多行：每个家族 + 剩余时间，rate-limit 原因截断到 80 字符避免过宽
  const familyTooltip = liveFamilyCooldowns
    .map((fc) => {
      const ms = Date.parse(fc.until) - now;
      const reason = fc.reason ? ` — ${fc.reason.slice(0, 80)}` : '';
      return `${fc.family} ${formatCountdown(ms)}${reason}`;
    })
    .join('\n');

  const familyLabel = t(
    'accounts.family_cooldown_label',
    '{{count}} 家族限流',
    { count: liveFamilyCooldowns.length },
  );

  return (
    <div className="inline-flex flex-wrap items-center gap-1">
      {mainBadge}
      {pill(
        familyLabel,
        'var(--ag-warning-subtle)',
        'var(--ag-warning)',
        familyTooltip,
      )}
    </div>
  );
}

export default function AccountsPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { platforms, platformName } = usePlatforms();
  const { toast } = useToast();

  const applyQuotaRefreshResult = (
    id: number,
    result: Awaited<ReturnType<typeof accountsApi.refreshQuota>>,
  ) => {
    queryClient.setQueriesData<PagedData<AccountResp>>(
      { queryKey: queryKeys.accounts() },
      (old) => {
        if (!old?.list?.length) return old;

        let matched = false;
        const list = old.list.map((account) => {
          if (account.id !== id) return account;
          matched = true;
          return {
            ...account,
            credentials: {
              ...account.credentials,
              ...(result.plan_type !== undefined ? { plan_type: result.plan_type } : {}),
              ...(result.email !== undefined ? { email: result.email } : {}),
              ...(result.subscription_active_until !== undefined
                ? { subscription_active_until: result.subscription_active_until }
                : {}),
            },
          };
        });

        return matched ? { ...old, list } : old;
      },
    );
  };

  const PLATFORM_OPTIONS = [
    { id: '', label: t('accounts.all_platforms') },
    ...platforms.map((p) => ({ id: p, label: platformName(p) })),
  ];

  const STATE_OPTIONS = [
    { id: '', label: t('users.all_status') },
    { id: 'active', label: t('status.active') },
    { id: 'rate_limited', label: t('status.rate_limited', '限流中') },
    { id: 'degraded', label: t('status.degraded', '降级中') },
    { id: 'disabled', label: t('status.disabled') },
  ];

  // 筛选状态
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [keyword, setKeyword] = useState('');
  const [platformFilter, setPlatformFilter] = useState('');
  const [stateFilter, setStateFilter] = useState('');
  const [typeFilter, setTypeFilter] = useState('');
  const [groupFilter, setGroupFilter] = useState('');
  const [proxyFilter, setProxyFilter] = useState('');

  // 自动刷新
  const AUTO_REFRESH_OPTIONS = [0, 5, 10, 15, 30];
  const [autoRefresh, setAutoRefresh] = useState(0); // 秒，0=关闭
  const [countdown, setCountdown] = useState(0);

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

  // 批量选择状态
  const [selectedIds, setSelectedIds] = useState<number[]>([]);
  const [showBulkEditModal, setShowBulkEditModal] = useState(false);
  const [showBulkDeleteConfirm, setShowBulkDeleteConfirm] = useState(false);
  const [bulkRefreshTargets, setBulkRefreshTargets] = useState<{ id: number; name: string }[] | null>(null);
  const clearSelection = () => setSelectedIds([]);

  // 切换筛选/分页时清空选择，避免不可见行仍被选中导致误操作
  useEffect(() => {
    setSelectedIds([]);
  }, [page, pageSize, keyword, platformFilter, stateFilter, typeFilter, groupFilter, proxyFilter]);

  // 查询账号列表
  const { data, isLoading } = useQuery({
    queryKey: queryKeys.accounts(page, pageSize, keyword, platformFilter, stateFilter, typeFilter, groupFilter, proxyFilter),
    queryFn: () =>
      accountsApi.list({
        page,
        page_size: pageSize,
        keyword: keyword || undefined,
        platform: platformFilter || undefined,
        state: stateFilter || undefined,
        account_type: typeFilter || undefined,
        group_id: groupFilter ? Number(groupFilter) : undefined,
        proxy_id: proxyFilter ? Number(proxyFilter) : undefined,
      }),
    placeholderData: keepPreviousData,
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
  // 代理列表（只用于顶部筛选器；之前的"代理"列已移除）
  const { data: allProxiesData } = useQuery({
    queryKey: queryKeys.proxiesAll(),
    queryFn: () => proxiesApi.list(FETCH_ALL_PARAMS),
  });

  // 查询用量窗口
  const { data: usageData } = useQuery({
    queryKey: queryKeys.accountUsage(platformFilter),
    queryFn: () => accountsApi.usage(platformFilter || ''),
    refetchInterval: 300_000, // 每 5 分钟刷新
  });

  // 创建账号
  const createMutation = useCrudMutation({
    mutationFn: (data: CreateAccountReq) => accountsApi.create(data),
    successMessage: t('accounts.create_success'),
    queryKey: queryKeys.accounts(),
    onSuccess: () => {
      setShowCreateModal(false);
      // 创建账号后立即刷新用量窗口
      queryClient.invalidateQueries({ queryKey: queryKeys.accountUsage(platformFilter) });
    },
  });

  // 导出账号：有选中项时仅导出选中账号；否则按当前筛选条件导出。
  const importInputRef = useRef<HTMLInputElement>(null);
  const exportMutation = useMutation({
    mutationFn: () => {
      if (selectedIds.length > 0) {
        return accountsApi.export({ ids: selectedIds });
      }
      return accountsApi.export({
        keyword: keyword || undefined,
        platform: platformFilter || undefined,
        state: stateFilter || undefined,
        account_type: typeFilter || undefined,
        group_id: groupFilter ? Number(groupFilter) : undefined,
        proxy_id: proxyFilter ? Number(proxyFilter) : undefined,
      });
    },
    onSuccess: (file: AccountExportFile) => {
      // 触发浏览器下载，文件名使用北京时间便于用户辨识。
      const blob = new Blob([JSON.stringify(file, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      const parts = new Intl.DateTimeFormat('en-GB', {
        timeZone: 'Asia/Shanghai',
        year: 'numeric', month: '2-digit', day: '2-digit',
        hour: '2-digit', minute: '2-digit', second: '2-digit',
        hour12: false,
      }).formatToParts(new Date());
      const pick = (type: string) => parts.find((p) => p.type === type)?.value ?? '';
      const ts = `${pick('year')}${pick('month')}${pick('day')}${pick('hour')}${pick('minute')}${pick('second')}`;
      a.href = url;
      a.download = `airgate-accounts-${ts}.json`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      toast('success', t('accounts.export_success', { count: file.count }));
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 导入账号
  const importMutation = useMutation({
    mutationFn: (accounts: AccountExportItem[]) => accountsApi.import(accounts),
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
      if (res.failed > 0) {
        toast('warning', t('accounts.import_partial', { imported: res.imported, failed: res.failed }));
      } else {
        toast('success', t('accounts.import_success', { count: res.imported }));
      }
    },
    onError: (err: Error) => toast('error', err.message),
  });

  function handleImportFile(event: React.ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    // 重置 input，允许重复选择同一文件
    if (importInputRef.current) importInputRef.current.value = '';
    if (!file) return;

    const reader = new FileReader();
    reader.onload = () => {
      try {
        const parsed = JSON.parse(reader.result as string);
        const accounts: AccountExportItem[] = Array.isArray(parsed) ? parsed : parsed.accounts;
        if (!Array.isArray(accounts) || accounts.length === 0) {
          toast('error', t('accounts.import_invalid'));
          return;
        }
        importMutation.mutate(accounts);
      } catch {
        toast('error', t('accounts.import_invalid'));
      }
    };
    reader.onerror = () => toast('error', t('accounts.import_invalid'));
    reader.readAsText(file);
  }

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

  // 刷新令牌：后端在 refresh_token 已失效但能从 access_token JWT 解析到 plan_type
  // 时，会以 reauth_warning 形式回传降级提示；此时提示用户重新授权而不是弹 success。
  const refreshQuotaMutation = useMutation({
    mutationFn: (id: number) => accountsApi.refreshQuota(id),
    onSuccess: (res, id) => {
      applyQuotaRefreshResult(id, res);
      queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
      if (res?.reauth_warning) {
        toast('warning', t('accounts.refresh_quota_reauth_warning'));
      } else {
        toast('success', t('accounts.refresh_quota_success'));
      }
    },
    onError: (err: Error) => toast('error', err.message),
  });

  const clearRateLimitMarkersMutation = useMutation({
    mutationFn: (id: number) => accountsApi.clearFamilyCooldowns(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
      queryClient.invalidateQueries({ queryKey: queryKeys.accountUsage(platformFilter) });
      toast('success', t('accounts.clear_family_cooldowns_success'));
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 批量操作通用的结果处理：全部成功 → success toast；部分成功 → warning；全部失败 → error。
  const handleBulkResult = (res: BulkOpResp, okKey: string) => {
    queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
    const total = res.success + res.failed;
    if (res.failed === 0) {
      toast('success', t(okKey, { count: res.success }));
    } else if (res.success === 0) {
      toast('error', t('accounts.bulk_all_failed'));
    } else {
      toast('warning', t('accounts.bulk_partial', { success: res.success, failed: res.failed, total }));
    }
    clearSelection();
  };

  // 批量更新
  const bulkUpdateMutation = useMutation({
    mutationFn: (data: BulkUpdateAccountsReq) => accountsApi.bulkUpdate(data),
    onSuccess: (res) => {
      handleBulkResult(res, 'accounts.bulk_update_success');
      setShowBulkEditModal(false);
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 批量删除
  const bulkDeleteMutation = useMutation({
    mutationFn: (ids: number[]) => accountsApi.bulkDelete(ids),
    onSuccess: (res) => {
      handleBulkResult(res, 'accounts.bulk_delete_success');
      setShowBulkDeleteConfirm(false);
    },
    onError: (err: Error) => toast('error', err.message),
  });

  const bulkClearRateLimitMarkersMutation = useMutation({
    mutationFn: (ids: number[]) => accountsApi.bulkClearFamilyCooldowns(ids),
    onSuccess: (res) => {
      handleBulkResult(res, 'accounts.bulk_clear_family_cooldowns_success');
      queryClient.invalidateQueries({ queryKey: queryKeys.accountUsage(platformFilter) });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  const handleBulkEnable = () =>
    bulkUpdateMutation.mutate({ account_ids: selectedIds, state: 'active' });
  const handleBulkDisable = () =>
    bulkUpdateMutation.mutate({ account_ids: selectedIds, state: 'disabled' });

  // 批量刷新令牌：只有 OAuth 类型账号支持，预先过滤后开进度弹窗
  const handleBulkRefresh = () => {
    const selectedRows = (data?.list ?? []).filter((a) => selectedIds.includes(a.id));
    const oauthRows = selectedRows
      .filter((a) => a.type === 'oauth')
      .map((a) => ({ id: a.id, name: a.name }));
    if (oauthRows.length === 0) {
      toast('warning', t('accounts.bulk_refresh_no_oauth'));
      return;
    }
    if (oauthRows.length < selectedIds.length) {
      toast('info', t('accounts.bulk_refresh_filtered', {
        count: oauthRows.length,
        skipped: selectedIds.length - oauthRows.length,
      }));
    }
    setBulkRefreshTargets(oauthRows);
  };

  // 统计弹窗
  const [statsAccountId, setStatsAccountId] = useState<number | null>(null);

  // 表格列定义
  const columns: AccountTableColumn[] = [
    {
      key: 'name',
      title: t('common.name'),
      width: '150px',
      render: (row) => {
        const email = row.credentials?.email;
        return (
          <div className="flex flex-col items-center">
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
        const subUntil = row.credentials?.subscription_active_until;
        const subExpired = subUntil ? new Date(subUntil) < new Date() : false;
        const hasQuotaMetadata = row.platform === 'openai' && row.type === 'oauth' && (
          planType !== undefined || row.credentials?.email !== undefined || subUntil !== undefined
        );
        const rawDisplayPlanType = planType || (hasQuotaMetadata ? 'free' : '');
        // 订阅过期时降级显示为 free
        const displayPlanType = (rawDisplayPlanType && subExpired && rawDisplayPlanType.toLowerCase() !== 'free') ? 'free' : rawDisplayPlanType;
        // 仅未过期的付费订阅 hover 显示过期时间
        const isPaid = displayPlanType && displayPlanType.toLowerCase() !== 'free';
        const planTooltip = isPaid && subUntil && !subExpired
          ? `${t('accounts.expires_at')}: ${new Date(subUntil).toLocaleDateString()}`
          : undefined;
        return (
          <div className="flex flex-col items-center gap-1.5">
            <span className="inline-flex items-center gap-1">
              <PlatformIcon platform={row.platform} className="w-3.5 h-3.5" />
              <span>{platformName(row.platform)}</span>
            </span>
            <div className="flex items-center gap-1">
              {row.type && (
                <span className="text-[10px] px-1 py-0 rounded" style={{ background: 'var(--ag-bg-surface)', border: '1px solid var(--ag-glass-border)', color: 'var(--ag-text-secondary)' }}>
                  {{ oauth: 'OAuth', session_key: 'Session Key', apikey: 'API Key' }[row.type] ?? row.type}
                </span>
              )}
              {displayPlanType && (
                <span className="text-[10px] px-1 py-0 rounded font-medium cursor-default" title={planTooltip} style={{ background: 'var(--ag-primary)', color: 'var(--ag-text-inverse)', opacity: 0.85 }}>
                  {displayPlanType.charAt(0).toUpperCase() + displayPlanType.slice(1)}
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
      width: '84px',
      render: (row) => <AccountStatusCell row={row} />,
    },
    {
      key: 'scheduling',
      title: t('accounts.scheduling'),
      width: '80px',
      hideOnMobile: true,
      render: (row) => (
        <Switch
          aria-label={t('accounts.scheduling')}
          isDisabled={toggleMutation.isPending}
          isSelected={row.state !== 'disabled'}
          size="sm"
          onChange={() => {
            toggleMutation.mutate(row.id);
          }}
        >
          <Switch.Control>
            <Switch.Thumb />
          </Switch.Control>
        </Switch>
      ),
    },
    {
      key: 'rate_multiplier',
      title: t('accounts.rate_multiplier'),
      width: '80px',
      hideOnMobile: true,
      render: (row) => (
        <span className="font-mono" style={{ color: 'var(--ag-primary)' }}>
          {row.rate_multiplier}x
        </span>
      ),
    },
    {
      key: 'groups',
      title: t('accounts.groups'),
      width: '140px',
      align: 'center',
      hideOnMobile: true,
      render: (row) => {
        if (!row.group_ids || row.group_ids.length === 0) {
          return <span style={{ color: 'var(--ag-text-tertiary)' }}>-</span>;
        }
        return (
          <div className="flex flex-col items-center gap-1">
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
    // 用量窗口 —— 始终显示该列。历史上这里用 accounts.length > 0 作为
    // 显示门槛，但当插件尚未加载 / 上游 quota 接口都超时等边缘情况下，后端
    // 可能返回空 accounts map 导致整列消失。那样用户连点"刷新用量"的入口都
    // 没有。正确做法是：列始终在，每一行的 cell 自己处理 usage 缺失显示 "-"。
    ...[{
      key: 'usage_window',
      title: t('accounts.usage_window'),
      width: '260px',
      hideOnMobile: true,
      render: (row: AccountResp) => {
        const usage = usageData?.accounts?.[String(row.id)];

        // 整个区域可点击刷新
        const handleRefreshClick = async (e: React.MouseEvent) => {
          e.stopPropagation();
          const target = e.currentTarget as HTMLElement;
          target.style.opacity = '0.5';
          target.style.pointerEvents = 'none';
          try {
            const result = await accountsApi.refreshQuota(row.id);
            applyQuotaRefreshResult(row.id, result);
            queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
            queryClient.invalidateQueries({ queryKey: queryKeys.accountUsage(platformFilter) });
            toast('success', t('accounts.refresh_usage_success', '用量刷新成功'));
          } catch (err) {
            // 展开后端返回的具体原因（如"账号凭证已失效，请重新授权"）；
            // 没有 message 时才回退到通用文案。
            const message = err instanceof Error && err.message ? err.message : t('accounts.refresh_usage_failed', '用量刷新失败');
            toast('error', message);
          }
          target.style.opacity = '1';
          target.style.pointerEvents = '';
        };

        if (!usage) {
          // 非活跃账号（backend 没 seed 占位）或平台不支持：显示占位 + 刷新
          return (
            <div
              className="flex items-center gap-1 cursor-pointer rounded px-1 py-0.5 transition-colors hover:bg-[var(--ag-glass-border)]"
              title={t('accounts.refresh_usage', '点击刷新用量')}
              onClick={handleRefreshClick}
            >
              <span style={{ color: 'var(--ag-text-tertiary)' }}>-</span>
              <RefreshCw size={11} style={{ color: 'var(--ag-text-tertiary)' }} />
            </div>
          );
        }

        type TodayStats = { requests: number; tokens: number; account_cost: number; user_cost: number };
        type UsageWindow = { label: string; used_percent: number; reset_seconds: number };
        const windows: UsageWindow[] = usage.windows || [];
        const credits: { balance: number; unlimited: boolean } | null = usage.credits || null;
        const todayStats: TodayStats | null = usage.today_stats || null;

        // 紧凑数字格式化（和 sub2api 对齐：K / M / B 后缀）
        const formatCompact = (num: number, allowBillions = true) => {
          if (!num) return '0';
          const abs = Math.abs(num);
          if (allowBillions && abs >= 1_000_000_000) return `${(num / 1_000_000_000).toFixed(1)}B`;
          if (abs >= 1_000_000) return `${(num / 1_000_000).toFixed(1)}M`;
          if (abs >= 1_000) return `${(num / 1_000).toFixed(1)}K`;
          return String(num);
        };

        const hasTodayStats = !!todayStats && (todayStats.requests > 0 || todayStats.tokens > 0);
        const canRefresh = row.type !== 'apikey';
        if (windows.length === 0 && !credits && !hasTodayStats) {
          return (
            <div
              className={
                canRefresh
                  ? 'flex items-center gap-1 cursor-pointer rounded px-1 py-0.5 transition-colors hover:bg-[var(--ag-glass-border)]'
                  : 'flex items-center gap-1 rounded px-1 py-0.5'
              }
              title={canRefresh ? t('accounts.refresh_usage', '点击刷新用量') : undefined}
              onClick={canRefresh ? handleRefreshClick : undefined}
            >
              <span style={{ color: 'var(--ag-text-tertiary)' }}>-</span>
              {canRefresh && <RefreshCw size={11} style={{ color: 'var(--ag-text-tertiary)' }} />}
            </div>
          );
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
        const todayMetricClass = 'inline-grid h-5 min-w-0 grid-cols-[2.5rem_1fr] items-center gap-1 rounded-[var(--field-radius)] border px-1.5 text-[10px] leading-none shadow-sm';
        const todayMetricStyle = (color: string, foreground = color) => ({
          background: `color-mix(in srgb, ${color} 10%, transparent)`,
          borderColor: `color-mix(in srgb, ${color} 22%, var(--ag-border))`,
          color: foreground,
        });

        return (
          <div
            className={
              canRefresh
                ? 'flex flex-col gap-1.5 text-[11px] cursor-pointer rounded px-1 py-0.5 transition-colors hover:bg-[var(--ag-glass-border)]'
                : 'flex flex-col gap-1.5 text-[11px] rounded px-1 py-0.5'
            }
            style={{ fontFamily: 'var(--ag-font-mono)', minWidth: 232, width: '100%' }}
            title={canRefresh ? t('accounts.refresh_usage', '点击刷新用量') : undefined}
            onClick={canRefresh ? handleRefreshClick : undefined}
          >
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
            {hasTodayStats && todayStats && (
              <div
                className="mt-0.5 grid grid-cols-2 gap-1"
                title={t('accounts.today_stats_tooltip', '今日账号消耗（本地时区自然日）')}
              >
                <span className={todayMetricClass} style={todayMetricStyle('var(--ag-info)')}>
                  <span className="truncate text-text-tertiary">{t('accounts.today_access_count', '访问')}</span>
                  <span className="text-right font-semibold tabular-nums">{formatCompact(todayStats.requests, false)}</span>
                </span>
                <span className={todayMetricClass} style={todayMetricStyle('var(--ag-primary)')}>
                  <span className="truncate text-text-tertiary">Token</span>
                  <span className="text-right font-semibold tabular-nums">{formatCompact(todayStats.tokens)}</span>
                </span>
                <span
                  className={todayMetricClass}
                  style={todayMetricStyle('var(--ag-warning)')}
                  title={t('accounts.window_account_cost', '账号成本（上游计费）')}
                >
                  <span className="truncate text-text-tertiary">{t('accounts.account_cost_short', '成本')}</span>
                  <span className="text-right tabular-nums">${todayStats.account_cost.toFixed(2)}</span>
                </span>
                <span
                  className={todayMetricClass}
                  style={todayMetricStyle('var(--ag-success)', 'var(--ag-success-foreground)')}
                  title={t('accounts.window_user_cost', '用户消耗（平台计费）')}
                >
                  <span className="truncate text-text-tertiary">{t('accounts.user_cost_short', '消费')}</span>
                  <span className="text-right tabular-nums">${todayStats.user_cost.toFixed(2)}</span>
                </span>
                {row.platform === 'openai' && (row.today_image_count ?? 0) > 0 && (
                  <span
                    className={todayMetricClass}
                    style={todayMetricStyle('var(--ag-success)', 'var(--ag-success-foreground)')}
                    title={t('accounts.image_count_tooltip', '今日生图请求数（gpt-image 系列）')}
                  >
                    <span className="truncate text-text-tertiary">{t('accounts.image_count_inline_label', '图')}</span>
                    <span className="text-right font-semibold tabular-nums">{formatCompact(row.today_image_count ?? 0, false)}</span>
                  </span>
                )}
              </div>
            )}
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
    }],
    {
      key: 'last_used_at',
      title: t('accounts.last_used'),
      width: '120px',
      hideOnMobile: true,
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
      key: 'actions',
      title: t('common.actions'),
      width: '128px',
      render: (row) => (
        <div className="flex items-center justify-center gap-1">
          <Button
            isIconOnly
            aria-label={t('common.edit')}
            size="sm"
            variant="secondary"
            onPress={() => setEditingAccount(row)}
          >
            <Pencil className="w-3.5 h-3.5" />
          </Button>
          <Button
            isIconOnly
            aria-label={t('common.delete')}
            size="sm"
            variant="danger-soft"
            className="text-danger"
            onPress={() => setDeletingAccount(row)}
          >
            <Trash2 className="w-3.5 h-3.5" />
          </Button>
          <Dropdown>
            <Dropdown.Trigger>
              <Button isIconOnly aria-label={t('common.more')} size="sm" variant="secondary">
                <MoreHorizontal className="w-3.5 h-3.5" />
              </Button>
            </Dropdown.Trigger>
            <Dropdown.Popover placement="bottom end">
              <Dropdown.Menu
                aria-label={t('common.actions')}
                onAction={(key) => {
                  switch (String(key)) {
                    case 'test':
                      setTestingAccount(row);
                      break;
                    case 'stats':
                      setStatsAccountId(row.id);
                      break;
                    case 'refresh_quota':
                      refreshQuotaMutation.mutate(row.id);
                      break;
                    case 'clear_cooldowns':
                      clearRateLimitMarkersMutation.mutate(row.id);
                      break;
                  }
                }}
              >
                <Dropdown.Item id="test" textValue={t('accounts.test_connection')}>
                  <span className="flex items-center gap-2">
                    <Zap className="w-3.5 h-3.5" style={{ color: 'var(--ag-warning)' }} />
                    {t('accounts.test_connection')}
                  </span>
                </Dropdown.Item>
                <Dropdown.Item id="stats" textValue={t('accounts.view_stats')}>
                  <span className="flex items-center gap-2">
                    <BarChart3 className="w-3.5 h-3.5" style={{ color: 'var(--ag-primary)' }} />
                    {t('accounts.view_stats')}
                  </span>
                </Dropdown.Item>
                {row.type === 'oauth' ? (
                  <Dropdown.Item id="refresh_quota" textValue={t('accounts.refresh_quota')}>
                    <span className="flex items-center gap-2">
                      <RefreshCw className="w-3.5 h-3.5" style={{ color: 'var(--ag-success)' }} />
                      {t('accounts.refresh_quota')}
                    </span>
                  </Dropdown.Item>
                ) : null}
                <Dropdown.Item id="clear_cooldowns" textValue={t('accounts.clear_family_cooldowns')}>
                  <span className="flex items-center gap-2">
                    <Eraser className="w-3.5 h-3.5" style={{ color: 'var(--ag-warning)' }} />
                    {t('accounts.clear_family_cooldowns')}
                  </span>
                </Dropdown.Item>
              </Dropdown.Menu>
            </Dropdown.Popover>
          </Dropdown>
        </div>
      ),
    },
  ];
  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);
  const selectedKeys = new Set(selectedIds.map(String));
  const typeOptions = [
    { id: '', label: t('accounts.all_types', '全部类型') },
    { id: 'oauth', label: 'OAuth' },
    { id: 'apikey', label: 'API Key' },
  ];
  const groupOptions = [
    { id: '', label: t('accounts.all_groups') },
    ...(allGroupsData?.list ?? []).map((g) => ({ id: String(g.id), label: g.name })),
  ];
  const proxyOptions = [
    { id: '', label: t('accounts.all_proxies') },
    ...(allProxiesData?.list ?? []).map((p) => ({ id: String(p.id), label: p.name })),
  ];
  const selectedPlatformLabel = PLATFORM_OPTIONS.find((item) => item.id === platformFilter)?.label ?? t('accounts.all_platforms');
  const selectedStateLabel = STATE_OPTIONS.find((item) => item.id === stateFilter)?.label ?? t('users.all_status');
  const selectedTypeLabel = typeOptions.find((item) => item.id === typeFilter)?.label ?? t('accounts.all_types', '全部类型');
  const selectedGroupLabel = groupOptions.find((item) => item.id === groupFilter)?.label ?? t('accounts.all_groups');
  const selectedProxyLabel = proxyOptions.find((item) => item.id === proxyFilter)?.label ?? t('accounts.all_proxies');

  return (
    <div>
      {/* 筛选 */}
      <div className="flex items-end gap-3 mb-5 flex-wrap">
        <div className="w-full sm:w-[200px]">
          <HeroTextField fullWidth>
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
              <Input
                className="pl-9"
                value={keyword}
                onChange={(e) => { setKeyword(e.target.value); setPage(1); }}
                placeholder={t('common.search')}
              />
            </div>
          </HeroTextField>
        </div>
        <div className="w-40">
          <Select
            fullWidth
            selectedKey={platformFilter}
            onSelectionChange={(key) => { setPlatformFilter(key == null ? '' : String(key)); setPage(1); }}
          >
            <Label className="sr-only">{t('groups.platform')}</Label>
            <Select.Trigger>
              <Select.Value>{selectedPlatformLabel}</Select.Value>
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox items={PLATFORM_OPTIONS}>
                {(item) => (
                  <ListBox.Item id={item.id} textValue={item.label}>
                    {item.label}
                  </ListBox.Item>
                )}
              </ListBox>
            </Select.Popover>
          </Select>
        </div>
        <div className="w-40">
          <Select
            fullWidth
            selectedKey={stateFilter}
            onSelectionChange={(key) => { setStateFilter(key == null ? '' : String(key)); setPage(1); }}
          >
            <Label className="sr-only">{t('common.status')}</Label>
            <Select.Trigger>
              <Select.Value>{selectedStateLabel}</Select.Value>
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox items={STATE_OPTIONS}>
                {(item) => (
                  <ListBox.Item id={item.id} textValue={item.label}>
                    {item.label}
                  </ListBox.Item>
                )}
              </ListBox>
            </Select.Popover>
          </Select>
        </div>
        <div className="w-40">
          <Select
            fullWidth
            selectedKey={typeFilter}
            onSelectionChange={(key) => { setTypeFilter(key == null ? '' : String(key)); setPage(1); }}
          >
            <Label className="sr-only">{t('common.type')}</Label>
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
        </div>
        <div className="w-40">
          <Select
            fullWidth
            selectedKey={groupFilter}
            onSelectionChange={(key) => { setGroupFilter(key == null ? '' : String(key)); setPage(1); }}
          >
            <Label className="sr-only">{t('accounts.group')}</Label>
            <Select.Trigger>
              <Select.Value>{selectedGroupLabel}</Select.Value>
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox items={groupOptions}>
                {(item) => (
                  <ListBox.Item id={item.id} textValue={item.label}>
                    {item.label}
                  </ListBox.Item>
                )}
              </ListBox>
            </Select.Popover>
          </Select>
        </div>
        <div className="w-40">
          <Select
            fullWidth
            selectedKey={proxyFilter}
            onSelectionChange={(key) => { setProxyFilter(key == null ? '' : String(key)); setPage(1); }}
          >
            <Label className="sr-only">{t('accounts.proxy')}</Label>
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

        {/* 刷新 & 自动刷新 & 创建 */}
        <div className="flex items-center gap-2 ml-auto">
          <Button
            isIconOnly
            aria-label={t('common.refresh')}
            variant="ghost"
            onPress={() => queryClient.invalidateQueries({ queryKey: queryKeys.accounts() })}
          >
            <RefreshCw className="w-4 h-4" />
          </Button>
          <Dropdown>
            <Dropdown.Trigger>
              <Button size="sm" variant={autoRefresh ? 'secondary' : 'ghost'}>
                {autoRefresh ? `${t('accounts.auto_refresh')}${countdown}s` : t('accounts.auto_refresh_off')}
                <ChevronDown className="w-3 h-3" />
              </Button>
            </Dropdown.Trigger>
            <Dropdown.Popover placement="bottom end">
              <Dropdown.Menu
                aria-label={t('accounts.auto_refresh')}
                selectedKeys={new Set([String(autoRefresh)])}
                selectionMode="single"
                onAction={(key) => setAutoRefresh(Number(key))}
              >
                {AUTO_REFRESH_OPTIONS.map((sec) => (
                  <Dropdown.Item key={sec} id={String(sec)} textValue={sec === 0 ? t('accounts.auto_refresh_off') : `${sec}s`}>
                    <span className="flex items-center justify-between gap-4">
                      <span>{sec === 0 ? t('accounts.auto_refresh_off') : `${sec}s`}</span>
                      {autoRefresh === sec ? <span className="text-primary">✓</span> : null}
                    </span>
                  </Dropdown.Item>
                ))}
              </Dropdown.Menu>
            </Dropdown.Popover>
          </Dropdown>
          <Button
            variant="secondary"
            onPress={() => importInputRef.current?.click()}
            isDisabled={importMutation.isPending}
            aria-busy={importMutation.isPending}
          >
            <Upload className="w-4 h-4" />
            {t('accounts.import')}
          </Button>
          <Button
            variant="secondary"
            onPress={() => exportMutation.mutate()}
            isDisabled={exportMutation.isPending}
            aria-busy={exportMutation.isPending}
          >
            <Download className="w-4 h-4" />
            {t('accounts.export')}
          </Button>
          <Button variant="primary" onPress={() => setShowCreateModal(true)}>
            <Plus className="w-4 h-4" />
            {t('accounts.create')}
          </Button>
        </div>
      </div>
      {/* 隐藏的文件选择器（供导入按钮触发） */}
      <input
        ref={importInputRef}
        type="file"
        accept="application/json,.json"
        className="hidden"
        onChange={handleImportFile}
      />

      {/* 批量操作工具栏 */}
      <BulkActionsBar
        selectedCount={selectedIds.length}
        onClear={clearSelection}
        onEdit={() => setShowBulkEditModal(true)}
        onEnable={handleBulkEnable}
        onDisable={handleBulkDisable}
        onRefreshQuota={handleBulkRefresh}
        onClearRateLimitMarkers={() => bulkClearRateLimitMarkersMutation.mutate(selectedIds)}
        onDelete={() => setShowBulkDeleteConfirm(true)}
      />

      {/* 表格 */}
      <HeroTable variant="primary">
        <HeroTable.ScrollContainer>
          <HeroTable.Content
            aria-label={t('accounts.title', 'Accounts')}
            selectionMode="multiple"
            selectedKeys={selectedKeys}
            onSelectionChange={(keys) => {
              if (keys === 'all') {
                setSelectedIds(rows.map((row) => row.id));
                return;
              }
              setSelectedIds(Array.from(keys).map((key) => Number(key)));
            }}
          >
            <HeroTable.Header>
              <HeroTable.Column id="__selection__" style={{ width: 52 }}>
                <Checkbox slot="selection" aria-label={t('common.select_all', 'Select all')} />
              </HeroTable.Column>
              {columns.map((column) => (
                <HeroTable.Column
                  id={column.key}
                  key={column.key}
                  className={column.hideOnMobile ? 'hidden md:table-cell' : undefined}
                  style={column.width ? { minWidth: column.width, width: column.width } : undefined}
                >
                  {column.title}
                </HeroTable.Column>
              ))}
            </HeroTable.Header>
            <HeroTable.Body>
              {isLoading ? (
                <TableLoadingRow colSpan={columns.length + 1} />
              ) : rows.length === 0 ? (
                <HeroTable.Row id="empty">
                  <HeroTable.Cell colSpan={columns.length + 1}>
                    <EmptyState />
                  </HeroTable.Cell>
                </HeroTable.Row>
              ) : (
                rows.map((row) => (
                  <HeroTable.Row id={String(row.id)} key={row.id}>
                    <HeroTable.Cell>
                      <Checkbox slot="selection" aria-label={t('common.select', 'Select')} />
                    </HeroTable.Cell>
                    {columns.map((column) => (
                      <HeroTable.Cell
                        key={column.key}
                        className={column.hideOnMobile ? 'hidden md:table-cell' : undefined}
                      >
                        <div
                          className={`flex items-center ${
                            column.align === 'left'
                              ? 'justify-start'
                              : column.align === 'right'
                                ? 'justify-end'
                                : 'justify-center'
                          }`}
                        >
                          {column.render(row)}
                        </div>
                      </HeroTable.Cell>
                    ))}
                  </HeroTable.Row>
                ))
              )}
            </HeroTable.Body>
          </HeroTable.Content>
        </HeroTable.ScrollContainer>
        <HeroTable.Footer>
          <TablePaginationFooter
            page={page}
            pageSize={pageSize}
            pageSizeOptions={PAGE_SIZE_OPTIONS}
            setPage={setPage}
            setPageSize={setPageSize}
            total={total}
            totalPages={totalPages}
          />
        </HeroTable.Footer>
      </HeroTable>

      {/* 创建弹窗 */}
      <CreateAccountModal
        open={showCreateModal}
        onClose={() => setShowCreateModal(false)}
        onSubmit={(data) => createMutation.mutate(data)}
        onBatchImport={async (accounts) => {
          const res = await accountsApi.import(accounts);
          queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
          queryClient.invalidateQueries({ queryKey: queryKeys.accountUsage(platformFilter) });
          if (res.failed > 0) {
            toast('warning', t('accounts.import_partial', { imported: res.imported, failed: res.failed }));
          } else {
            toast('success', t('accounts.import_success', { count: res.imported }));
          }
          setShowCreateModal(false);
          return { imported: res.imported, failed: res.failed };
        }}
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
      <AlertDialog
        isOpen={!!deletingAccount}
        onOpenChange={(open) => {
          if (!open) setDeletingAccount(null);
        }}
      >
        <AlertDialog.Backdrop>
          <AlertDialog.Container placement="center" size="sm">
            <AlertDialog.Dialog className="ag-elevation-modal">
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t('accounts.delete_title')}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>{t('accounts.delete_confirm', { name: deletingAccount?.name })}</AlertDialog.Body>
              <AlertDialog.Footer>
                <Button variant="secondary" onPress={() => setDeletingAccount(null)}>
                  {t('common.cancel')}
                </Button>
                <Button
                  aria-busy={deleteMutation.isPending}
                  isDisabled={deleteMutation.isPending}
                  variant="danger"
                  onPress={() => deletingAccount && deleteMutation.mutate(deletingAccount.id)}
                >
                  {deleteMutation.isPending ? <Spinner size="sm" /> : null}
                  {t('common.confirm')}
                </Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>

      {/* 批量编辑弹窗 */}
      <BulkEditAccountModal
        open={showBulkEditModal}
        count={selectedIds.length}
        onClose={() => setShowBulkEditModal(false)}
        onSubmit={(patch) =>
          bulkUpdateMutation.mutate({ account_ids: selectedIds, ...patch })
        }
        loading={bulkUpdateMutation.isPending}
      />

      {/* 批量删除确认 */}
      <AlertDialog isOpen={showBulkDeleteConfirm} onOpenChange={setShowBulkDeleteConfirm}>
        <AlertDialog.Backdrop>
          <AlertDialog.Container placement="center" size="sm">
            <AlertDialog.Dialog className="ag-elevation-modal">
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t('accounts.bulk_delete_title')}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>{t('accounts.bulk_delete_confirm', { count: selectedIds.length })}</AlertDialog.Body>
              <AlertDialog.Footer>
                <Button variant="secondary" onPress={() => setShowBulkDeleteConfirm(false)}>
                  {t('common.cancel')}
                </Button>
                <Button
                  aria-busy={bulkDeleteMutation.isPending}
                  isDisabled={bulkDeleteMutation.isPending}
                  variant="danger"
                  onPress={() => bulkDeleteMutation.mutate(selectedIds)}
                >
                  {bulkDeleteMutation.isPending ? <Spinner size="sm" /> : null}
                  {t('common.confirm')}
                </Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>

      {/* 批量刷新令牌进度弹窗 */}
      {bulkRefreshTargets && (
        <BulkRefreshProgressModal
          open
          accounts={bulkRefreshTargets}
          onClose={() => setBulkRefreshTargets(null)}
          onFinished={() => {
            queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
            clearSelection();
          }}
        />
      )}

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
          // 累计生图数从列表行直接传：BatchImageStats 一次查到，避免再让 stats endpoint 多跑一次。
          // 仅 OpenAI 平台账号有该字段；非 openai 时 modal 内部会跳过显示。
          lifetimeImageCount={data?.list.find((a) => a.id === statsAccountId)?.total_image_count}
          onClose={() => setStatsAccountId(null)}
        />
      )}
    </div>
  );
}
