import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  AlertDialog, Button, Chip, EmptyState, Input, Label, ListBox, Modal, Select,
  Spinner, TextField as HeroTextField, useOverlayState,
} from '@heroui/react';
import {
  Ban, Copy, Plus, RefreshCw, RotateCcw, Search, Ticket, Trash2,
} from 'lucide-react';
import { DialogTriggerShim } from '../../shared/components/DialogTriggerShim';
import { redemptionApi } from '../../shared/api/redemption';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { useDebouncedValue } from '../../shared/hooks/useDebouncedValue';
import { useClipboard } from '../../shared/hooks/useClipboard';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { CommonTable } from '../../shared/components/CommonTable';
import { formatDateTime } from '../../shared/utils/format';
import type { GenerateRedemptionCodesReq, RedemptionCode, RedemptionCodeStatus } from '../../shared/types';

// 状态徽章配色
const STATUS_CHIP_COLORS: Record<RedemptionCodeStatus, 'success' | 'default' | 'warning' | 'danger'> = {
  unused: 'success',
  used: 'default',
  disabled: 'warning',
  expired: 'danger',
};

function RedemptionStatusChip({ status }: { status: RedemptionCodeStatus }) {
  const { t } = useTranslation();
  return (
    <Chip color={STATUS_CHIP_COLORS[status] ?? 'default'} size="sm" variant="soft">
      {t(`redemption.status_${status}`, status)}
    </Chip>
  );
}

