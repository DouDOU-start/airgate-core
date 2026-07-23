import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Chip, Dropdown, EmptyState, Input, Label, ListBox, Select, TextField as HeroTextField } from '@heroui/react';
import { usersApi } from '../../shared/api/users';
import { settingsApi } from '../../shared/api/settings';
import { tiersApi } from '../../shared/api/tiers';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { useDebouncedValue } from '../../shared/hooks/useDebouncedValue';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { CommonTable } from '../../shared/components/CommonTable';
import { MetricChips } from '../../shared/components/MetricChips';
import { NativeSwitch } from '../../shared/components/NativeSwitch';
import { SortableHeader } from '../../shared/components/SortableHeader';
import { getAvatarColor } from '../../shared/utils/avatar';
import { formatDateTime } from '../../shared/utils/format';
import { CreateUserModal } from './users/CreateUserModal';
import { EditUserModal } from './users/EditUserModal';
import { BalanceModal } from './users/BalanceModal';
import { UserApiKeysModal } from './users/UserApiKeysModal';
import { BalanceHistoryModal } from './users/BalanceHistoryModal';
import { UserGroupsModal } from './users/UserGroupsModal';
import { TiersModal } from './users/TiersModal';
import type { SortOrder, UserResp, UserSortBy } from '../../shared/types';
import { ConfirmDialog } from '../../shared/components/ConfirmDialog';
import { RefreshButton } from '../../shared/components/RefreshButton';
import {
  Plus, Search, Pencil, MoreHorizontal,
  Key, Users, PlusCircle, MinusCircle, Clock, Trash2, Crown,
} from 'lucide-react';

const FALLBACK_DEFAULT_USER_MAX_CONCURRENCY = 5;

function defaultUserMaxConcurrency(settings?: Array<{ key: string; value: string }>) {
  const raw = settings?.find((item) => item.key === 'default_concurrency')?.value;
  const value = Number.parseInt((raw ?? '').trim(), 10);
  return Number.isFinite(value) && value > 0 ? value : FALLBACK_DEFAULT_USER_MAX_CONCURRENCY;
}

