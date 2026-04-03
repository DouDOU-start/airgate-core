import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  Plus,
  Users,
  Settings2,
  Layers,
  User,
  RefreshCw,
} from 'lucide-react';
import { Button } from '../../shared/components/Button';
import { Select } from '../../shared/components/Input';
import { Table, type Column } from '../../shared/components/Table';
import { StatusBadge } from '../../shared/components/Badge';
import { subscriptionsApi } from '../../shared/api/subscriptions';
import { groupsApi } from '../../shared/api/groups';
import { usersApi } from '../../shared/api/users';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE, FETCH_ALL_PARAMS } from '../../shared/constants';
import { AssignModal } from './subscriptions/AssignModal';
import { BulkAssignModal } from './subscriptions/BulkAssignModal';
import { AdjustModal } from './subscriptions/AdjustModal';
import type {
  SubscriptionResp,
  AssignSubscriptionReq,
  BulkAssignReq,
  AdjustSubscriptionReq,
  UserResp,
} from '../../shared/types';

export default function SubscriptionsPage() {
  const { t } = useTranslation();

  const STATUS_OPTIONS = [
    { value: '', label: t('subscriptions.all_status') },
    { value: 'active', label: t('status.active') },
    { value: 'expired', label: t('status.expired') },
    { value: 'suspended', label: t('status.suspended') },
  ];

  // 筛选状态
  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE);
  const [statusFilter, setStatusFilter] = useState('');

  // 弹窗状态
  const [showAssignModal, setShowAssignModal] = useState(false);
  const [showBulkModal, setShowBulkModal] = useState(false);
  const [adjustingSub, setAdjustingSub] = useState<SubscriptionResp | null>(null);

  // 查询订阅列表
  const { data, isLoading, refetch } = useQuery({
    queryKey: queryKeys.subscriptions(page, pageSize, statusFilter),
    queryFn: () =>
      subscriptionsApi.adminList({
        page,
        page_size: pageSize,
        status: statusFilter || undefined,
      }),
  });

  // 查询分组列表
  const { data: groupsData } = useQuery({
    queryKey: queryKeys.groupsAll(),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
  });

  // 查询用户列表（用于选择用户）
  const { data: usersData } = useQuery({
    queryKey: queryKeys.usersAll(),
    queryFn: () => usersApi.list(FETCH_ALL_PARAMS),
  });

  // 分配订阅
  const assignMutation = useCrudMutation<unknown, AssignSubscriptionReq>({
    mutationFn: (data) => subscriptionsApi.assign(data),
    successMessage: t('subscriptions.assign_success'),
    queryKey: queryKeys.subscriptions(),
    onSuccess: () => setShowAssignModal(false),
  });

  // 批量分配
  const bulkMutation = useCrudMutation<unknown, BulkAssignReq>({
    mutationFn: (data) => subscriptionsApi.bulkAssign(data),
    successMessage: t('subscriptions.bulk_success'),
    queryKey: queryKeys.subscriptions(),
    onSuccess: () => setShowBulkModal(false),
  });

  // 调整订阅
  const adjustMutation = useCrudMutation<unknown, { id: number; data: AdjustSubscriptionReq }>({
    mutationFn: ({ id, data }) => subscriptionsApi.adjust(id, data),
    successMessage: t('subscriptions.adjust_success'),
    queryKey: queryKeys.subscriptions(),
    onSuccess: () => setAdjustingSub(null),
  });

  // 格式化日期
  const formatDate = (date: string) => {
    return new Date(date).toLocaleDateString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    });
  };

  // 查找用户邮箱
  const getUserEmail = (userId: number) => {
    const user = usersData?.list?.find((u: UserResp) => u.id === userId);
    return user ? user.email : `${t('subscriptions.user')} #${userId}`;
  };

  // 表格列定义
  const columns: Column<SubscriptionResp>[] = [
    {
      key: 'id',
      title: t('common.id'),
      width: '60px',
      render: (row) => (
        <span className="font-mono">
          {row.id}
        </span>
      ),
    },
    {
      key: 'user_id',
      title: t('subscriptions.user'),
      render: (row) => (
        <span className="inline-flex items-center gap-1.5">
          <User className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />
          {getUserEmail(row.user_id)}
        </span>
      ),
    },
    {
      key: 'group_name',
      title: t('subscriptions.group'),
      render: (row) => (
        <span className="inline-flex items-center gap-1.5">
          <Layers className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />
          <span style={{ color: 'var(--ag-text)' }} className="font-medium">
            {row.group_name}
          </span>
        </span>
      ),
    },
    {
      key: 'effective_at',
      title: t('subscriptions.effective_time'),
      render: (row) => (
        <span className="font-mono">
          {formatDate(row.effective_at)}
        </span>
      ),
    },
    {
      key: 'expires_at',
      title: t('subscriptions.expire_time'),
      render: (row) => (
        <span className="font-mono">
          {formatDate(row.expires_at)}
        </span>
      ),
    },
    {
      key: 'status',
      title: t('common.status'),
      render: (row) => <StatusBadge status={row.status} />,
    },
    {
      key: 'actions',
      title: t('common.actions'),
      render: (row) => (
        <Button
          size="sm"
          variant="ghost"
          icon={<Settings2 className="w-3.5 h-3.5" />}
          onClick={() => setAdjustingSub(row)}
        >
          {t('subscriptions.adjust')}
        </Button>
      ),
    },
  ];

  return (
    <div>
      {/* 筛选 */}
      <div className="flex flex-wrap items-center gap-3 mb-5">
        <Select
          value={statusFilter}
          onChange={(e) => {
            setStatusFilter(e.target.value);
            setPage(1);
          }}
          options={STATUS_OPTIONS}
        />
        <div className="flex items-center gap-2 ml-auto">
          <button
            onClick={() => refetch()}
            className="flex items-center justify-center w-9 h-9 rounded-[10px] text-text-tertiary hover:text-text-secondary hover:bg-bg-hover transition-colors"
          >
            <RefreshCw className="w-4 h-4" />
          </button>
          <Button
            variant="secondary"
            icon={<Users className="w-4 h-4" />}
            onClick={() => setShowBulkModal(true)}
          >
            {t('subscriptions.bulk_assign')}
          </Button>
          <Button
            icon={<Plus className="w-4 h-4" />}
            onClick={() => setShowAssignModal(true)}
          >
            {t('subscriptions.assign')}
          </Button>
        </div>
      </div>

      {/* 表格 */}
      <Table<SubscriptionResp>
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

      {/* 分配订阅弹窗 */}
      <AssignModal
        open={showAssignModal}
        groups={groupsData?.list ?? []}
        users={usersData?.list ?? []}
        onClose={() => setShowAssignModal(false)}
        onSubmit={(data) => assignMutation.mutate(data)}
        loading={assignMutation.isPending}
      />

      {/* 批量分配弹窗 */}
      <BulkAssignModal
        open={showBulkModal}
        groups={groupsData?.list ?? []}
        users={usersData?.list ?? []}
        onClose={() => setShowBulkModal(false)}
        onSubmit={(data) => bulkMutation.mutate(data)}
        loading={bulkMutation.isPending}
      />

      {/* 调整弹窗 */}
      {adjustingSub && (
        <AdjustModal
          open
          subscription={adjustingSub}
          onClose={() => setAdjustingSub(null)}
          onSubmit={(data) =>
            adjustMutation.mutate({ id: adjustingSub.id, data })
          }
          loading={adjustMutation.isPending}
        />
      )}
    </div>
  );
}
