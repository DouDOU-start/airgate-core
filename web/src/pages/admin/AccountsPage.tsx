import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  ArrowUpDown, BarChart3, Boxes, CircleCheck, CircleOff, Download, Pencil, Play, Plus, Power, Trash2, Upload,
} from 'lucide-react';
import {
  Button, Checkbox, Chip, EmptyState, Input, Label, ListBox, Modal, Select, Spinner,
  TextField as HeroTextField, useOverlayState,
} from '@heroui/react';
import { accountsApi } from '../../shared/api/accounts';
import { groupsApi } from '../../shared/api/groups';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { useDebouncedValue } from '../../shared/hooks/useDebouncedValue';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE, FETCH_ALL_PARAMS } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { CommonTable } from '../../shared/components/CommonTable';
import { DialogTriggerShim } from '../../shared/components/DialogTriggerShim';
import { SortableHeader } from '../../shared/components/SortableHeader';
import { AccountFormModal } from './accounts/AccountFormModal';
import { AccountIdentityCell } from './accounts/AccountIdentityCell';
import {
  AccountUsageCell,
  accountSupportsUsageRefresh,
  accountSupportsUsageReset,
} from './accounts/AccountUsageCell';
import { AccountTestModal } from './accounts/AccountTestModal';
import { AccountStatsModal } from './accounts/AccountStatsModal';
import { AccountModelsModal } from './accounts/AccountModelsModal';
import { useBackgroundAccountUsageRefresh } from './accounts/useBackgroundAccountUsageRefresh';
import { ConfirmDialog } from '../../shared/components/ConfirmDialog';
import { MetricChips } from '../../shared/components/MetricChips';
import { RefreshButton } from '../../shared/components/RefreshButton';
import { useToast } from '../../shared/ui';
import type {
  AccountExportItem,
  AccountImportReq,
  AccountResp,
  AccountSortBy,
  AccountState,
  BulkOpResp,
  BulkUpdateAccountsReq,
  CreateAccountReq,
  SortOrder,
  UpdateAccountReq,
} from '../../shared/types';

/** 分组列最多展示条数，超出显示 +N */
const GROUP_DISPLAY_LIMIT = 3;
const UNGROUPED_FILTER = '__ungrouped__';

const COLUMN_COUNT = 10;

function fmtMoney(n?: number): string {
  return `$${(n ?? 0).toFixed(2)}`;
}

const STATE_CHIP_COLOR: Record<AccountState, 'success' | 'warning' | 'danger' | 'default'> = {
  active: 'success',
  rate_limited: 'warning',
  degraded: 'warning',
  disabled: 'default',
};

function accountStateChipColor(row: AccountResp): 'success' | 'warning' | 'danger' | 'default' {
  if (row.state === 'disabled' && row.error_msg?.trim()) return 'danger';
  return STATE_CHIP_COLOR[row.state as AccountState] ?? 'default';
}

function downloadJson(filename: string, data: unknown) {
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = filename;
  a.click();
  URL.revokeObjectURL(a.href);
}

function toastBulkResult(
  toast: (type: 'success' | 'error' | 'warning', message: string) => void,
  t: (key: string, opts?: Record<string, unknown>) => string,
  resp: BulkOpResp,
) {
  if (resp.failed > 0) {
    toast(
      resp.success > 0 ? 'warning' : 'error',
      t('accounts.bulk_partial', { success: resp.success, failed: resp.failed }),
    );
    const firstErr = resp.results?.find((item) => !item.success)?.error;
    if (firstErr) toast('error', firstErr);
    return;
  }
  toast('success', t('accounts.bulk_success', { count: resp.success }));
}

