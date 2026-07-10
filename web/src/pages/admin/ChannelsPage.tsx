import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Button, Checkbox, Chip, EmptyState, Input, Label, ListBox, Modal,
  Select, Spinner, TextField as HeroTextField, Tooltip, useOverlayState,
} from '@heroui/react';
import {
  ArrowUpDown, BarChart3, Boxes, CircleCheck, CircleOff, Pencil, Plus, RefreshCw, Search, Trash2,
} from 'lucide-react';
import { channelsApi } from '../../shared/api/channels';
import { upstreamLogsApi } from '../../shared/api/upstreamLogs';
import { queryKeys } from '../../shared/queryKeys';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { usePagination } from '../../shared/hooks/usePagination';
import { useDebouncedValue } from '../../shared/hooks/useDebouncedValue';
import { useToast } from '../../shared/ui';
import { getTotalPages } from '../../shared/utils/pagination';
import { CommonTable } from '../../shared/components/CommonTable';
import { MetricChips } from '../../shared/components/MetricChips';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { DialogTriggerShim } from '../../shared/components/DialogTriggerShim';
import { ChannelFormModal, CHANNEL_TYPE_OPTIONS } from './channels/ChannelFormModal';
import { ChannelStatsModal } from './channels/ChannelStatsModal';
import { ChannelTestModal } from './channels/ChannelTestModal';
import { formatDate, formatDateTime } from '../../shared/utils/format';
import type { BulkChannelAction, ChannelFailureCounts, ChannelResp, ChannelType } from '../../shared/types';
import { ConfirmDialog } from '../../shared/components/ConfirmDialog';

const COLUMN_COUNT = 14;

// 仅 openai_compatible 中转站支持经 key 查余额；官方直连渠道无此接口。
const BALANCE_SUPPORTED_TYPES = new Set(['openai_compatible']);

// 余额自动刷新的陈旧阈值：更新时间早于此则视为陈旧、进入页面时后台刷新。
// 取 60s：主要用于「打开/切回渠道页时看到较新余额」，同时把 React 双挂载/快速
// 连续刷新去重掉，不至于每次渲染都打上游。
const BALANCE_STALE_MS = 60_000;

// isBalanceStale 从未刷新过、或超过阈值 → 陈旧。
function isBalanceStale(updatedAt: string | undefined): boolean {
  if (!updatedAt) return true;
  return Date.now() - new Date(updatedAt).getTime() > BALANCE_STALE_MS;
}

// 渠道类型 → 徽章配色
const TYPE_CHIP_COLORS: Record<ChannelType, 'accent' | 'warning' | 'success' | 'default'> = {
  openai_compatible: 'accent',
  anthropic: 'warning',
  gemini: 'success',
  custom: 'default',
  openai_video: 'accent',
  suno: 'warning',
};

function typeLabel(type: string): string {
  return CHANNEL_TYPE_OPTIONS.find((item) => item.id === type)?.label ?? type;
}

// 状态徽章：enabled 绿 / disabled_manual 灰 / disabled_auto 红 + error_msg tooltip
function ChannelStatusChip({ channel }: { channel: ChannelResp }) {
  const { t } = useTranslation();

  if (channel.status === 'disabled_auto') {
    const chip = (
      <Chip color="danger" size="sm" variant="soft">
        {t('channels.status_disabled_auto')}
      </Chip>
    );
    if (!channel.error_msg) return chip;
    return (
      <Tooltip>
        <Tooltip.Trigger className="inline-flex">{chip}</Tooltip.Trigger>
        <Tooltip.Content className="max-w-xs break-all">{channel.error_msg}</Tooltip.Content>
      </Tooltip>
    );
  }

  if (channel.status === 'disabled_manual') {
    return (
      <Chip color="default" size="sm" variant="soft">
        {t('channels.status_disabled_manual')}
      </Chip>
    );
  }

  return (
    <Chip color="success" size="sm" variant="soft">
      {t('channels.status_enabled')}
    </Chip>
  );
}

