import { useState, useRef, useEffect } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { PageHeader } from '../../shared/components/PageHeader';
import { Button } from '../../shared/components/Button';
import { Input } from '../../shared/components/Input';
import { Select } from '../../shared/components/Input';
import { Table, type Column } from '../../shared/components/Table';
import { Modal, ConfirmModal } from '../../shared/components/Modal';
import { Badge, StatusBadge } from '../../shared/components/Badge';
import { useToast } from '../../shared/components/Toast';
import { usersApi } from '../../shared/api/users';
import { groupsApi } from '../../shared/api/groups';
import type { UserResp, CreateUserReq, UpdateUserReq, AdjustBalanceReq, BalanceLogResp, APIKeyResp, GroupResp } from '../../shared/types';
import {
  Plus, Search, Pencil, MoreHorizontal,
  Key, Users, PlusCircle, MinusCircle, Clock, Trash2, RefreshCw, Eye, EyeOff,
} from 'lucide-react';

const PAGE_SIZE = 20;

// 头像颜色池
const AVATAR_COLORS = [
  '#10b981', '#6366f1', '#f59e0b', '#ef4444', '#8b5cf6',
  '#ec4899', '#14b8a6', '#f97316', '#06b6d4', '#84cc16',
];
function getAvatarColor(str: string): string {
  let hash = 0;
  for (let i = 0; i < str.length; i++) hash = str.charCodeAt(i) + ((hash << 5) - hash);
  return AVATAR_COLORS[Math.abs(hash) % AVATAR_COLORS.length]!;
}

