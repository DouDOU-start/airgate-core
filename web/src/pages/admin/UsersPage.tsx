import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertDialog, Button, Chip, Dropdown, EmptyState, Input, Label, ListBox, Select, Spinner, Switch, Table as HeroTable, TextField as HeroTextField } from '@heroui/react';
import { usersApi } from '../../shared/api/users';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { getAvatarColor } from '../../shared/utils/avatar';
import { formatDateTime } from '../../shared/utils/format';
import { CreateUserModal } from './users/CreateUserModal';
import { EditUserModal } from './users/EditUserModal';
import { BalanceModal } from './users/BalanceModal';
import { UserApiKeysModal } from './users/UserApiKeysModal';
import { BalanceHistoryModal } from './users/BalanceHistoryModal';
import { UserGroupsModal } from './users/UserGroupsModal';
import type { UserResp } from '../../shared/types';
import {
  Plus, Search, Pencil, MoreHorizontal, RefreshCw,
  Key, Users, PlusCircle, MinusCircle, Clock, Trash2,
} from 'lucide-react';

export default function UsersPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();

  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE);
  const [keyword, setKeyword] = useState('');
  const [statusFilter, setStatusFilter] = useState('');

  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingUser, setEditingUser] = useState<UserResp | null>(null);
  const [balanceUser, setBalanceUser] = useState<{ user: UserResp; defaultAction: 'add' | 'subtract' } | null>(null);
  const [deletingUser, setDeletingUser] = useState<UserResp | null>(null);
  const [disablingUser, setDisablingUser] = useState<UserResp | null>(null);
  const [apiKeysUser, setApiKeysUser] = useState<UserResp | null>(null);
  const [balanceHistoryUser, setBalanceHistoryUser] = useState<UserResp | null>(null);
  const [groupsUser, setGroupsUser] = useState<UserResp | null>(null);

  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: queryKeys.users(page, pageSize, keyword, statusFilter),
    queryFn: () =>
      usersApi.list({
        page,
        page_size: pageSize,
        keyword: keyword || undefined,
        status: statusFilter || undefined,
      }),
    placeholderData: keepPreviousData,
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

  return (
    <div>
      <div className="flex flex-wrap items-end gap-3 mb-5">
        <div className="w-full sm:w-64">
          <HeroTextField fullWidth>
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
        <div className="w-36">
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
        <div className="flex items-center gap-2 ml-auto">
          {isFetching ? (
            <RefreshCw className="w-4 h-4 text-text-tertiary animate-spin" />
          ) : (
            <Button
              isIconOnly
              aria-label={t('common.refresh', 'Refresh')}
              size="md"
              variant="ghost"
              onPress={() => refetch()}
            >
              <RefreshCw className="w-4 h-4" />
            </Button>
          )}
          <Button variant="primary" onPress={() => setShowCreateModal(true)}>
            <Plus className="w-4 h-4" />
            {t('users.create')}
          </Button>
        </div>
      </div>

      <HeroTable variant="primary">
        <HeroTable.ScrollContainer>
          <HeroTable.Content aria-label={t('users.title', 'Users')}>
            <HeroTable.Header>
              <HeroTable.Column id="id" style={{ width: 72 }}>
                ID
              </HeroTable.Column>
              <HeroTable.Column id="email">{t('users.email')}</HeroTable.Column>
              <HeroTable.Column id="username">{t('users.username')}</HeroTable.Column>
              <HeroTable.Column id="role">{t('users.role')}</HeroTable.Column>
              <HeroTable.Column id="balance">{t('users.balance')}</HeroTable.Column>
              <HeroTable.Column id="status">{t('common.status')}</HeroTable.Column>
              <HeroTable.Column id="created_at">{t('users.created_at')}</HeroTable.Column>
              <HeroTable.Column id="actions">{t('common.actions')}</HeroTable.Column>
            </HeroTable.Header>
            <HeroTable.Body>
              {isLoading ? (
                <TableLoadingRow colSpan={8} />
              ) : rows.length === 0 ? (
                <HeroTable.Row id="empty">
                  <HeroTable.Cell colSpan={8}>
                    <EmptyState />
                  </HeroTable.Cell>
                </HeroTable.Row>
              ) : (
                rows.map((row) => (
                  <HeroTable.Row id={String(row.id)} key={row.id}>
                    <HeroTable.Cell>
                      <span className="text-text-tertiary font-mono">{row.id}</span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <div className="flex items-center gap-2.5">
                        <div
                          className="w-7 h-7 rounded-full flex items-center justify-center text-white text-xs font-semibold flex-shrink-0"
                          style={{ backgroundColor: getAvatarColor(row.email) }}
                        >
                          {(row.email[0] ?? '?').toUpperCase()}
                        </div>
                        <span className="text-text truncate">{row.email}</span>
                      </div>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="text-text-secondary">{row.username || '-'}</span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <Chip color={row.role === 'admin' ? 'accent' : 'default'} size="sm" variant="soft">
                        {row.role === 'admin' ? t('users.role_admin') : t('users.role_user')}
                      </Chip>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="font-mono">${row.balance.toFixed(2)}</span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <Switch
                        aria-label={row.status === 'active' ? t('users.disable') : t('users.enable')}
                        isDisabled={row.role === 'admin'}
                        isSelected={row.status === 'active'}
                        size="sm"
                        onChange={(isSelected) => {
                          if (isSelected) {
                            toggleMutation.mutate(row.id);
                          } else {
                            setDisablingUser(row);
                          }
                        }}
                      >
                        <Switch.Control>
                          <Switch.Thumb />
                        </Switch.Control>
                        <Switch.Content
                          className="text-xs"
                          style={{ color: row.status === 'active' ? 'var(--ag-success)' : 'var(--ag-text-tertiary)' }}
                        >
                          {row.status === 'active' ? t('status.enabled') : t('status.disabled')}
                        </Switch.Content>
                      </Switch>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="text-xs text-text-secondary">{formatDateTime(row.created_at)}</span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <div className="flex items-center justify-center gap-0.5">
                        <Button
                          isIconOnly
                          size="sm"
                          variant="ghost"
                          aria-label={t('common.edit')}
                          onPress={() => setEditingUser(row)}
                        >
                          <Pencil className="w-3.5 h-3.5" />
                        </Button>
                        <Dropdown>
                          <Dropdown.Trigger>
                            <Button
                              isIconOnly
                              aria-label={t('common.more')}
                              size="sm"
                              variant="ghost"
                            >
                              <MoreHorizontal className="w-3.5 h-3.5" />
                            </Button>
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

      <CreateUserModal
        open={showCreateModal}
        onClose={() => setShowCreateModal(false)}
        onSubmit={(data) => createMutation.mutate(data)}
        loading={createMutation.isPending}
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

      <AlertDialog
        isOpen={!!disablingUser}
        onOpenChange={(open) => {
          if (!open) setDisablingUser(null);
        }}
      >
        <AlertDialog.Backdrop>
          <AlertDialog.Container placement="center" size="sm">
            <AlertDialog.Dialog className="ag-elevation-modal">
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t('users.disable_title')}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>{t('users.disable_confirm', { email: disablingUser?.email })}</AlertDialog.Body>
              <AlertDialog.Footer>
                <Button variant="secondary" onPress={() => setDisablingUser(null)}>
                  {t('common.cancel')}
                </Button>
                <Button
                  aria-busy={toggleMutation.isPending}
                  isDisabled={toggleMutation.isPending}
                  variant="danger"
                  onPress={() => disablingUser && toggleMutation.mutate(disablingUser.id)}
                >
                  {toggleMutation.isPending ? <Spinner size="sm" /> : null}
                  {t('common.confirm')}
                </Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>

      <AlertDialog
        isOpen={!!deletingUser}
        onOpenChange={(open) => {
          if (!open) setDeletingUser(null);
        }}
      >
        <AlertDialog.Backdrop>
          <AlertDialog.Container placement="center" size="sm">
            <AlertDialog.Dialog className="ag-elevation-modal">
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t('users.delete_title')}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>{t('users.delete_confirm', { email: deletingUser?.email })}</AlertDialog.Body>
              <AlertDialog.Footer>
                <Button variant="secondary" onPress={() => setDeletingUser(null)}>
                  {t('common.cancel')}
                </Button>
                <Button
                  aria-busy={deleteMutation.isPending}
                  isDisabled={deleteMutation.isPending}
                  variant="danger"
                  onPress={() => deletingUser && deleteMutation.mutate(deletingUser.id)}
                >
                  {deleteMutation.isPending ? <Spinner size="sm" /> : null}
                  {t('common.confirm')}
                </Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>

      {apiKeysUser && (
        <UserApiKeysModal open user={apiKeysUser} onClose={() => setApiKeysUser(null)} />
      )}

      {balanceHistoryUser && (
        <BalanceHistoryModal open user={balanceHistoryUser} onClose={() => setBalanceHistoryUser(null)} />
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
