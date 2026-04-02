import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useMutation } from '@tanstack/react-query';
import { proxiesApi } from '../../shared/api/proxies';
import { useToast } from '../../shared/components/Toast';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { usePagination } from '../../shared/hooks/usePagination';
import { PageHeader } from '../../shared/components/PageHeader';
import { Table, type Column } from '../../shared/components/Table';
import { Button } from '../../shared/components/Button';
import { Input, Select } from '../../shared/components/Input';
import { Modal, ConfirmModal } from '../../shared/components/Modal';
import { Badge, StatusBadge } from '../../shared/components/Badge';
import { Plus, Pencil, Trash2, Zap } from 'lucide-react';
import type { ProxyResp, CreateProxyReq, UpdateProxyReq } from '../../shared/types';

// 代理表单数据
interface ProxyForm {
  name: string;
  protocol: 'http' | 'socks5';
  address: string;
  port: string;
  username: string;
  password: string;
}

const emptyForm: ProxyForm = {
  name: '',
  protocol: 'http',
  address: '',
  port: '',
  username: '',
  password: '',
};

export default function ProxiesPage() {
  const { t } = useTranslation();
  const { toast } = useToast();

  const { page, setPage, pageSize, setPageSize } = usePagination(20);
  const [modalOpen, setModalOpen] = useState(false);
  const [editingProxy, setEditingProxy] = useState<ProxyResp | null>(null);
  const [form, setForm] = useState<ProxyForm>(emptyForm);
  const [deleteTarget, setDeleteTarget] = useState<ProxyResp | null>(null);
  const [testingId, setTestingId] = useState<number | null>(null);

  // 查询代理列表
  const { data, isLoading } = useQuery({
    queryKey: queryKeys.proxies(page, pageSize),
    queryFn: () => proxiesApi.list({ page, page_size: pageSize }),
  });

  // 创建代理
  const createMutation = useCrudMutation({
    mutationFn: (data: CreateProxyReq) => proxiesApi.create(data),
    successMessage: t('proxies.create_success'),
    queryKey: queryKeys.proxies(),
    onSuccess: () => closeModal(),
  });

  // 更新代理
  const updateMutation = useCrudMutation({
    mutationFn: ({ id, data }: { id: number; data: UpdateProxyReq }) =>
      proxiesApi.update(id, data),
    successMessage: t('proxies.update_success'),
    queryKey: queryKeys.proxies(),
    onSuccess: () => closeModal(),
  });

  // 删除代理
  const deleteMutation = useCrudMutation({
    mutationFn: (id: number) => proxiesApi.delete(id),
    successMessage: t('proxies.delete_success'),
    queryKey: queryKeys.proxies(),
    onSuccess: () => setDeleteTarget(null),
  });

  // 测试连通性
  const testMutation = useMutation({
    mutationFn: (id: number) => proxiesApi.test(id),
    onSuccess: (result) => {
      if (result.success) {
        const location = [result.country, result.city].filter(Boolean).join(' · ');
        const parts = [`${result.latency_ms}ms`];
        if (result.ip_address) parts.push(result.ip_address);
        if (location) parts.push(location);
        toast('success', t('proxies.test_success', { detail: parts.join('  |  ') }));
      } else {
        toast('error', t('proxies.test_failed', { error: result.error_msg || '' }));
      }
      setTestingId(null);
    },
    onError: (err: Error) => {
      toast('error', err.message);
      setTestingId(null);
    },
  });

  // 打开创建弹窗
  function openCreate() {
    setEditingProxy(null);
    setForm(emptyForm);
    setModalOpen(true);
  }

  // 打开编辑弹窗
  function openEdit(proxy: ProxyResp) {
    setEditingProxy(proxy);
    setForm({
      name: proxy.name,
      protocol: proxy.protocol,
      address: proxy.address,
      port: String(proxy.port),
      username: proxy.username || '',
      password: '',
    });
    setModalOpen(true);
  }

  // 关闭弹窗
  function closeModal() {
    setModalOpen(false);
    setEditingProxy(null);
    setForm(emptyForm);
  }

  // 提交表单
  function handleSubmit() {
    if (!form.name || !form.address || !form.port) {
      toast('error', t('common.fill_required'));
      return;
    }

    const payload = {
      name: form.name,
      protocol: form.protocol,
      address: form.address,
      port: Number(form.port),
      username: form.username || undefined,
      password: form.password || undefined,
    };

    if (editingProxy) {
      updateMutation.mutate({ id: editingProxy.id, data: payload });
    } else {
      createMutation.mutate(payload as CreateProxyReq);
    }
  }

  // 测试连通性
  function handleTest(id: number) {
    setTestingId(id);
    testMutation.mutate(id);
  }

  const columns: Column<ProxyResp>[] = [
    {
      key: 'id',
      title: t('common.id'),
      width: '60px',
      render: (row) => (
        <span className="text-text-tertiary font-mono">
          {row.id}
        </span>
      ),
    },
    {
      key: 'name',
      title: t('common.name'),
      render: (row) => <span className="text-text">{row.name}</span>,
    },
    {
      key: 'protocol',
      title: t('proxies.protocol'),
      render: (row) => (
        <Badge variant={row.protocol === 'http' ? 'info' : 'warning'}>
          {row.protocol.toUpperCase()}
        </Badge>
      ),
    },
    {
      key: 'endpoint',
      title: t('proxies.address'),
      render: (row) => (
        <span className="font-mono">
          {row.address}:{row.port}
        </span>
      ),
    },
    {
      key: 'username',
      title: t('proxies.username'),
      render: (row) => (
        <span className="text-text-secondary">
          {row.username || '-'}
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
            icon={<Pencil className="w-3.5 h-3.5" />}
            onClick={() => openEdit(row)}
          >
            {t('common.edit')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            icon={<Zap className="w-3.5 h-3.5" />}
            loading={testingId === row.id}
            onClick={() => handleTest(row.id)}
          >
            {t('common.test')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            icon={<Trash2 className="w-3.5 h-3.5" />}
            className="text-danger"
            onClick={() => setDeleteTarget(row)}
          >
            {t('common.delete')}
          </Button>
        </div>
      ),
    },
  ];

  const saving = createMutation.isPending || updateMutation.isPending;

  return (
    <div>
      <PageHeader
        title={t('proxies.title')}
        description={t('proxies.description')}
        actions={
          <Button icon={<Plus className="w-4 h-4" />} onClick={openCreate}>
            {t('proxies.create')}
          </Button>
        }
      />

      <Table
        columns={columns}
        data={data?.list ?? []}
        loading={isLoading}
        rowKey={(row) => row.id as number}
        page={page}
        pageSize={pageSize}
        total={data?.total ?? 0}
        onPageChange={setPage}
        onPageSizeChange={setPageSize}
      />

      {/* 创建/编辑弹窗 */}
      <Modal
        open={modalOpen}
        onClose={closeModal}
        title={editingProxy ? t('proxies.edit') : t('proxies.create')}
        footer={
          <>
            <Button variant="secondary" onClick={closeModal}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleSubmit} loading={saving}>
              {editingProxy ? t('common.save') : t('common.create')}
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
            placeholder={t('proxies.name_placeholder')}
          />
          <Select
            label={t('proxies.protocol')}
            required
            value={form.protocol}
            onChange={(e) =>
              setForm({
                ...form,
                protocol: e.target.value as 'http' | 'socks5',
              })
            }
            options={[
              { value: 'http', label: 'HTTP' },
              { value: 'socks5', label: 'SOCKS5' },
            ]}
          />
          <Input
            label={t('proxies.address')}
            required
            value={form.address}
            onChange={(e) => setForm({ ...form, address: e.target.value })}
            placeholder={t('proxies.address_placeholder')}
          />
          <Input
            label={t('proxies.port')}
            required
            type="number"
            value={form.port}
            onChange={(e) => setForm({ ...form, port: e.target.value })}
            placeholder={t('proxies.port_placeholder')}
          />
          <Input
            label={t('proxies.username')}
            value={form.username}
            onChange={(e) => setForm({ ...form, username: e.target.value })}
          />
          <Input
            label={t('proxies.password_label')}
            type="password"
            value={form.password}
            onChange={(e) => setForm({ ...form, password: e.target.value })}
            placeholder={editingProxy ? t('proxies.password_hint') : ''}
          />
        </div>
      </Modal>

      {/* 删除确认 */}
      <ConfirmModal
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        title={t('proxies.delete_proxy')}
        message={t('proxies.delete_confirm', { name: deleteTarget?.name })}
        loading={deleteMutation.isPending}
        danger
      />
    </div>
  );
}
