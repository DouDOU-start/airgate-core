import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  Plus,
  Pencil,
  Layers,
  ArrowUpDown,
  Trash2,
  RefreshCw,
  Percent,
} from 'lucide-react';
import { Button } from '../../shared/components/Button';
import { Select } from '../../shared/components/Input';
import { Table, type Column } from '../../shared/components/Table';
import { ConfirmModal } from '../../shared/components/Modal';
import { Badge } from '../../shared/components/Badge';
import { PlatformIcon } from '../../shared/components/PlatformIcon';
import { groupsApi } from '../../shared/api/groups';
import { usePlatforms } from '../../shared/hooks/usePlatforms';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE } from '../../shared/constants';
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
  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE);
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

  // 表格列定义
  const columns: Column<GroupResp>[] = [
    {
      key: 'name',
      title: t('common.name'),
      render: (row) => (
        <span className="inline-flex items-center gap-1.5">
          <Layers className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />
          <span style={{ color: 'var(--ag-text)' }} className="font-medium">
            {row.name}
          </span>
        </span>
      ),
    },
    {
      key: 'platform',
      title: t('groups.platform'),
      render: (row) => (
        <span className="inline-flex items-center gap-1.5">
          <PlatformIcon platform={row.platform} className="w-3.5 h-3.5" />
          {platformName(row.platform)}
        </span>
      ),
    },
    {
      key: 'subscription_type',
      title: t('groups.subscription_type'),
      render: (row) => (
        <Badge variant={row.subscription_type === 'subscription' ? 'info' : 'default'}>
          {row.subscription_type === 'subscription' ? t('groups.type_subscription') : t('groups.type_standard')}
        </Badge>
      ),
    },
    {
      key: 'rate_multiplier',
      title: t('groups.rate_multiplier'),
      width: '80px',
      hideOnMobile: true,
      render: (row) => (
        <span className="font-mono" style={{ color: 'var(--ag-primary)' }}>
          {row.rate_multiplier}x
        </span>
      ),
    },
    {
      key: 'is_exclusive',
      title: t('groups.group_type'),
      width: '80px',
      hideOnMobile: true,
      render: (row) =>
        row.is_exclusive ? (
          <Badge variant="warning">{t('groups.type_exclusive')}</Badge>
        ) : (
          <Badge variant="default">{t('groups.type_public')}</Badge>
        ),
    },
    {
      key: 'account_stats',
      title: t('groups.account_stats'),
      width: '130px',
      hideOnMobile: true,
      render: (row) => (
        <div className="text-xs leading-relaxed">
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
      ),
    },
    {
      key: 'usage',
      title: t('groups.usage'),
      width: '115px',
      render: (row) => (
        <div className="text-xs leading-relaxed">
          <div>
            <span style={{ color: 'var(--ag-text-tertiary)' }}>{t('groups.today_cost')} </span>
            <span className="font-mono" style={{ color: 'var(--ag-primary)' }}>{formatCost(row.today_cost)}</span>
          </div>
          <div>
            <span style={{ color: 'var(--ag-text-tertiary)' }}>{t('groups.total_cost')} </span>
            <span className="font-mono">{formatCost(row.total_cost)}</span>
          </div>
        </div>
      ),
    },
    {
      key: 'capacity',
      title: t('groups.capacity'),
      width: '120px',
      hideOnMobile: true,
      render: (row) => (
        <div>
          <span className="font-mono" style={{ color: row.capacity_used > 0 ? 'var(--ag-primary)' : undefined }}>
            {row.capacity_used}
          </span>
          <span style={{ color: 'var(--ag-text-tertiary)' }}> / </span>
          <span className="font-mono">{row.capacity_total}</span>
        </div>
      ),
    },
    {
      key: 'sort_weight',
      title: t('groups.sort_weight'),
      width: '80px',
      hideOnMobile: true,
      render: (row) => (
        <span className="inline-flex items-center gap-1 font-mono">
          <ArrowUpDown className="w-3 h-3" style={{ color: 'var(--ag-text-tertiary)' }} />
          {row.sort_weight}
        </span>
      ),
    },
    {
      key: 'actions',
      title: t('common.actions'),
      width: '120px',
      fixed: 'right',
      render: (row) => (
        <div className="flex items-center justify-center gap-0.5">
          <button
            className="p-1.5 rounded hover:bg-bg-hover transition-colors"
            style={{ color: 'var(--ag-text-secondary)' }}
            title={t('common.edit')}
            onClick={() => setEditingGroup(row)}
          >
            <Pencil className="w-3.5 h-3.5" />
          </button>
          <button
            className="p-1.5 rounded hover:bg-bg-hover transition-colors"
            style={{ color: 'var(--ag-text-secondary)' }}
            title={t('groups.rate_override_manage')}
            onClick={() => setRateOverrideGroup(row)}
          >
            <Percent className="w-3.5 h-3.5" />
          </button>
          <button
            className="p-1.5 rounded hover:bg-bg-hover transition-colors"
            style={{ color: 'var(--ag-danger)' }}
            title={t('common.delete')}
            onClick={() => setDeletingGroup(row)}
          >
            <Trash2 className="w-3.5 h-3.5" />
          </button>
        </div>
      ),
    },
  ];

  return (
    <div>
      {/* 筛选 */}
      <div className="flex flex-wrap items-center gap-3 mb-5">
        <Select
          value={platformFilter}
          onChange={(e) => {
            setPlatformFilter(e.target.value);
            setPage(1);
          }}
          options={PLATFORM_OPTIONS}
        />
        <div className="flex items-center gap-2 ml-auto">
          <button
            onClick={() => refetch()}
            className="flex items-center justify-center w-9 h-9 rounded-[10px] text-text-tertiary hover:text-text-secondary hover:bg-bg-hover transition-colors"
          >
            <RefreshCw className="w-4 h-4" />
          </button>
          <Button icon={<Plus className="w-4 h-4" />} onClick={() => setShowCreateModal(true)}>
            {t('groups.create')}
          </Button>
        </div>
      </div>

      {/* 表格 */}
      <Table<GroupResp>
        columns={columns}
        data={data?.list ?? []}
        loading={isLoading}
        rowKey={(row) => row.id}
        page={page}
        pageSize={pageSize}
        total={data?.total ?? 0}
        onPageChange={setPage}
        onPageSizeChange={setPageSize}
      />

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
      <ConfirmModal
        open={!!deletingGroup}
        onClose={() => setDeletingGroup(null)}
        onConfirm={() => deletingGroup && deleteMutation.mutate(deletingGroup.id)}
        title={t('groups.delete_title')}
        message={t('groups.delete_confirm', { name: deletingGroup?.name })}
        loading={deleteMutation.isPending}
        danger
      />
    </div>
  );
}
