import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  Plus,
  Pencil,
  Trash2,
  Key,
  Copy,
  AlertTriangle,
  Shield,
  Layers,
  Eye,
} from 'lucide-react';
import { PageHeader } from '../../shared/components/PageHeader';
import { Button } from '../../shared/components/Button';
import { Input, Select } from '../../shared/components/Input';
import { Table, type Column } from '../../shared/components/Table';
import { Modal, ConfirmModal } from '../../shared/components/Modal';
import { StatusBadge } from '../../shared/components/Badge';
import { useToast } from '../../shared/components/Toast';
import { apikeysApi } from '../../shared/api/apikeys';
import { groupsApi } from '../../shared/api/groups';
import type { APIKeyResp, CreateAPIKeyReq, UpdateAPIKeyReq, GroupResp } from '../../shared/types';

const PAGE_SIZE = 20;

export default function APIKeysPage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();

  // 状态
  const [page, setPage] = useState(1);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingKey, setEditingKey] = useState<APIKeyResp | null>(null);
  const [deletingKey, setDeletingKey] = useState<APIKeyResp | null>(null);
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [revealedKey, setRevealedKey] = useState<string | null>(null);

  // 查询密钥列表
  const { data, isLoading } = useQuery({
    queryKey: ['apikeys', page],
    queryFn: () => apikeysApi.list({ page, page_size: PAGE_SIZE }),
  });

  // 查询分组列表（用于创建密钥时选择分组）
  const { data: groupsData } = useQuery({
    queryKey: ['groups-all'],
    queryFn: () => groupsApi.list({ page: 1, page_size: 100 }),
  });

  // 创建密钥
  const createMutation = useMutation({
    mutationFn: (data: CreateAPIKeyReq) => apikeysApi.create(data),
    onSuccess: (resp) => {
      toast('success', t('api_keys.create_success'));
      setShowCreateModal(false);
      // 显示完整密钥
      if (resp.key) {
        setCreatedKey(resp.key);
      }
      queryClient.invalidateQueries({ queryKey: ['apikeys'] });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 更新密钥（管理员接口，支持修改分组）
  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: number; data: UpdateAPIKeyReq }) =>
      apikeysApi.adminUpdate(id, data),
    onSuccess: () => {
      toast('success', t('api_keys.update_success'));
      setEditingKey(null);
      queryClient.invalidateQueries({ queryKey: ['apikeys'] });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 删除密钥
  const deleteMutation = useMutation({
    mutationFn: (id: number) => apikeysApi.delete(id),
    onSuccess: () => {
      toast('success', t('api_keys.delete_success'));
      setDeletingKey(null);
      queryClient.invalidateQueries({ queryKey: ['apikeys'] });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 查看密钥
  const revealMutation = useMutation({
    mutationFn: (id: number) => apikeysApi.reveal(id),
    onSuccess: (resp) => {
      if (resp.key) {
        setRevealedKey(resp.key);
      }
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 格式化过期时间
  const formatExpiry = (date?: string) => {
    if (!date) return t('common.never_expire');
    const d = new Date(date);
    return d.toLocaleDateString('zh-CN');
  };

  // 表格列定义
  const columns: Column<APIKeyResp>[] = [
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
      key: 'name',
      title: t('common.name'),
      render: (row) => (
        <span className="inline-flex items-center gap-1.5">
          <Key className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />
          <span style={{ color: 'var(--ag-text)' }} className="font-medium">
            {row.name}
          </span>
        </span>
      ),
    },
    {
      key: 'key_prefix',
      title: t('api_keys.key_prefix'),
      render: (row) => (
        <code
          className="text-xs px-2 py-0.5 rounded"
          style={{
            fontFamily: 'var(--ag-font-mono)',
            background: 'var(--ag-bg-surface)',
            color: 'var(--ag-text-secondary)',
            border: '1px solid var(--ag-border-subtle)',
          }}
        >
          {row.key_prefix}...
        </code>
      ),
    },
    {
      key: 'group_id',
      title: t('api_keys.group'),
      render: (row) => {
        const group = groupsData?.list?.find(
          (g: GroupResp) => g.id === row.group_id,
        );
        return (
          <span className="inline-flex items-center gap-1.5">
            <Layers className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />
            {group ? group.name : `#${row.group_id}`}
          </span>
        );
      },
    },
    {
      key: 'quota',
      title: t('api_keys.quota_used'),
      render: (row) => (
        <span className="font-mono">
          <span style={{ color: 'var(--ag-primary)' }}>
            ${row.used_quota.toFixed(2)}
          </span>
          <span style={{ color: 'var(--ag-text-tertiary)' }}> / </span>
          <span>
            {row.quota_usd > 0 ? `$${row.quota_usd.toFixed(2)}` : t('common.unlimited')}
          </span>
        </span>
      ),
    },
    {
      key: 'expires_at',
      title: t('api_keys.expire_time'),
      render: (row) => (
        <span className="font-mono">
          {formatExpiry(row.expires_at)}
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
        <div className="flex gap-1">
          <Button
            size="sm"
            variant="ghost"
            icon={<Eye className="w-3.5 h-3.5" />}
            onClick={() => revealMutation.mutate(row.id)}
            loading={revealMutation.isPending}
          >
            {t('api_keys.reveal')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            icon={<Pencil className="w-3.5 h-3.5" />}
            onClick={() => setEditingKey(row)}
          >
            {t('common.edit')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            icon={<Trash2 className="w-3.5 h-3.5" />}
            style={{ color: 'var(--ag-danger)' }}
            onClick={() => setDeletingKey(row)}
          >
            {t('common.delete')}
          </Button>
        </div>
      ),
    },
  ];

  return (
    <div>
      <PageHeader
        title={t('api_keys.title')}
        description={t('api_keys.description')}
        actions={
          <Button icon={<Plus className="w-4 h-4" />} onClick={() => setShowCreateModal(true)}>
            {t('api_keys.create')}
          </Button>
        }
      />

      {/* 表格 */}
      <Table<APIKeyResp>
        columns={columns}
        data={data?.list ?? []}
        loading={isLoading}
        rowKey={(row) => row.id}
        page={page}
        pageSize={PAGE_SIZE}
        total={data?.total ?? 0}
        onPageChange={setPage}
      />

      {/* 创建弹窗 */}
      <CreateKeyModal
        open={showCreateModal}
        groups={groupsData?.list ?? []}
        onClose={() => setShowCreateModal(false)}
        onSubmit={(data) => createMutation.mutate(data)}
        loading={createMutation.isPending}
      />

      {/* 密钥展示弹窗（创建后） */}
      <KeyRevealModal
        open={!!createdKey}
        keyValue={createdKey ?? ''}
        onClose={() => setCreatedKey(null)}
      />

      {/* 密钥展示弹窗（查看） */}
      <KeyRevealModal
        open={!!revealedKey}
        keyValue={revealedKey ?? ''}
        onClose={() => setRevealedKey(null)}
      />

      {/* 编辑弹窗 */}
      {editingKey && (
        <EditKeyModal
          open
          apiKey={editingKey}
          groups={groupsData?.list ?? []}
          onClose={() => setEditingKey(null)}
          onSubmit={(data) =>
            updateMutation.mutate({ id: editingKey.id, data })
          }
          loading={updateMutation.isPending}
        />
      )}

      {/* 删除确认 */}
      <ConfirmModal
        open={!!deletingKey}
        onClose={() => setDeletingKey(null)}
        onConfirm={() => deletingKey && deleteMutation.mutate(deletingKey.id)}
        title={t('api_keys.delete_key')}
        message={t('api_keys.delete_key_confirm', { name: deletingKey?.name })}
        loading={deleteMutation.isPending}
        danger
      />
    </div>
  );
}

// ==================== 创建密钥弹窗 ====================

function CreateKeyModal({
  open,
  groups,
  onClose,
  onSubmit,
  loading,
}: {
  open: boolean;
  groups: GroupResp[];
  onClose: () => void;
  onSubmit: (data: CreateAPIKeyReq) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const [form, setForm] = useState<CreateAPIKeyReq>({
    name: '',
    group_id: 0,
    quota_usd: 0,
    expires_at: '',
  });
  const [ipWhitelist, setIpWhitelist] = useState('');
  const [ipBlacklist, setIpBlacklist] = useState('');

  const handleSubmit = () => {
    if (!form.name || !form.group_id) return;
    const whitelist = ipWhitelist.trim()
      ? ipWhitelist.split('\n').map((s) => s.trim()).filter(Boolean)
      : undefined;
    const blacklist = ipBlacklist.trim()
      ? ipBlacklist.split('\n').map((s) => s.trim()).filter(Boolean)
      : undefined;
    onSubmit({
      ...form,
      quota_usd: form.quota_usd || undefined,
      expires_at: form.expires_at || undefined,
      ip_whitelist: whitelist,
      ip_blacklist: blacklist,
    });
  };

  const handleClose = () => {
    setForm({ name: '', group_id: 0, quota_usd: 0, expires_at: '' });
    setIpWhitelist('');
    setIpBlacklist('');
    onClose();
  };

  const groupOptions = [
    { value: '0', label: t('api_keys.select_group') },
    ...groups.map((g) => ({ value: String(g.id), label: `${g.name} (${g.platform})` })),
  ];

  return (
    <Modal
      open={open}
      onClose={handleClose}
      title={t('api_keys.create')}
      width="560px"
      footer={
        <>
          <Button variant="secondary" onClick={handleClose}>
            {t('common.cancel')}
          </Button>
          <Button onClick={handleSubmit} loading={loading}>
            {t('common.create')}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Input
          label={t('common.name')}
          required
          value={form.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
          placeholder={t('api_keys.name_placeholder')}
          icon={<Key className="w-4 h-4" />}
        />

        <Select
          label={t('api_keys.group')}
          required
          value={String(form.group_id)}
          onChange={(e) =>
            setForm({ ...form, group_id: Number(e.target.value) })
          }
          options={groupOptions}
        />

        <Input
          label={t('api_keys.quota_label')}
          type="number"
          step="0.01"
          min="0"
          value={String(form.quota_usd ?? 0)}
          onChange={(e) =>
            setForm({ ...form, quota_usd: Number(e.target.value) })
          }
          hint={t('api_keys.quota_hint')}
        />

        <Input
          label={t('api_keys.expire_time')}
          type="date"
          value={form.expires_at ? form.expires_at.split('T')[0] : ''}
          onChange={(e) =>
            setForm({
              ...form,
              expires_at: e.target.value
                ? `${e.target.value}T23:59:59Z`
                : '',
            })
          }
          hint={t('api_keys.expire_hint')}
        />

        {/* IP 白名单 */}
        <div className="space-y-1.5">
          <label
            className="block text-xs font-medium uppercase tracking-wider"
            style={{ color: 'var(--ag-text-secondary)' }}
          >
            {t('api_keys.ip_whitelist')}
          </label>
          <textarea
            className="block w-full rounded-md border px-3 py-2 text-sm transition-all duration-200 focus:outline-none min-h-[60px] resize-y"
            style={{
              borderColor: 'var(--ag-glass-border)',
              background: 'var(--ag-bg-surface)',
              color: 'var(--ag-text)',
              fontFamily: 'var(--ag-font-mono)',
            }}
            placeholder={t('api_keys.ip_placeholder')}
            value={ipWhitelist}
            onChange={(e) => setIpWhitelist(e.target.value)}
            rows={2}
          />
          <p className="text-xs" style={{ color: 'var(--ag-text-tertiary)' }}>
            {t('api_keys.ip_hint')}
          </p>
        </div>

        {/* IP 黑名单 */}
        <div className="space-y-1.5">
          <label
            className="block text-xs font-medium uppercase tracking-wider"
            style={{ color: 'var(--ag-text-secondary)' }}
          >
            {t('api_keys.ip_blacklist')}
          </label>
          <textarea
            className="block w-full rounded-md border px-3 py-2 text-sm transition-all duration-200 focus:outline-none min-h-[60px] resize-y"
            style={{
              borderColor: 'var(--ag-glass-border)',
              background: 'var(--ag-bg-surface)',
              color: 'var(--ag-text)',
              fontFamily: 'var(--ag-font-mono)',
            }}
            placeholder={t('api_keys.ip_placeholder')}
            value={ipBlacklist}
            onChange={(e) => setIpBlacklist(e.target.value)}
            rows={2}
          />
          <p className="text-xs" style={{ color: 'var(--ag-text-tertiary)' }}>
            {t('api_keys.ip_hint')}
          </p>
        </div>
      </div>
    </Modal>
  );
}

// ==================== 密钥展示弹窗 ====================

function KeyRevealModal({
  open,
  keyValue,
  onClose,
}: {
  open: boolean;
  keyValue: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const { toast } = useToast();

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(keyValue);
      toast('success', t('common.copied'));
    } catch {
      toast('error', t('common.copy_failed'));
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t('api_keys.key_created')}
      footer={
        <Button onClick={onClose}>{t('api_keys.key_saved_close')}</Button>
      }
    >
      <div className="space-y-4">
        <div
          className="rounded-md p-4 flex items-start gap-3"
          style={{
            background: 'var(--ag-warning-subtle)',
            border: '1px solid var(--ag-warning)',
          }}
        >
          <AlertTriangle className="w-4 h-4 flex-shrink-0 mt-0.5" style={{ color: 'var(--ag-warning)' }} />
          <p className="text-sm font-medium" style={{ color: 'var(--ag-warning)' }}>
            {t('api_keys.key_created_warning')}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <code
            className="flex-1 px-3 py-2 rounded-md text-sm break-all"
            style={{
              fontFamily: 'var(--ag-font-mono)',
              background: 'var(--ag-bg-surface)',
              color: 'var(--ag-text)',
              border: '1px solid var(--ag-glass-border)',
            }}
          >
            {keyValue}
          </code>
          <Button
            size="sm"
            variant="secondary"
            icon={<Copy className="w-3.5 h-3.5" />}
            onClick={handleCopy}
          >
            {t('common.copy')}
          </Button>
        </div>
      </div>
    </Modal>
  );
}

// ==================== 编辑密钥弹窗 ====================

function EditKeyModal({
  open,
  apiKey,
  groups,
  onClose,
  onSubmit,
  loading,
}: {
  open: boolean;
  apiKey: APIKeyResp;
  groups: GroupResp[];
  onClose: () => void;
  onSubmit: (data: UpdateAPIKeyReq) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const [groupId, setGroupId] = useState(apiKey.group_id);
  const [form, setForm] = useState<UpdateAPIKeyReq>({
    name: apiKey.name,
    quota_usd: apiKey.quota_usd,
    expires_at: apiKey.expires_at,
    status: apiKey.status as 'active' | 'disabled',
  });
  const [ipWhitelist, setIpWhitelist] = useState(
    apiKey.ip_whitelist?.join('\n') ?? '',
  );
  const [ipBlacklist, setIpBlacklist] = useState(
    apiKey.ip_blacklist?.join('\n') ?? '',
  );

  const handleSubmit = () => {
    const whitelist = ipWhitelist.trim()
      ? ipWhitelist.split('\n').map((s) => s.trim()).filter(Boolean)
      : undefined;
    const blacklist = ipBlacklist.trim()
      ? ipBlacklist.split('\n').map((s) => s.trim()).filter(Boolean)
      : undefined;
    onSubmit({
      ...form,
      group_id: groupId !== apiKey.group_id ? groupId : undefined,
      ip_whitelist: whitelist,
      ip_blacklist: blacklist,
    });
  };

  const groupOptions = groups.map((g) => ({
    value: String(g.id),
    label: `${g.name} (${g.platform})`,
  }));

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t('api_keys.edit')}
      width="560px"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button onClick={handleSubmit} loading={loading}>
            {t('common.save')}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Input
          label={t('common.name')}
          value={form.name ?? ''}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
          icon={<Key className="w-4 h-4" />}
        />

        <Select
          label={t('api_keys.group')}
          value={String(groupId)}
          onChange={(e) => setGroupId(Number(e.target.value))}
          options={groupOptions}
        />

        <Input
          label={t('api_keys.quota_label')}
          type="number"
          step="0.01"
          min="0"
          value={String(form.quota_usd ?? 0)}
          onChange={(e) =>
            setForm({ ...form, quota_usd: Number(e.target.value) })
          }
          hint={t('api_keys.quota_hint')}
        />

        <Input
          label={t('api_keys.expire_time')}
          type="date"
          value={
            form.expires_at ? form.expires_at.split('T')[0] : ''
          }
          onChange={(e) =>
            setForm({
              ...form,
              expires_at: e.target.value
                ? `${e.target.value}T23:59:59Z`
                : undefined,
            })
          }
          hint={t('api_keys.expire_hint')}
        />

        <Select
          label={t('common.status')}
          value={form.status ?? 'active'}
          onChange={(e) =>
            setForm({
              ...form,
              status: e.target.value as 'active' | 'disabled',
            })
          }
          options={[
            { value: 'active', label: t('status.active') },
            { value: 'disabled', label: t('status.disabled') },
          ]}
        />

        {/* IP 白名单 */}
        <div className="space-y-1.5">
          <label
            className="flex items-center gap-1.5 text-xs font-medium uppercase tracking-wider"
            style={{ color: 'var(--ag-text-secondary)' }}
          >
            <Shield className="w-3.5 h-3.5" />
            {t('api_keys.ip_whitelist')}
          </label>
          <textarea
            className="block w-full rounded-md border px-3 py-2 text-sm transition-all duration-200 focus:outline-none min-h-[60px] resize-y"
            style={{
              borderColor: 'var(--ag-glass-border)',
              background: 'var(--ag-bg-surface)',
              color: 'var(--ag-text)',
              fontFamily: 'var(--ag-font-mono)',
            }}
            placeholder={t('api_keys.ip_placeholder')}
            value={ipWhitelist}
            onChange={(e) => setIpWhitelist(e.target.value)}
            rows={2}
          />
        </div>

        {/* IP 黑名单 */}
        <div className="space-y-1.5">
          <label
            className="flex items-center gap-1.5 text-xs font-medium uppercase tracking-wider"
            style={{ color: 'var(--ag-text-secondary)' }}
          >
            <Shield className="w-3.5 h-3.5" />
            {t('api_keys.ip_blacklist')}
          </label>
          <textarea
            className="block w-full rounded-md border px-3 py-2 text-sm transition-all duration-200 focus:outline-none min-h-[60px] resize-y"
            style={{
              borderColor: 'var(--ag-glass-border)',
              background: 'var(--ag-bg-surface)',
              color: 'var(--ag-text)',
              fontFamily: 'var(--ag-font-mono)',
            }}
            placeholder={t('api_keys.ip_placeholder')}
            value={ipBlacklist}
            onChange={(e) => setIpBlacklist(e.target.value)}
            rows={2}
          />
        </div>
      </div>
    </Modal>
  );
}
