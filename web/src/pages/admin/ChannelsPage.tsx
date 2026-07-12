import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Button, Checkbox, Chip, EmptyState, Input, Label, ListBox, Modal,
  Select, Spinner, TextField as HeroTextField, Tooltip, useOverlayState,
} from '@heroui/react';
import {
  ArrowUpDown, BarChart3, Boxes, ChevronDown, ChevronRight, CircleCheck, CircleOff,
  KeyRound, Pencil, Plus, RefreshCw, Search, Trash2,
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
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { DialogTriggerShim } from '../../shared/components/DialogTriggerShim';
import { NativeSwitch } from '../../shared/components/NativeSwitch';
import { ChannelFormModal, CHANNEL_TYPE_OPTIONS } from './channels/ChannelFormModal';
import { KeyFormModal } from './channels/KeyFormModal';
import { ChannelStatsModal } from './channels/ChannelStatsModal';
import { ChannelTestModal } from './channels/ChannelTestModal';
import { formatDate, formatDateTime } from '../../shared/utils/format';
import type {
  ChannelFailureCounts, ChannelKeyResp, ChannelResp, ChannelStatus, ChannelType,
} from '../../shared/types';
import { ConfirmDialog } from '../../shared/components/ConfirmDialog';

const COLUMN_COUNT = 7;

// 仅 openai_compatible 中转站支持经 key 查余额。
function keySupportsBalance(key: ChannelKeyResp): boolean {
  return key.type === 'openai_compatible';
}

// 余额陈旧阈值：更新时间早于此则进入页面时后台自动刷新。
const BALANCE_STALE_MS = 60_000;

// isKeyBalanceStale 可查余额的 key 从未刷新过、或超过阈值 → 陈旧。
function isKeyBalanceStale(key: ChannelKeyResp): boolean {
  if (!keySupportsBalance(key)) return false;
  if (!key.balance_updated_at) return true;
  return Date.now() - new Date(key.balance_updated_at).getTime() > BALANCE_STALE_MS;
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

// key 状态徽章：enabled 绿 / disabled_manual 灰 / disabled_auto 红 + error_msg tooltip
function KeyStatusChip({ status, errorMsg }: { status: ChannelStatus; errorMsg: string }) {
  const { t } = useTranslation();

  if (status === 'disabled_auto') {
    const chip = (
      <Chip color="danger" size="sm" variant="soft">
        {t('channels.status_disabled_auto')}
      </Chip>
    );
    if (!errorMsg) return chip;
    return (
      <Tooltip>
        <Tooltip.Trigger className="inline-flex">{chip}</Tooltip.Trigger>
        <Tooltip.Content className="max-w-xs break-all">{errorMsg}</Tooltip.Content>
      </Tooltip>
    );
  }

  if (status === 'disabled_manual') {
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

// 一段带标签的行内指标：小标题在上、值在下，右对齐数值成列。
function Metric({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1 leading-none">
      <span className="whitespace-nowrap text-[10px] uppercase tracking-wide text-text-tertiary">{label}</span>
      <span className="whitespace-nowrap font-mono text-xs text-text-secondary">{children}</span>
    </div>
  );
}

// 指标分组之间的竖向分隔线。
function MetricDivider() {
  return <span className="hidden h-7 w-px self-center bg-border sm:block" />;
}

// 展开区的一把 key 子行：两行卡片——头部（名称/类型/启停/密钥/操作）+ 指标行。
function KeyRow({
  channelKey,
  onOpenModels,
  onEdit,
  onDelete,
  onStats,
  onRefreshBalance,
  refreshingBalance,
  onToggleEnabled,
  toggling,
}: {
  channelKey: ChannelKeyResp;
  onOpenModels: () => void;
  onEdit: () => void;
  onDelete: () => void;
  onStats: () => void;
  onRefreshBalance: () => void;
  refreshingBalance: boolean;
  onToggleEnabled: (enabled: boolean) => void;
  toggling: boolean;
}) {
  const { t } = useTranslation();
  const supportsBalance = keySupportsBalance(channelKey);
  const balanceUpdated = channelKey.balance_updated_at ? new Date(channelKey.balance_updated_at) : null;
  const fmt = (n: number) => `$${n.toFixed(2)}`;

  return (
    <div className="border-b border-border px-4 py-3 last:border-b-0">
      {/* 头部：名称 · 类型 · 启停开关 · 密钥提示 —— 操作靠右 */}
      <div className="flex items-center gap-2.5">
        <span className="max-w-[200px] truncate font-medium text-text" title={channelKey.name}>
          {channelKey.name || t('channels.key_unnamed')}
        </span>
        <Chip color={TYPE_CHIP_COLORS[channelKey.type] ?? 'default'} size="sm" variant="soft">
          {typeLabel(channelKey.type)}
        </Chip>
        {/* 直接点击启停：on=enabled，off=手动禁用 */}
        <NativeSwitch
          ariaLabel={t('channels.status_enabled')}
          isDisabled={toggling}
          isSelected={channelKey.status === 'enabled'}
          onChange={onToggleEnabled}
        />
        {channelKey.status === 'disabled_auto' ? (
          <KeyStatusChip errorMsg={channelKey.error_msg} status={channelKey.status} />
        ) : null}
        <span className="font-mono text-[11px] text-text-tertiary" title={t('channels.api_key')}>
          {channelKey.api_key_hint || '-'}
        </span>
        <div className="ml-auto flex items-center gap-1">
          <Button size="sm" variant="secondary" onPress={onStats}>
            <BarChart3 className="h-3.5 w-3.5" />
            {t('channels.stats_action')}
          </Button>
          <Button size="sm" variant="secondary" onPress={onOpenModels}>
            <Boxes className="h-3.5 w-3.5" />
            {t('channels.models')}
          </Button>
          <Button size="sm" variant="secondary" onPress={onEdit}>
            <Pencil className="h-3.5 w-3.5" />
            {t('common.edit')}
          </Button>
          <Button className="text-danger" size="sm" variant="danger-soft" onPress={onDelete}>
            <Trash2 className="h-3.5 w-3.5" />
            {t('common.delete')}
          </Button>
        </div>
      </div>

      {/* 指标行：配置组 | 运行时组 | 金额组 | 标签，组间竖线分隔 */}
      <div className="mt-3 flex flex-wrap items-start gap-x-4 gap-y-3">
        {/* 配置组：模型 · 优先级权重 · 成本倍率 */}
        <div className="flex items-start gap-x-4">
          <Metric label={t('channels.models')}>
            {channelKey.models.length > 0 ? (
              <Tooltip>
                <Tooltip.Trigger className="inline-flex cursor-help">
                  <span className="text-text">{channelKey.models.length}</span>
                </Tooltip.Trigger>
                <Tooltip.Content className="max-w-sm">
                  <div className="max-h-56 overflow-y-auto font-mono text-xs leading-5">
                    {channelKey.models.map((model) => (
                      <div key={model}>{model}</div>
                    ))}
                  </div>
                </Tooltip.Content>
              </Tooltip>
            ) : (
              <span className="text-text-tertiary">-</span>
            )}
          </Metric>
          <Metric label={`${t('channels.priority')}·${t('channels.weight')}`}>
            P{channelKey.priority} · W{channelKey.weight}
          </Metric>
          <Metric label={t('channels.cost_ratio')}>×{channelKey.cost_ratio}</Metric>
        </div>

        <MetricDivider />

        {/* 运行时组：并发 · RPM */}
        <div className="flex items-start gap-x-4">
          <Metric label={t('channels.concurrency_label')}>
            {channelKey.current_concurrency}/{channelKey.max_concurrency > 0 ? channelKey.max_concurrency : '∞'}
          </Metric>
          <Metric label="RPM">{channelKey.current_rpm}</Metric>
        </div>

        <MetricDivider />

        {/* 金额组：今日成本 · 今日收益 · 成本 · 收益 · 余额 */}
        <div className="flex items-start gap-x-4">
          <Metric label={t('channels.stats_today_cost')}>
            <span className={channelKey.today_cost > 0 ? 'text-warning' : ''}>{fmt(channelKey.today_cost)}</span>
          </Metric>
          <Metric label={t('channels.stats_today_revenue')}>
            <span className={channelKey.today_revenue > 0 ? 'text-success' : ''}>{fmt(channelKey.today_revenue)}</span>
          </Metric>
          <Metric label={t('channels.stats_cost')}>
            <span className={channelKey.total_cost > 0 ? 'text-warning' : ''}>{fmt(channelKey.total_cost)}</span>
          </Metric>
          <Metric label={t('channels.stats_revenue')}>
            <span className={channelKey.total_revenue > 0 ? 'text-success' : ''}>{fmt(channelKey.total_revenue)}</span>
          </Metric>
          {supportsBalance ? (
            <Metric label={t('channels.balance')}>
              <span className="inline-flex items-center gap-1">
                {balanceUpdated ? fmt(channelKey.balance) : <span className="text-text-tertiary">{t('channels.balance_never')}</span>}
                <Button
                  isIconOnly
                  aria-label={t('channels.refresh_balance')}
                  className="h-5 min-h-0 w-5"
                  isDisabled={refreshingBalance}
                  size="sm"
                  variant="ghost"
                  onPress={onRefreshBalance}
                >
                  {refreshingBalance ? <Spinner size="sm" /> : <RefreshCw className="h-3 w-3" />}
                </Button>
              </span>
            </Metric>
          ) : null}
        </div>

        {channelKey.tags.length > 0 ? (
          <>
            <MetricDivider />
            <div className="flex flex-wrap gap-1 self-center">
              {channelKey.tags.map((tag) => (
                <Chip color="default" key={tag} size="sm" variant="soft">
                  {tag}
                </Chip>
              ))}
            </div>
          </>
        ) : null}
      </div>
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
  const [expandedIds, setExpandedIds] = useState<Set<number>>(new Set());

  const [formOpen, setFormOpen] = useState(false);
  const [editingChannel, setEditingChannel] = useState<ChannelResp | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<ChannelResp | null>(null);
  const [bulkDeleteOpen, setBulkDeleteOpen] = useState(false);
  const [priorityModalOpen, setPriorityModalOpen] = useState(false);
  const [bulkPriority, setBulkPriority] = useState('50');
  // 「模型」弹窗与统计弹窗都按 key
  const [testTarget, setTestTarget] = useState<ChannelKeyResp | null>(null);
  const [keyStatsTarget, setKeyStatsTarget] = useState<ChannelKeyResp | null>(null);
  // 新增/编辑 key 弹窗：addChannelId 走新增模式，editingKey 走编辑模式
  const [keyFormOpen, setKeyFormOpen] = useState(false);
  const [addKeyChannelId, setAddKeyChannelId] = useState<number | null>(null);
  const [editingKey, setEditingKey] = useState<ChannelKeyResp | null>(null);
  const [deleteKeyTarget, setDeleteKeyTarget] = useState<ChannelKeyResp | null>(null);

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

  // 删除单把 key
  const deleteKeyMutation = useCrudMutation({
    mutationFn: (id: number) => channelsApi.deleteKey(id),
    successMessage: t('channels.delete_key_success'),
    queryKey: queryKeys.channels(),
    onSuccess: () => setDeleteKeyTarget(null),
  });

  // 批量操作（启用/禁用/删除/改优先级，作用于选中渠道下全部 key）
  const bulkMutation = useMutation({
    mutationFn: (payload: { ids: number[]; action: 'enable' | 'disable' | 'delete' | 'set_priority'; priority?: number }) =>
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

  // 直接启停单把 key（不弹窗）：on=enabled，off=手动禁用。
  const keyStatusMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) =>
      channelsApi.updateKey(id, { status: enabled ? 'enabled' : 'disabled_manual' }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.channels() });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 刷新单把 key 余额。variables 记录目标 keyID，用于给对应按钮显示 loading。
  const keyBalanceMutation = useMutation({
    mutationFn: (keyId: number) => channelsApi.refreshBalance(keyId),
    onSuccess: (resp) => {
      toast('success', t('channels.balance_refreshed', { amount: resp.balance.toFixed(2) }));
      queryClient.invalidateQueries({ queryKey: queryKeys.channels() });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 一键刷新所有渠道下可查余额（openai_compatible）的 key：串行逐个刷，失败跳过不中断。
  const [batchBalanceRunning, setBatchBalanceRunning] = useState(false);
  async function handleRefreshAllBalance() {
    setBatchBalanceRunning(true);
    let ok = 0;
    try {
      const all = await channelsApi.list({ page: 1, page_size: 1000 });
      const keys = all.list.flatMap((ch) => ch.keys).filter((k) => k.type === 'openai_compatible');
      const total = keys.length;
      if (total === 0) {
        toast('info', t('channels.balance_batch_none'));
        return;
      }
      for (const k of keys) {
        try {
          await channelsApi.refreshBalance(k.id);
          ok += 1;
        } catch {
          // 单把 key 失败跳过，不中断整批。
        }
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.channels() });
      toast('success', t('channels.balance_batch_done', { ok, total }));
    } finally {
      setBatchBalanceRunning(false);
    }
  }

  // 进入渠道页 / 翻页时自动刷新可见渠道下陈旧的 key 余额（后台、串行、只刷陈旧的、每 key 每次挂载只刷一次）。
  const autoRefreshedRef = useRef<Set<number>>(new Set());
  useEffect(() => {
    const stale = rows
      .flatMap((ch) => ch.keys)
      .filter((k) => isKeyBalanceStale(k) && !autoRefreshedRef.current.has(k.id));
    if (stale.length === 0) return;
    stale.forEach((k) => autoRefreshedRef.current.add(k.id));

    let cancelled = false;
    void (async () => {
      let updated = false;
      for (const k of stale) {
        if (cancelled) break;
        try {
          await channelsApi.refreshBalance(k.id);
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

  function openCreate() {
    setEditingChannel(null);
    setFormOpen(true);
  }

  function openEdit(channel: ChannelResp) {
    setEditingChannel(channel);
    setFormOpen(true);
  }

  function openAddKey(channelId: number) {
    setEditingKey(null);
    setAddKeyChannelId(channelId);
    setKeyFormOpen(true);
    setExpandedIds((prev) => new Set(prev).add(channelId));
  }

  function openEditKey(key: ChannelKeyResp) {
    setAddKeyChannelId(null);
    setEditingKey(key);
    setKeyFormOpen(true);
  }

  function toggleExpanded(id: number) {
    setExpandedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
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
            isDisabled={batchBalanceRunning}
            variant="secondary"
            onPress={handleRefreshAllBalance}
          >
            {batchBalanceRunning ? <Spinner size="sm" /> : <RefreshCw className="h-4 w-4" />}
            {t('channels.refresh_all_balance')}
          </Button>
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
        minWidth={980}
      >
        <CommonTable.Header>
          <CommonTable.Column id="expand" style={{ width: 40 }} />
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
          <CommonTable.Column id="keys">{t('channels.keys_label')}</CommonTable.Column>
          <CommonTable.Column id="created">{t('channels.created_at')}</CommonTable.Column>
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
            rows.map((row) => {
              const expanded = expandedIds.has(row.id);
              const failures = failureByChannel.get(row.id)?.total ?? 0;
              return (
                <Fragment key={row.id}>
                  <CommonTable.Row id={String(row.id)}>
                    <CommonTable.Cell>
                      <Button
                        isIconOnly
                        aria-label={expanded ? t('channels.collapse') : t('channels.expand')}
                        size="sm"
                        variant="ghost"
                        onPress={() => toggleExpanded(row.id)}
                      >
                        {expanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
                      </Button>
                    </CommonTable.Cell>
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
                      <div className="flex items-center gap-1.5">
                        <Chip color="default" size="sm" variant="soft">
                          {t('channels.key_count', { count: row.keys.length })}
                        </Chip>
                        {failures > 0 ? (
                          <Tooltip>
                            <Tooltip.Trigger className="inline-flex">
                              <Chip color="danger" size="sm" variant="soft">
                                {failures}
                              </Chip>
                            </Tooltip.Trigger>
                            <Tooltip.Content className="max-w-xs">{t('channels.concurrency_rpm_hint')}</Tooltip.Content>
                          </Tooltip>
                        ) : null}
                      </div>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <span className="text-xs text-text-secondary" title={formatDateTime(row.created_at)}>
                        {formatDate(row.created_at)}
                      </span>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <div className="ag-table-row-actions flex justify-center gap-1">
                        <Button size="sm" variant="secondary" onPress={() => openAddKey(row.id)}>
                          <KeyRound className="h-3.5 w-3.5" />
                          {t('channels.add_key')}
                        </Button>
                        <Button size="sm" variant="secondary" onPress={() => openEdit(row)}>
                          <Pencil className="h-3.5 w-3.5" />
                          {t('common.edit')}
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
                  {expanded ? (
                    <CommonTable.Row id={`${row.id}-keys`}>
                      <CommonTable.Cell colSpan={COLUMN_COUNT}>
                        <div className="rounded-[var(--radius)] border border-border bg-surface">
                          {row.keys.length === 0 ? (
                            <div className="flex items-center justify-between px-3 py-4">
                              <span className="text-xs text-text-tertiary">{t('channels.no_keys')}</span>
                              <Button size="sm" variant="secondary" onPress={() => openAddKey(row.id)}>
                                <Plus className="h-3.5 w-3.5" />
                                {t('channels.add_key')}
                              </Button>
                            </div>
                          ) : (
                            row.keys.map((key) => (
                              <KeyRow
                                channelKey={key}
                                key={key.id}
                                refreshingBalance={keyBalanceMutation.isPending && keyBalanceMutation.variables === key.id}
                                toggling={keyStatusMutation.isPending && keyStatusMutation.variables?.id === key.id}
                                onDelete={() => setDeleteKeyTarget(key)}
                                onEdit={() => openEditKey(key)}
                                onOpenModels={() => setTestTarget(key)}
                                onRefreshBalance={() => keyBalanceMutation.mutate(key.id)}
                                onStats={() => setKeyStatsTarget(key)}
                                onToggleEnabled={(enabled) => keyStatusMutation.mutate({ id: key.id, enabled })}
                              />
                            ))
                          )}
                        </div>
                      </CommonTable.Cell>
                    </CommonTable.Row>
                  ) : null}
                </Fragment>
              );
            })
          )}
        </CommonTable.Body>
      </CommonTable>

      {/* 创建/编辑渠道弹窗（仅 name / base_url） */}
      <ChannelFormModal
        channel={editingChannel}
        open={formOpen}
        onClose={() => {
          setFormOpen(false);
          setEditingChannel(null);
        }}
      />

      {/* 新增/编辑单把 key 弹窗 */}
      <KeyFormModal
        channelId={addKeyChannelId}
        channelKey={editingKey}
        open={keyFormOpen}
        onClose={() => {
          setKeyFormOpen(false);
          setEditingKey(null);
          setAddKeyChannelId(null);
        }}
      />

      {/* 模型与测试弹窗（按 key：模型清单/映射/测试模型管理 + 逐个或全部测试） */}
      <ChannelTestModal
        channelKey={testTarget}
        onClose={() => setTestTarget(null)}
      />

      {/* 消耗统计弹窗（每日消耗 + 模型分布，按 key 过滤的仪表盘趋势） */}
      <ChannelStatsModal
        channelKey={keyStatsTarget}
        onClose={() => setKeyStatsTarget(null)}
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

      {/* 删除单个渠道确认 */}
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

      {/* 删除单把 key 确认 */}
      <ConfirmDialog
        open={!!deleteKeyTarget}
        onOpenChange={(open) => {
          if (!open) setDeleteKeyTarget(null);
        }}
        title={t('channels.delete_key')}
        description={t('channels.delete_key_confirm', { name: deleteKeyTarget?.name || deleteKeyTarget?.api_key_hint })}
        loading={deleteKeyMutation.isPending}
        onConfirm={() => deleteKeyTarget && deleteKeyMutation.mutate(deleteKeyTarget.id)}
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
