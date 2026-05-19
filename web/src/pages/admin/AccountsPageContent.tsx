import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertDialog, Button, Dropdown, EmptyState, Input, Label, ListBox, Select, Spinner, TextField as HeroTextField } from '@heroui/react';
import {
  Plus,
  RefreshCw,
  ChevronDown,
  Search,
  Download,
  Upload,
} from 'lucide-react';
import { useToast } from '../../shared/ui';
import { accountsApi } from '../../shared/api/accounts';
import { groupsApi } from '../../shared/api/groups';
import { proxiesApi } from '../../shared/api/proxies';
import { AccountTestModal } from './AccountTestModal';
import { AccountStatsModal } from './AccountStatsModal';
import { usePlatforms } from '../../shared/hooks/usePlatforms';
import {
  getAccountIdentityVersion,
  subscribeAccountIdentityChange,
} from '../../app/plugin-frontend-registry';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { useDebouncedValue } from '../../shared/hooks/useDebouncedValue';
import { usePagination } from '../../shared/hooks/usePagination';
import { queryKeys } from '../../shared/queryKeys';
import { PAGE_SIZE_OPTIONS, FETCH_ALL_PARAMS } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { DialogTriggerShim } from '../../shared/components/DialogTriggerShim';
import { CreateAccountModal } from './accounts/CreateAccountModal';
import { EditAccountModal } from './accounts/EditAccountModal';
import { AccountTypeFilterSelect } from './accounts/AccountTypeFilterSelect';
import { useAccountTableColumns } from './accounts/useAccountTableColumns';
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

import {
  ACCOUNT_SELECTION_COLUMN_STYLE,
  AccountSelectionStore,
  AccountTableRow,
  AccountsTableLoadingRow,
  AutoRefreshCountdownLabel,
  TableSelectionCheckbox,
  UNGROUPED_GROUP_FILTER,
  columnAlignClass,
  columnWidthStyle,
  mergeCachedUsageWindows,
  runAfterInputFrame,
  useLatestRef,
  type AccountTypeFilterOption,
  type AccountUsageData,
  type AccountUsageInfo,
  type AccountUsageWindowCache,
} from './accounts/AccountPageSupport';

