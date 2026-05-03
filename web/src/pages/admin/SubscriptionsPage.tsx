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
import { Button, EmptyState, Label, ListBox, Select, Skeleton, Table as HeroTable } from '@heroui/react';
import {
  StatusChip,
} from '../../shared/ui';
import { subscriptionsApi } from '../../shared/api/subscriptions';
import { groupsApi } from '../../shared/api/groups';
import { usersApi } from '../../shared/api/users';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE, FETCH_ALL_PARAMS } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
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

  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);
  const selectedStatusLabel = STATUS_OPTIONS.find((option) => option.value === statusFilter)?.label ?? t('subscriptions.all_status');

  return (
    <div>
      {/* 筛选 */}
      <div className="flex flex-wrap items-center gap-3 mb-5">
        <div className="w-44">
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
              <ListBox items={STATUS_OPTIONS}>
                {(item) => (
                  <ListBox.Item id={item.value} textValue={item.label}>
                    {item.label}
                  </ListBox.Item>
                )}
              </ListBox>
            </Select.Popover>
          </Select>
        </div>
        <div className="flex items-center gap-2 ml-auto">
          <Button
            isIconOnly
            aria-label={t('common.refresh', 'Refresh')}
            size="md"
            variant="ghost"
            onPress={() => refetch()}
          >
            <RefreshCw className="w-4 h-4" />
          </Button>
          <Button
            variant="secondary"
            onPress={() => setShowBulkModal(true)}
          >
            <Users className="w-4 h-4" />
            {t('subscriptions.bulk_assign')}
          </Button>
          <Button
            variant="primary"
            onPress={() => setShowAssignModal(true)}
          >
            <Plus className="w-4 h-4" />
            {t('subscriptions.assign')}
          </Button>
        </div>
      </div>

      <HeroTable variant="primary">
        <HeroTable.ScrollContainer>
          <HeroTable.Content aria-label={t('subscriptions.title', 'Subscriptions')}>
            <HeroTable.Header>
              <HeroTable.Column id="id" style={{ width: 72 }}>
                {t('common.id')}
              </HeroTable.Column>
              <HeroTable.Column id="user_id">{t('subscriptions.user')}</HeroTable.Column>
              <HeroTable.Column id="group_name">{t('subscriptions.group')}</HeroTable.Column>
              <HeroTable.Column id="effective_at">{t('subscriptions.effective_time')}</HeroTable.Column>
              <HeroTable.Column id="expires_at">{t('subscriptions.expire_time')}</HeroTable.Column>
              <HeroTable.Column id="status">{t('common.status')}</HeroTable.Column>
              <HeroTable.Column id="actions">{t('common.actions')}</HeroTable.Column>
            </HeroTable.Header>
            <HeroTable.Body>
              {isLoading ? (
                Array.from({ length: 5 }).map((_, index) => (
                  <HeroTable.Row id={`loading-${index}`} key={`loading-${index}`}>
                    {Array.from({ length: 7 }).map((__, cellIndex) => (
                      <HeroTable.Cell key={cellIndex}>
                        <Skeleton
                          className="h-4 w-24"
                          style={{ animationDelay: `${index * 90 + cellIndex * 20}ms` }}
                        />
                      </HeroTable.Cell>
                    ))}
                  </HeroTable.Row>
                ))
              ) : rows.length === 0 ? (
                <HeroTable.Row id="empty">
                  <HeroTable.Cell colSpan={7}>
                    <EmptyState />
                  </HeroTable.Cell>
                </HeroTable.Row>
              ) : (
                rows.map((row) => (
                  <HeroTable.Row id={String(row.id)} key={row.id}>
                    <HeroTable.Cell>
                      <span className="font-mono">{row.id}</span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="inline-flex items-center gap-1.5">
                        <User className="h-3.5 w-3.5 text-text-tertiary" />
                        {getUserEmail(row.user_id)}
                      </span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="inline-flex items-center gap-1.5">
                        <Layers className="h-3.5 w-3.5 text-text-tertiary" />
                        <span className="font-medium text-text">{row.group_name}</span>
                      </span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="font-mono">{formatDate(row.effective_at)}</span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="font-mono">{formatDate(row.expires_at)}</span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <StatusChip status={row.status} />
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <Button
                        size="sm"
                        variant="ghost"
                        onPress={() => setAdjustingSub(row)}
                      >
                        <Settings2 className="w-3.5 h-3.5" />
                        {t('subscriptions.adjust')}
                      </Button>
                    </HeroTable.Cell>
                  </HeroTable.Row>
                ))
              )}
            </HeroTable.Body>
          </HeroTable.Content>
        </HeroTable.ScrollContainer>
        <HeroTable.Footer>
          <TablePaginationFooter
            page={page}
            pageSize={pageSize}
            setPage={setPage}
            setPageSize={setPageSize}
            total={total}
            totalPages={totalPages}
          />
        </HeroTable.Footer>
      </HeroTable>

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
