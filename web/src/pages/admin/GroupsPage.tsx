import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import {
  Plus,
  Pencil,
  ArrowUpDown,
  Trash2,
  Percent,
  Users,
  Link2,
} from 'lucide-react';
import { Button, Chip, EmptyState } from '@heroui/react';
import { groupsApi } from '../../shared/api/groups';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { CommonTable } from '../../shared/components/CommonTable';
import { MetricChips } from '../../shared/components/MetricChips';
import { GroupFormModal } from './groups/EditGroupModal';
import { GroupRateOverridesModal } from './groups/GroupRateOverridesModal';
import { GroupAllowedUsersModal } from './groups/GroupAllowedUsersModal';
import { GroupChannelKeysModal } from './groups/GroupChannelKeysModal';
import type { GroupResp, CreateGroupReq, UpdateGroupReq } from '../../shared/types';
import { ConfirmDialog } from '../../shared/components/ConfirmDialog';
import { RefreshButton } from '../../shared/components/RefreshButton';

export default function GroupsPage() {
  const { t } = useTranslation();

  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE, 'admin.groups');

  // 弹窗状态
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingGroup, setEditingGroup] = useState<GroupResp | null>(null);
  const [deletingGroup, setDeletingGroup] = useState<GroupResp | null>(null);
  const [rateOverrideGroup, setRateOverrideGroup] = useState<GroupResp | null>(null);
  const [allowedUsersGroup, setAllowedUsersGroup] = useState<GroupResp | null>(null);
  const [channelKeysGroup, setChannelKeysGroup] = useState<GroupResp | null>(null);

  // 查询分组列表
  const { data, isFetching, isLoading, refetch } = useQuery({
    queryKey: queryKeys.groups(page, pageSize),
    queryFn: () =>
      groupsApi.list({
        page,
        page_size: pageSize,
      }),
    placeholderData: keepPreviousData,
  });

  // 创建分组
  const createMutation = useCrudMutation<unknown, CreateGroupReq>({
    mutationFn: (data) => groupsApi.create(data),
    successMessage: t('groups.create_success'),
    queryKey: queryKeys.groups(),
    onSuccess: () => setShowCreateModal(false),
  });

  // 更新分组
  const updateMutation = useCrudMutation<unknown, { id: number; data: UpdateGroupReq }>({
    mutationFn: ({ id, data }) => groupsApi.update(id, data),
    successMessage: t('groups.update_success'),
    queryKey: queryKeys.groups(),
    onSuccess: () => setEditingGroup(null),
  });

  // 删除分组
  const deleteMutation = useCrudMutation<unknown, number>({
    mutationFn: (id) => groupsApi.delete(id),
    successMessage: t('groups.delete_success'),
    queryKey: queryKeys.groups(),
    onSuccess: () => {
      setDeletingGroup(null);
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
        <div className="ag-mobile-actions flex items-center gap-2 sm:ml-auto">
          <RefreshButton
            ariaLabel={t('common.refresh', 'Refresh')}
            isRefreshing={isFetching}
            onRefresh={refetch}
          />
          <Button variant="primary" onPress={() => setShowCreateModal(true)}>
            <Plus className="w-4 h-4" />
            {t('groups.create')}
          </Button>
        </div>
      </div>

      {/* 表格 */}
      <CommonTable
        ariaLabel={t('groups.title', 'Groups')}
        className="ag-groups-table"
        contentClassName="ag-groups-table-content"
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
        minWidth={880}
      >
            <CommonTable.Header>
              <CommonTable.Column id="name" style={{ width: 200 }}>{t('common.name')}</CommonTable.Column>
              <CommonTable.Column id="rate_multiplier" style={{ width: 88 }}>
                {t('groups.rate_multiplier')}
              </CommonTable.Column>
              <CommonTable.Column id="is_exclusive" style={{ width: 84 }}>
                {t('groups.group_type')}
              </CommonTable.Column>
              <CommonTable.Column id="usage" style={{ width: '10.75rem' }}>
                {t('groups.usage')}
              </CommonTable.Column>
              <CommonTable.Column id="runtime" style={{ width: '9.75rem' }}>
                <span title={t('groups.concurrency_rpm_hint')}>{t('groups.concurrency_rpm')}</span>
              </CommonTable.Column>
              <CommonTable.Column id="sort_weight" style={{ width: 80 }}>
                {t('groups.sort_weight')}
              </CommonTable.Column>
              <CommonTable.Column id="actions" style={{ width: 168 }}>
                {t('common.actions')}
              </CommonTable.Column>
            </CommonTable.Header>
            <CommonTable.Body>
              {isLoading ? (
                <TableLoadingRow colSpan={7} />
              ) : rows.length === 0 ? (
                <CommonTable.Row id="empty">
                  <CommonTable.Cell colSpan={7}>
                    <EmptyState>
                      <div className="text-sm text-default-500">{t('common.no_data')}</div>
                    </EmptyState>
                  </CommonTable.Cell>
                </CommonTable.Row>
              ) : (
                rows.map((row) => (
                    <CommonTable.Row id={String(row.id)} key={row.id}>
                    <CommonTable.Cell>
                      <span className="inline-flex max-w-[11.5rem] items-center gap-1.5">
                        <span style={{ color: 'var(--ag-text)' }} className="truncate font-medium">
                          {row.name}
                        </span>
                      </span>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <div className="min-w-0">
                        <span className="font-mono" style={{ color: 'var(--ag-primary)' }}>
                          {row.rate_multiplier}x
                        </span>
                      </div>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <div className="flex flex-wrap gap-1">
                        {row.is_exclusive ? (
                          <Chip color="warning" size="sm" variant="soft">{t('groups.type_exclusive')}</Chip>
                        ) : (
                          <Chip color="default" size="sm" variant="soft">{t('groups.type_public')}</Chip>
                        )}
                        {row.allowed_clients && row.allowed_clients.length > 0 && (
                          <Chip color="accent" size="sm" variant="soft">
                            {row.allowed_clients.map((c) => t(`groups.client_${c}`)).join(' / ')}
                          </Chip>
                        )}
                      </div>
                    </CommonTable.Cell>
                    <CommonTable.Cell className="ag-groups-metric-cell">
                      <MetricChips
                        className="ag-metric-chips--stack ag-metric-chips--markup ag-metric-chips--compact-y"
                        items={[
                          {
                            amount: row.today_cost,
                            color: 'warning' as const,
                            dollarTone: 'warning',
                            label: t('groups.today_cost'),
                            mutedWhenZero: true,
                          },
                          {
                            amount: row.total_cost,
                            color: 'warning' as const,
                            dollarTone: 'warning',
                            label: t('groups.total_cost'),
                            mutedWhenZero: true,
                          },
                        ]}
                      />
                    </CommonTable.Cell>
                    <CommonTable.Cell className="ag-groups-metric-cell">
                      <MetricChips
                        className="ag-metric-chips--stack ag-metric-chips--compact-y ag-metric-chips--runtime"
                        items={[
                          {
                            color: 'accent' as const,
                            label: t('groups.concurrency_label'),
                            muted: (row.current_concurrency ?? 0) === 0,
                            value: String(row.current_concurrency ?? 0),
                          },
                          {
                            color: 'success' as const,
                            label: 'RPM',
                            muted: (row.current_rpm ?? 0) === 0,
                            value: String(row.current_rpm ?? 0),
                          },
                        ]}
                      />
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <span className="inline-flex items-center gap-1 font-mono">
                        <ArrowUpDown className="w-3 h-3" style={{ color: 'var(--ag-text-tertiary)' }} />
                        {row.sort_weight}
                      </span>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <div className="ag-table-row-actions flex items-center justify-center gap-0.5">
                        <Button
                          isIconOnly
                          size="sm"
                          variant="secondary"
                          aria-label={t('common.edit')}
                          onPress={() => setEditingGroup(row)}
                        >
                          <Pencil className="w-3.5 h-3.5" />
                        </Button>
                        <Button
                          isIconOnly
                          size="sm"
                          variant="secondary"
                          aria-label={t('groups.rate_override_manage')}
                          onPress={() => setRateOverrideGroup(row)}
                        >
                          <Percent className="w-3.5 h-3.5" />
                        </Button>
                        <Button
                          isIconOnly
                          size="sm"
                          variant="secondary"
                          aria-label={t('groups.channel_keys_manage')}
                          onPress={() => setChannelKeysGroup(row)}
                        >
                          <Link2 className="w-3.5 h-3.5" />
                        </Button>
                        {row.is_exclusive ? (
                          <Button
                            isIconOnly
                            size="sm"
                            variant="secondary"
                            aria-label={t('groups.allowed_users_manage')}
                            onPress={() => setAllowedUsersGroup(row)}
                          >
                            <Users className="w-3.5 h-3.5" />
                          </Button>
                        ) : null}
                        <Button
                          isIconOnly
                          size="sm"
                          variant="danger-soft"
                          className="text-danger"
                          aria-label={t('common.delete')}
                          onPress={() => setDeletingGroup(row)}
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
      <GroupFormModal
        open={showCreateModal}
        title={t('groups.create')}
        groups={data?.list}
        onClose={() => setShowCreateModal(false)}
        onSubmit={(data) => createMutation.mutate(data as CreateGroupReq)}
        loading={createMutation.isPending}
      />

      {/* 编辑弹窗 */}
      {editingGroup && (
        <GroupFormModal
          open
          title={t('groups.edit')}
          group={editingGroup}
          groups={data?.list}
          onClose={() => setEditingGroup(null)}
          onSubmit={(data) =>
            updateMutation.mutate({ id: editingGroup.id, data })
          }
          loading={updateMutation.isPending}
        />
      )}

      {/* 分组专属倍率管理 */}
      {rateOverrideGroup && (
        <GroupRateOverridesModal
          open
          group={rateOverrideGroup}
          onClose={() => setRateOverrideGroup(null)}
        />
      )}

      {/* 专属分组用户管理 */}
      {allowedUsersGroup && (
        <GroupAllowedUsersModal
          open
          group={allowedUsersGroup}
          onClose={() => setAllowedUsersGroup(null)}
        />
      )}

      {/* 分组渠道 key 绑定管理 */}
      {channelKeysGroup && (
        <GroupChannelKeysModal
          open
          group={channelKeysGroup}
          onClose={() => setChannelKeysGroup(null)}
        />
      )}

      {/* 删除确认 */}
      <ConfirmDialog
        open={!!deletingGroup}
        onOpenChange={(open) => {
          if (!open) setDeletingGroup(null);
        }}
        title={t('groups.delete_title')}
        description={t('groups.delete_confirm', { name: deletingGroup?.name })}
        loading={deleteMutation.isPending}
        onConfirm={() => deletingGroup && deleteMutation.mutate(deletingGroup.id)}
      />
    </div>
  );
}
