import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apikeysApi } from '../../shared/api/apikeys';
import { usePagination } from '../../shared/hooks/usePagination';
import { groupsApi } from '../../shared/api/groups';
import { useToast } from '../../shared/ui';
import { Alert, AlertDialog, Button, Dropdown, EmptyState, Modal, Spinner, Table as HeroTable, useOverlayState } from '@heroui/react';
import {
  StatusChip,
} from '../../shared/ui';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE, FETCH_ALL_PARAMS } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { useClipboard } from '../../shared/hooks/useClipboard';
import {
  AlertTriangle,
  Copy,
  Plus,
  Pencil,
  Trash2,
  Key,
  Eye,
  Ban,
  CheckCircle,
  Terminal,
  Upload,
  MoreHorizontal,
  RefreshCw,
} from 'lucide-react';
import type { APIKeyResp, CreateAPIKeyReq, UpdateAPIKeyReq, GroupResp } from '../../shared/types';
import { useAuth } from '../../app/providers/AuthProvider';
import { EditKeyModal } from './userkeys/EditKeyModal';
import { CreateKeyModal } from './userkeys/CreateKeyModal';
import { UseKeyModal, useUseKeyModal } from './userkeys/UseKeyModal';
import { CcsImportModal, useCcsImportModal } from './userkeys/CcsImportModal';
import { type KeyForm, emptyForm } from './userkeys/types';