export default function AccountsPage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const initialUsageRefreshStartedRef = useRef(false);
  const {
    refresh: refreshUsageInBackground,
    refreshingIds: backgroundUsageRefreshingIds,
  } = useBackgroundAccountUsageRefresh();

  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE, 'admin.accounts');

  const [keyword, setKeyword] = useState('');
  const debouncedKeyword = useDebouncedValue(keyword, 300);
  const [platformFilter, setPlatformFilter] = useState('');
  const [stateFilter, setStateFilter] = useState('');
  const [groupFilter, setGroupFilter] = useState('');
  const [sort, setSort] = useState<{ by?: AccountSortBy; order: SortOrder }>({ order: 'desc' });
  const [selectedIds, setSelectedIds] = useState<number[]>([]);

  function handleSortChange(field: AccountSortBy) {
    setSort((prev) => (
      prev.by === field
        ? { by: field, order: prev.order === 'asc' ? 'desc' : 'asc' }
        : { by: field, order: 'desc' }
    ));
    setPage(1);
  }

  function sortState(field: AccountSortBy): SortOrder | null {
    return sort.by === field ? sort.order : null;
  }

  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingItem, setEditingItem] = useState<AccountResp | null>(null);
  const [deletingItem, setDeletingItem] = useState<AccountResp | null>(null);
  const [testingItem, setTestingItem] = useState<AccountResp | null>(null);
  const [statsItem, setStatsItem] = useState<AccountResp | null>(null);
  const [modelsTargets, setModelsTargets] = useState<AccountResp[]>([]);
  const [exportConfirmOpen, setExportConfirmOpen] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [importing, setImporting] = useState(false);
  const [bulkDeleteOpen, setBulkDeleteOpen] = useState(false);
  const [priorityModalOpen, setPriorityModalOpen] = useState(false);
  const [weightModalOpen, setWeightModalOpen] = useState(false);
  const [bulkPriority, setBulkPriority] = useState('50');
  const [bulkWeight, setBulkWeight] = useState('1');

  const listQuery = {
    page,
    page_size: pageSize,
    keyword: debouncedKeyword || undefined,
    platform: platformFilter || undefined,
    state: stateFilter || undefined,
    group_id: groupFilter && groupFilter !== UNGROUPED_FILTER ? Number(groupFilter) : undefined,
    ungrouped: groupFilter === UNGROUPED_FILTER || undefined,
    sort_by: sort.by,
    sort_order: sort.by ? sort.order : undefined,
  };

  const { data, isFetching, isLoading, refetch } = useQuery({
    queryKey: queryKeys.accounts(listQuery),
    queryFn: () => accountsApi.list(listQuery),
    placeholderData: keepPreviousData,
    // 运行时并发 / RPM 需要更密的刷新：官方 CLI 走原生插件后单次流可能只有数秒，30s 会整段错过。
    refetchInterval: 5_000,
  });

  const { data: groupsData } = useQuery({
    queryKey: queryKeys.groupsAll(),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
    staleTime: 60_000,
  });
  const groupNameById = useMemo(() => {
    const map = new Map<number, string>();
    for (const g of groupsData?.list ?? []) {
      map.set(g.id, g.name || `#${g.id}`);
    }
    return map;
  }, [groupsData?.list]);

  const createMutation = useCrudMutation<unknown, CreateAccountReq>({
    mutationFn: (payload) => accountsApi.create(payload),
    successMessage: t('accounts.create_success'),
    queryKey: queryKeys.accounts(),
    onSuccess: () => setShowCreateModal(false),
  });

  const updateMutation = useCrudMutation<unknown, { id: number; data: UpdateAccountReq }>({
    mutationFn: ({ id, data: payload }) => accountsApi.update(id, payload),
    successMessage: t('accounts.update_success'),
    queryKey: queryKeys.accounts(),
    onSuccess: () => setEditingItem(null),
  });

  const deleteMutation = useCrudMutation<unknown, number>({
    mutationFn: (id) => accountsApi.delete(id),
    successMessage: t('accounts.delete_success'),
    queryKey: queryKeys.accounts(),
    onSuccess: () => {
      setDeletingItem(null);
      setSelectedIds((prev) => prev.filter((id) => id !== deletingItem?.id));
      if ((data?.list?.length ?? 0) === 1 && page > 1) {
        setPage(page - 1);
      }
    },
  });

  const toggleMutation = useCrudMutation<unknown, number>({
    mutationFn: (id) => accountsApi.toggle(id),
    successMessage: t('accounts.toggle_success'),
    queryKey: queryKeys.accounts(),
  });

  const [usageRefreshingId, setUsageRefreshingId] = useState<number | null>(null);
  const [usageResettingId, setUsageResettingId] = useState<number | null>(null);
  const usageRefreshMutation = useMutation({
    mutationFn: (id: number) => accountsApi.refreshUsage(id),
    onMutate: (id) => setUsageRefreshingId(id),
    onSuccess: () => {
      toast('success', t('accounts.usage_refresh_success'));
      void queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
    },
    onError: (err: Error) => {
      // 不支持的平台：用简短提示，避免刷出长串技术说明
      const msg = err.message || t('accounts.usage_refresh_failed');
      if (msg.includes('不支持用量窗口') || msg.includes('usage')) {
        toast('warning', t('accounts.usage_not_supported'));
        return;
      }
      toast('error', msg);
    },
    onSettled: () => setUsageRefreshingId(null),
  });
  const usageResetMutation = useMutation({
    mutationFn: (id: number) => accountsApi.consumeUsageReset(id),
    onMutate: (id) => setUsageResettingId(id),
    onSuccess: (resp) => {
      if (resp.code === 'reset') {
        toast(
          'success',
          t('accounts.usage_reset_success', { count: resp.windows_reset ?? 0 }),
        );
      } else if (resp.code === 'nothing_to_reset') {
        toast('warning', t('accounts.usage_reset_nothing'));
      } else if (resp.code === 'no_credit') {
        toast('error', t('accounts.usage_reset_no_credit'));
      } else {
        toast('warning', t('accounts.usage_reset_other', { code: resp.code }));
      }
      void queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
    },
    onError: (err: Error) => toast('error', err.message),
    onSettled: () => setUsageResettingId(null),
  });

  const bulkMutation = useMutation({
    mutationFn: async (
      payload:
        | { kind: 'update'; data: BulkUpdateAccountsReq }
        | { kind: 'delete'; account_ids: number[] },
    ) => {
      if (payload.kind === 'delete') {
        return accountsApi.bulkDelete({ account_ids: payload.account_ids });
      }
      return accountsApi.bulkUpdate(payload.data);
    },
    onSuccess: (resp) => {
      toastBulkResult(toast, t, resp);
      void queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
      setSelectedIds([]);
      setBulkDeleteOpen(false);
      setPriorityModalOpen(false);
      setWeightModalOpen(false);
    },
    onError: (err: Error) => toast('error', err.message),
  });

  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);

  // 首次进入账号管理页时刷新一次当前页用量；不跟随 30 秒列表轮询重复执行。
  useEffect(() => {
    if (isLoading || rows.length === 0 || initialUsageRefreshStartedRef.current) return;
    initialUsageRefreshStartedRef.current = true;
    refreshUsageInBackground(rows);
  }, [isLoading, refreshUsageInBackground, rows]);

  async function handlePageRefresh() {
    const result = await refetch();
    if (result.isSuccess && result.data?.list) refreshUsageInBackground(result.data.list);
  }

  const pageIds = rows.map((row) => row.id);
  const allPageSelected = pageIds.length > 0 && pageIds.every((id) => selectedIds.includes(id));

  function toggleSelected(id: number, selected: boolean) {
    setSelectedIds((prev) => (selected ? [...new Set([...prev, id])] : prev.filter((item) => item !== id)));
  }

  function toggleSelectAll(selected: boolean) {
    setSelectedIds((prev) => (
      selected
        ? [...new Set([...prev, ...pageIds])]
        : prev.filter((id) => !pageIds.includes(id))
    ));
  }

  const platformFilterOptions = [
    { id: '', label: t('common.all') },
    { id: 'codex', label: 'Codex' },
    { id: 'claude', label: 'Claude' },
    { id: 'antigravity', label: 'Antigravity' },
    { id: 'kimi', label: 'Kimi' },
    { id: 'xai', label: 'xAI' },
    { id: 'cursor', label: 'Cursor' },
    { id: 'gemini', label: 'Gemini' },
    { id: 'vertex', label: 'Vertex' },
  ];
  const stateFilterOptions = [
    { id: '', label: t('common.all') },
    { id: 'active', label: t('accounts.state_active') },
    { id: 'rate_limited', label: t('accounts.state_rate_limited') },
    { id: 'degraded', label: t('accounts.state_degraded') },
    { id: 'disabled', label: t('accounts.state_disabled') },
  ];
  const groupFilterOptions = [
    { id: '', label: t('accounts.group_filter_all') },
    { id: UNGROUPED_FILTER, label: t('accounts.group_filter_ungrouped') },
    ...(groupsData?.list ?? []).map((group) => ({
      id: String(group.id),
      label: group.name || `#${group.id}`,
    })),
  ];
  const selectedPlatformLabel =
    platformFilterOptions.find((item) => item.id === platformFilter)?.label ?? t('common.all');
  const selectedStateLabel =
    stateFilterOptions.find((item) => item.id === stateFilter)?.label ?? t('common.all');
  const selectedGroupLabel =
    groupFilterOptions.find((item) => item.id === groupFilter)?.label ?? t('accounts.group_filter_all');

  const priorityDialogState = useOverlayState({
    isOpen: priorityModalOpen,
    onOpenChange: (open) => {
      if (!open) setPriorityModalOpen(false);
    },
  });
  const weightDialogState = useOverlayState({
    isOpen: weightModalOpen,
    onOpenChange: (open) => {
      if (!open) setWeightModalOpen(false);
    },
  });

  const bulkPending = bulkMutation.isPending;

  function resetToFirstPage() {
    setPage(1);
    setSelectedIds([]);
  }

  async function handleExportConfirm() {
    if (selectedIds.length === 0) return;
    setExporting(true);
    try {
      const resp = await accountsApi.export({ ids: selectedIds });
      const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-');
      downloadJson(`accounts-export-${stamp}.json`, resp);
      toast('success', t('accounts.export_success', { count: resp.count ?? resp.accounts?.length ?? 0 }));
      setExportConfirmOpen(false);
    } catch (err) {
      toast('error', err instanceof Error ? err.message : String(err));
    } finally {
      setExporting(false);
    }
  }

  async function handleImportFile(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    e.target.value = '';
    setImporting(true);
    try {
      const text = await file.text();
      const parsed = JSON.parse(text) as unknown;
      let accounts: AccountExportItem[] = [];
      if (Array.isArray(parsed)) {
        accounts = parsed as AccountExportItem[];
      } else if (
        parsed
        && typeof parsed === 'object'
        && Array.isArray((parsed as AccountImportReq).accounts)
      ) {
        accounts = (parsed as AccountImportReq).accounts;
      } else {
        throw new Error(t('accounts.import_invalid_format'));
      }
      if (accounts.length === 0) {
        throw new Error(t('accounts.import_empty'));
      }
      const resp = await accountsApi.import({ accounts });
      const msg = resp.failed > 0
        ? t('accounts.import_partial', { imported: resp.imported, failed: resp.failed })
        : t('accounts.import_success', { count: resp.imported });
      toast(resp.failed > 0 ? 'warning' : 'success', msg);
      if (resp.errors?.length) {
        toast('error', resp.errors.slice(0, 3).join('; '));
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
    } catch (err) {
      toast('error', err instanceof Error ? err.message : String(err));
    } finally {
      setImporting(false);
    }
  }

  function resolveGroupLabels(row: AccountResp): Array<{ id: number; name: string }> {
    const ids = row.group_ids ?? [];
    return ids.map((id) => ({ id, name: groupNameById.get(id) || `#${id}` }));
  }

  function stateLabel(state: AccountState | string) {
    const key = `accounts.state_${state}`;
    const translated = t(key);
    return translated === key ? state : translated;
  }

  return (
    <div>
      {/* 工具栏 */}
      <div className="ag-mobile-toolbar flex flex-col sm:flex-row items-stretch sm:items-center gap-3 mb-5 flex-wrap">
        <HeroTextField className="w-full sm:w-56">
          <Input
            placeholder={t('accounts.search_placeholder')}
            value={keyword}
            onChange={(e) => {
              setKeyword(e.target.value);
              resetToFirstPage();
            }}
          />
        </HeroTextField>

        <div className="w-full sm:w-36">
          <Select
            aria-label={t('accounts.platform')}
            fullWidth
            selectedKey={platformFilter}
            onSelectionChange={(key) => {
              setPlatformFilter(key == null ? '' : String(key));
              resetToFirstPage();
            }}
          >
            <Select.Trigger>
              <Select.Value>{selectedPlatformLabel}</Select.Value>
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox items={platformFilterOptions}>
                {(item) => (
                  <ListBox.Item id={item.id} textValue={item.label}>
                    {item.label}
                  </ListBox.Item>
                )}
              </ListBox>
            </Select.Popover>
          </Select>
        </div>

        <div className="w-full sm:w-40">
          <Select
            aria-label={t('accounts.state')}
            fullWidth
            selectedKey={stateFilter}
            onSelectionChange={(key) => {
              setStateFilter(key == null ? '' : String(key));
              resetToFirstPage();
            }}
          >
            <Select.Trigger>
              <Select.Value>{selectedStateLabel}</Select.Value>
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox items={stateFilterOptions}>
                {(item) => (
                  <ListBox.Item id={item.id} textValue={item.label}>
                    {item.label}
                  </ListBox.Item>
                )}
              </ListBox>
            </Select.Popover>
          </Select>
        </div>

        <div className="w-full sm:w-44">
          <Select
            aria-label={t('accounts.groups')}
            fullWidth
            selectedKey={groupFilter}
            onSelectionChange={(key) => {
              setGroupFilter(key == null ? '' : String(key));
              resetToFirstPage();
            }}
          >
            <Select.Trigger>
              <Select.Value>{selectedGroupLabel}</Select.Value>
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox items={groupFilterOptions}>
                {(item) => (
                  <ListBox.Item id={item.id} textValue={item.label}>
                    {item.label}
                  </ListBox.Item>
                )}
              </ListBox>
            </Select.Popover>
          </Select>
        </div>

        <div className="ag-mobile-actions flex items-center gap-2 sm:ml-auto flex-wrap">
          <Button
            isDisabled={importing}
            variant="secondary"
            onPress={() => fileInputRef.current?.click()}
          >
            {importing ? <Spinner size="sm" /> : <Upload className="h-4 w-4" />}
            {t('accounts.import')}
          </Button>
          <input
            ref={fileInputRef}
            accept=".json,application/json"
            className="hidden"
            type="file"
            onChange={handleImportFile}
          />
          <RefreshButton
            ariaLabel={t('common.refresh', '刷新')}
            isRefreshing={isFetching}
            onRefresh={handlePageRefresh}
          />
          <Button variant="primary" onPress={() => setShowCreateModal(true)}>
            <Plus className="w-4 h-4" />
            {t('accounts.create')}
          </Button>
        </div>
      </div>

      {/* 批量操作条 */}
      {selectedIds.length > 0 ? (
        <div className="mb-3 flex flex-wrap items-center gap-2 rounded-[var(--radius)] border border-border bg-surface px-3 py-2">
          <span className="text-sm text-text-secondary">
            {t('accounts.selected_count', { count: selectedIds.length })}
          </span>
          <Button
            isDisabled={bulkPending}
            size="sm"
            variant="secondary"
            onPress={() => bulkMutation.mutate({
              kind: 'update',
              data: { account_ids: selectedIds, state: 'active' },
            })}
          >
            <CircleCheck className="h-3.5 w-3.5" />
            {t('common.enable')}
          </Button>
          <Button
            isDisabled={bulkPending}
            size="sm"
            variant="secondary"
            onPress={() => bulkMutation.mutate({
              kind: 'update',
              data: { account_ids: selectedIds, state: 'disabled' },
            })}
          >
            <CircleOff className="h-3.5 w-3.5" />
            {t('common.disable')}
          </Button>
          <Button
            isDisabled={bulkPending}
            size="sm"
            variant="secondary"
            onPress={() => setPriorityModalOpen(true)}
          >
            <ArrowUpDown className="h-3.5 w-3.5" />
            {t('accounts.bulk_set_priority')}
          </Button>
          <Button
            isDisabled={bulkPending}
            size="sm"
            variant="secondary"
            onPress={() => setWeightModalOpen(true)}
          >
            <ArrowUpDown className="h-3.5 w-3.5" />
            {t('accounts.bulk_set_weight')}
          </Button>
          <Button
            isDisabled={bulkPending}
            size="sm"
            variant="secondary"
            onPress={() => {
              const selected = rows.filter((r) => selectedIds.includes(r.id));
              setModelsTargets(selected);
            }}
          >
            <Boxes className="h-3.5 w-3.5" />
            {t('accounts.bulk_set_models')}
          </Button>
          <Button
            isDisabled={exporting}
            size="sm"
            variant="secondary"
            onPress={() => setExportConfirmOpen(true)}
          >
            {exporting ? <Spinner size="sm" /> : <Download className="h-3.5 w-3.5" />}
            {t('accounts.export_selected')}
          </Button>
          <Button
            className="text-danger"
            isDisabled={bulkPending}
            size="sm"
            variant="danger-soft"
            onPress={() => setBulkDeleteOpen(true)}
          >
            <Trash2 className="h-3.5 w-3.5" />
            {t('common.delete')}
          </Button>
          <Button
            className="ml-auto"
            size="sm"
            variant="ghost"
            onPress={() => setSelectedIds([])}
          >
            {t('common.clear')}
          </Button>
        </div>
      ) : null}

      {/* 表格 */}
      <CommonTable
        ariaLabel={t('accounts.title', '账号管理')}
        className="ag-accounts-table"
        mobileLayout="cards"
        footer={(
          <TablePaginationFooter
            page={page}
            pageSize={pageSize}
            setPage={setPage}
            setPageSize={setPageSize}
            total={total}
            totalPages={totalPages}
          />
        )}
        minWidth={1500}
      >
        <CommonTable.Header>
          <CommonTable.Column id="select" style={{ width: 40 }}>
            <Checkbox
              aria-label={t('accounts.select_all')}
              isSelected={allPageSelected}
              onChange={toggleSelectAll}
            >
              <Checkbox.Control>
                <Checkbox.Indicator />
              </Checkbox.Control>
            </Checkbox>
          </CommonTable.Column>
          <CommonTable.Column id="account" style={{ width: 330 }}>
            {t('accounts.account')}
          </CommonTable.Column>
          <CommonTable.Column id="state" style={{ width: 76 }}>{t('accounts.state')}</CommonTable.Column>
          <CommonTable.Column id="usage" style={{ width: 320 }}>{t('accounts.usage')}</CommonTable.Column>
          <CommonTable.Column id="money" style={{ width: 148 }}>
            <span title={t('accounts.stats_money_hint')}>{t('accounts.stats_money')}</span>
          </CommonTable.Column>
          <CommonTable.Column id="sched" style={{ width: 76 }}>
            <span className="inline-flex items-center gap-2">
              <SortableHeader
                active={sortState('priority')}
                label={t('accounts.priority_short')}
                onClick={() => handleSortChange('priority')}
              />
              <SortableHeader
                active={sortState('weight')}
                label={t('accounts.weight_short')}
                onClick={() => handleSortChange('weight')}
              />
            </span>
          </CommonTable.Column>
          <CommonTable.Column id="runtime" style={{ width: 120 }}>
            <span className="inline-flex items-center gap-2" title={t('accounts.concurrency_rpm_hint')}>
              <SortableHeader
                active={sortState('concurrency')}
                label={t('accounts.concurrency_label')}
                onClick={() => handleSortChange('concurrency')}
              />
              <SortableHeader
                active={sortState('rpm')}
                label="RPM"
                onClick={() => handleSortChange('rpm')}
              />
            </span>
          </CommonTable.Column>
          <CommonTable.Column id="proxy" style={{ width: 96 }}>{t('accounts.proxy')}</CommonTable.Column>
          <CommonTable.Column id="groups" style={{ width: 134 }}>{t('accounts.groups')}</CommonTable.Column>
          <CommonTable.Column id="actions" style={{ width: 160 }}>{t('common.actions')}</CommonTable.Column>
        </CommonTable.Header>
        <CommonTable.Body>
          {isLoading ? (
            <TableLoadingRow colSpan={COLUMN_COUNT} />
          ) : rows.length === 0 ? (
            <CommonTable.Row id="empty">
              <CommonTable.Cell colSpan={COLUMN_COUNT}>
                <EmptyState>
                  <div className="text-sm text-default-500">{t('accounts.empty')}</div>
                </EmptyState>
              </CommonTable.Cell>
            </CommonTable.Row>
          ) : (
            rows.map((row) => (
              <CommonTable.Row id={String(row.id)} key={row.id}>
                <CommonTable.Cell>
                  <Checkbox
                    aria-label={`${t('accounts.account')} ${row.name}`}
                    isSelected={selectedIds.includes(row.id)}
                    onChange={(selected) => toggleSelected(row.id, selected)}
                  >
                    <Checkbox.Control>
                      <Checkbox.Indicator />
                    </Checkbox.Control>
                  </Checkbox>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <AccountIdentityCell
                    name={row.name}
                    platform={row.platform}
                    type={row.type}
                    email={row.email}
                    errorMsg={row.error_msg}
                    planType={row.plan_type || row.usage?.plan_type}
                    subscriptionActiveUntil={row.subscription_active_until}
                  />
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <Chip
                    className="whitespace-nowrap"
                    color={accountStateChipColor(row)}
                    size="sm"
                    variant="soft"
                  >
                    {stateLabel(row.state)}
                  </Chip>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <AccountUsageCell
                    usage={row.usage}
                    canRefresh={accountSupportsUsageRefresh(row.platform, row.type)}
                    canReset={accountSupportsUsageReset(row.platform, row.type)}
                    refreshing={
                      backgroundUsageRefreshingIds.has(row.id)
                      || (usageRefreshingId === row.id && usageRefreshMutation.isPending)
                    }
                    resetting={usageResettingId === row.id && usageResetMutation.isPending}
                    onRefresh={() => usageRefreshMutation.mutate(row.id)}
                    onReset={() => {
                      if (!window.confirm(t('accounts.usage_reset_confirm'))) return;
                      usageResetMutation.mutate(row.id);
                    }}
                  />
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <div
                    className="flex flex-col gap-1 text-xs tabular-nums leading-tight"
                    title={t('accounts.stats_money_hint')}
                  >
                    <div className="flex items-center gap-1">
                      <span className="w-7 shrink-0 text-[10px] text-text-tertiary">{t('accounts.stats_today')}</span>
                      <span className="font-mono text-warning">{fmtMoney(row.today_cost)}</span>
                      <span className="text-text-tertiary">/</span>
                      <span className="font-mono text-success">{fmtMoney(row.today_revenue)}</span>
                    </div>
                    <div className="flex items-center gap-1">
                      <span className="w-7 shrink-0 text-[10px] text-text-tertiary">{t('accounts.stats_total')}</span>
                      <span className="font-mono text-warning">{fmtMoney(row.total_cost)}</span>
                      <span className="text-text-tertiary">/</span>
                      <span className="font-mono text-success">{fmtMoney(row.total_revenue)}</span>
                    </div>
                  </div>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <div
                    className="flex flex-col gap-0.5 text-xs tabular-nums text-text-secondary"
                    title={`${t('accounts.priority')} ${row.priority} · ${t('accounts.weight')} ${row.weight}`}
                  >
                    <span><span className="text-text-tertiary">P</span> {row.priority}</span>
                    <span><span className="text-text-tertiary">W</span> {row.weight}</span>
                  </div>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <MetricChips
                    className="ag-metric-chips--stack ag-metric-chips--compact-y"
                    items={[
                      {
                        color: 'accent' as const,
                        label: t('accounts.concurrency_label'),
                        muted: (row.current_concurrency ?? 0) === 0,
                        value: `${row.current_concurrency ?? 0}/${row.max_concurrency > 0 ? row.max_concurrency : '∞'}`,
                      },
                      {
                        color: 'success' as const,
                        label: 'RPM',
                        muted: (row.current_rpm ?? 0) === 0,
                        value: row.max_rpm && row.max_rpm > 0
                          ? `${row.current_rpm ?? 0}/${row.max_rpm}`
                          : String(row.current_rpm ?? 0),
                      },
                    ]}
                  />
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span
                    className="inline-block max-w-[6.5rem] truncate text-xs text-text-secondary"
                    title={row.proxy_name || (row.proxy_id != null ? `#${row.proxy_id}` : '')}
                  >
                    {row.proxy_name || (row.proxy_id != null ? `#${row.proxy_id}` : '—')}
                  </span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  {(() => {
                    const labels = resolveGroupLabels(row);
                    if (labels.length === 0) {
                      return <span className="text-xs text-text-tertiary">—</span>;
                    }
                    const visible = labels.slice(0, GROUP_DISPLAY_LIMIT);
                    const rest = labels.length - visible.length;
                    return (
                      <div
                        className="flex max-w-[10rem] flex-wrap items-center gap-1"
                        title={labels.map((g) => g.name).join('、')}
                      >
                        {visible.map((g) => (
                          <Chip
                            key={g.id}
                            size="sm"
                            variant="soft"
                            className="h-5 max-w-full min-h-0 px-1.5"
                          >
                            <span className="block max-w-[4.5rem] truncate text-[10px]">{g.name}</span>
                          </Chip>
                        ))}
                        {rest > 0 ? (
                          <span className="text-[10px] tabular-nums text-text-tertiary">+{rest}</span>
                        ) : null}
                      </div>
                    );
                  })()}
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <div className="ag-table-row-actions flex items-center justify-center gap-0.5">
                    <Button
                      isIconOnly
                      size="sm"
                      variant="secondary"
                      aria-label={t('accounts.test_connection')}
                      onPress={() => setTestingItem(row)}
                    >
                      <Play className="w-3.5 h-3.5 text-success" />
                    </Button>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="secondary"
                      aria-label={t('accounts.view_stats')}
                      onPress={() => setStatsItem(row)}
                    >
                      <BarChart3 className="w-3.5 h-3.5 text-indigo-500" />
                    </Button>
                    <span title={t('accounts.model_routing')}>
                      <Button
                        isIconOnly
                        size="sm"
                        variant="secondary"
                        aria-label={t('accounts.model_routing')}
                        onPress={() => setModelsTargets([row])}
                      >
                        <Boxes className="w-3.5 h-3.5 text-sky-500" />
                      </Button>
                    </span>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="secondary"
                      aria-label={t('accounts.toggle')}
                      isDisabled={toggleMutation.isPending}
                      onPress={() => toggleMutation.mutate(row.id)}
                    >
                      <Power className={`w-3.5 h-3.5 ${row.state === 'active' ? 'text-success' : 'text-text-tertiary'}`} />
                    </Button>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="secondary"
                      aria-label={t('common.edit')}
                      onPress={() => setEditingItem(row)}
                    >
                      <Pencil className="w-3.5 h-3.5" />
                    </Button>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="danger-soft"
                      className="text-danger"
                      aria-label={t('common.delete')}
                      onPress={() => setDeletingItem(row)}
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </Button>
                  </div>
                </CommonTable.Cell>
              </CommonTable.Row>
            ))
          )}
        </CommonTable.Body>
      </CommonTable>

      <AccountFormModal
        open={showCreateModal}
        title={t('accounts.create')}
        onClose={() => setShowCreateModal(false)}
        onSubmit={(payload) => createMutation.mutate(payload as CreateAccountReq)}
        onOAuthSuccess={() => {
          void queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
          setShowCreateModal(false);
        }}
        loading={createMutation.isPending}
      />

      {editingItem && (
        <AccountFormModal
          open
          title={t('accounts.edit')}
          account={editingItem}
          onClose={() => setEditingItem(null)}
          onSubmit={(payload) => updateMutation.mutate({ id: editingItem.id, data: payload })}
          onOAuthSuccess={() => {
            void queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
            setEditingItem(null);
          }}
          loading={updateMutation.isPending}
        />
      )}

      <AccountTestModal account={testingItem} onClose={() => setTestingItem(null)} />
      <AccountStatsModal account={statsItem} onClose={() => setStatsItem(null)} />
      <AccountModelsModal
        accounts={modelsTargets}
        onClose={() => setModelsTargets([])}
      />

      <ConfirmDialog
        open={!!deletingItem}
        onOpenChange={(open) => {
          if (!open) setDeletingItem(null);
        }}
        title={t('common.delete')}
        description={t('accounts.delete_confirm', { name: deletingItem?.name })}
        loading={deleteMutation.isPending}
        onConfirm={() => deletingItem && deleteMutation.mutate(deletingItem.id)}
      />

      <ConfirmDialog
        open={bulkDeleteOpen}
        onOpenChange={(open) => {
          if (!open) setBulkDeleteOpen(false);
        }}
        title={t('common.delete')}
        description={t('accounts.bulk_delete_confirm', { count: selectedIds.length })}
        loading={bulkPending}
        onConfirm={() => bulkMutation.mutate({ kind: 'delete', account_ids: selectedIds })}
      />

      <ConfirmDialog
        open={exportConfirmOpen}
        onOpenChange={(open) => {
          if (!open && !exporting) setExportConfirmOpen(false);
        }}
        title={t('accounts.export_selected')}
        description={t('accounts.export_selected_confirm', { count: selectedIds.length })}
        loading={exporting}
        status="warning"
        confirmVariant="primary"
        onConfirm={handleExportConfirm}
      />

      {/* 批量改优先级 */}
      <Modal state={priorityDialogState}>
        <DialogTriggerShim />
        <Modal.Backdrop>
          <Modal.Container placement="center" size="sm">
            <Modal.Dialog className="ag-elevation-modal">
              <Modal.Header>
                <Modal.Heading>
                  {t('accounts.set_priority_title', { count: selectedIds.length })}
                </Modal.Heading>
                <Modal.CloseTrigger />
              </Modal.Header>
              <Modal.Body>
                <HeroTextField fullWidth>
                  <Label>{t('accounts.priority')}</Label>
                  <Input
                    min={0}
                    max={999}
                    type="number"
                    value={bulkPriority}
                    onChange={(event) => setBulkPriority(event.target.value)}
                  />
                </HeroTextField>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="secondary" onPress={() => setPriorityModalOpen(false)}>
                  {t('common.cancel')}
                </Button>
                <Button
                  isDisabled={bulkPending}
                  variant="primary"
                  onPress={() => bulkMutation.mutate({
                    kind: 'update',
                    data: {
                      account_ids: selectedIds,
                      priority: Number(bulkPriority) || 0,
                    },
                  })}
                >
                  {bulkPending ? <Spinner size="sm" /> : null}
                  {t('common.confirm')}
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      {/* 批量改权重 */}
      <Modal state={weightDialogState}>
        <DialogTriggerShim />
        <Modal.Backdrop>
          <Modal.Container placement="center" size="sm">
            <Modal.Dialog className="ag-elevation-modal">
              <Modal.Header>
                <Modal.Heading>
                  {t('accounts.set_weight_title', { count: selectedIds.length })}
                </Modal.Heading>
                <Modal.CloseTrigger />
              </Modal.Header>
              <Modal.Body>
                <HeroTextField fullWidth>
                  <Label>{t('accounts.weight')}</Label>
                  <Input
                    min={1}
                    max={9999}
                    type="number"
                    value={bulkWeight}
                    onChange={(event) => setBulkWeight(event.target.value)}
                  />
                </HeroTextField>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="secondary" onPress={() => setWeightModalOpen(false)}>
                  {t('common.cancel')}
                </Button>
                <Button
                  isDisabled={bulkPending}
                  variant="primary"
                  onPress={() => bulkMutation.mutate({
                    kind: 'update',
                    data: {
                      account_ids: selectedIds,
                      weight: Math.max(1, Number(bulkWeight) || 1),
                    },
                  })}
                >
                  {bulkPending ? <Spinner size="sm" /> : null}
                  {t('common.confirm')}
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </div>
  );
}
