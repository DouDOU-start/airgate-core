import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import {
  Plus,
  Pencil,
  Layers,
  ArrowUpDown,
  Trash2,
  RefreshCw,
  Percent,
} from 'lucide-react';
import { AlertDialog, Button, Chip, EmptyState, Label, ListBox, Select, Spinner } from '@heroui/react';
import { DialogTriggerShim } from '../../shared/components/DialogTriggerShim';
import { PlatformIcon } from '../../shared/ui';
import { groupsApi } from '../../shared/api/groups';
import { usePlatforms } from '../../shared/hooks/usePlatforms';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { CommonTable } from '../../shared/components/CommonTable';
import { GroupFormModal } from './groups/EditGroupModal';
import { GroupRateOverridesModal } from './groups/GroupRateOverridesModal';
import type { GroupResp, CreateGroupReq, UpdateGroupReq } from '../../shared/types';

export default function GroupsPage() {
  const { t } = useTranslation();
  const { platforms, platformName, instructionPresets } = usePlatforms();

  const PLATFORM_OPTIONS = [
    { value: '', label: t('groups.all_platforms') },
    ...platforms.map((p) => ({ value: p, label: platformName(p) })),
  ];
  // 筛选状态
  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE, 'admin.groups');
  const [platformFilter, setPlatformFilter] = useState('');

  // 弹窗状态
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingGroup, setEditingGroup] = useState<GroupResp | null>(null);
  const [deletingGroup, setDeletingGroup] = useState<GroupResp | null>(null);
  const [rateOverrideGroup, setRateOverrideGroup] = useState<GroupResp | null>(null);

  // 查询分组列表
  const { data, isLoading, refetch } = useQuery({
    queryKey: queryKeys.groups(page, pageSize, platformFilter),
    queryFn: () =>
      groupsApi.list({
        page,
        page_size: pageSize,
        platform: platformFilter || undefined,
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

  // 格式化费用
  const formatCost = (v: number) => `$${v.toFixed(2)}`;
  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);
  const selectedPlatformLabel = PLATFORM_OPTIONS.find((option) => option.value === platformFilter)?.label ?? t('groups.all_platforms');

  return (
    <div>
      {/* 筛选 */}
      <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-3 mb-5 flex-wrap">
        <div className="w-full sm:w-48">
          <Select
            fullWidth
            selectedKey={platformFilter}
            onSelectionChange={(key) => {
              setPlatformFilter(key == null ? '' : String(key));
              setPage(1);
            }}
          >
            <Label className="sr-only">{t('groups.platform')}</Label>
            <Select.Trigger>
              <Select.Value>{selectedPlatformLabel}</Select.Value>
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox items={PLATFORM_OPTIONS}>
                {(item) => (
                  <ListBox.Item id={item.value} textValue={item.label}>
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
        minWidth={1180}
      >
            <CommonTable.Header>
              <CommonTable.Column id="name">{t('common.name')}</CommonTable.Column>
              <CommonTable.Column id="platform">{t('groups.platform')}</CommonTable.Column>
              <CommonTable.Column id="subscription_type">{t('groups.subscription_type')}</CommonTable.Column>
              <CommonTable.Column id="rate_multiplier" style={{ width: 96 }}>
                {t('groups.rate_multiplier')}
              </CommonTable.Column>
              <CommonTable.Column id="is_exclusive" style={{ width: 96 }}>
                {t('groups.group_type')}
              </CommonTable.Column>
              <CommonTable.Column id="account_stats" style={{ width: 144 }}>
                {t('groups.account_stats')}
              </CommonTable.Column>
              <CommonTable.Column id="usage" style={{ width: 128 }}>
                {t('groups.usage')}
              </CommonTable.Column>
              <CommonTable.Column id="capacity" style={{ width: 128 }}>
                {t('groups.capacity')}
              </CommonTable.Column>
              <CommonTable.Column id="sort_weight" style={{ width: 96 }}>
                {t('groups.sort_weight')}
              </CommonTable.Column>
              <CommonTable.Column id="actions" style={{ width: 132 }}>
                {t('common.actions')}
              </CommonTable.Column>
            </CommonTable.Header>
            <CommonTable.Body>
              {isLoading ? (
                <TableLoadingRow colSpan={10} />
              ) : rows.length === 0 ? (
                <CommonTable.Row id="empty">
                  <CommonTable.Cell colSpan={10}>
                    <EmptyState>
                      <div className="text-sm text-default-500">{t('common.no_data')}</div>
                    </EmptyState>
                  </CommonTable.Cell>
                </CommonTable.Row>
              ) : (
                rows.map((row) => (
                    <CommonTable.Row id={String(row.id)} key={row.id}>
                    <CommonTable.Cell>
                      <span className="inline-flex items-center gap-1.5">
                        <Layers className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />
                        <span style={{ color: 'var(--ag-text)' }} className="font-medium">
                          {row.name}
                        </span>
                      </span>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <span className="inline-flex items-center gap-1.5">
                        <PlatformIcon platform={row.platform} className="w-3.5 h-3.5" />
                        {platformName(row.platform)}
                      </span>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <Chip color={row.subscription_type === 'subscription' ? 'accent' : 'default'} size="sm" variant="soft">
                        {row.subscription_type === 'subscription' ? t('groups.type_subscription') : t('groups.type_standard')}
                      </Chip>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <div className="min-w-0">
                        <span className="font-mono" style={{ color: 'var(--ag-primary)' }}>
                          {row.rate_multiplier}x
                        </span>
                      </div>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      {row.is_exclusive ? (
                        <Chip color="warning" size="sm" variant="soft">{t('groups.type_exclusive')}</Chip>
                      ) : (
                        <Chip color="default" size="sm" variant="soft">{t('groups.type_public')}</Chip>
                      )}
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <div className="text-xs leading-none">
                        <div>
                          <span style={{ color: 'var(--ag-text-tertiary)' }}>{t('groups.account_available')}: </span>
                          <span className="font-mono" style={{ color: 'var(--ag-success)' }}>{row.account_active}</span>
                        </div>
                        {row.account_error > 0 && (
                          <div>
                            <span style={{ color: 'var(--ag-text-tertiary)' }}>{t('groups.account_error')}: </span>
                            <span className="font-mono" style={{ color: 'var(--ag-danger)' }}>{row.account_error}</span>
                          </div>
                        )}
                        <div>
                          <span style={{ color: 'var(--ag-text-tertiary)' }}>{t('groups.account_total')}: </span>
                          <span className="font-mono">{row.account_total}</span>
                          <span style={{ color: 'var(--ag-text-tertiary)' }}> {t('groups.account_unit')}</span>
                        </div>
                      </div>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <div className="text-xs leading-none">
                        <div>
                          <span style={{ color: 'var(--ag-text-tertiary)' }}>{t('groups.today_cost')} </span>
                          <span className="font-mono" style={{ color: 'var(--ag-primary)' }}>{formatCost(row.today_cost)}</span>
                        </div>
                        <div>
                          <span style={{ color: 'var(--ag-text-tertiary)' }}>{t('groups.total_cost')} </span>
                          <span className="font-mono">{formatCost(row.total_cost)}</span>
                        </div>
                      </div>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <div>
                        <span className="font-mono" style={{ color: row.capacity_used > 0 ? 'var(--ag-primary)' : undefined }}>
                          {row.capacity_used}
                        </span>
                        <span style={{ color: 'var(--ag-text-tertiary)' }}> / </span>
                        <span className="font-mono">{row.capacity_total}</span>
                      </div>
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
        onClose={() => setShowCreateModal(false)}
        onSubmit={(data) => createMutation.mutate(data as CreateGroupReq)}
        loading={createMutation.isPending}
        platforms={platforms}
        instructionPresets={instructionPresets}
      />

      {/* 编辑弹窗 */}
      {editingGroup && (
        <GroupFormModal
          open
          title={t('groups.edit')}
          group={editingGroup}
          onClose={() => setEditingGroup(null)}
          onSubmit={(data) =>
            updateMutation.mutate({ id: editingGroup.id, data })
          }
          loading={updateMutation.isPending}
          platforms={platforms}
          instructionPresets={instructionPresets}
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

      {/* 删除确认 */}
      <AlertDialog
        isOpen={!!deletingGroup}
        onOpenChange={(open) => {
          if (!open) setDeletingGroup(null);
        }}
      >
        <DialogTriggerShim />
        <AlertDialog.Backdrop>
          <AlertDialog.Container placement="center" size="sm">
            <AlertDialog.Dialog className="ag-elevation-modal">
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t('groups.delete_title')}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>{t('groups.delete_confirm', { name: deletingGroup?.name })}</AlertDialog.Body>
              <AlertDialog.Footer>
                <Button variant="secondary" onPress={() => setDeletingGroup(null)}>
                  {t('common.cancel')}
                </Button>
                <Button
                  aria-busy={deleteMutation.isPending}
                  isDisabled={deleteMutation.isPending}
                  variant="danger"
                  onPress={() => deletingGroup && deleteMutation.mutate(deletingGroup.id)}
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
