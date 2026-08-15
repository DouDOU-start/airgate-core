import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Plus, Pencil, Trash2 } from 'lucide-react';
import { Button, EmptyState, Input, TextField as HeroTextField } from '@heroui/react';
import { bookmarksApi } from '../../shared/api/bookmarks';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { useDebouncedValue } from '../../shared/hooks/useDebouncedValue';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { formatDateTime } from '../../shared/utils/format';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { CommonTable } from '../../shared/components/CommonTable';
import { BookmarkFormModal } from './bookmarks/BookmarkFormModal';
import { ConfirmDialog } from '../../shared/components/ConfirmDialog';
import { RefreshButton } from '../../shared/components/RefreshButton';
import type {
  BookmarkResp,
  CreateBookmarkReq,
  UpdateBookmarkReq,
} from '../../shared/types';

export default function BookmarksPage() {
  const { t } = useTranslation();

  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE, 'admin.bookmarks');

  const [keyword, setKeyword] = useState('');
  const debouncedKeyword = useDebouncedValue(keyword, 300);

  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingItem, setEditingItem] = useState<BookmarkResp | null>(null);
  const [deletingItem, setDeletingItem] = useState<BookmarkResp | null>(null);

  const { data, isFetching, isLoading, refetch } = useQuery({
    queryKey: queryKeys.bookmarks(page, pageSize, debouncedKeyword),
    queryFn: () =>
      bookmarksApi.list({
        page,
        page_size: pageSize,
        keyword: debouncedKeyword || undefined,
      }),
    placeholderData: keepPreviousData,
  });

  const createMutation = useCrudMutation<unknown, CreateBookmarkReq>({
    mutationFn: (payload) => bookmarksApi.create(payload),
    successMessage: t('bookmarks.create_success'),
    queryKey: queryKeys.bookmarks(),
    onSuccess: () => setShowCreateModal(false),
  });

  const updateMutation = useCrudMutation<unknown, { id: number; data: UpdateBookmarkReq }>({
    mutationFn: ({ id, data: payload }) => bookmarksApi.update(id, payload),
    successMessage: t('bookmarks.update_success'),
    queryKey: queryKeys.bookmarks(),
    onSuccess: () => setEditingItem(null),
  });

  const deleteMutation = useCrudMutation<unknown, number>({
    mutationFn: (id) => bookmarksApi.delete(id),
    successMessage: t('bookmarks.delete_success'),
    queryKey: queryKeys.bookmarks(),
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

  return (
    <div>
      {/* 工具栏 */}
      <div className="ag-mobile-toolbar flex flex-col sm:flex-row items-stretch sm:items-center gap-3 mb-5 flex-wrap">
        <HeroTextField className="w-full sm:w-64">
          <Input
            placeholder={t('bookmarks.search_placeholder')}
            value={keyword}
            onChange={(e) => {
              setKeyword(e.target.value);
              setPage(1);
            }}
          />
        </HeroTextField>

        <div className="ag-mobile-actions flex items-center gap-2 sm:ml-auto">
          <RefreshButton
            ariaLabel={t('common.refresh', 'Refresh')}
            isRefreshing={isFetching}
            onRefresh={refetch}
          />
          <Button variant="primary" onPress={() => setShowCreateModal(true)}>
            <Plus className="w-4 h-4" />
            {t('bookmarks.create')}
          </Button>
        </div>
      </div>

      {/* 表格 */}
      <CommonTable
        ariaLabel={t('bookmarks.title', 'Bookmarks')}
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
        minWidth={700}
      >
        <CommonTable.Header>
          <CommonTable.Column id="name" style={{ width: 200 }}>{t('bookmarks.name')}</CommonTable.Column>
          <CommonTable.Column id="base_url" style={{ width: 280 }}>{t('bookmarks.base_url')}</CommonTable.Column>
          <CommonTable.Column id="remark">{t('bookmarks.remark')}</CommonTable.Column>
          <CommonTable.Column id="updated_at" style={{ width: 160 }}>{t('common.updated_at', '更新时间')}</CommonTable.Column>
          <CommonTable.Column id="actions" style={{ width: 100 }}>{t('common.actions')}</CommonTable.Column>
        </CommonTable.Header>
        <CommonTable.Body>
          {isLoading ? (
            <TableLoadingRow colSpan={5} />
          ) : rows.length === 0 ? (
            <CommonTable.Row id="empty">
              <CommonTable.Cell colSpan={5}>
                <EmptyState>
                  <div className="text-sm text-default-500">{t('bookmarks.empty')}</div>
                </EmptyState>
              </CommonTable.Cell>
            </CommonTable.Row>
          ) : (
            rows.map((row) => (
              <CommonTable.Row id={String(row.id)} key={row.id}>
                <CommonTable.Cell>
                  <span
                    className="inline-block max-w-[12rem] truncate font-medium"
                    style={{ color: 'var(--ag-text)' }}
                    title={row.name}
                  >
                    {row.name}
                  </span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  {row.base_url ? (
                    <a
                      href={row.base_url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="text-xs text-primary hover:underline break-all"
                      title={row.base_url}
                    >
                      {row.base_url}
                    </a>
                  ) : (
                    <span className="text-xs text-text-secondary">—</span>
                  )}
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span
                    className="inline-block max-w-[16rem] truncate text-xs text-text-secondary"
                    title={row.remark}
                  >
                    {row.remark || '—'}
                  </span>
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
      <BookmarkFormModal
        open={showCreateModal}
        title={t('bookmarks.create')}
        onClose={() => setShowCreateModal(false)}
        onSubmit={(payload) => createMutation.mutate(payload as CreateBookmarkReq)}
        loading={createMutation.isPending}
      />

      {/* 编辑弹窗 */}
      {editingItem && (
        <BookmarkFormModal
          open
          title={t('bookmarks.edit')}
          bookmark={editingItem}
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
        title={t('common.delete')}
        description={t('bookmarks.delete_confirm', { name: deletingItem?.name })}
        loading={deleteMutation.isPending}
        onConfirm={() => deletingItem && deleteMutation.mutate(deletingItem.id)}
      />
    </div>
  );
}
