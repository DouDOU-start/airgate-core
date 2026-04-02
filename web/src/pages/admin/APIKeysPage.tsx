import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Plus, Pencil, Trash2, Key, Layers, Eye } from 'lucide-react';
import { PageHeader } from '../../shared/components/PageHeader';
import { Button } from '../../shared/components/Button';
import { Table, type Column } from '../../shared/components/Table';
import { ConfirmModal } from '../../shared/components/Modal';
import { StatusBadge } from '../../shared/components/Badge';
import { KeyRevealModal } from '../../shared/components/KeyRevealModal';
import { apikeysApi } from '../../shared/api/apikeys';
import { groupsApi } from '../../shared/api/groups';
import { usePagination } from '../../shared/hooks/usePagination';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE, FETCH_ALL_PARAMS } from '../../shared/constants';
import { formatExpiry } from '../../shared/utils/format';
import { CreateKeyModal } from './apikeys/CreateKeyModal';
import { EditKeyModal } from './apikeys/EditKeyModal';
import type { APIKeyResp, GroupResp } from '../../shared/types';

export default function APIKeysPage() {
  const { t } = useTranslation();

  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingKey, setEditingKey] = useState<APIKeyResp | null>(null);
  const [deletingKey, setDeletingKey] = useState<APIKeyResp | null>(null);
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [revealedKey, setRevealedKey] = useState<string | null>(null);

  const { data, isLoading } = useQuery({
    queryKey: queryKeys.apikeys(page, pageSize),
    queryFn: () => apikeysApi.list({ page, page_size: pageSize }),
  });

  const { data: groupsData } = useQuery({
    queryKey: queryKeys.groupsAll(),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
  });

  const createMutation = useCrudMutation({
    mutationFn: apikeysApi.create,
    successMessage: t('api_keys.create_success'),
    queryKey: queryKeys.apikeys(),
    onSuccess: (resp) => {
      setShowCreateModal(false);
      if (resp.key) setCreatedKey(resp.key);
    },
  });

  const updateMutation = useCrudMutation({
    mutationFn: ({ id, data }: { id: number; data: Parameters<typeof apikeysApi.adminUpdate>[1] }) =>
      apikeysApi.adminUpdate(id, data),
    successMessage: t('api_keys.update_success'),
    queryKey: queryKeys.apikeys(),
    onSuccess: () => setEditingKey(null),
  });

  const deleteMutation = useCrudMutation({
    mutationFn: apikeysApi.delete,
    successMessage: t('api_keys.delete_success'),
    queryKey: queryKeys.apikeys(),
    onSuccess: () => setDeletingKey(null),
  });

  const revealMutation = useCrudMutation({
    mutationFn: apikeysApi.reveal,
    queryKey: queryKeys.apikeys(),
    onSuccess: (resp) => {
      if (resp.key) setRevealedKey(resp.key);
    },
  });

  const columns: Column<APIKeyResp>[] = [
    {
      key: 'id',
      title: t('common.id'),
      width: '60px',
      render: (row) => <span className="font-mono">{row.id}</span>,
    },
    {
      key: 'name',
      title: t('common.name'),
      render: (row) => (
        <span className="inline-flex items-center gap-1.5">
          <Key className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />
          <span style={{ color: 'var(--ag-text)' }} className="font-medium">{row.name}</span>
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
        const group = row.group_id == null
          ? null
          : groupsData?.list?.find((g: GroupResp) => g.id === row.group_id);
        return (
          <span className="inline-flex items-center gap-1.5">
            <Layers className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />
            {row.group_id == null ? t('api_keys.group_unbound') : group ? group.name : `#${row.group_id}`}
          </span>
        );
      },
    },
    {
      key: 'quota',
      title: t('api_keys.quota_used'),
      render: (row) => (
        <span className="font-mono">
          <span style={{ color: 'var(--ag-primary)' }}>${row.used_quota.toFixed(2)}</span>
          <span style={{ color: 'var(--ag-text-tertiary)' }}> / </span>
          <span>{row.quota_usd > 0 ? `$${row.quota_usd.toFixed(2)}` : t('common.unlimited')}</span>
        </span>
      ),
    },
    {
      key: 'usage',
      title: t('api_keys.usage'),
      render: (row) => (
        <div className="font-mono text-xs space-y-0.5">
          <div>
            <span style={{ color: 'var(--ag-text-tertiary)' }}>{t('api_keys.today')}: </span>
            <span style={{ color: 'var(--ag-primary)' }}>${row.today_cost.toFixed(4)}</span>
          </div>
          <div>
            <span style={{ color: 'var(--ag-text-tertiary)' }}>{t('api_keys.thirty_days')}: </span>
            <span style={{ color: 'var(--ag-text)' }}>${row.thirty_day_cost.toFixed(4)}</span>
          </div>
        </div>
      ),
    },
    {
      key: 'expires_at',
      title: t('api_keys.expire_time'),
      render: (row) => <span className="font-mono">{formatExpiry(row.expires_at)}</span>,
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

      <Table<APIKeyResp>
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

      <CreateKeyModal
        open={showCreateModal}
        groups={groupsData?.list ?? []}
        onClose={() => setShowCreateModal(false)}
        onSubmit={(data) => createMutation.mutate(data)}
        loading={createMutation.isPending}
      />

      <KeyRevealModal
        open={!!createdKey}
        keyValue={createdKey ?? ''}
        title={t('api_keys.key_created')}
        warningText={t('api_keys.key_created_warning')}
        closeText={t('api_keys.key_saved_close')}
        onClose={() => setCreatedKey(null)}
      />

      <KeyRevealModal
        open={!!revealedKey}
        keyValue={revealedKey ?? ''}
        title={t('api_keys.reveal')}
        warningText={t('api_keys.key_reveal_warning')}
        onClose={() => setRevealedKey(null)}
      />

      {editingKey && (
        <EditKeyModal
          open
          apiKey={editingKey}
          groups={groupsData?.list ?? []}
          onClose={() => setEditingKey(null)}
          onSubmit={(data) => updateMutation.mutate({ id: editingKey.id, data })}
          loading={updateMutation.isPending}
        />
      )}

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
