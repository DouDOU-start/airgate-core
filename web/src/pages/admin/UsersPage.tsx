import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { PageHeader } from '../../shared/components/PageHeader';
import { Button } from '../../shared/components/Button';
import { Input, Select } from '../../shared/components/Input';
import { Table, type Column } from '../../shared/components/Table';
import { ConfirmModal } from '../../shared/components/Modal';
import { Badge } from '../../shared/components/Badge';
import { DropdownMenu, type DropdownMenuItem } from '../../shared/components/DropdownMenu';
import { usersApi } from '../../shared/api/users';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { useDropdownMenu } from '../../shared/hooks/useDropdownMenu';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE } from '../../shared/constants';
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
  Plus, Search, Pencil, MoreHorizontal,
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

  const { menu, menuRef, open: openMenu, close: closeMenu } = useDropdownMenu();

  const { data, isLoading } = useQuery({
    queryKey: queryKeys.users(page, pageSize, keyword, statusFilter),
    queryFn: () =>
      usersApi.list({
        page,
        page_size: pageSize,
        keyword: keyword || undefined,
        status: statusFilter || undefined,
      }),
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

  const getMenuItems = (row: UserResp): DropdownMenuItem[] => {
    const items: DropdownMenuItem[] = [
      {
        icon: <Key className="w-3.5 h-3.5" style={{ color: 'var(--ag-primary)' }} />,
        label: t('users.api_keys'),
        onClick: () => setApiKeysUser(row),
      },
      {
        icon: <Users className="w-3.5 h-3.5" style={{ color: 'var(--ag-info)' }} />,
        label: t('users.groups'),
        onClick: () => setGroupsUser(row),
      },
      {
        icon: <PlusCircle className="w-3.5 h-3.5" style={{ color: 'var(--ag-success)' }} />,
        label: t('users.topup'),
        onClick: () => setBalanceUser({ user: row, defaultAction: 'add' }),
        divider: true,
      },
      {
        icon: <MinusCircle className="w-3.5 h-3.5" style={{ color: 'var(--ag-warning)' }} />,
        label: t('users.refund'),
        onClick: () => setBalanceUser({ user: row, defaultAction: 'subtract' }),
      },
      {
        icon: <Clock className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />,
        label: t('users.balance_history'),
        onClick: () => setBalanceHistoryUser(row),
      },
    ];
    if (row.role !== 'admin') {
      items.push({
        icon: <Trash2 className="w-3.5 h-3.5" />,
        label: t('common.delete'),
        onClick: () => setDeletingUser(row),
        danger: true,
        divider: true,
      });
    }
    return items;
  };

  const columns: Column<UserResp>[] = [
    {
      key: 'id',
      title: 'ID',
      width: '60px',
      render: (row) => <span className="text-text-tertiary font-mono">{row.id}</span>,
    },
    {
      key: 'email',
      title: <span className="pl-[38px]">{t('users.email')}</span>,
      align: 'left',
      render: (row) => (
        <div className="flex items-center gap-2.5">
          <div
            className="w-7 h-7 rounded-full flex items-center justify-center text-white text-xs font-semibold flex-shrink-0"
            style={{ backgroundColor: getAvatarColor(row.email) }}
          >
            {(row.email[0] ?? '?').toUpperCase()}
          </div>
          <span className="text-text truncate">{row.email}</span>
        </div>
      ),
    },
    {
      key: 'username',
      title: t('users.username'),
      render: (row) => <span className="text-text-secondary">{row.username || '-'}</span>,
    },
    {
      key: 'role',
      title: t('users.role'),
      render: (row) => (
        <Badge variant={row.role === 'admin' ? 'info' : 'default'}>
          {row.role === 'admin' ? t('users.role_admin') : t('users.role_user')}
        </Badge>
      ),
    },
    {
      key: 'balance',
      title: t('users.balance'),
      render: (row) => <span className="font-mono">${row.balance.toFixed(2)}</span>,
    },
    {
      key: 'status',
      title: t('common.status'),
      render: (row) => (
        <div className="flex items-center justify-center gap-2">
          <button
            type="button"
            className="relative inline-flex h-5 w-9 items-center rounded-full transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
            style={{ backgroundColor: row.status === 'active' ? 'var(--ag-success)' : 'var(--ag-text-tertiary)' }}
            disabled={row.role === 'admin'}
            title={row.status === 'active' ? t('users.disable') : t('users.enable')}
            onClick={() => row.status === 'active' ? setDisablingUser(row) : toggleMutation.mutate(row.id)}
          >
            <span
              className="inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform shadow-sm"
              style={{ transform: row.status === 'active' ? 'translateX(18px)' : 'translateX(3px)' }}
            />
          </button>
          <span className="text-xs" style={{ color: row.status === 'active' ? 'var(--ag-success)' : 'var(--ag-text-tertiary)' }}>
            {row.status === 'active' ? t('status.enabled') : t('status.disabled')}
          </span>
        </div>
      ),
    },
    {
      key: 'created_at',
      title: t('users.created_at'),
      render: (row) => (
        <span className="text-xs text-text-secondary">{formatDateTime(row.created_at)}</span>
      ),
    },
    {
      key: 'actions',
      title: t('common.actions'),
      fixed: 'right',
      render: (row) => (
        <div className="flex items-center justify-center gap-0.5">
          <button
            className="p-1.5 rounded hover:bg-bg-hover transition-colors cursor-pointer"
            style={{ color: 'var(--ag-text-secondary)' }}
            title={t('common.edit')}
            onClick={() => setEditingUser(row)}
          >
            <Pencil className="w-3.5 h-3.5" />
          </button>
          <button
            className="p-1.5 rounded hover:bg-bg-hover transition-colors cursor-pointer"
            style={{ color: 'var(--ag-text-secondary)' }}
            title={t('common.more')}
            onClick={(e) => {
              e.stopPropagation();
              if (menu?.id === row.id) {
                closeMenu();
              } else {
                openMenu(row.id, e);
              }
            }}
          >
            <MoreHorizontal className="w-3.5 h-3.5" />
          </button>
        </div>
      ),
    },
  ];

  return (
    <div>
      <PageHeader
        title={t('users.title')}
        actions={
          <Button icon={<Plus className="w-4 h-4" />} onClick={() => setShowCreateModal(true)}>
            {t('users.create')}
          </Button>
        }
      />

      <div className="flex items-end gap-3 mb-5">
        <div className="w-64">
          <Input
            placeholder={t('users.search_placeholder')}
            value={keyword}
            onChange={(e) => { setKeyword(e.target.value); setPage(1); }}
            icon={<Search className="w-4 h-4" />}
          />
        </div>
        <div className="w-36">
          <Select
            value={statusFilter}
            onChange={(e) => { setStatusFilter(e.target.value); setPage(1); }}
            options={[
              { value: '', label: t('users.all_status') },
              { value: 'active', label: t('status.active') },
              { value: 'disabled', label: t('status.disabled') },
            ]}
          />
        </div>
      </div>

      <Table<UserResp>
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

      {menu && (() => {
        const row = data?.list?.find((u) => u.id === menu.id);
        if (!row) return null;
        return (
          <DropdownMenu
            ref={menuRef}
            items={getMenuItems(row)}
            position={menu}
            onClose={closeMenu}
          />
        );
      })()}

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

      <ConfirmModal
        open={!!disablingUser}
        onClose={() => setDisablingUser(null)}
        onConfirm={() => disablingUser && toggleMutation.mutate(disablingUser.id)}
        title={t('users.disable_title')}
        message={t('users.disable_confirm', { email: disablingUser?.email })}
        loading={toggleMutation.isPending}
        danger
      />

      <ConfirmModal
        open={!!deletingUser}
        onClose={() => setDeletingUser(null)}
        onConfirm={() => deletingUser && deleteMutation.mutate(deletingUser.id)}
        title={t('users.delete_title')}
        message={t('users.delete_confirm', { email: deletingUser?.email })}
        loading={deleteMutation.isPending}
        danger
      />

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
