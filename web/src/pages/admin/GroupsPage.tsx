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
import { AlertDialog, Button, Chip, EmptyState, Label, ListBox, Select, Skeleton, Spinner, Table as HeroTable } from '@heroui/react';
import { PlatformIcon } from '../../shared/ui';
import { groupsApi } from '../../shared/api/groups';
import { usePlatforms } from '../../shared/hooks/usePlatforms';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
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
  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);
  const selectedPlatformLabel = PLATFORM_OPTIONS.find((option) => option.value === platformFilter)?.label ?? t('groups.all_platforms');

  return (
    <div>
      {/* 筛选 */}
      <div className="flex flex-wrap items-center gap-3 mb-5">
        <div className="w-48">
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
          <Button variant="primary" onPress={() => setShowCreateModal(true)}>
            <Plus className="w-4 h-4" />
            {t('groups.create')}
          </Button>
        </div>
      </div>

      {/* 表格 */}
      <HeroTable variant="primary">
        <HeroTable.ScrollContainer>
          <HeroTable.Content aria-label={t('groups.title', 'Groups')}>
            <HeroTable.Header>
              <HeroTable.Column id="name">{t('common.name')}</HeroTable.Column>
              <HeroTable.Column id="platform">{t('groups.platform')}</HeroTable.Column>
              <HeroTable.Column id="subscription_type">{t('groups.subscription_type')}</HeroTable.Column>
              <HeroTable.Column id="rate_multiplier" style={{ width: 96 }}>
                {t('groups.rate_multiplier')}
              </HeroTable.Column>
              <HeroTable.Column id="is_exclusive" style={{ width: 96 }}>
                {t('groups.group_type')}
              </HeroTable.Column>
              <HeroTable.Column id="account_stats" style={{ width: 144 }}>
                {t('groups.account_stats')}
              </HeroTable.Column>
              <HeroTable.Column id="usage" style={{ width: 128 }}>
                {t('groups.usage')}
              </HeroTable.Column>
              <HeroTable.Column id="capacity" style={{ width: 128 }}>
                {t('groups.capacity')}
              </HeroTable.Column>
              <HeroTable.Column id="sort_weight" style={{ width: 96 }}>
                {t('groups.sort_weight')}
              </HeroTable.Column>
              <HeroTable.Column id="actions" style={{ width: 132 }}>
                {t('common.actions')}
              </HeroTable.Column>
            </HeroTable.Header>
            <HeroTable.Body>
              {isLoading ? (
                Array.from({ length: 5 }).map((_, index) => (
                  <HeroTable.Row id={`loading-${index}`} key={`loading-${index}`}>
                    {Array.from({ length: 10 }).map((__, cellIndex) => (
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
                  <HeroTable.Cell colSpan={10}>
                    <EmptyState />
                  </HeroTable.Cell>
                </HeroTable.Row>
              ) : (
                rows.map((row) => (
                  <HeroTable.Row id={String(row.id)} key={row.id}>
                    <HeroTable.Cell>
                      <span className="inline-flex items-center gap-1.5">
                        <Layers className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />
                        <span style={{ color: 'var(--ag-text)' }} className="font-medium">
                          {row.name}
                        </span>
                      </span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="inline-flex items-center gap-1.5">
                        <PlatformIcon platform={row.platform} className="w-3.5 h-3.5" />
                        {platformName(row.platform)}
                      </span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <Chip color={row.subscription_type === 'subscription' ? 'accent' : 'default'} size="sm" variant="soft">
                        {row.subscription_type === 'subscription' ? t('groups.type_subscription') : t('groups.type_standard')}
                      </Chip>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="font-mono" style={{ color: 'var(--ag-primary)' }}>
                        {row.rate_multiplier}x
                      </span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      {row.is_exclusive ? (
                        <Chip color="warning" size="sm" variant="soft">{t('groups.type_exclusive')}</Chip>
                      ) : (
                        <Chip color="default" size="sm" variant="soft">{t('groups.type_public')}</Chip>
                      )}
                    </HeroTable.Cell>
                    <HeroTable.Cell>
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
                    </HeroTable.Cell>
                    <HeroTable.Cell>
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
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <div>
                        <span className="font-mono" style={{ color: row.capacity_used > 0 ? 'var(--ag-primary)' : undefined }}>
                          {row.capacity_used}
                        </span>
                        <span style={{ color: 'var(--ag-text-tertiary)' }}> / </span>
                        <span className="font-mono">{row.capacity_total}</span>
                      </div>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="inline-flex items-center gap-1 font-mono">
                        <ArrowUpDown className="w-3 h-3" style={{ color: 'var(--ag-text-tertiary)' }} />
                        {row.sort_weight}
                      </span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <div className="flex items-center justify-center gap-0.5">
                        <Button
                          isIconOnly
                          size="sm"
                          variant="ghost"
                          aria-label={t('common.edit')}
                          onPress={() => setEditingGroup(row)}
                        >
                          <Pencil className="w-3.5 h-3.5" />
                        </Button>
                        <Button
                          isIconOnly
                          size="sm"
                          variant="ghost"
                          aria-label={t('groups.rate_override_manage')}
                          onPress={() => setRateOverrideGroup(row)}
                        >
                          <Percent className="w-3.5 h-3.5" />
                        </Button>
                        <Button
                          isIconOnly
                          size="sm"
                          variant="ghost"
                          className="text-danger"
                          aria-label={t('common.delete')}
                          onPress={() => setDeletingGroup(row)}
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </Button>
                      </div>
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
