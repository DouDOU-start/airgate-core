import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query';
import { proxiesApi } from '../../shared/api/proxies';
import { useToast } from '../../shared/ui';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { usePagination } from '../../shared/hooks/usePagination';
import { AlertDialog, Button, Chip, EmptyState, Form, Input, Label, ListBox, Modal, Select, Spinner, Table as HeroTable, TextField as HeroTextField, useOverlayState } from '@heroui/react';
import { StatusChip } from '../../shared/ui';
import { Plus, Pencil, Trash2, Zap, RefreshCw } from 'lucide-react';
import type { ProxyResp, CreateProxyReq, UpdateProxyReq } from '../../shared/types';
import { getTotalPages } from '../../shared/utils/pagination';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';

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
  const { data, isLoading, refetch } = useQuery({
    queryKey: queryKeys.proxies(page, pageSize),
    queryFn: () => proxiesApi.list({ page, page_size: pageSize }),
    placeholderData: keepPreviousData,
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
  function handleSubmit(event?: FormEvent<HTMLFormElement>) {
    event?.preventDefault();
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

  const saving = createMutation.isPending || updateMutation.isPending;
  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);
  const protocolOptions = [
    { id: 'http', label: 'HTTP' },
    { id: 'socks5', label: 'SOCKS5' },
  ];
  const selectedProtocolLabel = protocolOptions.find((item) => item.id === form.protocol)?.label ?? 'HTTP';
  const proxyDialogState = useOverlayState({
    isOpen: modalOpen,
    onOpenChange: (open) => {
      if (!open) closeModal();
    },
  });

  return (
    <div>
      <div className="flex justify-end mb-5">
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
          <Button variant="primary" onPress={openCreate}>
            <Plus className="w-4 h-4" />
            {t('proxies.create')}
          </Button>
        </div>
      </div>

      <HeroTable variant="primary">
        <HeroTable.ScrollContainer>
          <HeroTable.Content aria-label={t('proxies.title', 'Proxies')}>
            <HeroTable.Header>
              <HeroTable.Column id="id" style={{ width: 72 }}>
                {t('common.id')}
              </HeroTable.Column>
              <HeroTable.Column id="name">{t('common.name')}</HeroTable.Column>
              <HeroTable.Column id="protocol">{t('proxies.protocol')}</HeroTable.Column>
              <HeroTable.Column id="endpoint">{t('proxies.address')}</HeroTable.Column>
              <HeroTable.Column id="username">{t('proxies.username')}</HeroTable.Column>
              <HeroTable.Column id="status">{t('common.status')}</HeroTable.Column>
              <HeroTable.Column id="actions">{t('common.actions')}</HeroTable.Column>
            </HeroTable.Header>
            <HeroTable.Body>
              {isLoading ? (
                <TableLoadingRow colSpan={7} />
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
                      <span className="font-mono text-text-tertiary">{row.id}</span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="text-text">{row.name}</span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <Chip color={row.protocol === 'http' ? 'accent' : 'warning'} size="sm" variant="soft">
                        {row.protocol.toUpperCase()}
                      </Chip>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="font-mono">
                        {row.address}:{row.port}
                      </span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <span className="text-text-secondary">{row.username || '-'}</span>
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <StatusChip status={row.status} />
                    </HeroTable.Cell>
                    <HeroTable.Cell>
                      <div className="flex gap-1">
                        <Button
                          size="sm"
                          variant="ghost"
                          onPress={() => openEdit(row)}
                        >
                          <Pencil className="w-3.5 h-3.5" />
                          {t('common.edit')}
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          isDisabled={testingId === row.id}
                          onPress={() => handleTest(row.id)}
                        >
                          {testingId === row.id ? <Spinner size="sm" /> : <Zap className="w-3.5 h-3.5" />}
                          {t('common.test')}
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          className="text-danger"
                          onPress={() => setDeleteTarget(row)}
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                          {t('common.delete')}
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

      {/* 创建/编辑弹窗 */}
      <Modal state={proxyDialogState}>
        <Modal.Backdrop>
          <Modal.Container placement="center" scroll="inside" size="md">
            <Modal.Dialog className="ag-elevation-modal">
              <Modal.Header>
                <Modal.Heading>{editingProxy ? t('proxies.edit') : t('proxies.create')}</Modal.Heading>
                <Modal.CloseTrigger />
              </Modal.Header>
              <Modal.Body>
                <Form id="proxy-form" className="space-y-4" onSubmit={handleSubmit}>
                  <HeroTextField fullWidth isRequired>
                    <Label>{t('common.name')}</Label>
                    <Input
                      name="name"
                      autoComplete="off"
                      value={form.name}
                      onChange={(e) => setForm({ ...form, name: e.target.value })}
                      placeholder={t('proxies.name_placeholder')}
                      required
                    />
                  </HeroTextField>
                  <Select
                    fullWidth
                    isRequired
                    selectedKey={form.protocol}
                    onSelectionChange={(key) =>
                      setForm({
                        ...form,
                        protocol: (key ?? 'http') as 'http' | 'socks5',
                      })
                    }
                  >
                    <Label>{t('proxies.protocol')}</Label>
                    <Select.Trigger>
                      <Select.Value>{selectedProtocolLabel}</Select.Value>
                      <Select.Indicator />
                    </Select.Trigger>
                    <Select.Popover>
                      <ListBox items={protocolOptions}>
                        {(item) => (
                          <ListBox.Item id={item.id} textValue={item.label}>
                            {item.label}
                          </ListBox.Item>
                        )}
                      </ListBox>
                    </Select.Popover>
                  </Select>
                  <HeroTextField fullWidth isRequired>
                    <Label>{t('proxies.address')}</Label>
                    <Input
                      value={form.address}
                      onChange={(e) => setForm({ ...form, address: e.target.value })}
                      placeholder={t('proxies.address_placeholder')}
                      required
                    />
                  </HeroTextField>
                  <HeroTextField fullWidth isRequired>
                    <Label>{t('proxies.port')}</Label>
                    <Input
                      type="number"
                      value={form.port}
                      onChange={(e) => setForm({ ...form, port: e.target.value })}
                      placeholder={t('proxies.port_placeholder')}
                      required
                    />
                  </HeroTextField>
                  <HeroTextField fullWidth>
                    <Label>{t('proxies.username')}</Label>
                    <Input
                      name="username"
                      autoComplete="username"
                      value={form.username}
                      onChange={(e) => setForm({ ...form, username: e.target.value })}
                    />
                  </HeroTextField>
                  <HeroTextField fullWidth>
                    <Label>{t('proxies.password_label')}</Label>
                    <Input
                      name="password"
                      type="password"
                      value={form.password}
                      onChange={(e) => setForm({ ...form, password: e.target.value })}
                      placeholder={editingProxy ? t('proxies.password_hint') : ''}
                      autoComplete="off"
                    />
                  </HeroTextField>
                </Form>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="secondary" onPress={closeModal}>
                  {t('common.cancel')}
                </Button>
                <Button variant="primary" isDisabled={saving} onPress={() => handleSubmit()}>
                  {saving ? <Spinner size="sm" /> : null}
                  {editingProxy ? t('common.save') : t('common.create')}
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      {/* 删除确认 */}
      <AlertDialog
        isOpen={!!deleteTarget}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
      >
        <AlertDialog.Backdrop>
          <AlertDialog.Container placement="center" size="sm">
            <AlertDialog.Dialog className="ag-elevation-modal">
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t('proxies.delete_proxy')}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>{t('proxies.delete_confirm', { name: deleteTarget?.name })}</AlertDialog.Body>
              <AlertDialog.Footer>
                <Button variant="secondary" onPress={() => setDeleteTarget(null)}>
                  {t('common.cancel')}
                </Button>
                <Button
                  aria-busy={deleteMutation.isPending}
                  isDisabled={deleteMutation.isPending}
                  variant="danger"
                  onPress={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
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