export default function UsersPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();

  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE, 'admin.users');
  const [keyword, setKeyword] = useState('');
  const debouncedKeyword = useDebouncedValue(keyword.trim(), 250);
  const [statusFilter, setStatusFilter] = useState('');
  const [tierFilter, setTierFilter] = useState('');
  const [sort, setSort] = useState<{ by?: UserSortBy; order: SortOrder }>({ order: 'desc' });

  function handleSortChange(field: UserSortBy) {
    setSort((prev) => (prev.by === field ? { by: field, order: prev.order === 'asc' ? 'desc' : 'asc' } : { by: field, order: 'desc' }));
    setPage(1);
  }

  function sortState(field: UserSortBy): SortOrder | null {
    return sort.by === field ? sort.order : null;
  }

  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingUser, setEditingUser] = useState<UserResp | null>(null);
  const [balanceUser, setBalanceUser] = useState<{ user: UserResp; defaultAction: 'add' | 'subtract' } | null>(null);
  const [deletingUser, setDeletingUser] = useState<UserResp | null>(null);
  const [disablingUser, setDisablingUser] = useState<UserResp | null>(null);
  const [apiKeysUser, setApiKeysUser] = useState<UserResp | null>(null);
  const [balanceHistoryUser, setBalanceHistoryUser] = useState<UserResp | null>(null);
  const [groupsUser, setGroupsUser] = useState<UserResp | null>(null);
  const [showTiersModal, setShowTiersModal] = useState(false);

  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: queryKeys.users(page, pageSize, debouncedKeyword, statusFilter, tierFilter, sort.by, sort.order),
    queryFn: () =>
      usersApi.list({
        page,
        page_size: pageSize,
        keyword: debouncedKeyword || undefined,
        status: statusFilter || undefined,
        tier_id: tierFilter ? Number(tierFilter) : undefined,
        sort_by: sort.by,
        sort_order: sort.by ? sort.order : undefined,
      }),
    placeholderData: keepPreviousData,
  });

  const { data: tiersData } = useQuery({
    queryKey: queryKeys.tiers(),
    queryFn: () => tiersApi.list({ page: 1, page_size: 100 }),
  });

  const { data: settings } = useQuery({
    queryKey: queryKeys.settings(),
    queryFn: () => settingsApi.list(),
  });

  const createMutation = useCrudMutation({
    mutationFn: usersApi.create,
    successMessage: t('users.create_success'),
    queryKey: queryKeys.users(),
    onSuccess: () => setShowCreateModal(false),
  });

  const updateMutation = useCrudMutation({
    mutationFn: ({ id, data }: { id: number; data: Parameters<typeof usersApi.update>[1] }) =>
      usersApi.update(id, data),
    successMessage: t('users.update_success'),
    queryKey: queryKeys.users(),
    onSuccess: () => setEditingUser(null),
  });

  const balanceMutation = useCrudMutation({
    mutationFn: ({ id, data }: { id: number; data: Parameters<typeof usersApi.adjustBalance>[1] }) =>
      usersApi.adjustBalance(id, data),
    successMessage: t('users.balance_success'),
    queryKey: queryKeys.users(),
    onSuccess: () => setBalanceUser(null),
  });

  const toggleMutation = useCrudMutation({
    mutationFn: usersApi.toggleStatus,
    successMessage: t('users.toggle_success'),
    queryKey: queryKeys.users(),
    onSuccess: () => setDisablingUser(null),
  });

  const deleteMutation = useCrudMutation({
    mutationFn: usersApi.delete,
    successMessage: t('users.delete_success'),
    queryKey: queryKeys.users(),
    onSuccess: () => setDeletingUser(null),
  });

  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);
  const statusOptions = [
    { id: '', label: t('users.all_status') },
    { id: 'active', label: t('status.active') },
    { id: 'disabled', label: t('status.disabled') },
  ];
  const selectedStatusLabel = statusOptions.find((item) => item.id === statusFilter)?.label ?? t('users.all_status');
  const tierOptions = [
    { id: '', label: t('tiers.all') },
    ...(tiersData?.list ?? []).map((tier) => ({ id: String(tier.id), label: tier.name })),
  ];
  const selectedTierLabel = tierOptions.find((item) => item.id === tierFilter)?.label ?? t('tiers.all');

  return (
    <div>
      <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-3 mb-5 flex-wrap">
        <div className="w-full sm:w-48">
          <HeroTextField fullWidth aria-label={t('users.search_placeholder')}>
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
              <Input
                className="pl-9"
                placeholder={t('users.search_placeholder')}
                value={keyword}
                onChange={(e) => { setKeyword(e.target.value); setPage(1); }}
              />
            </div>
          </HeroTextField>
        </div>
        <div className="w-full sm:w-48">
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
        {tierOptions.length > 1 ? (
          <div className="w-full sm:w-48">
            <Select
              fullWidth
              selectedKey={tierFilter}
              onSelectionChange={(key) => {
                setTierFilter(key == null ? '' : String(key));
                setPage(1);
              }}
            >
              <Label className="sr-only">{t('users.tier')}</Label>
              <Select.Trigger>
                <Select.Value>{selectedTierLabel}</Select.Value>
                <Select.Indicator />
              </Select.Trigger>
              <Select.Popover>
                <ListBox items={tierOptions}>
                  {(item) => (
                    <ListBox.Item id={item.id} textValue={item.label}>
                      {item.label}
                    </ListBox.Item>
                  )}
                </ListBox>
              </Select.Popover>
            </Select>
          </div>
        ) : null}
        <div className="flex items-center gap-2 sm:ml-auto">
          <RefreshButton
            ariaLabel={t('common.refresh', 'Refresh')}
            isRefreshing={isFetching}
            onRefresh={refetch}
          />
          <Button variant="secondary" onPress={() => setShowTiersModal(true)}>
            <Crown className="w-4 h-4" />
            {t('tiers.title')}
          </Button>
          <Button variant="primary" onPress={() => setShowCreateModal(true)}>
            <Plus className="w-4 h-4" />
            {t('users.create')}
          </Button>
        </div>
      </div>

      <CommonTable
        ariaLabel={t('users.title', 'Users')}
        className="ag-users-table"
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
        minWidth={980}
      >
            <CommonTable.Header>
              <CommonTable.Column id="id" style={{ width: 72 }}>
                ID
              </CommonTable.Column>
              <CommonTable.Column id="email">{t('users.email')}</CommonTable.Column>
              <CommonTable.Column id="username">{t('users.username')}</CommonTable.Column>
              <CommonTable.Column id="role">{t('users.role')}</CommonTable.Column>
              <CommonTable.Column id="balance">
                <SortableHeader active={sortState('balance')} label={t('users.balance')} onClick={() => handleSortChange('balance')} />
              </CommonTable.Column>
              <CommonTable.Column id="runtime" style={{ width: '9.75rem' }}>
                <span className="inline-flex items-center gap-2" title={t('users.concurrency_rpm_hint')}>
                  <SortableHeader active={sortState('concurrency')} label={t('users.concurrency_label')} onClick={() => handleSortChange('concurrency')} />
                  <SortableHeader active={sortState('rpm')} label="RPM" onClick={() => handleSortChange('rpm')} />
                </span>
              </CommonTable.Column>
              <CommonTable.Column id="status">{t('common.status')}</CommonTable.Column>
              <CommonTable.Column id="created_at">{t('users.created_at')}</CommonTable.Column>
              <CommonTable.Column id="actions">{t('common.actions')}</CommonTable.Column>
            </CommonTable.Header>
            <CommonTable.Body>
              {isLoading ? (
                <TableLoadingRow colSpan={9} />
              ) : rows.length === 0 ? (
                <CommonTable.Row id="empty">
                  <CommonTable.Cell colSpan={9}>
                    <EmptyState>
                      <div className="text-sm text-default-500">{t('common.no_data')}</div>
                    </EmptyState>
                  </CommonTable.Cell>
                </CommonTable.Row>
              ) : (
                rows.map((row) => (
                  <CommonTable.Row id={String(row.id)} key={row.id}>
                    <CommonTable.Cell>
                      <span className="text-text-tertiary font-mono">{row.id}</span>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <div className="flex items-center gap-2.5">
                        <div
                          className="w-7 h-7 rounded-full flex items-center justify-center text-white text-xs font-semibold flex-shrink-0"
                          style={{ backgroundColor: getAvatarColor(row.email) }}
                        >
                          {(row.email[0] ?? '?').toUpperCase()}
                        </div>
                        <span className="text-text truncate">{row.email}</span>
                      </div>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <span className="text-text-secondary">{row.username || '-'}</span>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <div className="flex items-center gap-1.5">
                        <Chip color={row.role === 'admin' ? 'accent' : 'default'} size="sm" variant="soft">
                          {row.role === 'admin' ? t('users.role_admin') : t('users.role_user')}
                        </Chip>
                        {row.tier_name ? (
                          <Chip color="warning" size="sm" variant="soft">
                            {row.tier_name}
                          </Chip>
                        ) : null}
                      </div>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <span className="font-mono">${row.balance.toFixed(2)}</span>
                    </CommonTable.Cell>
                    <CommonTable.Cell className="ag-users-metric-cell">
                      <MetricChips
                        className="ag-metric-chips--stack ag-metric-chips--compact-y"
                        items={[
                          {
                            color: 'accent' as const,
                            label: t('users.concurrency_label'),
                            muted: (row.current_concurrency ?? 0) === 0,
                            value: `${row.current_concurrency ?? 0}/${row.max_concurrency > 0 ? row.max_concurrency : '∞'}`,
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
                      <NativeSwitch
                        ariaLabel={row.status === 'active' ? t('users.disable') : t('users.enable')}
                        isDisabled={row.role === 'admin'}
                        isSelected={row.status === 'active'}
                        contentClassName="text-xs"
                        contentStyle={{ color: row.status === 'active' ? 'var(--ag-success)' : 'var(--ag-text-tertiary)' }}
                        label={row.status === 'active' ? t('status.enabled') : t('status.disabled')}
                        onChange={(isSelected) => {
                          if (isSelected) {
                            toggleMutation.mutate(row.id);
                          } else {
                            setDisablingUser(row);
                          }
                        }}
                      />
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <span className="text-xs text-text-secondary">{formatDateTime(row.created_at)}</span>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <div className="ag-table-row-actions flex items-center justify-center gap-0.5">
                        <Button
                          isIconOnly
                          size="sm"
                          variant="secondary"
                          aria-label={t('common.edit')}
                          onPress={() => setEditingUser(row)}
                        >
                          <Pencil className="w-3.5 h-3.5" />
                        </Button>
                        <Dropdown>
                          <Dropdown.Trigger
                            aria-label={t('common.more')}
                            className="ag-table-row-more-trigger button button--icon-only button--sm button--secondary"
                          >
                            <MoreHorizontal className="w-3.5 h-3.5" />
                          </Dropdown.Trigger>
                          <Dropdown.Popover placement="bottom end">
                            <Dropdown.Menu
                              aria-label={t('common.actions')}
                              onAction={(key) => {
                                switch (String(key)) {
                                  case 'api_keys':
                                    setApiKeysUser(row);
                                    break;
                                  case 'groups':
                                    setGroupsUser(row);
                                    break;
                                  case 'topup':
                                    setBalanceUser({ user: row, defaultAction: 'add' });
                                    break;
                                  case 'refund':
                                    setBalanceUser({ user: row, defaultAction: 'subtract' });
                                    break;
                                  case 'balance_history':
                                    setBalanceHistoryUser(row);
                                    break;
                                  case 'delete':
                                    setDeletingUser(row);
                                    break;
                                }
                              }}
                            >
                              <Dropdown.Item id="api_keys" textValue={t('users.api_keys')}>
                                <span className="flex items-center gap-2">
                                  <Key className="w-3.5 h-3.5" style={{ color: 'var(--ag-primary)' }} />
                                  {t('users.api_keys')}
                                </span>
                              </Dropdown.Item>
                              <Dropdown.Item id="groups" textValue={t('users.groups')}>
                                <span className="flex items-center gap-2">
                                  <Users className="w-3.5 h-3.5" style={{ color: 'var(--ag-info)' }} />
                                  {t('users.groups')}
                                </span>
                              </Dropdown.Item>
                              <Dropdown.Item id="topup" textValue={t('users.topup')}>
                                <span className="flex items-center gap-2">
                                  <PlusCircle className="w-3.5 h-3.5" style={{ color: 'var(--ag-success)' }} />
                                  {t('users.topup')}
                                </span>
                              </Dropdown.Item>
                              <Dropdown.Item id="refund" textValue={t('users.refund')}>
                                <span className="flex items-center gap-2">
                                  <MinusCircle className="w-3.5 h-3.5" style={{ color: 'var(--ag-warning)' }} />
                                  {t('users.refund')}
                                </span>
                              </Dropdown.Item>
                              <Dropdown.Item id="balance_history" textValue={t('users.balance_history')}>
                                <span className="flex items-center gap-2">
                                  <Clock className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />
                                  {t('users.balance_history')}
                                </span>
                              </Dropdown.Item>
                              {row.role !== 'admin' ? (
                                <Dropdown.Item id="delete" className="text-danger" textValue={t('common.delete')}>
                                  <span className="flex items-center gap-2">
                                    <Trash2 className="w-3.5 h-3.5" />
                                    {t('common.delete')}
                                  </span>
                                </Dropdown.Item>
                              ) : null}
                            </Dropdown.Menu>
                          </Dropdown.Popover>
                        </Dropdown>
                      </div>
                    </CommonTable.Cell>
                  </CommonTable.Row>
                ))
              )}
            </CommonTable.Body>
      </CommonTable>

      <CreateUserModal
        open={showCreateModal}
        onClose={() => setShowCreateModal(false)}
        onSubmit={(data) => createMutation.mutate(data)}
        loading={createMutation.isPending}
        defaultMaxConcurrency={defaultUserMaxConcurrency(settings)}
      />

      {editingUser && (
        <EditUserModal
          open
          user={editingUser}
          onClose={() => setEditingUser(null)}
          onSubmit={(data) => updateMutation.mutate({ id: editingUser.id, data })}
          loading={updateMutation.isPending}
        />
      )}

      {balanceUser && (
        <BalanceModal
          open
          user={balanceUser.user}
          defaultAction={balanceUser.defaultAction}
          onClose={() => setBalanceUser(null)}
          onSubmit={(data) => balanceMutation.mutate({ id: balanceUser.user.id, data })}
          loading={balanceMutation.isPending}
        />
      )}

      <ConfirmDialog
        open={!!disablingUser}
        onOpenChange={(open) => {
          if (!open) setDisablingUser(null);
        }}
        title={t('users.disable_title')}
        description={t('users.disable_confirm', { email: disablingUser?.email })}
        loading={toggleMutation.isPending}
        onConfirm={() => disablingUser && toggleMutation.mutate(disablingUser.id)}
      />

      <ConfirmDialog
        open={!!deletingUser}
        onOpenChange={(open) => {
          if (!open) setDeletingUser(null);
        }}
        title={t('users.delete_title')}
        description={t('users.delete_confirm', { email: deletingUser?.email })}
        loading={deleteMutation.isPending}
        onConfirm={() => deletingUser && deleteMutation.mutate(deletingUser.id)}
      />

      {apiKeysUser && (
        <UserApiKeysModal open user={apiKeysUser} onClose={() => setApiKeysUser(null)} />
      )}

      {balanceHistoryUser && (
        <BalanceHistoryModal open user={balanceHistoryUser} onClose={() => setBalanceHistoryUser(null)} />
      )}

      {showTiersModal && (
        <TiersModal open onClose={() => setShowTiersModal(false)} />
      )}

      {groupsUser && (
        <UserGroupsModal
          open
          user={groupsUser}
          onClose={() => setGroupsUser(null)}
          onSaved={() => {
            queryClient.invalidateQueries({ queryKey: queryKeys.users() });
            setGroupsUser(null);
          }}
        />
      )}
    </div>
  );
}