export default function UsersPage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();

  const [page, setPage] = useState(1);
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
  const [moreMenu, setMoreMenu] = useState<{ id: number; top: number; left: number } | null>(null);
  const moreMenuRef = useRef<HTMLDivElement>(null);

  const { data, isLoading } = useQuery({
    queryKey: ['users', page, keyword, statusFilter],
    queryFn: () =>
      usersApi.list({
        page,
        page_size: PAGE_SIZE,
        keyword: keyword || undefined,
        status: statusFilter || undefined,
      }),
  });

  // 点击外部关闭更多菜单
  useEffect(() => {
    if (!moreMenu) return;
    const handler = (e: MouseEvent) => {
      if (moreMenuRef.current && !moreMenuRef.current.contains(e.target as Node)) {
        setMoreMenu(null);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [moreMenu]);

  const createMutation = useMutation({
    mutationFn: (data: CreateUserReq) => usersApi.create(data),
    onSuccess: () => {
      toast('success', t('users.create_success'));
      setShowCreateModal(false);
      queryClient.invalidateQueries({ queryKey: ['users'] });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: number; data: UpdateUserReq }) => usersApi.update(id, data),
    onSuccess: () => {
      toast('success', t('users.update_success'));
      setEditingUser(null);
      queryClient.invalidateQueries({ queryKey: ['users'] });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  const balanceMutation = useMutation({
    mutationFn: ({ id, data }: { id: number; data: AdjustBalanceReq }) => usersApi.adjustBalance(id, data),
    onSuccess: () => {
      toast('success', t('users.balance_success'));
      setBalanceUser(null);
      queryClient.invalidateQueries({ queryKey: ['users'] });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  const toggleMutation = useMutation({
    mutationFn: (id: number) => usersApi.toggleStatus(id),
    onSuccess: () => {
      toast('success', t('users.toggle_success'));
      setDisablingUser(null);
      queryClient.invalidateQueries({ queryKey: ['users'] });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => usersApi.delete(id),
    onSuccess: () => {
      toast('success', t('users.delete_success'));
      setDeletingUser(null);
      queryClient.invalidateQueries({ queryKey: ['users'] });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  const columns: Column<UserResp>[] = [
    {
      key: 'email',
      title: t('users.email'),
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
      key: 'id',
      title: 'ID',
      width: '60px',
      render: (row) => <span className="text-text-tertiary font-mono">{row.id}</span>,
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
      render: (row) => (
        <span className="font-mono">${row.balance.toFixed(2)}</span>
      ),
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
        <span className="text-xs text-text-secondary">
          {new Date(row.created_at).toLocaleString('zh-CN', {
            year: 'numeric', month: '2-digit', day: '2-digit',
            hour: '2-digit', minute: '2-digit', second: '2-digit',
          })}
        </span>
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
              if (moreMenu?.id === row.id) {
                setMoreMenu(null);
              } else {
                const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
                setMoreMenu({ id: row.id, top: rect.bottom + 4, left: rect.right });
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

      {/* 筛选栏 */}
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
        pageSize={PAGE_SIZE}
        total={data?.total ?? 0}
        onPageChange={setPage}
      />

      {/* 更多操作下拉菜单 (Portal) */}
      {moreMenu && createPortal(
        <div
          ref={moreMenuRef}
          className="fixed py-1 rounded-lg shadow-lg min-w-[140px]"
          style={{
            top: moreMenu.top,
            left: moreMenu.left,
            transform: 'translateX(-100%)',
            zIndex: 9999,
            background: 'var(--ag-bg-elevated)',
            border: '1px solid var(--ag-glass-border)',
          }}
        >
          {(() => {
            const row = data?.list?.find((u) => u.id === moreMenu.id);
            if (!row) return null;
            return (
              <>
                <button
                  className="flex items-center gap-2 w-full px-3 py-1.5 text-xs hover:bg-bg-hover transition-colors text-left cursor-pointer"
                  style={{ color: 'var(--ag-text-secondary)' }}
                  onClick={() => { setApiKeysUser(row); setMoreMenu(null); }}
                >
                  <Key className="w-3.5 h-3.5" style={{ color: 'var(--ag-primary)' }} />
                  {t('users.api_keys')}
                </button>
                <button
                  className="flex items-center gap-2 w-full px-3 py-1.5 text-xs hover:bg-bg-hover transition-colors text-left cursor-pointer"
                  style={{ color: 'var(--ag-text-secondary)' }}
                  onClick={() => {
                    setGroupsUser(row);
                    setMoreMenu(null);
                  }}
                >
                  <Users className="w-3.5 h-3.5" style={{ color: 'var(--ag-info)' }} />
                  {t('users.groups')}
                </button>
                <div className="my-1 border-t" style={{ borderColor: 'var(--ag-border-subtle)' }} />
                <button
                  className="flex items-center gap-2 w-full px-3 py-1.5 text-xs hover:bg-bg-hover transition-colors text-left cursor-pointer"
                  style={{ color: 'var(--ag-text-secondary)' }}
                  onClick={() => { setBalanceUser({ user: row, defaultAction: 'add' }); setMoreMenu(null); }}
                >
                  <PlusCircle className="w-3.5 h-3.5" style={{ color: 'var(--ag-success)' }} />
                  {t('users.topup')}
                </button>
                <button
                  className="flex items-center gap-2 w-full px-3 py-1.5 text-xs hover:bg-bg-hover transition-colors text-left cursor-pointer"
                  style={{ color: 'var(--ag-text-secondary)' }}
                  onClick={() => { setBalanceUser({ user: row, defaultAction: 'subtract' }); setMoreMenu(null); }}
                >
                  <MinusCircle className="w-3.5 h-3.5" style={{ color: 'var(--ag-warning)' }} />
                  {t('users.refund')}
                </button>
                <button
                  className="flex items-center gap-2 w-full px-3 py-1.5 text-xs hover:bg-bg-hover transition-colors text-left cursor-pointer"
                  style={{ color: 'var(--ag-text-secondary)' }}
                  onClick={() => { setBalanceHistoryUser(row); setMoreMenu(null); }}
                >
                  <Clock className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />
                  {t('users.balance_history')}
                </button>
                {row.role !== 'admin' && (
                  <>
                    <div className="my-1 border-t" style={{ borderColor: 'var(--ag-border-subtle)' }} />
                    <button
                      className="flex items-center gap-2 w-full px-3 py-1.5 text-xs hover:bg-bg-hover transition-colors text-left cursor-pointer"
                      style={{ color: 'var(--ag-danger)' }}
                      onClick={() => { setDeletingUser(row); setMoreMenu(null); }}
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                      {t('common.delete')}
                    </button>
                  </>
                )}
              </>
            );
          })()}
        </div>,
        document.body,
      )}

      {/* 创建用户弹窗 */}
      <CreateUserModal
        open={showCreateModal}
        onClose={() => setShowCreateModal(false)}
        onSubmit={(data) => createMutation.mutate(data)}
        loading={createMutation.isPending}
      />

      {/* 编辑用户弹窗 */}
      {editingUser && (
        <EditUserModal
          open
          user={editingUser}
          onClose={() => setEditingUser(null)}
          onSubmit={(data) => updateMutation.mutate({ id: editingUser.id, data })}
          loading={updateMutation.isPending}
        />
      )}

      {/* 余额调整弹窗 */}
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

      {/* 禁用确认 */}
      <ConfirmModal
        open={!!disablingUser}
        onClose={() => setDisablingUser(null)}
        onConfirm={() => disablingUser && toggleMutation.mutate(disablingUser.id)}
        title={t('users.disable_title')}
        message={t('users.disable_confirm', { email: disablingUser?.email })}
        loading={toggleMutation.isPending}
        danger
      />

      {/* 删除确认 */}
      <ConfirmModal
        open={!!deletingUser}
        onClose={() => setDeletingUser(null)}
        onConfirm={() => deletingUser && deleteMutation.mutate(deletingUser.id)}
        title={t('users.delete_title')}
        message={t('users.delete_confirm', { email: deletingUser?.email })}
        loading={deleteMutation.isPending}
        danger
      />

      {/* API 密钥弹窗 */}
      {apiKeysUser && (
        <UserApiKeysModal
          open
          user={apiKeysUser}
          onClose={() => setApiKeysUser(null)}
        />
      )}

      {/* 余额记录弹窗 */}
      {balanceHistoryUser && (
        <UserBalanceHistoryModal
          open
          user={balanceHistoryUser}
          onClose={() => setBalanceHistoryUser(null)}
        />
      )}

      {/* 分组分配弹窗 */}
      {groupsUser && (
        <UserGroupsModal
          open
          user={groupsUser}
          onClose={() => setGroupsUser(null)}
          onSaved={() => {
            queryClient.invalidateQueries({ queryKey: ['users'] });
            setGroupsUser(null);
          }}
        />
      )}
    </div>
  );
}

/* ==================== 创建用户弹窗 ==================== */

function CreateUserModal({
  open, onClose, onSubmit, loading,
}: {
  open: boolean; onClose: () => void; onSubmit: (data: CreateUserReq) => void; loading: boolean;
}) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const [form, setForm] = useState<CreateUserReq>({
    email: '', password: '', username: '', role: 'user', max_concurrency: 5,
  });
  const [showPassword, setShowPassword] = useState(false);

  const generatePassword = () => {
    const chars = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%&*';
    const arr = new Uint8Array(16);
    crypto.getRandomValues(arr);
    const pwd = Array.from(arr, (b) => chars[b % chars.length]).join('');
    setForm({ ...form, password: pwd });
    navigator.clipboard.writeText(pwd).then(
      () => toast('success', t('common.copied')),
      () => { /* ignore */ },
    );
  };

  const handleSubmit = () => {
    if (!form.email || !form.password) return;
    onSubmit(form);
  };

  const handleClose = () => {
    setForm({ email: '', password: '', username: '', role: 'user', max_concurrency: 5 });
    onClose();
  };

  return (
    <Modal
      open={open}
      onClose={handleClose}
      title={t('users.create')}
      footer={
        <>
          <Button variant="secondary" onClick={handleClose}>{t('common.cancel')}</Button>
          <Button onClick={handleSubmit} loading={loading}>{t('common.create')}</Button>
        </>
      }
    >
      <div className="space-y-4">
        <Input label={t('users.email')} type="email" required value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} />
        <div>
          <div className="relative">
            <Input label={t('users.password')} type={showPassword ? 'text' : 'password'} required value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} />
            <button
              type="button"
              className="absolute right-3 bottom-[10px] text-text-tertiary hover:text-text-secondary transition-colors cursor-pointer"
              onClick={() => setShowPassword(!showPassword)}
            >
              {showPassword ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
            </button>
          </div>
          <button
            type="button"
            className="mt-1.5 flex items-center gap-1 text-[11px] text-primary hover:text-primary/80 transition-colors cursor-pointer"
            onClick={generatePassword}
          >
            <RefreshCw className="w-3 h-3" />
            {t('users.generate_password')}
          </button>
        </div>
        <Input label={t('users.username')} value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} />
        <Input
          label={t('users.max_concurrency')}
          type="number"
          value={String(form.max_concurrency ?? 5)}
          onChange={(e) => setForm({ ...form, max_concurrency: Number(e.target.value) })}
        />
      </div>
    </Modal>
  );
}

/* ==================== 编辑用户弹窗 ==================== */

function EditUserModal({
  open, user, onClose, onSubmit, loading,
}: {
  open: boolean; user: UserResp; onClose: () => void; onSubmit: (data: UpdateUserReq) => void; loading: boolean;
}) {
  const { t } = useTranslation();
  const [form, setForm] = useState<UpdateUserReq>({
    username: user.username,
    role: user.role,
    max_concurrency: user.max_concurrency,
    status: user.status as 'active' | 'disabled',
  });
  const [showPassword, setShowPassword] = useState(false);

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t('users.edit')}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>{t('common.cancel')}</Button>
          <Button onClick={() => onSubmit(form)} loading={loading}>{t('common.save')}</Button>
        </>
      }
    >
      <div className="space-y-4">
        <Input label={t('users.email')} value={user.email} disabled />
        <Input label={t('users.username')} value={form.username ?? ''} onChange={(e) => setForm({ ...form, username: e.target.value })} />
        <div className="relative">
          <Input
            label={t('users.password')}
            type={showPassword ? 'text' : 'password'}
            placeholder={t('accounts.leave_empty_to_keep')}
            value={form.password ?? ''}
            onChange={(e) => setForm({ ...form, password: e.target.value || undefined })}
          />
          <button
            type="button"
            className="absolute right-3 bottom-[10px] text-text-tertiary hover:text-text-secondary transition-colors cursor-pointer"
            onClick={() => setShowPassword(!showPassword)}
          >
            {showPassword ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
          </button>
        </div>
        <Input
          label={t('users.max_concurrency')}
          type="number"
          value={String(form.max_concurrency ?? 5)}
          onChange={(e) => setForm({ ...form, max_concurrency: Number(e.target.value) })}
        />
        <div className="space-y-1.5">
          <label className="block text-xs font-medium text-text-secondary uppercase tracking-wider">{t('common.status')}</label>
          <div className="flex items-center gap-2">
            <button
              type="button"
              className="relative inline-flex h-5 w-9 items-center rounded-full transition-colors cursor-pointer"
              style={{ backgroundColor: form.status === 'active' ? 'var(--ag-success)' : 'var(--ag-text-tertiary)' }}
              onClick={() => setForm({ ...form, status: form.status === 'active' ? 'disabled' : 'active' })}
            >
              <span
                className="inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform shadow-sm"
                style={{ transform: form.status === 'active' ? 'translateX(18px)' : 'translateX(3px)' }}
              />
            </button>
            <span className="text-xs" style={{ color: form.status === 'active' ? 'var(--ag-success)' : 'var(--ag-text-tertiary)' }}>
              {form.status === 'active' ? t('status.enabled') : t('status.disabled')}
            </span>
          </div>
        </div>
      </div>
    </Modal>
  );
}

/* ==================== 余额调整弹窗 ==================== */

function BalanceModal({
  open, user, defaultAction, onClose, onSubmit, loading,
}: {
  open: boolean; user: UserResp; defaultAction: 'add' | 'subtract'; onClose: () => void;
  onSubmit: (data: AdjustBalanceReq) => void; loading: boolean;
}) {
  const { t } = useTranslation();
  const [form, setForm] = useState<AdjustBalanceReq>({ action: defaultAction, amount: 0, remark: t('users.remark_admin_adjust') });

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={defaultAction === 'add' ? t('users.topup') : t('users.refund')}
      width="420px"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>{t('common.cancel')}</Button>
          <Button onClick={() => onSubmit(form)} loading={loading}>{t('common.confirm')}</Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="border border-glass-border bg-bg-elevated shadow-sm rounded-lg px-4 py-3">
          <p className="text-xs text-text-tertiary uppercase tracking-wider">{t('users.current_balance')}</p>
          <p className="text-lg font-bold mt-1 font-mono">${user.balance.toFixed(2)}</p>
        </div>
        <div>
          <Input
            label={t('users.amount')}
            type="number"
            required
            min="0"
            max={defaultAction === 'subtract' ? String(user.balance) : undefined}
            step="0.01"
            value={String(form.amount)}
            onChange={(e) => {
              const val = Number(e.target.value);
              setForm({ ...form, amount: defaultAction === 'subtract' ? Math.min(val, user.balance) : val });
            }}
          />
          {defaultAction === 'subtract' && (
            <button
              type="button"
              className="mt-1 text-[11px] text-primary hover:text-primary/80 transition-colors cursor-pointer"
              onClick={() => setForm({ ...form, amount: user.balance })}
            >
              {t('users.withdraw_all')}
            </button>
          )}
        </div>
        <Input
          label={t('users.remark')}
          placeholder={t('users.remark_placeholder')}
          value={form.remark ?? ''}
          onChange={(e) => setForm({ ...form, remark: e.target.value })}
        />
      </div>
    </Modal>
  );
}

/* ==================== 用户 API 密钥弹窗 ==================== */

function UserApiKeysModal({
  open, user, onClose,
}: {
  open: boolean; user: UserResp; onClose: () => void;
}) {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);

  const { data, isLoading } = useQuery({
    queryKey: ['user-api-keys', user.id, page],
    queryFn: () => usersApi.apiKeys(user.id, { page, page_size: 10 }),
    enabled: open,
  });

  const columns: Column<APIKeyResp>[] = [
    { key: 'name', title: t('api_keys.title') },
    {
      key: 'key_prefix',
      title: t('api_keys.key_prefix'),
      render: (row) => <span className="font-mono text-text-secondary text-xs">{row.key_prefix}</span>,
    },
    {
      key: 'quota_usd',
      title: t('api_keys.quota_used'),
      render: (row) => (
        <span className="font-mono text-xs">
          ${row.used_quota.toFixed(2)} / {row.quota_usd > 0 ? `$${row.quota_usd.toFixed(2)}` : '∞'}
        </span>
      ),
    },
    {
      key: 'status',
      title: t('common.status'),
      render: (row) => <StatusBadge status={row.status} />,
    },
    {
      key: 'created_at',
      title: t('users.created_at'),
      render: (row) => (
        <span className="text-xs text-text-secondary">
          {new Date(row.created_at).toLocaleDateString('zh-CN')}
        </span>
      ),
    },
  ];

  return (
    <Modal open={open} onClose={onClose} title={`${t('users.api_keys')} - ${user.email}`} width="700px">
      {!isLoading && (!data?.list || data.list.length === 0) ? (
        <p className="text-sm text-text-tertiary text-center py-8">{t('users.no_api_keys')}</p>
      ) : (
        <Table<APIKeyResp>
          columns={columns}
          data={data?.list ?? []}
          loading={isLoading}
          rowKey={(row) => row.id}
          page={page}
          pageSize={10}
          total={data?.total ?? 0}
          onPageChange={setPage}
        />
      )}
    </Modal>
  );
}

/* ==================== 余额变更记录弹窗 ==================== */

function UserBalanceHistoryModal({
  open, user, onClose,
}: {
  open: boolean; user: UserResp; onClose: () => void;
}) {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);

  const { data, isLoading } = useQuery({
    queryKey: ['user-balance-history', user.id, page],
    queryFn: () => usersApi.balanceHistory(user.id, { page, page_size: 10 }),
    enabled: open,
  });

  const actionLabel = (action: string) => {
    switch (action) {
      case 'add': return t('users.action_add');
      case 'subtract': return t('users.action_subtract');
      case 'set': return t('users.action_set');
      default: return action;
    }
  };

  const actionVariant = (action: string): 'success' | 'warning' | 'info' => {
    switch (action) {
      case 'add': return 'success';
      case 'subtract': return 'warning';
      default: return 'info';
    }
  };

  const columns: Column<BalanceLogResp>[] = [
    {
      key: 'action',
      title: t('users.action_type'),
      width: '80px',
      render: (row) => <Badge variant={actionVariant(row.action)}>{actionLabel(row.action)}</Badge>,
    },
    {
      key: 'amount',
      title: t('users.amount'),
      render: (row) => (
        <span className={`font-mono text-xs font-semibold ${row.action === 'add' ? 'text-success' : row.action === 'subtract' ? 'text-danger' : 'text-info'}`}>
          {row.action === 'add' ? '+' : row.action === 'subtract' ? '-' : '='}{row.amount.toFixed(2)}
        </span>
      ),
    },
    {
      key: 'balance_change',
      title: `${t('users.before_balance')} → ${t('users.after_balance')}`,
      render: (row) => (
        <span className="font-mono text-xs text-text-secondary">
          ${row.before_balance.toFixed(2)} → ${row.after_balance.toFixed(2)}
        </span>
      ),
    },
    {
      key: 'remark',
      title: t('users.remark'),
      render: (row) => <span className="text-xs text-text-tertiary">{row.remark || '-'}</span>,
    },
    {
      key: 'created_at',
      title: t('users.created_at'),
      render: (row) => (
        <span className="text-xs text-text-secondary">
          {new Date(row.created_at).toLocaleString('zh-CN', {
            month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
          })}
        </span>
      ),
    },
  ];

  return (
    <Modal open={open} onClose={onClose} title={`${t('users.balance_history')} - ${user.email}`} width="750px">
      {/* 当前余额头部 */}
      <div className="rounded-md bg-surface border border-glass-border px-4 py-3 mb-4">
        <p className="text-xs text-text-tertiary uppercase tracking-wider">{t('users.current_balance')}</p>
        <p className="text-lg font-bold mt-1 font-mono">${user.balance.toFixed(2)}</p>
      </div>

      {!isLoading && (!data?.list || data.list.length === 0) ? (
        <p className="text-sm text-text-tertiary text-center py-8">{t('users.no_balance_history')}</p>
      ) : (
        <Table<BalanceLogResp>
          columns={columns}
          data={data?.list ?? []}
          loading={isLoading}
          rowKey={(row) => row.id}
          page={page}
          pageSize={10}
          total={data?.total ?? 0}
          onPageChange={setPage}
        />
      )}
    </Modal>
  );
}

/* ==================== 用户分组分配弹窗 ==================== */

function UserGroupsModal({
  open, user, onClose, onSaved,
}: {
  open: boolean; user: UserResp; onClose: () => void; onSaved: () => void;
}) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const [selectedIds, setSelectedIds] = useState<number[]>(user.allowed_group_ids ?? []);

  const { data: groupsData, isLoading: groupsLoading } = useQuery({
    queryKey: ['groups-all'],
    queryFn: () => groupsApi.list({ page: 1, page_size: 100 }),
    enabled: open,
  });

  const updateMutation = useMutation({
    mutationFn: () => usersApi.update(user.id, { allowed_group_ids: selectedIds }),
    onSuccess: () => {
      toast('success', t('users.update_success'));
      onSaved();
    },
    onError: (err: Error) => toast('error', err.message),
  });

  const allGroups = groupsData?.list ?? [];
  const exclusiveGroups = allGroups.filter((g: GroupResp) => g.is_exclusive);
  const normalGroups = allGroups.filter((g: GroupResp) => !g.is_exclusive);

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={`${t('users.groups')} - ${user.email}`}
      width="480px"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>{t('common.cancel')}</Button>
          <Button onClick={() => updateMutation.mutate()} loading={updateMutation.isPending}>{t('common.save')}</Button>
        </>
      }
    >
      {groupsLoading ? (
        <p className="text-sm text-text-tertiary text-center py-8">{t('common.loading')}</p>
      ) : allGroups.length === 0 ? (
        <p className="text-sm text-text-tertiary text-center py-8">{t('common.no_data')}</p>
      ) : (
        <div className="space-y-4 max-h-80 overflow-y-auto">
          {/* 普通分组 —— 所有用户可用 */}
          {normalGroups.length > 0 && (
            <div>
              <p className="text-xs text-text-tertiary mb-2 font-medium uppercase tracking-wider">{t('users.normal_groups')}</p>
              <div className="space-y-0.5">
                {normalGroups.map((g: GroupResp) => (
                  <div
                    key={g.id}
                    className="flex items-center gap-2.5 w-full px-3 py-2 rounded-lg text-sm"
                    style={{ color: 'var(--ag-text-secondary)' }}
                  >
                    <span
                      className="flex items-center justify-center w-4 h-4 rounded border flex-shrink-0"
                      style={{ borderColor: 'var(--ag-glass-border)', background: 'var(--ag-primary)', opacity: 0.5 }}
                    >
                      <svg className="w-3 h-3 text-white" viewBox="0 0 12 12" fill="none"><path d="M2.5 6l2.5 2.5 4.5-5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>
                    </span>
                    <span>{g.name}</span>
                    <span className="text-[10px]" style={{ color: 'var(--ag-text-tertiary)' }}>{g.platform}</span>
                    <span className="ml-auto text-[10px]" style={{ color: 'var(--ag-text-tertiary)' }}>{t('users.all_users_accessible')}</span>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* 专属分组 —— 需管理员分配 */}
          {exclusiveGroups.length > 0 && (
            <div>
              <p className="text-xs text-text-tertiary mb-2 font-medium uppercase tracking-wider">{t('users.exclusive_groups')}</p>
              <div className="space-y-0.5">
                {exclusiveGroups.map((g: GroupResp) => (
                  <button
                    key={g.id}
                    type="button"
                    onClick={() => {
                      setSelectedIds(
                        selectedIds.includes(g.id)
                          ? selectedIds.filter((v) => v !== g.id)
                          : [...selectedIds, g.id],
                      );
                    }}
                    className="flex items-center gap-2.5 w-full px-3 py-2 rounded-lg text-sm hover:bg-bg-hover transition-colors text-left cursor-pointer"
                    style={{ color: 'var(--ag-text)' }}
                  >
                    <span
                      className="flex items-center justify-center w-4 h-4 rounded border flex-shrink-0 transition-colors"
                      style={{
                        borderColor: selectedIds.includes(g.id) ? 'var(--ag-primary)' : 'var(--ag-glass-border)',
                        background: selectedIds.includes(g.id) ? 'var(--ag-primary)' : 'transparent',
                      }}
                    >
                      {selectedIds.includes(g.id) && (
                        <svg className="w-3 h-3 text-white" viewBox="0 0 12 12" fill="none"><path d="M2.5 6l2.5 2.5 4.5-5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>
                      )}
                    </span>
                    <span>{g.name}</span>
                    <span className="text-[10px]" style={{ color: 'var(--ag-text-tertiary)' }}>{g.platform}</span>
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </Modal>
  );
}
