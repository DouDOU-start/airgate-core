import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apikeysApi } from '../../shared/api/apikeys';
import { groupsApi } from '../../shared/api/groups';
import { useToast } from '../../shared/components/Toast';
import { PageHeader } from '../../shared/components/PageHeader';
import { Table, type Column } from '../../shared/components/Table';
import { Button } from '../../shared/components/Button';
import { Input, Select } from '../../shared/components/Input';
import { Modal, ConfirmModal } from '../../shared/components/Modal';
import { StatusBadge } from '../../shared/components/Badge';
import {
  Plus,
  Pencil,
  Trash2,
  Key,
  Copy,
  AlertTriangle,
  Eye,
} from 'lucide-react';
import type { APIKeyResp, CreateAPIKeyReq, UpdateAPIKeyReq } from '../../shared/types';

interface KeyForm {
  name: string;
  group_id: string;
  quota_usd: string;
  expires_at: string;
}

const emptyForm: KeyForm = {
  name: '',
  group_id: '',
  quota_usd: '',
  expires_at: '',
};

export default function UserKeysPage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();

  const [page, setPage] = useState(1);
  const [modalOpen, setModalOpen] = useState(false);
  const [editingKey, setEditingKey] = useState<APIKeyResp | null>(null);
  const [form, setForm] = useState<KeyForm>(emptyForm);
  const [deleteTarget, setDeleteTarget] = useState<APIKeyResp | null>(null);

  // 显示新创建密钥的弹窗
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [revealedKey, setRevealedKey] = useState<string | null>(null);

  // 密钥列表
  const { data, isLoading } = useQuery({
    queryKey: ['user-keys', page],
    queryFn: () => apikeysApi.list({ page, page_size: 20 }),
  });

  // 分组列表（用于选择）
  const { data: groupsData } = useQuery({
    queryKey: ['groups-for-keys'],
    queryFn: () => groupsApi.list({ page: 1, page_size: 100 }),
  });

  // 创建密钥
  const createMutation = useMutation({
    mutationFn: (data: CreateAPIKeyReq) => apikeysApi.create(data),
    onSuccess: (result) => {
      toast('success', t('user_keys.create_success'));
      queryClient.invalidateQueries({ queryKey: ['user-keys'] });
      closeModal();
      // 显示完整密钥
      if (result.key) {
        setCreatedKey(result.key);
      }
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 更新密钥
  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: number; data: UpdateAPIKeyReq }) =>
      apikeysApi.update(id, data),
    onSuccess: () => {
      toast('success', t('user_keys.update_success'));
      queryClient.invalidateQueries({ queryKey: ['user-keys'] });
      closeModal();
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 删除密钥
  const deleteMutation = useMutation({
    mutationFn: (id: number) => apikeysApi.delete(id),
    onSuccess: () => {
      toast('success', t('user_keys.delete_success'));
      queryClient.invalidateQueries({ queryKey: ['user-keys'] });
      setDeleteTarget(null);
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

  function openCreate() {
    setEditingKey(null);
    setForm(emptyForm);
    setModalOpen(true);
  }

  function openEdit(key: APIKeyResp) {
    setEditingKey(key);
    setForm({
      name: key.name,
      group_id: String(key.group_id),
      quota_usd: key.quota_usd ? String(key.quota_usd) : '',
      expires_at: key.expires_at ? key.expires_at.slice(0, 10) : '',
    });
    setModalOpen(true);
  }

  function closeModal() {
    setModalOpen(false);
    setEditingKey(null);
    setForm(emptyForm);
  }

  function handleSubmit() {
    if (!form.name) {
      toast('error', t('user_keys.name_placeholder'));
      return;
    }
    if (!editingKey && !form.group_id) {
      toast('error', t('user_keys.select_group'));
      return;
    }

    if (editingKey) {
      const payload: UpdateAPIKeyReq = {
        name: form.name,
        group_id: form.group_id ? Number(form.group_id) : undefined,
        quota_usd: form.quota_usd ? Number(form.quota_usd) : undefined,
        expires_at: form.expires_at || undefined,
      };
      updateMutation.mutate({ id: editingKey.id, data: payload });
    } else {
      const payload: CreateAPIKeyReq = {
        name: form.name,
        group_id: Number(form.group_id),
        quota_usd: form.quota_usd ? Number(form.quota_usd) : undefined,
        expires_at: form.expires_at || undefined,
      };
      createMutation.mutate(payload);
    }
  }

  // 查找分组名称
  const groupMap = new Map(
    (groupsData?.list ?? []).map((g) => [g.id, g.name]),
  );

  // 分组选项
  const groupOptions = [
    { value: '', label: t('user_keys.select_group') },
    ...(groupsData?.list ?? []).map((g) => ({
      value: String(g.id),
      label: `${g.name} (${g.platform})`,
    })),
  ];

  const columns: Column<APIKeyResp>[] = [
    { key: 'name', title: t('common.name') },
    {
      key: 'key_prefix',
      title: t('user_keys.title'),
      render: (row) => (
        <span
          className="inline-flex items-center gap-1.5 text-xs px-2 py-0.5 rounded-sm border border-glass-border bg-surface text-text-secondary font-mono"
        >
          <Key className="w-3 h-3 text-text-tertiary" />
          {row.key_prefix}...
        </span>
      ),
    },
    {
      key: 'group_id',
      title: t('user_keys.group'),
      render: (row) => groupMap.get(row.group_id) || `#${row.group_id}`,
    },
    {
      key: 'quota',
      title: t('user_keys.quota_label'),
      render: (row) => (
        <span className="font-mono">
          {row.quota_usd > 0 ? (
            <>
              ${row.used_quota.toFixed(4)} / ${row.quota_usd.toFixed(4)}
            </>
          ) : (
            <span className="text-text-tertiary">{t('user_keys.quota_unlimited_hint')}</span>
          )}
        </span>
      ),
    },
    {
      key: 'expires_at',
      title: t('user_keys.expire_hint'),
      render: (row) =>
        row.expires_at
          ? new Date(row.expires_at).toLocaleDateString('zh-CN')
          : t('user_keys.expire_hint'),
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
            onClick={() => revealMutation.mutate(row.id)}
            icon={<Eye className="w-3.5 h-3.5" />}
            loading={revealMutation.isPending}
          >
            {t('api_keys.reveal')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => openEdit(row)}
            icon={<Pencil className="w-3.5 h-3.5" />}
          >
            {t('common.edit')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => setDeleteTarget(row)}
            icon={<Trash2 className="w-3.5 h-3.5" />}
            className="text-danger hover:text-danger"
          >
            {t('common.delete')}
          </Button>
        </div>
      ),
    },
  ];

  const saving = createMutation.isPending || updateMutation.isPending;

  return (
    <div className="p-6">
      <PageHeader
        title={t('user_keys.title')}
        actions={
          <Button onClick={openCreate} icon={<Plus className="w-4 h-4" />}>
            {t('user_keys.create')}
          </Button>
        }
      />

      <Table
        columns={columns}
        data={data?.list ?? []}
        loading={isLoading}
        rowKey={(row) => row.id as number}
        page={page}
        pageSize={20}
        total={data?.total ?? 0}
        onPageChange={setPage}
      />

      {/* 创建/编辑弹窗 */}
      <Modal
        open={modalOpen}
        onClose={closeModal}
        title={editingKey ? t('user_keys.edit') : t('user_keys.create')}
        footer={
          <>
            <Button variant="secondary" onClick={closeModal}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleSubmit} loading={saving}>
              {editingKey ? t('common.save') : t('common.create')}
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
            placeholder={t('user_keys.name_placeholder')}
          />
          <Select
            label={t('user_keys.group')}
            required
            value={form.group_id}
            onChange={(e) => setForm({ ...form, group_id: e.target.value })}
            options={groupOptions}
          />
          <Input
            label={t('user_keys.quota_label')}
            type="number"
            value={form.quota_usd}
            onChange={(e) => setForm({ ...form, quota_usd: e.target.value })}
            placeholder={t('user_keys.quota_unlimited_hint')}
            hint={t('user_keys.quota_hint')}
          />
          <Input
            label={t('user_keys.expire_hint')}
            type="date"
            value={form.expires_at}
            onChange={(e) => setForm({ ...form, expires_at: e.target.value })}
            hint={t('user_keys.expire_hint')}
          />
        </div>
      </Modal>

      {/* 新建密钥后显示完整密钥 */}
      <Modal
        open={!!createdKey}
        onClose={() => setCreatedKey(null)}
        title={t('user_keys.create_success')}
        footer={
          <Button onClick={() => setCreatedKey(null)}>{t('user_keys.key_created_warning')}</Button>
        }
      >
        <div className="space-y-4">
          <div className="flex items-start gap-2.5 rounded-md bg-danger-subtle border border-danger border-opacity-20 px-4 py-3">
            <AlertTriangle className="w-4 h-4 text-danger mt-0.5 shrink-0" />
            <p className="text-sm text-danger font-medium">
              {t('user_keys.key_created_warning')}
            </p>
          </div>
          <div
            className="rounded-md border border-glass-border bg-surface p-3 break-all text-sm text-text font-mono"
          >
            {createdKey}
          </div>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              navigator.clipboard.writeText(createdKey || '');
              toast('success', t('user_keys.copy_key'));
            }}
            icon={<Copy className="w-3.5 h-3.5" />}
          >
            {t('user_keys.copy_key')}
          </Button>
        </div>
      </Modal>

      {/* 查看密钥弹窗 */}
      <Modal
        open={!!revealedKey}
        onClose={() => setRevealedKey(null)}
        title={t('api_keys.reveal')}
        footer={
          <Button onClick={() => setRevealedKey(null)}>{t('common.close')}</Button>
        }
      >
        <div className="space-y-4">
          <div
            className="rounded-[var(--ag-radius-md)] border border-[var(--ag-glass-border)] bg-[var(--ag-bg-surface)] p-3 break-all text-sm text-[var(--ag-text)]"
            style={{ fontFamily: 'var(--ag-font-mono)' }}
          >
            {revealedKey}
          </div>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              navigator.clipboard.writeText(revealedKey || '');
              toast('success', t('user_keys.copy_key'));
            }}
            icon={<Copy className="w-3.5 h-3.5" />}
          >
            {t('user_keys.copy_key')}
          </Button>
        </div>
      </Modal>

      {/* 删除确认 */}
      <ConfirmModal
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        title={t('user_keys.delete_key')}
        message={t('user_keys.delete_confirm', { name: deleteTarget?.name })}
        loading={deleteMutation.isPending}
        danger
      />
    </div>
  );
}
