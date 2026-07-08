import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  AlertDialog, Button, Checkbox, Chip, EmptyState, Input, Label, ListBox, Modal,
  Select, Spinner, TextField as HeroTextField, Tooltip, useOverlayState,
} from '@heroui/react';
import {
  ArrowUpDown, CircleCheck, CircleOff, Pencil, Plus, RefreshCw, Search, Trash2, Zap,
} from 'lucide-react';
import { channelsApi } from '../../shared/api/channels';
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
import { ChannelFormModal, CHANNEL_TYPE_OPTIONS } from './channels/ChannelFormModal';
import type { BulkChannelAction, ChannelResp, ChannelType } from '../../shared/types';

const COLUMN_COUNT = 11;

// 渠道类型 → 徽章配色
const TYPE_CHIP_COLORS: Record<ChannelType, 'accent' | 'warning' | 'success' | 'default'> = {
  openai_compatible: 'accent',
  anthropic: 'warning',
  gemini: 'success',
  custom: 'default',
};

function typeLabel(type: string): string {
  return CHANNEL_TYPE_OPTIONS.find((item) => item.id === type)?.label ?? type;
}

// 冷却中 = enabled 且 status_until 未过期
function isCoolingDown(channel: ChannelResp): boolean {
  return channel.status === 'enabled'
    && !!channel.status_until
    && new Date(channel.status_until).getTime() > Date.now();
}

// 状态徽章：enabled 绿 / disabled_manual 灰 / disabled_auto 红 + error_msg tooltip / 冷却中显示 status_until
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

  if (isCoolingDown(channel)) {
    const until = new Date(channel.status_until!).toLocaleString();
    return (
      <Tooltip>
        <Tooltip.Trigger className="inline-flex">
          <Chip color="warning" size="sm" variant="soft">
            {t('channels.status_cooldown')}
          </Chip>
        </Tooltip.Trigger>
        <Tooltip.Content>{t('channels.cooldown_until', { time: until })}</Tooltip.Content>
      </Tooltip>
    );
  }

  return (
    <Chip color="success" size="sm" variant="soft">
      {t('channels.status_enabled')}
    </Chip>
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
  const [testingId, setTestingId] = useState<number | null>(null);

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

  // 测试渠道连通性
  const testMutation = useMutation({
    mutationFn: (id: number) => channelsApi.test(id),
    onSuccess: (resp) => {
      toast('success', t('channels.test_success', { latency: resp.latency_ms }));
      queryClient.invalidateQueries({ queryKey: queryKeys.channels() });
      setTestingId(null);
    },
    onError: (err: Error) => {
      toast('error', t('channels.test_failed', { error: err.message }));
      setTestingId(null);
    },
  });

  function openCreate() {
    setEditingChannel(null);
    setFormOpen(true);
  }

  function openEdit(channel: ChannelResp) {
    setEditingChannel(channel);
    setFormOpen(true);
  }

  function handleTest(id: number) {
    setTestingId(id);
    testMutation.mutate(id);
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
        minWidth={1080}
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
          <CommonTable.Column id="response_time">{t('channels.response_time')}</CommonTable.Column>
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
                <CommonTable.Cell>
                  <span className="font-mono text-text-secondary">
                    {row.response_time_ms > 0 ? `${row.response_time_ms}ms` : '-'}
                  </span>
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
                      isDisabled={testingId === row.id}
                      size="sm"
                      variant="secondary"
                      onPress={() => handleTest(row.id)}
                    >
                      {testingId === row.id ? <Spinner size="sm" /> : <Zap className="h-3.5 w-3.5" />}
                      {t('common.test')}
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
      <AlertDialog
        isOpen={!!deleteTarget}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
      >
        <DialogTriggerShim />
        <AlertDialog.Backdrop>
          <AlertDialog.Container placement="center" size="sm">
            <AlertDialog.Dialog className="ag-elevation-modal">
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t('channels.delete_channel')}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>{t('channels.delete_confirm', { name: deleteTarget?.name })}</AlertDialog.Body>
              <AlertDialog.Footer>
                <Button variant="secondary" onPress={() => setDeleteTarget(null)}>
                  {t('common.cancel')}
                </Button>
                <Button
                  aria-busy={deleteMutation.isPending}
                  isDisabled={deleteMutation.isPending}
                  variant="danger"
                  onPress={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
                >
                  {deleteMutation.isPending ? <Spinner size="sm" /> : null}
                  {t('common.confirm')}
                </Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>

      {/* 批量删除确认 */}
      <AlertDialog
        isOpen={bulkDeleteOpen}
        onOpenChange={(open) => {
          if (!open) setBulkDeleteOpen(false);
        }}
      >
        <DialogTriggerShim />
        <AlertDialog.Backdrop>
          <AlertDialog.Container placement="center" size="sm">
            <AlertDialog.Dialog className="ag-elevation-modal">
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t('channels.delete_channel')}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>{t('channels.bulk_delete_confirm', { count: selectedIds.length })}</AlertDialog.Body>
              <AlertDialog.Footer>
                <Button variant="secondary" onPress={() => setBulkDeleteOpen(false)}>
                  {t('common.cancel')}
                </Button>
                <Button
                  aria-busy={bulkPending}
                  isDisabled={bulkPending}
                  variant="danger"
                  onPress={() => bulkMutation.mutate({ ids: selectedIds, action: 'delete' })}
                >
                  {bulkPending ? <Spinner size="sm" /> : null}
                  {t('common.confirm')}
                </Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>
    </div>
  );
}
