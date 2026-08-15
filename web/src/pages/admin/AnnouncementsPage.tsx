import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Plus, Pencil, Trash2, BellRing, Bell } from 'lucide-react';
import { Button, Chip, EmptyState } from '@heroui/react';
import { announcementsApi } from '../../shared/api/announcements';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { formatDateTime } from '../../shared/utils/format';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { CommonTable } from '../../shared/components/CommonTable';
import { AnnouncementFormModal } from './announcements/AnnouncementFormModal';
import { ConfirmDialog } from '../../shared/components/ConfirmDialog';
import { RefreshButton } from '../../shared/components/RefreshButton';
import type {
  AnnouncementResp,
  AnnouncementStatus,
  CreateAnnouncementReq,
  UpdateAnnouncementReq,
} from '../../shared/types';

const STATUS_CHIP_COLOR: Record<AnnouncementStatus, 'default' | 'success' | 'warning'> = {
  active: 'success',
  archived: 'warning',
  draft: 'default',
};

export default function AnnouncementsPage() {
  const { t } = useTranslation();

  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE, 'admin.announcements');

  // 弹窗状态
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingItem, setEditingItem] = useState<AnnouncementResp | null>(null);
  const [deletingItem, setDeletingItem] = useState<AnnouncementResp | null>(null);

  // 查询公告列表
  const { data, isFetching, isLoading, refetch } = useQuery({
    queryKey: queryKeys.announcements(page, pageSize),
    queryFn: () =>
      announcementsApi.list({
        page,
        page_size: pageSize,
      }),
    placeholderData: keepPreviousData,
  });

  // 创建公告
  const createMutation = useCrudMutation<unknown, CreateAnnouncementReq>({
    mutationFn: (payload) => announcementsApi.create(payload),
    successMessage: t('announcements.create_success'),
    queryKey: queryKeys.announcements(),
    onSuccess: () => setShowCreateModal(false),
  });

  // 更新公告
  const updateMutation = useCrudMutation<unknown, { id: number; data: UpdateAnnouncementReq }>({
    mutationFn: ({ id, data: payload }) => announcementsApi.update(id, payload),
    successMessage: t('announcements.update_success'),
    queryKey: queryKeys.announcements(),
    onSuccess: () => setEditingItem(null),
  });

  // 删除公告
  const deleteMutation = useCrudMutation<unknown, number>({
    mutationFn: (id) => announcementsApi.delete(id),
    successMessage: t('announcements.delete_success'),
    queryKey: queryKeys.announcements(),
    onSuccess: () => {
      setDeletingItem(null);
      if ((data?.list?.length ?? 0) === 1 && page > 1) {
        setPage(page - 1);
      }
    },
  });

  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);

  const statusLabel = (status: AnnouncementStatus) => t(`announcements.status_${status}`);
  const timeWindowLabel = (row: AnnouncementResp) => {
    const starts = row.starts_at ? formatDateTime(row.starts_at) : t('announcements.immediate');
    const ends = row.ends_at ? formatDateTime(row.ends_at) : t('announcements.permanent');
    return `${starts} ~ ${ends}`;
  };

  return (
    <div>
      {/* 工具栏 */}
      <div className="ag-mobile-toolbar flex flex-col sm:flex-row items-stretch sm:items-center gap-3 mb-5 flex-wrap">
        <div className="ag-mobile-actions flex items-center gap-2 sm:ml-auto">
          <RefreshButton
            ariaLabel={t('common.refresh', 'Refresh')}
            isRefreshing={isFetching}
            onRefresh={refetch}
          />
          <Button variant="primary" onPress={() => setShowCreateModal(true)}>
            <Plus className="w-4 h-4" />
            {t('announcements.create')}
          </Button>
        </div>
      </div>

      {/* 表格 */}
      <CommonTable
        ariaLabel={t('announcements.title', 'Announcements')}
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
        minWidth={880}
      >
        <CommonTable.Header>
          <CommonTable.Column id="title" style={{ width: 260 }}>{t('announcements.form_title')}</CommonTable.Column>
          <CommonTable.Column id="status" style={{ width: 90 }}>{t('announcements.status')}</CommonTable.Column>
          <CommonTable.Column id="notify_mode" style={{ width: 110 }}>{t('announcements.notify_mode')}</CommonTable.Column>
          <CommonTable.Column id="time_window" style={{ width: 260 }}>{t('announcements.time_window')}</CommonTable.Column>
          <CommonTable.Column id="updated_at" style={{ width: 150 }}>{t('announcements.updated_at')}</CommonTable.Column>
          <CommonTable.Column id="actions" style={{ width: 100 }}>{t('common.actions')}</CommonTable.Column>
        </CommonTable.Header>
        <CommonTable.Body>
          {isLoading ? (
            <TableLoadingRow colSpan={6} />
          ) : rows.length === 0 ? (
            <CommonTable.Row id="empty">
              <CommonTable.Cell colSpan={6}>
                <EmptyState>
                  <div className="text-sm text-default-500">{t('common.no_data')}</div>
                </EmptyState>
              </CommonTable.Cell>
            </CommonTable.Row>
          ) : (
            rows.map((row) => (
              <CommonTable.Row id={String(row.id)} key={row.id}>
                <CommonTable.Cell>
                  <span
                    className="inline-block max-w-[15rem] truncate font-medium"
                    style={{ color: 'var(--ag-text)' }}
                    title={row.title}
                  >
                    {row.title}
                  </span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <Chip color={STATUS_CHIP_COLOR[row.status]} size="sm" variant="soft">
                    {statusLabel(row.status)}
                  </Chip>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <Chip
                    color={row.notify_mode === 'popup' ? 'accent' : 'default'}
                    size="sm"
                    variant="soft"
                  >
                    <span className="inline-flex items-center gap-1">
                      {row.notify_mode === 'popup'
                        ? <BellRing className="w-3 h-3" />
                        : <Bell className="w-3 h-3" />}
                      {t(`announcements.notify_${row.notify_mode}`)}
                    </span>
                  </Chip>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="text-xs text-text-secondary">{timeWindowLabel(row)}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="text-xs text-text-secondary">{formatDateTime(row.updated_at)}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <div className="ag-table-row-actions flex items-center justify-center gap-0.5">
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

      {/* 创建弹窗 */}
      <AnnouncementFormModal
        open={showCreateModal}
        title={t('announcements.create')}
        onClose={() => setShowCreateModal(false)}
        onSubmit={(payload) => createMutation.mutate(payload as CreateAnnouncementReq)}
        loading={createMutation.isPending}
      />

      {/* 编辑弹窗 */}
      {editingItem && (
        <AnnouncementFormModal
          open
          title={t('announcements.edit')}
          announcement={editingItem}
          onClose={() => setEditingItem(null)}
          onSubmit={(payload) => updateMutation.mutate({ id: editingItem.id, data: payload })}
          loading={updateMutation.isPending}
        />
      )}

      {/* 删除确认 */}
      <ConfirmDialog
        open={!!deletingItem}
        onOpenChange={(open) => {
          if (!open) setDeletingItem(null);
        }}
        title={t('announcements.delete_title')}
        description={t('announcements.delete_confirm', { title: deletingItem?.title })}
        loading={deleteMutation.isPending}
        onConfirm={() => deletingItem && deleteMutation.mutate(deletingItem.id)}
      />
    </div>
  );
}