// 余额单元格：openai_compatible 显示 $X.XX + 刷新时间 + 刷新按钮；其余类型显示"不支持"。
function BalanceCell({
  row,
  isRefreshing,
  onRefresh,
}: {
  row: ChannelResp;
  isRefreshing: boolean;
  onRefresh: () => void;
}) {
  const { t } = useTranslation();

  if (!BALANCE_SUPPORTED_TYPES.has(row.type)) {
    return (
      <span className="text-xs text-text-tertiary" title={t('channels.balance_unsupported_hint')}>
        {t('channels.balance_unsupported')}
      </span>
    );
  }

  const updated = row.balance_updated_at ? new Date(row.balance_updated_at) : null;
  return (
    <div className="flex items-center gap-1.5">
      <div className="flex min-w-0 flex-col">
        {updated ? (
          <span className="font-mono text-[13px] font-medium text-text">${row.balance.toFixed(2)}</span>
        ) : (
          <span className="text-xs text-text-tertiary">{t('channels.balance_never')}</span>
        )}
        {updated ? (
          <span className="text-[11px] text-text-tertiary" title={formatDateTime(updated)}>
            {formatDate(updated)}
          </span>
        ) : null}
      </div>
      <Button
        isIconOnly
        aria-label={t('channels.refresh_balance')}
        isDisabled={isRefreshing}
        size="sm"
        variant="ghost"
        onPress={onRefresh}
      >
        {isRefreshing ? <Spinner size="sm" /> : <RefreshCw className="h-3.5 w-3.5" />}
      </Button>
    </div>
  );
}