export default function AccountsPageContent() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { platforms, platformName: resolvePlatformName, oauthPlanFilters, isLoading: platformsLoading } = usePlatforms();
  const platformNameRef = useLatestRef(resolvePlatformName);
  const platformName = useCallback((platform: string) => platformNameRef.current(platform), [platformNameRef]);
  const platformsKey = platforms.join('\u0000');
  const { toast } = useToast();
  useSyncExternalStore(subscribeAccountIdentityChange, getAccountIdentityVersion);

  const applyQuotaRefreshResult = useCallback((
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
  }, [queryClient]);

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
  const { page, setPage, pageSize, setPageSize } = usePagination(20, 'admin.accounts');
  const [keyword, setKeyword] = useState('');
  const debouncedKeyword = useDebouncedValue(keyword.trim(), 250);
  const [platformFilter, setPlatformFilter] = useState('');
  const [stateFilter, setStateFilter] = useState('');
  const [typeFilter, setTypeFilter] = useState('');
  const [groupFilter, setGroupFilter] = useState('');
  const [proxyFilter, setProxyFilter] = useState('');

  // 自动刷新
  const AUTO_REFRESH_OPTIONS = [0, 5, 10, 15, 30];
  const [autoRefresh, setAutoRefresh] = useState(0); // 秒，0=关闭
  const refreshAccounts = useCallback(() => {
    queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
  }, [queryClient]);
  const autoRefreshLabel = t('accounts.auto_refresh');
  const autoRefreshOffLabel = t('accounts.auto_refresh_off');

  // 弹窗状态
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingAccount, setEditingAccount] = useState<AccountResp | null>(null);
  const [deletingAccount, setDeletingAccount] = useState<AccountResp | null>(null);
  const [testingAccount, setTestingAccount] = useState<AccountResp | null>(null);
  const [statsAccountId, setStatsAccountId] = useState<number | null>(null);

  // 批量选择状态
  const selectionStoreRef = useRef<AccountSelectionStore | null>(null);
  if (selectionStoreRef.current === null) {
    selectionStoreRef.current = new AccountSelectionStore();
  }
  const selectionStore = selectionStoreRef.current;
  const selectionVersion = useSyncExternalStore(
    selectionStore.subscribe,
    selectionStore.getSnapshot,
    selectionStore.getSnapshot,
  );
  const selectedIds = useMemo(() => selectionStore.getSelectedIds(), [selectionStore, selectionVersion]);
  const selectedCount = selectedIds.length;
  const [pendingToggleIds, setPendingToggleIds] = useState<Set<number>>(() => new Set());
  const pendingToggleIdsRef = useRef(pendingToggleIds);
  pendingToggleIdsRef.current = pendingToggleIds;
  const [showBulkEditModal, setShowBulkEditModal] = useState(false);
  const [showBulkDeleteConfirm, setShowBulkDeleteConfirm] = useState(false);
  const [bulkRefreshTargets, setBulkRefreshTargets] = useState<{ id: number; name: string }[] | null>(null);
  const clearSelection = useCallback(() => {
    runAfterInputFrame(() => selectionStore.clear());
  }, [selectionStore]);

  // 切换筛选/分页时清空选择，避免不可见行仍被选中导致误操作
  useEffect(() => {
    selectionStore.clear();
  }, [groupFilter, keyword, page, pageSize, platformFilter, proxyFilter, selectionStore, stateFilter, typeFilter]);

  // 查询账号列表
  const { data, isLoading } = useQuery({
    queryKey: queryKeys.accounts(page, pageSize, debouncedKeyword, platformFilter, stateFilter, typeFilter, groupFilter, proxyFilter),
    queryFn: () =>
      accountsApi.list({
        page,
        page_size: pageSize,
        keyword: debouncedKeyword || undefined,
        platform: platformFilter || undefined,
        state: stateFilter || undefined,
        account_type: typeFilter || undefined,
        group_id: groupFilter && groupFilter !== UNGROUPED_GROUP_FILTER ? Number(groupFilter) : undefined,
        ungrouped: groupFilter === UNGROUPED_GROUP_FILTER ? true : undefined,
        proxy_id: proxyFilter ? Number(proxyFilter) : undefined,
      }),
    placeholderData: keepPreviousData,
  });
  const rows = data?.list ?? [];

  // 查询分组列表（用于表格中 ID→名称映射）
  const { data: allGroupsData } = useQuery({
    queryKey: queryKeys.groupsAll(),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
  });
  const groupMap = useMemo(
    () => new Map((allGroupsData?.list ?? []).map((g) => [g.id, g.name])),
    [allGroupsData?.list],
  );

  // 查询代理列表（用于表格中 ID→名称映射）
  // 代理列表（只用于顶部筛选器；之前的"代理"列已移除）
  const { data: allProxiesData } = useQuery({
    queryKey: queryKeys.proxiesAll(),
    queryFn: () => proxiesApi.list(FETCH_ALL_PARAMS),
  });

  // 查询用量窗口
  const usageWindowCacheRef = useRef<AccountUsageWindowCache>(new Map());
  const { data: rawUsageData } = useQuery({
    queryKey: queryKeys.accountUsage(platformFilter),
    queryFn: () => accountsApi.usage(platformFilter || ''),
    meta: { globalLoading: false },
    refetchInterval: 300_000,
    refetchIntervalInBackground: false,
  });
  const singleUsageResults = useQueries({
    queries: rows.map((row) => ({
      queryKey: queryKeys.accountUsage(platformFilter, 'account', row.id),
      queryFn: ({ signal }: { signal?: AbortSignal }) =>
        accountsApi.usageOne(row.id, { signal }) as Promise<AccountUsageInfo>,
      enabled: row.type !== 'apikey',
      meta: { globalLoading: false },
      staleTime: 300_000,
      gcTime: 10 * 60_000,
      refetchOnWindowFocus: false,
    })),
    combine: (results) => results.map((query) => ({
      data: query.data as AccountUsageInfo | undefined,
    })),
  });
  const rawUsageWithSingleAccounts = useMemo<AccountUsageData | undefined>(() => {
    const accounts: Record<string, AccountUsageInfo> = { ...(rawUsageData?.accounts ?? {}) };
    let hasSingleAccount = false;
    const hasUpstreamUsage = (usage: AccountUsageInfo | undefined) =>
      Boolean((Array.isArray(usage?.windows) && usage.windows.length > 0) || usage?.credits);
    const updatedAtMs = (usage: AccountUsageInfo | undefined) => {
      if (!usage?.updated_at) return 0;
      const parsed = Date.parse(usage.updated_at);
      return Number.isFinite(parsed) ? parsed : 0;
    };

    rows.forEach((row, index) => {
      const singleUsage = singleUsageResults[index]?.data;
      if (!singleUsage) return;
      const accountKey = String(row.id);
      const existingUsage = accounts[accountKey];
      const singleHasUpstream = hasUpstreamUsage(singleUsage);

      if (!existingUsage) {
        accounts[accountKey] = singleUsage;
      } else if (!singleHasUpstream) {
        accounts[accountKey] = {
          ...existingUsage,
          today_stats: singleUsage.today_stats ?? existingUsage.today_stats,
        };
      } else if (!hasUpstreamUsage(existingUsage) || updatedAtMs(singleUsage) >= updatedAtMs(existingUsage)) {
        accounts[accountKey] = {
          ...existingUsage,
          ...singleUsage,
          today_stats: singleUsage.today_stats ?? existingUsage.today_stats,
        };
      }
      hasSingleAccount = true;
    });

    if (!rawUsageData && !hasSingleAccount) return undefined;
    return {
      ...rawUsageData,
      accounts,
    };
  }, [rawUsageData, rows, singleUsageResults]);
  const usageData = useMemo(
    () => mergeCachedUsageWindows(rawUsageWithSingleAccounts, usageWindowCacheRef.current),
    [rawUsageWithSingleAccounts],
  );
  const usageDataRef = useRef(usageData);
  usageDataRef.current = usageData;

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
        keyword: debouncedKeyword || undefined,
        platform: platformFilter || undefined,
        state: stateFilter || undefined,
        account_type: typeFilter || undefined,
        group_id: groupFilter && groupFilter !== UNGROUPED_GROUP_FILTER ? Number(groupFilter) : undefined,
        ungrouped: groupFilter === UNGROUPED_GROUP_FILTER ? true : undefined,
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

  // 切换调度状态。这里做乐观更新，避免开关先跳回旧状态再等列表刷新。
  const toggleMutation = useMutation({
    mutationFn: (id: number) => accountsApi.toggleScheduling(id),
    onMutate: async (id) => {
      setPendingToggleIds((prev) => new Set(prev).add(id));
      await queryClient.cancelQueries({ queryKey: queryKeys.accounts() });
      const previous = queryClient.getQueriesData<PagedData<AccountResp>>({ queryKey: queryKeys.accounts() });

      queryClient.setQueriesData<PagedData<AccountResp>>({ queryKey: queryKeys.accounts() }, (old) => {
        if (!old?.list) return old;
        return {
          ...old,
          list: old.list.map((account) => (
            account.id === id
              ? {
                  ...account,
                  state: (account.state === 'disabled' ? 'active' : 'disabled') as AccountResp['state'],
                  state_until: undefined,
                }
              : account
          )),
        };
      });

      return { previous };
    },
    onSuccess: (res, id) => {
      queryClient.setQueriesData<PagedData<AccountResp>>({ queryKey: queryKeys.accounts() }, (old) => {
        if (!old?.list) return old;
        return {
          ...old,
          list: old.list.map((account) => (
            account.id === id
              ? { ...account, state: res.state as AccountResp['state'], state_until: undefined }
              : account
          )),
        };
      });
    },
    onError: (err: Error, _id, context) => {
      context?.previous.forEach(([queryKey, value]) => {
        queryClient.setQueryData(queryKey, value);
      });
      toast('error', err.message);
    },
    onSettled: (_data, _error, id) => {
      setPendingToggleIds((prev) => {
        const next = new Set(prev);
        next.delete(id);
        return next;
      });
      queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
    },
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

  const toggleSchedulingMutateRef = useLatestRef(toggleMutation.mutate);
  const refreshQuotaMutateRef = useLatestRef(refreshQuotaMutation.mutate);
  const clearRateLimitMarkersMutateRef = useLatestRef(clearRateLimitMarkersMutation.mutate);
  const handleToggleScheduling = useCallback((id: number) => {
    if (pendingToggleIdsRef.current.has(id)) return;
    toggleSchedulingMutateRef.current(id);
  }, [toggleSchedulingMutateRef]);
  const handleEditAccount = useCallback((row: AccountResp) => {
    setEditingAccount(row);
  }, []);
  const handleDeleteAccount = useCallback((row: AccountResp) => {
    setDeletingAccount(row);
  }, []);
  const handleTestAccount = useCallback((row: AccountResp) => {
    setTestingAccount(row);
  }, []);
  const handleStatsAccount = useCallback((id: number) => {
    setStatsAccountId(id);
  }, []);
  const handleRefreshQuota = useCallback((id: number) => {
    refreshQuotaMutateRef.current(id);
  }, [refreshQuotaMutateRef]);
  const handleClearRateLimitMarkers = useCallback((id: number) => {
    clearRateLimitMarkersMutateRef.current(id);
  }, [clearRateLimitMarkersMutateRef]);

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

  const columns = useAccountTableColumns({
    applyQuotaRefreshResult,
    groupMap,
    onClearRateLimitMarkers: handleClearRateLimitMarkers,
    onDeleteAccount: handleDeleteAccount,
    onEditAccount: handleEditAccount,
    onRefreshQuota: handleRefreshQuota,
    onStatsAccount: handleStatsAccount,
    onTestAccount: handleTestAccount,
    onToggleScheduling: handleToggleScheduling,
    platformFilter,
    platformName,
    platformsKey,
    usageData,
  });
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);
  const visibleRowIds = useMemo(() => rows.map((row) => row.id), [rows]);
  const selectedVisibleCount = useMemo(
    () => selectionStore.countVisible(visibleRowIds),
    [selectionStore, selectionVersion, visibleRowIds],
  );
  const allVisibleSelected = visibleRowIds.length > 0 && selectedVisibleCount === visibleRowIds.length;
  const someVisibleSelected = selectedVisibleCount > 0 && !allVisibleSelected;
  const selectAllAriaLabel = t('common.select_all', 'Select all');
  const selectRowAriaLabel = t('common.select', 'Select');
  const setVisibleRowsSelected = useCallback((isSelected: boolean) => {
    runAfterInputFrame(() => selectionStore.setRows(visibleRowIds, isSelected));
  }, [selectionStore, visibleRowIds]);
  const setRowSelected = useCallback((id: number, isSelected: boolean) => {
    runAfterInputFrame(() => selectionStore.setRow(id, isSelected));
  }, [selectionStore]);
  const typeOptions = useMemo<AccountTypeFilterOption[]>(() => [
    { id: '', label: t('accounts.all_types', '全部类型') },
    { id: 'oauth', label: 'OAuth' },
    { id: 'apikey', label: 'API Key' },
  ], [t]);
  const oauthPlanOptions = useMemo<AccountTypeFilterOption[]>(() => oauthPlanFilters
    .filter((item) => !platformFilter || item.platform === platformFilter)
    .map((item) => ({
      id: item.id,
      label: platformFilter ? `OAuth ${item.planLabel}` : `${item.platformLabel} OAuth ${item.planLabel}`,
      platformLabel: platformFilter ? undefined : item.platformLabel,
      planLabel: item.planLabel,
    })), [oauthPlanFilters, platformFilter]);
  const groupOptions = useMemo(() => [
    { id: '', label: t('accounts.all_groups') },
    { id: UNGROUPED_GROUP_FILTER, label: t('accounts.ungrouped') },
    ...(allGroupsData?.list ?? []).map((g) => ({ id: String(g.id), label: g.name })),
  ], [allGroupsData?.list, t]);
  const proxyOptions = useMemo(() => [
    { id: '', label: t('accounts.all_proxies') },
    ...(allProxiesData?.list ?? []).map((p) => ({ id: String(p.id), label: p.name })),
  ], [allProxiesData?.list, t]);
  const selectedPlatformLabel = PLATFORM_OPTIONS.find((item) => item.id === platformFilter)?.label ?? t('accounts.all_platforms');
  const selectedStateLabel = STATE_OPTIONS.find((item) => item.id === stateFilter)?.label ?? t('users.all_status');
  const selectedTypeOption = typeOptions.find((item) => item.id === typeFilter)
    ?? oauthPlanOptions.find((item) => item.id === typeFilter);
  const selectedGroupLabel = groupOptions.find((item) => item.id === groupFilter)?.label ?? t('accounts.all_groups');
  const selectedProxyLabel = proxyOptions.find((item) => item.id === proxyFilter)?.label ?? t('accounts.all_proxies');
  useEffect(() => {
    if (!typeFilter) return;
    if (typeOptions.some((item) => item.id === typeFilter)) return;
    if (oauthPlanOptions.some((item) => item.id === typeFilter)) return;
    if (typeFilter.startsWith('oauth_plan:') && platformsLoading) return;
    setTypeFilter(typeFilter.startsWith('oauth_plan:') ? 'oauth' : '');
    setPage(1);
  }, [oauthPlanOptions, platformsLoading, setPage, typeFilter, typeOptions]);
  const toolbarFilters = [
    {
      key: 'platform',
      label: t('groups.platform'),
      value: platformFilter,
      selectedLabel: selectedPlatformLabel,
      options: PLATFORM_OPTIONS,
      setValue: setPlatformFilter,
      widthClass: 'w-full sm:w-48',
    },
    {
      key: 'state',
      label: t('common.status'),
      value: stateFilter,
      selectedLabel: selectedStateLabel,
      options: STATE_OPTIONS,
      setValue: setStateFilter,
      widthClass: 'w-full sm:w-48',
    },
    {
      key: 'type',
      label: t('common.type'),
      value: typeFilter,
      selectedLabel: selectedTypeOption?.label ?? t('accounts.all_types', '全部类型'),
      options: typeOptions,
      setValue: setTypeFilter,
      widthClass: 'w-full sm:w-48',
    },
    {
      key: 'group',
      label: t('accounts.group'),
      value: groupFilter,
      selectedLabel: selectedGroupLabel,
      options: groupOptions,
      setValue: setGroupFilter,
      widthClass: 'w-full sm:w-48',
    },
    {
      key: 'proxy',
      label: t('accounts.proxy'),
      value: proxyFilter,
      selectedLabel: selectedProxyLabel,
      options: proxyOptions,
      setValue: setProxyFilter,
      widthClass: 'w-full sm:w-48',
    },
  ];
  return (
    <div>
      <div className="mb-5 flex min-h-12 flex-col gap-3 xl:flex-row xl:items-start">
        <div className="min-w-0 flex-1">
          <div className="flex min-h-12 flex-col flex-wrap items-stretch gap-3 sm:flex-row sm:items-center">
            <div className="w-full sm:w-48">
              <HeroTextField fullWidth aria-label={t('accounts.search_placeholder', '搜索账号名称...')}>
                <div className="relative">
                  <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                  <Input
                    className="pl-9"
                    value={keyword}
                    onChange={(e) => { setKeyword(e.target.value); setPage(1); }}
                    placeholder={t('accounts.search_placeholder', '搜索账号名称...')}
                  />
                </div>
              </HeroTextField>
            </div>

            {toolbarFilters.map((filter) => (
              <div
                key={filter.key}
                className={filter.widthClass}
              >
                {filter.key === 'type' ? (
                  <AccountTypeFilterSelect
                    oauthPlanOptions={oauthPlanOptions}
                    platformsLoading={platformsLoading}
                    selectedOption={selectedTypeOption}
                    typeOptions={typeOptions}
                    onSelect={(nextValue) => {
                      setTypeFilter(nextValue);
                      setPage(1);
                    }}
                  />
                ) : (
                  <Select
                    aria-label={filter.label}
                    fullWidth
                    selectedKey={filter.value}
                    onSelectionChange={(key) => {
                      const nextValue = key == null ? '' : String(key);
                      filter.setValue(nextValue);
                      setPage(1);
                    }}
                  >
                    <Label className="sr-only">{filter.label}</Label>
                    <Select.Trigger>
                      <Select.Value>{filter.selectedLabel}</Select.Value>
                      <Select.Indicator />
                    </Select.Trigger>
                    <Select.Popover>
                      <ListBox items={filter.options}>
                        {(item) => (
                          <ListBox.Item
                            id={item.id}
                            textValue={item.label}
                          >
                            {item.label}
                          </ListBox.Item>
                        )}
                      </ListBox>
                    </Select.Popover>
                  </Select>
                )}
              </div>
            ))}
          </div>
        </div>

        <div className="flex shrink-0 flex-wrap items-center justify-start gap-2 xl:ml-auto xl:justify-end">
          <Button
            isIconOnly
            aria-label={t('common.refresh')}
            size="sm"
            variant="ghost"
            className="h-8 w-8 min-w-8"
            onPress={refreshAccounts}
          >
            <RefreshCw className="h-4 w-4" />
          </Button>
          <Dropdown>
            <Dropdown.Trigger
              className={`ag-account-auto-refresh-trigger button button--sm ${autoRefresh ? 'button--secondary' : 'button--ghost'} h-8 min-w-[7.5rem] whitespace-nowrap px-3`}
            >
              <AutoRefreshCountdownLabel
                autoRefresh={autoRefresh}
                label={autoRefreshLabel}
                offLabel={autoRefreshOffLabel}
                onRefresh={refreshAccounts}
              />
              <ChevronDown className="h-3 w-3 shrink-0" />
            </Dropdown.Trigger>
            <Dropdown.Popover placement="bottom end">
              <Dropdown.Menu
                aria-label={t('accounts.auto_refresh')}
                selectedKeys={new Set([`auto_${autoRefresh}`])}
                selectionMode="single"
                onAction={(key) => {
                  const action = String(key);
                  setAutoRefresh(Number(action.replace('auto_', '')));
                }}
              >
                {AUTO_REFRESH_OPTIONS.map((sec) => (
                  <Dropdown.Item key={sec} id={`auto_${sec}`} textValue={sec === 0 ? t('accounts.auto_refresh_off') : `${t('accounts.auto_refresh')}${sec}s`}>
                    <span className="flex items-center justify-between gap-6">
                      <span>{sec === 0 ? t('accounts.auto_refresh_off') : `${t('accounts.auto_refresh')}${sec}s`}</span>
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
            <Upload className="h-4 w-4" />
            {t('accounts.import')}
          </Button>
          <Button
            variant="secondary"
            onPress={() => exportMutation.mutate()}
            isDisabled={exportMutation.isPending}
            aria-busy={exportMutation.isPending}
          >
            <Download className="h-4 w-4" />
            {t('accounts.export')}
          </Button>
          <Button variant="primary" onPress={() => setShowCreateModal(true)}>
            <Plus className="h-4 w-4" />
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

      {/* 表格 */}
      <div className="ag-resource-table ag-accounts-table">
        <div className="ag-resource-table-scroll" data-slot="wrapper">
          {selectedCount > 0 ? (
            <div onClick={(event) => event.stopPropagation()}>
              <BulkActionsBar
                overlay
                selectedCount={selectedCount}
                onClear={clearSelection}
                onEdit={() => setShowBulkEditModal(true)}
                onEnable={handleBulkEnable}
                onDisable={handleBulkDisable}
                onRefreshQuota={handleBulkRefresh}
                onClearRateLimitMarkers={() => bulkClearRateLimitMarkersMutation.mutate(selectedIds)}
                onDelete={() => setShowBulkDeleteConfirm(true)}
              />
            </div>
          ) : null}
          <table
            aria-label={t('accounts.title', 'Accounts')}
            className="ag-resource-table-content ag-accounts-table-content"
            data-slot="table"
            style={{ minWidth: 'var(--ag-accounts-current-table-width)' }}
          >
            <thead data-slot="thead">
              <tr data-slot="tr">
                <th data-slot="th" scope="col" className="text-center" style={ACCOUNT_SELECTION_COLUMN_STYLE}>
                  <div className="inline-flex" onClick={(event) => event.stopPropagation()}>
                    <TableSelectionCheckbox
                      ariaLabel={selectAllAriaLabel}
                      isIndeterminate={someVisibleSelected}
                      isSelected={allVisibleSelected}
                      onChange={setVisibleRowsSelected}
                    />
                  </div>
                </th>
                {columns.map((column) => (
                  <th
                    data-slot="th"
                    id={column.key}
                    key={column.key}
                    scope="col"
                    className={columnAlignClass(column.align)}
                    style={columnWidthStyle(column)}
                  >
                    {column.title}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody data-slot="tbody">
              {isLoading ? (
                <AccountsTableLoadingRow colSpan={columns.length + 1} />
              ) : rows.length === 0 ? (
                <tr data-slot="tr" data-key="empty">
                  <td data-slot="td" colSpan={columns.length + 1}>
                    <EmptyState>
                      <div className="text-sm text-default-500">{t('common.no_data')}</div>
                    </EmptyState>
                  </td>
                </tr>
              ) : (
                rows.map((row) => (
                  <AccountTableRow
                    key={row.id}
                    columns={columns}
                    row={row}
                    selectRowAriaLabel={selectRowAriaLabel}
                    selectionStore={selectionStore}
                    onSelectedChange={setRowSelected}
                  />
                ))
              )}
            </tbody>
          </table>
        </div>
        <TablePaginationFooter
          page={page}
          pageSize={pageSize}
          pageSizeOptions={PAGE_SIZE_OPTIONS}
          setPage={setPage}
          setPageSize={setPageSize}
          total={total}
          totalPages={totalPages}
        />
      </div>

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
        <DialogTriggerShim />
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
        <DialogTriggerShim />
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