// 生成表单弹窗
function GenerateModal({
  open,
  onClose,
  onSubmit,
  loading,
}: {
  open: boolean;
  onClose: () => void;
  onSubmit: (data: GenerateRedemptionCodesReq) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const [count, setCount] = useState('10');
  const [value, setValue] = useState('10');
  const [remark, setRemark] = useState('');
  const [validDays, setValidDays] = useState('');

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  const handleSubmit = () => {
    const countNum = Number(count);
    const valueNum = Number(value);
    if (!Number.isInteger(countNum) || countNum < 1 || countNum > 500) return;
    if (!Number.isFinite(valueNum) || valueNum <= 0) return;
    const days = Number(validDays);
    const expiresAt = validDays.trim() && Number.isFinite(days) && days > 0
      ? new Date(Date.now() + days * 24 * 60 * 60 * 1000).toISOString()
      : undefined;
    onSubmit({
      count: countNum,
      value: valueNum,
      remark: remark.trim() || undefined,
      expires_at: expiresAt,
    });
  };

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog className="ag-elevation-modal" style={{ maxWidth: '480px', width: 'min(100%, calc(100vw - 2rem))' }}>
            <Modal.Header>
              <Modal.Heading>{t('redemption.generate')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                <HeroTextField fullWidth isRequired>
                  <Label>{t('redemption.generate_count')}</Label>
                  <Input
                    max={500}
                    min={1}
                    type="number"
                    value={count}
                    onChange={(e) => setCount(e.target.value)}
                  />
                </HeroTextField>
                <HeroTextField fullWidth isRequired>
                  <Label>{t('redemption.generate_value')}</Label>
                  <Input
                    min={0}
                    step="0.01"
                    type="number"
                    value={value}
                    onChange={(e) => setValue(e.target.value)}
                  />
                </HeroTextField>
                <HeroTextField fullWidth>
                  <Label>{t('redemption.generate_valid_days')}</Label>
                  <Input
                    min={1}
                    placeholder={t('redemption.generate_valid_days_placeholder')}
                    type="number"
                    value={validDays}
                    onChange={(e) => setValidDays(e.target.value)}
                  />
                </HeroTextField>
                <HeroTextField fullWidth>
                  <Label>{t('redemption.generate_remark')}</Label>
                  <Input
                    maxLength={200}
                    placeholder={t('redemption.generate_remark_placeholder')}
                    value={remark}
                    onChange={(e) => setRemark(e.target.value)}
                  />
                </HeroTextField>
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={onClose}>{t('common.cancel')}</Button>
              <Button
                aria-busy={loading}
                isDisabled={loading}
                variant="primary"
                onPress={handleSubmit}
              >
                {loading ? <Spinner size="sm" /> : <Ticket className="w-4 h-4" />}
                {t('redemption.generate_submit')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

// 生成结果弹窗：明文码仅此一次完整展示，关闭前提示复制
function GeneratedCodesModal({
  codes,
  onClose,
}: {
  codes: RedemptionCode[];
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const copy = useClipboard();
  const text = codes.map((c) => c.code).join('\n');

  const modalState = useOverlayState({
    isOpen: codes.length > 0,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog className="ag-elevation-modal" style={{ maxWidth: '520px', width: 'min(100%, calc(100vw - 2rem))' }}>
            <Modal.Header>
              <Modal.Heading>{t('redemption.generated_title', { count: codes.length })}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <p className="mb-2 text-sm text-text-tertiary">{t('redemption.generated_hint')}</p>
              <pre className="max-h-72 overflow-auto rounded-lg bg-surface-secondary p-3 font-mono text-xs leading-6 text-text">
                {text}
              </pre>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={onClose}>{t('common.close')}</Button>
              <Button variant="primary" onPress={() => copy(text)}>
                <Copy className="w-4 h-4" />
                {t('redemption.copy_all')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

export default function RedemptionCodesPage() {
  const { t } = useTranslation();
  const copy = useClipboard();
  const queryClient = useQueryClient();

  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE, 'admin.redemption');
  const [keyword, setKeyword] = useState('');
  const [statusFilter, setStatusFilter] = useState('');
  const debouncedKeyword = useDebouncedValue(keyword, 300);

  const [showGenerateModal, setShowGenerateModal] = useState(false);
  const [generatedCodes, setGeneratedCodes] = useState<RedemptionCode[]>([]);
  const [deletingCode, setDeletingCode] = useState<RedemptionCode | null>(null);

  // 列表
  const { data, isLoading, refetch } = useQuery({
    queryKey: queryKeys.redemptionCodes(page, pageSize, statusFilter, debouncedKeyword),
    queryFn: () =>
      redemptionApi.adminList({
        page,
        page_size: pageSize,
        status: statusFilter || undefined,
        keyword: debouncedKeyword || undefined,
      }),
    placeholderData: keepPreviousData,
  });

  // 统计
  const { data: stats } = useQuery({
    queryKey: queryKeys.redemptionStats(),
    queryFn: () => redemptionApi.adminStats(),
  });

  // 生成
  const generateMutation = useCrudMutation<RedemptionCode[], GenerateRedemptionCodesReq>({
    mutationFn: (input) => redemptionApi.adminGenerate(input),
    successMessage: t('redemption.generate_success'),
    queryKey: queryKeys.redemptionCodes(),
    onSuccess: (created) => {
      setShowGenerateModal(false);
      setGeneratedCodes(created ?? []);
      queryClient.invalidateQueries({ queryKey: queryKeys.redemptionStats() });
    },
  });

  // 停用/恢复
  const toggleMutation = useCrudMutation<unknown, { id: number; disabled: boolean }>({
    mutationFn: ({ id, disabled }) => redemptionApi.adminUpdateStatus(id, disabled),
    successMessage: t('redemption.status_update_success'),
    queryKey: queryKeys.redemptionCodes(),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.redemptionStats() }),
  });

  // 删除
  const deleteMutation = useCrudMutation<unknown, number>({
    mutationFn: (id) => redemptionApi.adminDelete(id),
    successMessage: t('redemption.delete_success'),
    queryKey: queryKeys.redemptionCodes(),
    onSuccess: () => {
      setDeletingCode(null);
      queryClient.invalidateQueries({ queryKey: queryKeys.redemptionStats() });
      if ((data?.list?.length ?? 0) === 1 && page > 1) {
        setPage(page - 1);
      }
    },
  });

  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);

  const statusOptions = [
    { id: '', label: t('redemption.all_status') },
    { id: 'unused', label: t('redemption.status_unused') },
    { id: 'used', label: t('redemption.status_used') },
    { id: 'disabled', label: t('redemption.status_disabled') },
    { id: 'expired', label: t('redemption.status_expired') },
  ];
  const selectedStatusLabel = statusOptions.find((item) => item.id === statusFilter)?.label ?? t('redemption.all_status');

  return (
    <div>
      {/* 统计概览 */}
      {stats && (
        <div className="mb-4 flex flex-wrap items-center gap-2 text-sm">
          <Chip color="success" size="sm" variant="soft">
            {t('redemption.stats_unused', { count: stats.unused })}
            <span className="ml-1 font-mono">${stats.unused_value.toFixed(2)}</span>
          </Chip>
          <Chip color="default" size="sm" variant="soft">
            {t('redemption.stats_used', { count: stats.used })}
            <span className="ml-1 font-mono">${stats.used_value.toFixed(2)}</span>
          </Chip>
          {stats.disabled > 0 && (
            <Chip color="warning" size="sm" variant="soft">
              {t('redemption.stats_disabled', { count: stats.disabled })}
            </Chip>
          )}
          {stats.expired > 0 && (
            <Chip color="danger" size="sm" variant="soft">
              {t('redemption.stats_expired', { count: stats.expired })}
            </Chip>
          )}
        </div>
      )}

      {/* 工具栏 */}
      <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-3 mb-5 flex-wrap">
        <div className="w-full sm:w-56">
          <HeroTextField fullWidth aria-label={t('redemption.search_placeholder')}>
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
              <Input
                className="pl-9"
                placeholder={t('redemption.search_placeholder')}
                value={keyword}
                onChange={(e) => { setKeyword(e.target.value); setPage(1); }}
              />
            </div>
          </HeroTextField>
        </div>
        <div className="w-full sm:w-44">
          <Select
            fullWidth
            selectedKey={statusFilter}
            onSelectionChange={(key) => {
              setStatusFilter(key == null ? '' : String(key));
              setPage(1);
            }}
          >
            <Label className="sr-only">{t('common.status')}</Label>
            <Select.Trigger>
              <Select.Value>{selectedStatusLabel}</Select.Value>
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox items={statusOptions}>
                {(item) => (
                  <ListBox.Item id={item.id} textValue={item.label}>
                    {item.label}
                  </ListBox.Item>
                )}
              </ListBox>
            </Select.Popover>
          </Select>
        </div>
        <div className="flex items-center gap-2 sm:ml-auto">
          <Button
            isIconOnly
            aria-label={t('common.refresh', 'Refresh')}
            size="sm"
            variant="ghost"
            onPress={() => refetch()}
          >
            <RefreshCw className="w-4 h-4" />
          </Button>
          <Button variant="primary" onPress={() => setShowGenerateModal(true)}>
            <Plus className="w-4 h-4" />
            {t('redemption.generate')}
          </Button>
        </div>
      </div>

      {/* 表格 */}
      <CommonTable
        ariaLabel={t('redemption.title')}
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
        minWidth={960}
      >
        <CommonTable.Header>
          <CommonTable.Column id="code" style={{ width: 300 }}>{t('redemption.code')}</CommonTable.Column>
          <CommonTable.Column id="value" style={{ width: 90 }}>{t('redemption.value')}</CommonTable.Column>
          <CommonTable.Column id="status" style={{ width: 90 }}>{t('common.status')}</CommonTable.Column>
          <CommonTable.Column id="remark" style={{ width: 140 }}>{t('redemption.remark')}</CommonTable.Column>
          <CommonTable.Column id="used_by" style={{ width: 200 }}>{t('redemption.used_by')}</CommonTable.Column>
          <CommonTable.Column id="expires_at" style={{ width: 150 }}>{t('redemption.expires_at')}</CommonTable.Column>
          <CommonTable.Column id="created_at" style={{ width: 150 }}>{t('redemption.created_at')}</CommonTable.Column>
          <CommonTable.Column id="actions" style={{ width: 100 }}>{t('common.actions')}</CommonTable.Column>
        </CommonTable.Header>
        <CommonTable.Body>
          {isLoading ? (
            <TableLoadingRow colSpan={8} />
          ) : rows.length === 0 ? (
            <CommonTable.Row id="empty">
              <CommonTable.Cell colSpan={8}>
                <EmptyState>
                  <div className="text-sm text-default-500">{t('common.no_data')}</div>
                </EmptyState>
              </CommonTable.Cell>
            </CommonTable.Row>
          ) : (
            rows.map((row) => (
              <CommonTable.Row id={String(row.id)} key={row.id}>
                <CommonTable.Cell>
                  <span className="inline-flex items-center gap-1">
                    <span className="font-mono text-xs" style={{ color: 'var(--ag-text)' }}>{row.code}</span>
                    <Button
                      isIconOnly
                      aria-label={t('common.copy')}
                      size="sm"
                      variant="ghost"
                      onPress={() => copy(row.code)}
                    >
                      <Copy className="w-3 h-3" />
                    </Button>
                  </span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono" style={{ color: 'var(--ag-primary)' }}>${row.value.toFixed(2)}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <RedemptionStatusChip status={row.status} />
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="block max-w-[8.5rem] truncate text-sm" title={row.remark}>
                    {row.remark || '-'}
                  </span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  {row.status === 'used' ? (
                    <div className="min-w-0 text-xs">
                      <div className="truncate" style={{ color: 'var(--ag-text)' }}>{row.used_by_email || `#${row.used_by_id}`}</div>
                      {row.used_at && (
                        <div style={{ color: 'var(--ag-text-tertiary)' }}>{formatDateTime(row.used_at)}</div>
                      )}
                    </div>
                  ) : (
                    <span className="text-sm" style={{ color: 'var(--ag-text-tertiary)' }}>-</span>
                  )}
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="text-xs" style={{ color: 'var(--ag-text-secondary)' }}>
                    {row.expires_at ? formatDateTime(row.expires_at) : t('redemption.never_expires')}
                  </span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="text-xs" style={{ color: 'var(--ag-text-secondary)' }}>
                    {formatDateTime(row.created_at)}
                  </span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <div className="ag-table-row-actions flex items-center justify-center gap-0.5">
                    {row.status !== 'used' && (
                      <Button
                        isIconOnly
                        aria-label={row.status === 'disabled' ? t('redemption.enable') : t('redemption.disable')}
                        size="sm"
                        variant="secondary"
                        onPress={() => toggleMutation.mutate({ id: row.id, disabled: row.status !== 'disabled' })}
                      >
                        {row.status === 'disabled'
                          ? <RotateCcw className="w-3.5 h-3.5" />
                          : <Ban className="w-3.5 h-3.5" />}
                      </Button>
                    )}
                    {row.status !== 'used' && (
                      <Button
                        isIconOnly
                        aria-label={t('common.delete')}
                        className="text-danger"
                        size="sm"
                        variant="danger-soft"
                        onPress={() => setDeletingCode(row)}
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </Button>
                    )}
                  </div>
                </CommonTable.Cell>
              </CommonTable.Row>
            ))
          )}
        </CommonTable.Body>
      </CommonTable>

      {/* 生成弹窗（key 重置表单状态） */}
      {showGenerateModal && (
        <GenerateModal
          loading={generateMutation.isPending}
          open={showGenerateModal}
          onClose={() => setShowGenerateModal(false)}
          onSubmit={(input) => generateMutation.mutate(input)}
        />
      )}

      {/* 生成结果 */}
      {generatedCodes.length > 0 && (
        <GeneratedCodesModal codes={generatedCodes} onClose={() => setGeneratedCodes([])} />
      )}

      {/* 删除确认 */}
      <AlertDialog
        isOpen={!!deletingCode}
        onOpenChange={(open) => {
          if (!open) setDeletingCode(null);
        }}
      >
        <DialogTriggerShim />
        <AlertDialog.Backdrop>
          <AlertDialog.Container placement="center" size="sm">
            <AlertDialog.Dialog className="ag-elevation-modal">
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t('redemption.delete_title')}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>{t('redemption.delete_confirm', { code: deletingCode?.code })}</AlertDialog.Body>
              <AlertDialog.Footer>
                <Button variant="secondary" onPress={() => setDeletingCode(null)}>
                  {t('common.cancel')}
                </Button>
                <Button
                  aria-busy={deleteMutation.isPending}
                  isDisabled={deleteMutation.isPending}
                  variant="danger"
                  onPress={() => deletingCode && deleteMutation.mutate(deletingCode.id)}
                >
                  {deleteMutation.isPending ? <Spinner size="sm" /> : null}
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