export default function ChannelsPage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();

  const { page, setPage, pageSize, setPageSize } = usePagination(20, 'admin.channels');
  const [keyword, setKeyword] = useState('');
  const debouncedKeyword = useDebouncedValue(keyword.trim(), 250);
  const [typeFilter, setTypeFilter] = useState('');
  const [statusFilter, setStatusFilter] = useState('');
  const [selectedIds, setSelectedIds] = useState<number[]>([]);

  const [formOpen, setFormOpen] = useState(false);
  const [editingChannel, setEditingChannel] = useState<ChannelResp | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<ChannelResp | null>(null);
  const [bulkDeleteOpen, setBulkDeleteOpen] = useState(false);
  const [priorityModalOpen, setPriorityModalOpen] = useState(false);
  const [bulkPriority, setBulkPriority] = useState('50');
  const [testTarget, setTestTarget] = useState<ChannelResp | null>(null);
  const [statsTarget, setStatsTarget] = useState<ChannelResp | null>(null);

  const listQuery = useMemo(() => ({
    page,
    page_size: pageSize,
    keyword: debouncedKeyword || undefined,
    type: typeFilter || undefined,
    status: statusFilter || undefined,
  }), [page, pageSize, debouncedKeyword, typeFilter, statusFilter]);

  const { data, isLoading, refetch } = useQuery({
    queryKey: queryKeys.channels(listQuery),
    queryFn: () => channelsApi.list(listQuery),
    placeholderData: keepPreviousData,
  });

  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);

  // 渠道近 30 分钟失败计数（errlog Redis 分钟桶），30s 轮询；Redis 缺失时后端返回全 0。
  const channelIds = rows.map((row) => row.id);
  const { data: failureStats } = useQuery({
    queryKey: queryKeys.channelFailureStats(channelIds),
    queryFn: () => upstreamLogsApi.channelFailureStats(channelIds),
    enabled: channelIds.length > 0,
    refetchInterval: 30_000,
    placeholderData: keepPreviousData,
  });
  const failureByChannel = useMemo(() => {
    const map = new Map<number, ChannelFailureCounts>();
    for (const item of failureStats?.channels ?? []) map.set(item.channel_id, item);
    return map;
  }, [failureStats]);

  // 删除单个渠道
  const deleteMutation = useCrudMutation({
    mutationFn: (id: number) => channelsApi.delete(id),
    successMessage: t('channels.delete_success'),
    queryKey: queryKeys.channels(),
    onSuccess: (_, id) => {
      setDeleteTarget(null);
      setSelectedIds((prev) => prev.filter((item) => item !== id));
    },
  });

  // 批量操作（启用/禁用/删除/改优先级）
  const bulkMutation = useMutation({
    mutationFn: (payload: { ids: number[]; action: BulkChannelAction; priority?: number }) =>
      channelsApi.bulkUpdate(payload),
    onSuccess: (resp) => {
      toast('success', t('channels.bulk_success', { count: resp.affected }));
      queryClient.invalidateQueries({ queryKey: queryKeys.channels() });
      setSelectedIds([]);
      setBulkDeleteOpen(false);
      setPriorityModalOpen(false);
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 刷新单个渠道余额（经 key 查上游）。variables 记录目标 id，用于给对应行按钮显示 loading。
  const balanceMutation = useMutation({
    mutationFn: (id: number) => channelsApi.refreshBalance(id),
    onSuccess: (resp) => {
      toast('success', t('channels.balance_refreshed', { amount: resp.balance.toFixed(2) }));
      queryClient.invalidateQueries({ queryKey: queryKeys.channels() });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 进入渠道页 / 翻页时自动刷新可见渠道的余额（后台、串行、只刷陈旧的）。
  // autoRefreshedRef 记录本次挂载已发起过的渠道，防 React 重渲染/双挂载重复打上游；
  // 刷新后 balance_updated_at 变新 → isBalanceStale 返回 false → 不再重刷（天然收敛）。
  const autoRefreshedRef = useRef<Set<number>>(new Set());
  useEffect(() => {
    const stale = rows.filter(
      (row) =>
        BALANCE_SUPPORTED_TYPES.has(row.type) &&
        !autoRefreshedRef.current.has(row.id) &&
        isBalanceStale(row.balance_updated_at),
    );
    if (stale.length === 0) return;
    stale.forEach((row) => autoRefreshedRef.current.add(row.id));

    let cancelled = false;
    void (async () => {
      let updated = false;
      for (const row of stale) {
        if (cancelled) break;
        try {
          await channelsApi.refreshBalance(row.id);
          updated = true;
        } catch {
          // 不支持/失败静默跳过：自动刷新不打扰用户，手动刷新才提示错误。
        }
      }
      if (!cancelled && updated) {
        queryClient.invalidateQueries({ queryKey: queryKeys.channels() });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [rows, queryClient]);

  // 批量刷新余额：串行逐个刷（避免并发打爆中转站），支持的渠道成功、不支持的跳过。
  const [batchBalanceRunning, setBatchBalanceRunning] = useState(false);
  async function handleBatchRefreshBalance() {
    setBatchBalanceRunning(true);
    let ok = 0;
    for (const id of selectedIds) {
      try {
        await channelsApi.refreshBalance(id);
        ok += 1;
      } catch {
        // 不支持/失败的渠道跳过，不中断整批。
      }
    }
    setBatchBalanceRunning(false);
    queryClient.invalidateQueries({ queryKey: queryKeys.channels() });
    toast('success', t('channels.balance_batch_done', { ok, total: selectedIds.length }));
  }

  function openCreate() {
    setEditingChannel(null);
    setFormOpen(true);
  }

  function openEdit(channel: ChannelResp) {
    setEditingChannel(channel);
    setFormOpen(true);
  }

  function toggleSelected(id: number, selected: boolean) {
    setSelectedIds((prev) => (selected ? [...new Set([...prev, id])] : prev.filter((item) => item !== id)));
  }

  const pageIds = rows.map((row) => row.id);
  const allPageSelected = pageIds.length > 0 && pageIds.every((id) => selectedIds.includes(id));

  function toggleSelectAll(selected: boolean) {
    setSelectedIds((prev) => (
      selected
        ? [...new Set([...prev, ...pageIds])]
        : prev.filter((id) => !pageIds.includes(id))
    ));
  }

  const typeFilterOptions = [
    { id: '', label: t('channels.all_types') },
    ...CHANNEL_TYPE_OPTIONS,
  ];
  const statusFilterOptions = [
    { id: '', label: t('channels.all_statuses') },
    { id: 'enabled', label: t('channels.status_enabled') },
    { id: 'disabled_manual', label: t('channels.status_disabled_manual') },
    { id: 'disabled_auto', label: t('channels.status_disabled_auto') },
  ];
  const selectedTypeLabel = typeFilterOptions.find((item) => item.id === typeFilter)?.label ?? t('channels.all_types');
  const selectedStatusLabel = statusFilterOptions.find((item) => item.id === statusFilter)?.label ?? t('channels.all_statuses');

  const priorityDialogState = useOverlayState({
    isOpen: priorityModalOpen,
    onOpenChange: (open) => {
      if (!open) setPriorityModalOpen(false);
    },
  });

  const bulkPending = bulkMutation.isPending;

  return (
    <div>
      {/* 筛选 + 工具栏 */}
      <div className="mb-5 flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-center">
        <div className="relative w-full sm:w-56">
          <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
          <Input
            aria-label={t('common.search')}
            className="pl-9"
            placeholder={t('channels.search_placeholder')}
            value={keyword}
            onChange={(event) => {
              setKeyword(event.target.value);
              setPage(1);
            }}
          />
        </div>
        <div className="w-full sm:w-44">
          <Select
            aria-label={t('common.type')}
            fullWidth
            selectedKey={typeFilter}
            onSelectionChange={(key) => {
              setTypeFilter(key == null ? '' : String(key));
              setPage(1);
            }}
          >
            <Select.Trigger>
              <Select.Value>{selectedTypeLabel}</Select.Value>
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox items={typeFilterOptions}>
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
            aria-label={t('common.status')}
            fullWidth
            selectedKey={statusFilter}
            onSelectionChange={(key) => {
              setStatusFilter(key == null ? '' : String(key));
              setPage(1);
            }}
          >
            <Select.Trigger>
              <Select.Value>{selectedStatusLabel}</Select.Value>
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox items={statusFilterOptions}>
                {(item) => (
                  <ListBox.Item id={item.id} textValue={item.label}>
                    {item.label}
                  </ListBox.Item>
                )}
              </ListBox>
            </Select.Popover>
          </Select>
        </div>
        <div className="ml-auto flex items-center gap-2">
          <Button
            isIconOnly
            aria-label={t('common.refresh', 'Refresh')}
            size="md"
            variant="ghost"
            onPress={() => refetch()}
          >
            <RefreshCw className="h-4 w-4" />
          </Button>
          <Button variant="primary" onPress={openCreate}>
            <Plus className="h-4 w-4" />
            {t('channels.create')}
          </Button>
        </div>
      </div>

      {/* 批量操作条 */}
      {selectedIds.length > 0 ? (
        <div className="mb-3 flex flex-wrap items-center gap-2 rounded-[var(--radius)] border border-border bg-surface px-3 py-2">
          <span className="text-sm text-text-secondary">
            {t('channels.selected_count', { count: selectedIds.length })}
          </span>
          <Button
            isDisabled={bulkPending}
            size="sm"
            variant="secondary"
            onPress={() => bulkMutation.mutate({ ids: selectedIds, action: 'enable' })}
          >
            <CircleCheck className="h-3.5 w-3.5" />
            {t('common.enable')}
          </Button>
          <Button
            isDisabled={bulkPending}
            size="sm"
            variant="secondary"
            onPress={() => bulkMutation.mutate({ ids: selectedIds, action: 'disable' })}
          >
            <CircleOff className="h-3.5 w-3.5" />
            {t('common.disable')}
          </Button>
          <Button
            isDisabled={bulkPending || batchBalanceRunning}
            size="sm"
            variant="secondary"
            onPress={handleBatchRefreshBalance}
          >
            {batchBalanceRunning ? <Spinner size="sm" /> : <RefreshCw className="h-3.5 w-3.5" />}
            {t('channels.refresh_balance')}
          </Button>
          <Button
            isDisabled={bulkPending}
            size="sm"
            variant="secondary"
            onPress={() => setPriorityModalOpen(true)}
          >
            <ArrowUpDown className="h-3.5 w-3.5" />
            {t('channels.bulk_set_priority')}
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

      <CommonTable
        ariaLabel={t('channels.title')}
        className="ag-channels-table"
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
        minWidth={1220}
      >
        <CommonTable.Header>
          <CommonTable.Column id="select" style={{ width: 44 }}>
            <Checkbox
              aria-label={t('channels.select_all')}
              isSelected={allPageSelected}
              onChange={toggleSelectAll}
            >
              <Checkbox.Control>
                <Checkbox.Indicator />
              </Checkbox.Control>
            </Checkbox>
          </CommonTable.Column>
          <CommonTable.Column id="id" style={{ width: 64 }}>
            {t('common.id')}
          </CommonTable.Column>
          <CommonTable.Column id="name">{t('common.name')}</CommonTable.Column>
          <CommonTable.Column id="type">{t('common.type')}</CommonTable.Column>
          <CommonTable.Column id="status">{t('common.status')}</CommonTable.Column>
          <CommonTable.Column id="priority">{t('channels.priority')}</CommonTable.Column>
          <CommonTable.Column id="weight">{t('channels.weight')}</CommonTable.Column>
          <CommonTable.Column id="models">{t('channels.models')}</CommonTable.Column>
          <CommonTable.Column id="runtime" style={{ width: '9.75rem' }}>
            <span title={t('channels.concurrency_rpm_hint')}>{t('channels.concurrency_rpm')}</span>
          </CommonTable.Column>
          <CommonTable.Column id="money" style={{ width: '9.75rem' }}>
            <span title={t('channels.stats_hint')}>{t('channels.stats_header')}</span>
          </CommonTable.Column>
          <CommonTable.Column id="response_time">{t('channels.response_time')}</CommonTable.Column>
          <CommonTable.Column id="balance" style={{ width: '9rem' }}>
            <span title={t('channels.balance_hint')}>{t('channels.balance')}</span>
          </CommonTable.Column>
          <CommonTable.Column id="tags">{t('channels.tags')}</CommonTable.Column>
          <CommonTable.Column id="actions">{t('common.actions')}</CommonTable.Column>
        </CommonTable.Header>
        <CommonTable.Body>
          {isLoading ? (
            <TableLoadingRow colSpan={COLUMN_COUNT} />
          ) : rows.length === 0 ? (
            <CommonTable.Row id="empty">
              <CommonTable.Cell colSpan={COLUMN_COUNT}>
                <EmptyState>
                  <div className="text-sm text-default-500">{t('common.no_data')}</div>
                </EmptyState>
              </CommonTable.Cell>
            </CommonTable.Row>
          ) : (
            rows.map((row) => (
              <CommonTable.Row id={String(row.id)} key={row.id}>
                <CommonTable.Cell>
                  <Checkbox
                    aria-label={`select ${row.name}`}
                    isSelected={selectedIds.includes(row.id)}
                    onChange={(selected) => toggleSelected(row.id, selected)}
                  >
                    <Checkbox.Control>
                      <Checkbox.Indicator />
                    </Checkbox.Control>
                  </Checkbox>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono text-text-tertiary">{row.id}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <div className="flex min-w-0 flex-col">
                    <span className="truncate font-medium text-text" title={row.name}>{row.name}</span>
                    <span className="truncate font-mono text-[11px] text-text-tertiary" title={row.base_url}>
                      {row.base_url}
                    </span>
                  </div>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <Chip color={TYPE_CHIP_COLORS[row.type] ?? 'default'} size="sm" variant="soft">
                    {typeLabel(row.type)}
                  </Chip>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <ChannelStatusChip channel={row} />
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono text-text-secondary">{row.priority}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono text-text-secondary">{row.weight}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  {row.models.length > 0 ? (
                    <Tooltip>
                      <Tooltip.Trigger className="inline-flex">
                        <Chip color="default" size="sm" variant="soft">
                          {t('channels.model_count', { count: row.models.length })}
                        </Chip>
                      </Tooltip.Trigger>
                      <Tooltip.Content className="max-w-sm">
                        <div className="max-h-56 overflow-y-auto font-mono text-xs leading-5">
                          {row.models.map((model) => (
                            <div key={model}>{model}</div>
                          ))}
                        </div>
                      </Tooltip.Content>
                    </Tooltip>
                  ) : (
                    <span className="text-text-tertiary">-</span>
                  )}
                </CommonTable.Cell>
                <CommonTable.Cell className="ag-channels-metric-cell">
                  <MetricChips
                    className="ag-metric-chips--stack ag-metric-chips--compact-y"
                    items={[
                      {
                        color: 'accent' as const,
                        label: t('channels.concurrency_label'),
                        muted: (row.current_concurrency ?? 0) === 0,
                        value: `${row.current_concurrency ?? 0}/${row.max_concurrency > 0 ? row.max_concurrency : '∞'}`,
                      },
                      {
                        color: 'success' as const,
                        label: 'RPM',
                        muted: (row.current_rpm ?? 0) === 0,
                        value: String(row.current_rpm ?? 0),
                      },
                      {
                        color: 'danger' as const,
                        label: t('channels.failures_label'),
                        muted: (failureByChannel.get(row.id)?.total ?? 0) === 0,
                        value: String(failureByChannel.get(row.id)?.total ?? 0),
                      },
                    ]}
                  />
                </CommonTable.Cell>
                <CommonTable.Cell className="ag-channels-metric-cell">
                  <MetricChips
                    className="ag-metric-chips--stack ag-metric-chips--compact-y"
                    items={[
                      {
                        amount: row.today_cost ?? 0,
                        color: 'warning' as const,
                        decimals: 2,
                        dollarTone: 'warning' as const,
                        label: t('channels.stats_today_cost'),
                        mutedWhenZero: true,
                      },
                      {
                        amount: row.total_cost ?? 0,
                        color: 'warning' as const,
                        decimals: 2,
                        dollarTone: 'warning' as const,
                        label: t('channels.stats_cost'),
                        mutedWhenZero: true,
                      },
                      {
                        amount: row.total_revenue ?? 0,
                        color: 'success' as const,
                        decimals: 2,
                        dollarTone: 'success' as const,
                        label: t('channels.stats_revenue'),
                        mutedWhenZero: true,
                      },
                    ]}
                  />
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono text-text-secondary">
                    {row.response_time_ms > 0 ? `${row.response_time_ms}ms` : '-'}
                  </span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <BalanceCell
                    row={row}
                    isRefreshing={balanceMutation.isPending && balanceMutation.variables === row.id}
                    onRefresh={() => balanceMutation.mutate(row.id)}
                  />
                </CommonTable.Cell>
                <CommonTable.Cell>
                  {row.tags.length > 0 ? (
                    <div className="flex max-w-[180px] flex-wrap gap-1">
                      {row.tags.map((tag) => (
                        <Chip color="default" key={tag} size="sm" variant="soft">
                          {tag}
                        </Chip>
                      ))}
                    </div>
                  ) : (
                    <span className="text-text-tertiary">-</span>
                  )}
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <div className="ag-table-row-actions flex justify-center gap-1">
                    <Button size="sm" variant="secondary" onPress={() => openEdit(row)}>
                      <Pencil className="h-3.5 w-3.5" />
                      {t('common.edit')}
                    </Button>
                    <Button
                      size="sm"
                      variant="secondary"
                      onPress={() => setTestTarget(row)}
                    >
                      <Boxes className="h-3.5 w-3.5" />
                      {t('channels.models')}
                    </Button>
                    <Button
                      size="sm"
                      variant="secondary"
                      onPress={() => setStatsTarget(row)}
                    >
                      <BarChart3 className="h-3.5 w-3.5" />
                      {t('channels.stats_action')}
                    </Button>
                    <Button
                      className="text-danger"
                      size="sm"
                      variant="danger-soft"
                      onPress={() => setDeleteTarget(row)}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                      {t('common.delete')}
                    </Button>
                  </div>
                </CommonTable.Cell>
              </CommonTable.Row>
            ))
          )}
        </CommonTable.Body>
      </CommonTable>

      {/* 创建/编辑弹窗 */}
      <ChannelFormModal
        channel={editingChannel}
        open={formOpen}
        onClose={() => {
          setFormOpen(false);
          setEditingChannel(null);
        }}
      />

      {/* 模型与测试弹窗（模型清单/映射/测试模型管理 + 逐个或全部测试） */}
      <ChannelTestModal
        channel={testTarget}
        onClose={() => setTestTarget(null)}
      />

      {/* 消耗统计弹窗（每日消耗 + 模型分布，按渠道过滤的仪表盘趋势） */}
      <ChannelStatsModal
        channel={statsTarget}
        onClose={() => setStatsTarget(null)}
      />

      {/* 批量改优先级 */}
      <Modal state={priorityDialogState}>
        <DialogTriggerShim />
        <Modal.Backdrop>
          <Modal.Container placement="center" size="sm">
            <Modal.Dialog className="ag-elevation-modal">
              <Modal.Header>
                <Modal.Heading>{t('channels.set_priority_title', { count: selectedIds.length })}</Modal.Heading>
                <Modal.CloseTrigger />
              </Modal.Header>
              <Modal.Body>
                <HeroTextField fullWidth>
                  <Label>{t('channels.priority')}</Label>
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
                    ids: selectedIds,
                    action: 'set_priority',
                    priority: Number(bulkPriority) || 0,
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

      {/* 删除单个确认 */}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
        title={t('channels.delete_channel')}
        description={t('channels.delete_confirm', { name: deleteTarget?.name })}
        loading={deleteMutation.isPending}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
      />

      {/* 批量删除确认 */}
      <ConfirmDialog
        open={bulkDeleteOpen}
        onOpenChange={(open) => {
          if (!open) setBulkDeleteOpen(false);
        }}
        title={t('channels.delete_channel')}
        description={t('channels.bulk_delete_confirm', { count: selectedIds.length })}
        loading={bulkPending}
        onConfirm={() => bulkMutation.mutate({ ids: selectedIds, action: 'delete' })}
      />
    </div>
  );
}
