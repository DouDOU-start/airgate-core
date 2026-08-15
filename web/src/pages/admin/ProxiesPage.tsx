import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query';
import { Plus, Pencil, Trash2, Zap } from 'lucide-react';
import { Button, Chip, EmptyState, Input, ListBox, Select, TextField as HeroTextField } from '@heroui/react';
import { proxiesApi } from '../../shared/api/proxies';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { useDebouncedValue } from '../../shared/hooks/useDebouncedValue';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { CommonTable } from '../../shared/components/CommonTable';
import { ProxyFormModal } from './proxies/ProxyFormModal';
import { ConfirmDialog } from '../../shared/components/ConfirmDialog';
import { RefreshButton } from '../../shared/components/RefreshButton';
import { useToast } from '../../shared/ui';
import type {
  CreateProxyReq,
  ProxyResp,
  ProxyStatus,
  ProxyTestResult,
  UpdateProxyReq,
} from '../../shared/types';

const COLUMN_COUNT = 5;

export default function ProxiesPage() {
  const { t } = useTranslation();
  const { toast } = useToast();

  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE, 'admin.proxies');

  const [keyword, setKeyword] = useState('');
  const debouncedKeyword = useDebouncedValue(keyword, 300);
  const [statusFilter, setStatusFilter] = useState('');

  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingItem, setEditingItem] = useState<ProxyResp | null>(null);
  const [deletingItem, setDeletingItem] = useState<ProxyResp | null>(null);
  const [testingId, setTestingId] = useState<number | null>(null);

  const { data, isFetching, isLoading, refetch } = useQuery({
    queryKey: queryKeys.proxies(page, pageSize, debouncedKeyword, statusFilter),
    queryFn: () =>
      proxiesApi.list({
        page,
        page_size: pageSize,
        keyword: debouncedKeyword || undefined,
        status: statusFilter || undefined,
      }),
    placeholderData: keepPreviousData,
  });

  const createMutation = useCrudMutation<unknown, CreateProxyReq>({
    mutationFn: (payload) => proxiesApi.create(payload),
    successMessage: t('proxies.create_success'),
    queryKey: queryKeys.proxies(),
    onSuccess: () => setShowCreateModal(false),
  });

  const updateMutation = useCrudMutation<unknown, { id: number; data: UpdateProxyReq }>({
    mutationFn: ({ id, data: payload }) => proxiesApi.update(id, payload),
    successMessage: t('proxies.update_success'),
    queryKey: queryKeys.proxies(),
    onSuccess: () => setEditingItem(null),
  });

  const deleteMutation = useCrudMutation<unknown, number>({
    mutationFn: (id) => proxiesApi.delete(id),
    successMessage: t('proxies.delete_success'),
    queryKey: queryKeys.proxies(),
    onSuccess: () => {
      setDeletingItem(null);
      if ((data?.list?.length ?? 0) === 1 && page > 1) {
        setPage(page - 1);
      }
    },
  });

  const testMutation = useMutation({
    mutationFn: (id: number) => proxiesApi.test(id),
    onMutate: (id) => setTestingId(id),
    onSuccess: (result: ProxyTestResult) => {
      if (result.success) {
        const location = [result.country, result.city].filter(Boolean).join(' · ');
        const parts = [
          result.ip_address ? `IP ${result.ip_address}` : null,
          location || null,
          result.latency_ms != null ? `${result.latency_ms}ms` : null,
        ].filter(Boolean);
        toast(
          'success',
          parts.join(' · ') || t('proxies.test_success'),
          t('proxies.test_success'),
        );
      } else {
        toast('error', result.error_msg || t('proxies.test_failed'), t('proxies.test_failed'));
      }
    },
    onError: (err: Error) => toast('error', err.message, t('proxies.test_failed')),
    onSettled: () => setTestingId(null),
  });

  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);

  const statusFilterOptions = [
    { id: '', label: t('common.all') },
    { id: 'active', label: t('proxies.status_active') },
    { id: 'disabled', label: t('proxies.status_disabled') },
  ];
  const selectedStatusLabel =
    statusFilterOptions.find((item) => item.id === statusFilter)?.label ?? t('common.all');

  const statusChip = (status: ProxyStatus) => (
    <Chip color={status === 'active' ? 'success' : 'default'} size="sm" variant="soft">
      {status === 'active' ? t('proxies.status_active') : t('proxies.status_disabled')}
    </Chip>
  );

  return (
    <div>
      {/* 工具栏 */}
      <div className="ag-mobile-toolbar flex flex-col sm:flex-row items-stretch sm:items-center gap-3 mb-5 flex-wrap">
        <HeroTextField className="w-full sm:w-64">
          <Input
            placeholder={t('proxies.search_placeholder')}
            value={keyword}
            onChange={(e) => {
              setKeyword(e.target.value);
              setPage(1);
            }}
          />
        </HeroTextField>

        <div className="w-full sm:w-40">
          <Select
            aria-label={t('proxies.status')}
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

        <div className="ag-mobile-actions flex items-center gap-2 sm:ml-auto">
          <RefreshButton
            ariaLabel={t('common.refresh', 'Refresh')}
            isRefreshing={isFetching}
            onRefresh={refetch}
          />
          <Button variant="primary" onPress={() => setShowCreateModal(true)}>
            <Plus className="w-4 h-4" />
            {t('proxies.create')}
          </Button>
        </div>
      </div>

      {/* 表格 */}
      <CommonTable
        ariaLabel={t('proxies.title', 'Proxies')}
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
        minWidth={720}
      >
        <CommonTable.Header>
          <CommonTable.Column id="name" style={{ width: 180 }}>{t('proxies.name')}</CommonTable.Column>
          <CommonTable.Column id="protocol" style={{ width: 100 }}>{t('proxies.protocol')}</CommonTable.Column>
          <CommonTable.Column id="endpoint" style={{ width: 220 }}>{t('proxies.endpoint')}</CommonTable.Column>
          <CommonTable.Column id="status" style={{ width: 100 }}>{t('proxies.status')}</CommonTable.Column>
          <CommonTable.Column id="actions" style={{ width: 140 }}>{t('common.actions')}</CommonTable.Column>
        </CommonTable.Header>
        <CommonTable.Body>
          {isLoading ? (
            <TableLoadingRow colSpan={COLUMN_COUNT} />
          ) : rows.length === 0 ? (
            <CommonTable.Row id="empty">
              <CommonTable.Cell colSpan={COLUMN_COUNT}>
                <EmptyState>
                  <div className="text-sm text-default-500">{t('proxies.empty')}</div>
                </EmptyState>
              </CommonTable.Cell>
            </CommonTable.Row>
          ) : (
            rows.map((row) => (
              <CommonTable.Row id={String(row.id)} key={row.id}>
                <CommonTable.Cell>
                  <span
                    className="inline-block max-w-[11rem] truncate font-medium"
                    style={{ color: 'var(--ag-text)' }}
                    title={row.name}
                  >
                    {row.name}
                  </span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <Chip size="sm" variant="soft">
                    {row.protocol?.toUpperCase() || '—'}
                  </Chip>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono text-xs text-text-secondary" title={`${row.address}:${row.port}`}>
                    {row.address}:{row.port}
                  </span>
                </CommonTable.Cell>
                <CommonTable.Cell>{statusChip(row.status)}</CommonTable.Cell>
                <CommonTable.Cell>
                  <div className="ag-table-row-actions flex items-center justify-center gap-0.5">
                    <Button
                      isIconOnly
                      size="sm"
                      variant="secondary"
                      aria-label={t('common.test')}
                      isDisabled={testingId === row.id}
                      onPress={() => testMutation.mutate(row.id)}
                    >
                      <Zap className="w-3.5 h-3.5" />
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

      <ProxyFormModal
        open={showCreateModal}
        title={t('proxies.create')}
        onClose={() => setShowCreateModal(false)}
        onSubmit={(payload) => createMutation.mutate(payload as CreateProxyReq)}
        loading={createMutation.isPending}
      />

      {editingItem && (
        <ProxyFormModal
          open
          title={t('proxies.edit')}
          proxy={editingItem}
          onClose={() => setEditingItem(null)}
          onSubmit={(payload) => updateMutation.mutate({ id: editingItem.id, data: payload })}
          loading={updateMutation.isPending}
        />
      )}

      <ConfirmDialog
        open={!!deletingItem}
        onOpenChange={(open) => {
          if (!open) setDeletingItem(null);
        }}
        title={t('common.delete')}
        description={t('proxies.delete_confirm', { name: deletingItem?.name })}
        loading={deleteMutation.isPending}
        onConfirm={() => deletingItem && deleteMutation.mutate(deletingItem.id)}
      />
    </div>
  );
}