export default function UserKeysPage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const copy = useClipboard();
  const queryClient = useQueryClient();
  const { user } = useAuth();

  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE);
  const [modalOpen, setModalOpen] = useState(false);
  const [editingKey, setEditingKey] = useState<APIKeyResp | null>(null);
  const [form, setForm] = useState<KeyForm>(emptyForm);
  const [deleteTarget, setDeleteTarget] = useState<APIKeyResp | null>(null);

  // 显示新创建密钥的弹窗
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [revealedKey, setRevealedKey] = useState<string | null>(null);

  // 密钥列表
  const { data, isLoading, refetch } = useQuery({
    queryKey: queryKeys.userKeys(page, pageSize),
    queryFn: () => apikeysApi.list({ page, page_size: pageSize }),
    placeholderData: keepPreviousData,
  });

  // 分组列表（用于选择）
  const { data: groupsData } = useQuery({
    queryKey: queryKeys.groupsForKeys(),
    queryFn: () => groupsApi.listAvailable(FETCH_ALL_PARAMS),
  });

  // 创建密钥
  const createMutation = useCrudMutation<{ key?: string }, CreateAPIKeyReq>({
    mutationFn: (data) => apikeysApi.create(data),
    successMessage: t('user_keys.create_success'),
    queryKey: queryKeys.userKeys(),
    onSuccess: (result) => {
      closeModal();
      // 显示完整密钥
      if (result.key) {
        setCreatedKey(result.key);
      }
    },
  });

  // 更新密钥
  const updateMutation = useCrudMutation<unknown, { id: number; data: UpdateAPIKeyReq }>({
    mutationFn: ({ id, data }) => apikeysApi.update(id, data),
    successMessage: t('user_keys.update_success'),
    queryKey: queryKeys.userKeys(),
    onSuccess: () => closeModal(),
  });

  // 删除密钥
  const deleteMutation = useCrudMutation<unknown, number>({
    mutationFn: (id) => apikeysApi.delete(id),
    successMessage: t('user_keys.delete_success'),
    queryKey: queryKeys.userKeys(),
    onSuccess: () => setDeleteTarget(null),
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

  // 禁用/启用密钥（动态成功消息，无法使用 useCrudMutation）
  const toggleStatusMutation = useMutation({
    mutationFn: ({ id, status }: { id: number; status: 'active' | 'disabled' }) =>
      apikeysApi.update(id, { status }),
    onSuccess: (_resp, variables) => {
      toast(
        'success',
        variables.status === 'active'
          ? t('user_keys.enable_success')
          : t('user_keys.disable_success'),
      );
      queryClient.invalidateQueries({ queryKey: queryKeys.userKeys() });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  function openCreate() {
    if (!hasAvailableGroups) {
      toast('error', t('user_keys.no_groups_available'));
      return;
    }
    setEditingKey(null);
    setForm(emptyForm);
    setModalOpen(true);
  }

  function openEdit(key: APIKeyResp) {
    setEditingKey(key);
    setForm({
      name: key.name,
      group_id: key.group_id == null ? '' : String(key.group_id),
      quota_usd: key.quota_usd ? String(key.quota_usd) : '',
      sell_rate: key.sell_rate ? String(key.sell_rate) : '',
      max_concurrency: key.max_concurrency ? String(key.max_concurrency) : '',
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

    // 后端要求 RFC3339 格式；空字符串表示显式清除过期时间
    const expiresAt = form.expires_at ? `${form.expires_at}T23:59:59Z` : '';

    if (editingKey) {
      const payload: UpdateAPIKeyReq = {
        name: form.name,
        group_id: form.group_id ? Number(form.group_id) : undefined,
        quota_usd: form.quota_usd ? Number(form.quota_usd) : undefined,
        sell_rate: form.sell_rate ? Number(form.sell_rate) : 0,
        // 空字符串显式改为 0 = 关闭并发限制；后端看到 0 会清除旧值
        max_concurrency: form.max_concurrency ? Number(form.max_concurrency) : 0,
        expires_at: expiresAt,
      };
      updateMutation.mutate({ id: editingKey.id, data: payload });
    } else {
      const payload: CreateAPIKeyReq = {
        name: form.name,
        group_id: Number(form.group_id),
        quota_usd: form.quota_usd ? Number(form.quota_usd) : undefined,
        sell_rate: form.sell_rate ? Number(form.sell_rate) : undefined,
        max_concurrency: form.max_concurrency ? Number(form.max_concurrency) : undefined,
        expires_at: expiresAt,
      };
      createMutation.mutate(payload);
    }
  }

  // 查找分组
  const groupList = groupsData?.list ?? [];
  const groupMap = new Map<number, GroupResp>(groupList.map((g) => [g.id, g]));

  const hasAvailableGroups = groupList.length > 0;

  // 分组选项（如果用户有专属倍率，右侧显示划线原价 + 专属倍率）
  const userGroupRates = user?.group_rates;
  const groupOptions = groupList.map((g) => {
    const override = userGroupRates?.[g.id];
    const hasOverride = override != null && override > 0 && override !== g.rate_multiplier;
    return {
      value: String(g.id),
      label: g.name,
      suffix: hasOverride ? (
        <span className="text-text-tertiary">
          <span className="line-through opacity-60">{g.rate_multiplier}x</span>{' '}
          <span className="text-primary font-medium">{override}x</span>
        </span>
      ) : (
        <span className="text-text-tertiary">{g.rate_multiplier}x {t('user_keys.rate_suffix', '倍率')}</span>
      ),
    };
  });

  // 使用配置弹窗
  const {
    useKeyTarget,
    useKeyValue,
    useKeyTab,
    setUseKeyTab,
    useKeyShell,
    setUseKeyShell,
    useKeyPlatform,
    showClientTabs,
    openUseKeyModal,
    closeUseKeyModal,
  } = useUseKeyModal(groupMap);

  // CCS 导入弹窗
  const {
    ccsTarget,
    ccsKeyValue,
    ccsPlatform,
    openCcsModal,
    closeCcsModal,
  } = useCcsImportModal(groupMap);

  const saving = createMutation.isPending || updateMutation.isPending;
  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);
  const revealedKeyModalState = useOverlayState({
    isOpen: !!revealedKey,
    onOpenChange: (open) => {
      if (!open) setRevealedKey(null);
    },
  });

  return (
    <div className="p-6">
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
          <Button
            isDisabled={!hasAvailableGroups}
            variant="primary"
            onPress={openCreate}
          >
            <Plus className="w-4 h-4" />
            {hasAvailableGroups ? t('user_keys.create') : t('user_keys.create_disabled_no_groups')}
          </Button>
        </div>
      </div>

      <HeroTable variant="primary">
        <HeroTable.ScrollContainer>
          <HeroTable.Content aria-label={t('user_keys.title', 'API keys')}>
            <HeroTable.Header>
              <HeroTable.Column id="name">{t('common.name')}</HeroTable.Column>
              <HeroTable.Column id="key_prefix">{t('user_keys.title')}</HeroTable.Column>
              <HeroTable.Column id="group_id">{t('user_keys.group')}</HeroTable.Column>
              <HeroTable.Column id="quota">{t('user_keys.quota_label')}</HeroTable.Column>
              <HeroTable.Column id="markup">{t('user_keys.markup_title', '销售/成本')}</HeroTable.Column>
              <HeroTable.Column id="usage">{t('api_keys.usage')}</HeroTable.Column>
              <HeroTable.Column id="expires_at">{t('user_keys.expires_at')}</HeroTable.Column>
              <HeroTable.Column id="status">{t('common.status')}</HeroTable.Column>
              <HeroTable.Column id="actions" style={{ width: 132 }}>
                {t('common.actions')}
              </HeroTable.Column>
            </HeroTable.Header>
            <HeroTable.Body>
              {isLoading ? (
                <TableLoadingRow colSpan={9} />
              ) : rows.length === 0 ? (
                <HeroTable.Row id="empty">
                  <HeroTable.Cell colSpan={9}>
                    <EmptyState />
                  </HeroTable.Cell>
                </HeroTable.Row>
              ) : (
                rows.map((row) => {
                  const group = row.group_id == null ? null : groupMap.get(row.group_id);
                  const groupName = row.group_id == null
                    ? t('user_keys.group_unbound')
                    : group?.name || `#${row.group_id}`;
                  const hasSellRate = row.sell_rate != null && row.sell_rate > 0;
                  const userOverride = row.group_id == null ? undefined : user?.group_rates?.[row.group_id];
                  const hasOverride =
                    typeof userOverride === 'number' &&
                    Number.isFinite(userOverride) &&
                    userOverride > 0 &&
                    group != null &&
                    userOverride !== group.rate_multiplier;
                  const profit = (row.used_quota || 0) - (row.used_quota_actual || 0);
                  const isExpired = row.expires_at && new Date(row.expires_at) < new Date();
                  const displayStatus = isExpired ? 'expired' : row.status;

                  return (
                    <HeroTable.Row id={String(row.id)} key={row.id}>
                      <HeroTable.Cell>
                        <span className="font-medium text-text">{row.name}</span>
                      </HeroTable.Cell>
                      <HeroTable.Cell>
                        <span className="inline-flex items-center gap-1.5 text-xs px-2 py-0.5 rounded-sm border border-glass-border bg-surface text-text-secondary font-mono">
                          <Key className="w-3 h-3 text-text-tertiary" />
                          {row.key_prefix}...
                        </span>
                      </HeroTable.Cell>
                      <HeroTable.Cell>
                        <div className="space-y-0.5 text-center">
                          <div>{groupName}</div>
                          {group && (
                            <div className="font-mono text-xs text-text-tertiary">
                              {t('user_keys.group_rate_short', '分组倍率')}:{' '}
                              {hasOverride && userOverride != null ? (
                                <span
                                  title={`${t('user_keys.group_rate_default', '分组默认')}: ${group.rate_multiplier.toFixed(2)}`}
                                >
                                  {userOverride.toFixed(2)}
                                  <span className="ml-1 inline-block rounded bg-amber-500/10 px-1 text-[9px] leading-[14px] text-amber-500 align-middle">
                                    {t('user_keys.user_override_tag', '专属')}
                                  </span>
                                </span>
                              ) : (
                                group.rate_multiplier.toFixed(2)
                              )}
                            </div>
                          )}
                          {hasSellRate && (
                            <div className="font-mono text-xs text-text-tertiary">
                              {t('user_keys.sell_rate_short', '销售倍率')}: {row.sell_rate!.toFixed(2)}
                            </div>
                          )}
                        </div>
                      </HeroTable.Cell>
                      <HeroTable.Cell>
                        <span className="font-mono">
                          {row.quota_usd > 0 ? (
                            <>
                              ${row.used_quota.toFixed(4)} / ${row.quota_usd.toFixed(4)}
                            </>
                          ) : (
                            <span className="text-text-tertiary">{t('user_keys.quota_unlimited_hint')}</span>
                          )}
                        </span>
                      </HeroTable.Cell>
                      <HeroTable.Cell>
                        {!row.sell_rate || row.sell_rate <= 0 ? (
                          <span className="text-text-tertiary text-xs">—</span>
                        ) : (
                          <div className="font-mono text-xs space-y-0.5">
                            <div>
                              <span className="text-text-tertiary">{t('user_keys.sell_rate_short', '倍率')}: </span>
                              <span>{row.sell_rate.toFixed(2)}</span>
                            </div>
                            <div>
                              <span className="text-text-tertiary">{t('user_keys.cost_actual', '成本')}: </span>
                              <span>${(row.used_quota_actual || 0).toFixed(4)}</span>
                            </div>
                            <div>
                              <span className="text-text-tertiary">{t('user_keys.profit', '利润')}: </span>
                              <span style={{ color: profit > 0 ? 'var(--ag-success)' : undefined }}>
                                ${profit.toFixed(4)}
                              </span>
                            </div>
                          </div>
                        )}
                      </HeroTable.Cell>
                      <HeroTable.Cell>
                        <div className="font-mono text-xs space-y-0.5">
                          <div>
                            <span className="text-text-tertiary">{t('api_keys.today')}: </span>
                            <span style={{ color: 'var(--ag-primary)' }}>${row.today_cost.toFixed(4)}</span>
                          </div>
                          <div>
                            <span className="text-text-tertiary">{t('api_keys.thirty_days')}: </span>
                            <span>${row.thirty_day_cost.toFixed(4)}</span>
                          </div>
                        </div>
                      </HeroTable.Cell>
                      <HeroTable.Cell>
                        {row.expires_at
                          ? new Date(row.expires_at).toLocaleDateString('zh-CN')
                          : t('user_keys.never_expire')}
                      </HeroTable.Cell>
                      <HeroTable.Cell>
                        <StatusChip status={displayStatus} />
                      </HeroTable.Cell>
                      <HeroTable.Cell>
                        <div className="flex items-center justify-center gap-0.5">
                          <Button
                            isIconOnly
                            size="sm"
                            variant="ghost"
                            aria-label={t('api_keys.reveal')}
                            onPress={() => revealMutation.mutate(row.id)}
                          >
                            <Eye className="w-3.5 h-3.5" />
                          </Button>
                          <Button
                            isIconOnly
                            size="sm"
                            variant="ghost"
                            aria-label={t('user_keys.use_key')}
                            onPress={() => openUseKeyModal(row)}
                          >
                            <Terminal className="w-3.5 h-3.5" />
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
                                    case 'import_ccs':
                                      openCcsModal(row);
                                      break;
                                    case 'toggle':
                                      toggleStatusMutation.mutate({
                                        id: row.id,
                                        status: row.status === 'active' ? 'disabled' : 'active',
                                      });
                                      break;
                                    case 'edit':
                                      openEdit(row);
                                      break;
                                    case 'delete':
                                      setDeleteTarget(row);
                                      break;
                                  }
                                }}
                              >
                                <Dropdown.Item id="import_ccs" textValue={t('user_keys.import_ccs')}>
                                  <span className="flex items-center gap-2">
                                    <Upload className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />
                                    {t('user_keys.import_ccs')}
                                  </span>
                                </Dropdown.Item>
                                <Dropdown.Item
                                  id="toggle"
                                  textValue={row.status === 'active' ? t('user_keys.disable') : t('user_keys.enable')}
                                >
                                  <span className="flex items-center gap-2">
                                    {row.status === 'active'
                                      ? <Ban className="w-3.5 h-3.5" />
                                      : <CheckCircle className="w-3.5 h-3.5" />}
                                    {row.status === 'active' ? t('user_keys.disable') : t('user_keys.enable')}
                                  </span>
                                </Dropdown.Item>
                                <Dropdown.Item id="edit" textValue={t('common.edit')}>
                                  <span className="flex items-center gap-2">
                                    <Pencil className="w-3.5 h-3.5" />
                                    {t('common.edit')}
                                  </span>
                                </Dropdown.Item>
                                <Dropdown.Item id="delete" className="text-danger" textValue={t('common.delete')}>
                                  <span className="flex items-center gap-2">
                                    <Trash2 className="w-3.5 h-3.5" />
                                    {t('common.delete')}
                                  </span>
                                </Dropdown.Item>
                              </Dropdown.Menu>
                            </Dropdown.Popover>
                          </Dropdown>
                        </div>
                      </HeroTable.Cell>
                    </HeroTable.Row>
                  );
                })
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
      <EditKeyModal
        open={modalOpen}
        isEdit={!!editingKey}
        form={form}
        setForm={setForm}
        groupOptions={groupOptions}
        onClose={closeModal}
        onSubmit={handleSubmit}
        loading={saving}
      />

      {/* 新建密钥后显示完整密钥 */}
      <CreateKeyModal
        open={!!createdKey}
        createdKey={createdKey}
        onClose={() => setCreatedKey(null)}
      />

      {/* 查看密钥弹窗 */}
      <Modal state={revealedKeyModalState}>
        <Modal.Backdrop>
          <Modal.Container placement="center" scroll="inside" size="md">
            <Modal.Dialog className="ag-elevation-modal">
              <Modal.Header>
                <Modal.Heading>{t('api_keys.reveal')}</Modal.Heading>
                <Modal.CloseTrigger />
              </Modal.Header>
              <Modal.Body>
                <div className="space-y-4">
                  <Alert status="warning">
                    <Alert.Indicator>
                      <AlertTriangle className="h-4 w-4" />
                    </Alert.Indicator>
                    <Alert.Content>
                      <Alert.Description>{t('api_keys.key_reveal_warning')}</Alert.Description>
                    </Alert.Content>
                  </Alert>
                  <div className="flex items-center gap-2">
                    <code className="flex-1 break-all rounded-md border border-glass-border bg-surface px-3 py-2 font-mono text-sm text-text">
                      {revealedKey || ''}
                    </code>
                    <Button size="sm" variant="secondary" onPress={() => copy(revealedKey || '')}>
                      <Copy className="h-3.5 w-3.5" />
                      {t('common.copy')}
                    </Button>
                  </div>
                </div>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="primary" onPress={() => setRevealedKey(null)}>
                  {t('common.close')}
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      {/* 使用 API 密钥配置弹窗 */}
      <UseKeyModal
        useKeyTarget={useKeyTarget}
        useKeyValue={useKeyValue}
        useKeyPlatform={useKeyPlatform}
        showClientTabs={showClientTabs}
        useKeyTab={useKeyTab}
        setUseKeyTab={setUseKeyTab}
        useKeyShell={useKeyShell}
        setUseKeyShell={setUseKeyShell}
        onClose={closeUseKeyModal}
      />

      {/* CCS 导入弹窗 */}
      <CcsImportModal
        open={!!ccsTarget}
        ccsKeyValue={ccsKeyValue}
        ccsPlatform={ccsPlatform}
        onClose={closeCcsModal}
      />

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
                <AlertDialog.Heading>{t('user_keys.delete_key')}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>{t('user_keys.delete_confirm', { name: deleteTarget?.name })}</AlertDialog.Body>
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
