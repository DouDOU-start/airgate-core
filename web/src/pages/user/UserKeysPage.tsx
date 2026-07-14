import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apikeysApi } from '../../shared/api/apikeys';
import { usePagination } from '../../shared/hooks/usePagination';
import { groupsApi } from '../../shared/api/groups';
import { useToast } from '../../shared/ui';
import { Alert, Button, Dropdown, EmptyState, Modal, useOverlayState } from '@heroui/react';
import { DialogTriggerShim } from '../../shared/components/DialogTriggerShim';
import {
  StatusChip,
} from '../../shared/ui';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE, FETCH_ALL_PARAMS } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { CommonTable } from '../../shared/components/CommonTable';
import { MetricChips } from '../../shared/components/MetricChips';
import { GROUP_CHIP_STYLE } from '../../shared/components/groupChipStyle';
import { useClipboard } from '../../shared/hooks/useClipboard';
import { useCopyFeedback } from '../../shared/hooks/useCopyFeedback';
import {
  AlertTriangle,
  Check,
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
import { EditKeyModal } from './userkeys/EditKeyModal';
import { CreateKeyModal } from './userkeys/CreateKeyModal';
import { UseKeyModal, useUseKeyModal } from './userkeys/UseKeyModal';
import { EndpointsBar } from './userkeys/EndpointsBar';
import { CcsImportModal, useCcsImportModal } from './userkeys/CcsImportModal';
import { type KeyForm, emptyForm } from './userkeys/types';
import { endOfDayLocalISO, formatDate, localDateStr } from '../../shared/utils/format';
import { ConfirmDialog } from '../../shared/components/ConfirmDialog';

export default function UserKeysPage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const copy = useClipboard();
  const queryClient = useQueryClient();

  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE, 'user.keys');
  const [modalOpen, setModalOpen] = useState(false);
  const [editingKey, setEditingKey] = useState<APIKeyResp | null>(null);
  const [form, setForm] = useState<KeyForm>(emptyForm);
  const [deleteTarget, setDeleteTarget] = useState<APIKeyResp | null>(null);

  // 显示新创建密钥的弹窗
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [revealedKey, setRevealedKey] = useState<string | null>(null);
  const {
    copied: revealedKeyCopied,
    showCopied: showRevealedKeyCopied,
    resetCopied: resetRevealedKeyCopied,
  } = useCopyFeedback();

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
      max_rate: key.max_rate ? String(key.max_rate) : '',
      max_concurrency: key.max_concurrency ? String(key.max_concurrency) : '',
      // 按本地时区回填日期，与提交侧 endOfDayLocalISO 对称，避免跨时区漂移
      expires_at: key.expires_at ? localDateStr(key.expires_at) : '',
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

    // 后端要求 RFC3339 格式；空字符串表示显式清除过期时间。
    // 编辑态下若日期未改动，原样传回原 RFC3339 值，避免「回填→重编码」在
    // 重复保存时把过期时刻反复重算而产生漂移。
    let expiresAt = '';
    if (form.expires_at) {
      const original = editingKey?.expires_at;
      expiresAt = original && localDateStr(original) === form.expires_at
        ? original
        : endOfDayLocalISO(form.expires_at);
    }

    if (editingKey) {
      const payload: UpdateAPIKeyReq = {
        name: form.name,
        group_id: form.group_id ? Number(form.group_id) : undefined,
        // 空字符串显式改为 0 = 无限配额；省略字段只表示不修改旧配额
        quota_usd: form.quota_usd.trim() ? Number(form.quota_usd) : 0,
        sell_rate: form.sell_rate ? Number(form.sell_rate) : 0,
        // 空字符串显式改为 0 = 关闭最高倍率限制
        max_rate: form.max_rate ? Number(form.max_rate) : 0,
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
        max_rate: form.max_rate ? Number(form.max_rate) : undefined,
        max_concurrency: form.max_concurrency ? Number(form.max_concurrency) : undefined,
        expires_at: expiresAt,
      };
      createMutation.mutate(payload);
    }
  }

  // 查找分组
  const groupList = useMemo(() => groupsData?.list ?? [], [groupsData?.list]);
  const groupMap = useMemo(() => new Map<number, GroupResp>(groupList.map((g) => [g.id, g])), [groupList]);

  const hasAvailableGroups = groupList.length > 0;

  // 分组选项（后端已按"用户专属 > 等级 > 分组档位"解析 effective_rate；
  // 与分组档位不同时右侧显示划线原价 + 实际倍率）
  const groupOptions = useMemo(() => groupList.map((g) => {
    const effective = g.effective_rate != null && g.effective_rate > 0 ? g.effective_rate : g.rate_multiplier;
    const hasOverride = effective !== g.rate_multiplier;
    return {
      value: String(g.id),
      label: g.name,
      suffix: hasOverride ? (
        <span className="text-text-tertiary">
          <span className="line-through opacity-60">{g.rate_multiplier}x</span>{' '}
          <span className="text-primary font-medium">{effective}x</span>
        </span>
      ) : (
        <span className="text-text-tertiary">{g.rate_multiplier}x {t('user_keys.rate_suffix')}</span>
      ),
    };
  }), [groupList, t]);

  // 使用配置弹窗
  const {
    useKeyTarget,
    useKeyValue,
    useKeyTab,
    setUseKeyTab,
    useKeyShell,
    setUseKeyShell,
    openUseKeyModal,
    closeUseKeyModal,
  } = useUseKeyModal();

  // CCS 导入弹窗
  const {
    ccsTarget,
    ccsKeyValue,
    openCcsModal,
    closeCcsModal,
  } = useCcsImportModal();

  const saving = createMutation.isPending || updateMutation.isPending;
  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);
  const closeRevealedKeyModal = () => {
    resetRevealedKeyCopied();
    setRevealedKey(null);
  };
  const handleCopyRevealedKey = async () => {
    if (await copy(revealedKey || '')) {
      showRevealedKeyCopied();
    }
  };
  const revealedKeyModalState = useOverlayState({
    isOpen: !!revealedKey,
    onOpenChange: (open) => {
      if (!open) closeRevealedKeyModal();
    },
  });

  return (
    <div className="p-6">
      <div className="flex flex-wrap items-center justify-between gap-3 mb-5">
        <EndpointsBar />
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

      <CommonTable
        ariaLabel={t('user_keys.title', 'API keys')}
        className="ag-api-keys-table"
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
        minWidth={1040}
      >
        <CommonTable.Header>
          <CommonTable.Column id="name">{t('common.name')}</CommonTable.Column>
          <CommonTable.Column id="key_prefix">{t('user_keys.title')}</CommonTable.Column>
          <CommonTable.Column id="group_id">{t('user_keys.group')}</CommonTable.Column>
          <CommonTable.Column id="status">{t('common.status')}</CommonTable.Column>
          <CommonTable.Column id="quota" style={{ width: '17.5rem' }}>{t('user_keys.quota_label')}</CommonTable.Column>
          <CommonTable.Column id="markup" style={{ width: '10.75rem' }}>{t('user_keys.markup_title', '销售/成本')}</CommonTable.Column>
          <CommonTable.Column id="usage" style={{ width: '10.75rem' }}>{t('api_keys.usage')}</CommonTable.Column>
          <CommonTable.Column id="expires_at">{t('user_keys.expires_at')}</CommonTable.Column>
          <CommonTable.Column id="actions" style={{ width: 132 }}>
            {t('common.actions')}
          </CommonTable.Column>
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
            rows.map((row) => {
              const group = row.group_id == null ? null : groupMap.get(row.group_id);
              // group_id 为空（从未绑定/分组被删除时数据库外键自动置空）或者
              // 分组事后被设为专属且未把该用户加入白名单（此时接口不会再返回该分组，
              // 避免向用户泄露专属分组的名称/倍率）——两种情况统一按"分组已失效"
              // 处理，提示用户重新绑定，不去猜测/展示具体是哪个分组。
              const isGroupUnbound = row.group_id == null || group == null;
              const groupName = isGroupUnbound ? t('user_keys.group_unbound') : group.name;
              const hasSellRate = row.sell_rate != null && row.sell_rate > 0;
              // 后端已按"用户专属 > 等级 > 分组档位"解析 effective_rate
              const effectiveRate = group?.effective_rate != null && group.effective_rate > 0
                ? group.effective_rate
                : group?.rate_multiplier;
              const hasOverride = group != null && effectiveRate != null && effectiveRate !== group.rate_multiplier;
              const hasMaxRate = row.max_rate != null && row.max_rate > 0;
              // 当前生效倍率超过密钥最高倍率时该 key 的请求会被拒绝，标红提醒
              const maxRateExceeded = hasMaxRate && effectiveRate != null && effectiveRate > row.max_rate;
              const profit = (row.used_quota || 0) - (row.used_quota_actual || 0);
              const isExpired = row.expires_at && new Date(row.expires_at) < new Date();
              const displayStatus = isExpired ? 'expired' : row.status;

              return (
                <CommonTable.Row id={String(row.id)} key={row.id}>
                  <CommonTable.Cell>
                    <span className="font-medium text-text">{row.name}</span>
                  </CommonTable.Cell>
                  <CommonTable.Cell>
                    <button
                      type="button"
                      className="inline-flex items-center gap-1.5 text-xs px-2 py-0.5 rounded-sm border border-glass-border bg-surface text-text-secondary font-mono cursor-pointer transition-colors hover:border-accent hover:text-accent"
                      title={t('common.copy')}
                      onClick={async () => {
                        // 行内 async 需自行兜错：reveal 失败时 toast 提示而不是静默吞掉
                        try {
                          const resp = await apikeysApi.reveal(row.id);
                          if (resp.key) await copy(resp.key);
                        } catch (err) {
                          toast('error', err instanceof Error ? err.message : t('common.copy_failed'));
                        }
                      }}
                    >
                      <Key className="w-3 h-3 text-text-tertiary" />
                      {row.key_prefix}...
                      <Copy className="w-3 h-3 text-text-tertiary" />
                    </button>
                  </CommonTable.Cell>
                  <CommonTable.Cell>
                    <div className="space-y-0.5 text-center">
                      <div className="flex justify-center">
                        <span
                          className="inline-flex h-6 min-w-0 max-w-full items-center justify-center gap-1 rounded-[var(--radius)] px-1.5 text-[13px] font-medium leading-none text-text-secondary"
                          style={GROUP_CHIP_STYLE}
                          title={groupName}
                        >
                          {isGroupUnbound ? <AlertTriangle className="h-3 w-3 shrink-0 text-warning" /> : null}
                          <span className="min-w-0 truncate">{groupName}</span>
                        </span>
                      </div>
                      {(group || hasSellRate || hasMaxRate) && (
                        <MetricChips
                          className="ag-metric-chips--stack ag-metric-chips--markup"
                          items={[
                            ...(group ? [{
                              color: 'default' as const,
                              label: t('user_keys.group_rate_short', '分组倍率'),
                              value: hasOverride && effectiveRate != null
                                ? `${group.rate_multiplier.toFixed(2)} → ${effectiveRate.toFixed(2)} ${t('user_keys.user_override_tag', '专属')}`
                                : group.rate_multiplier.toFixed(2),
                              valueNode: hasOverride && effectiveRate != null ? (
                                <>
                                  <span className="line-through text-text-tertiary">{group.rate_multiplier.toFixed(2)}</span>
                                  <span className="ml-1">{effectiveRate.toFixed(2)}</span>
                                  <span className="ml-1 text-warning">{t('user_keys.user_override_tag', '专属')}</span>
                                </>
                              ) : undefined,
                            }] : []),
                            ...(hasSellRate ? [{
                              color: 'default' as const,
                              label: t('user_keys.sell_rate_short', '销售倍率'),
                              value: row.sell_rate!.toFixed(2),
                            }] : []),
                            ...(hasMaxRate ? [{
                              color: maxRateExceeded ? ('danger' as const) : ('default' as const),
                              label: t('user_keys.max_rate_short', '最高倍率'),
                              value: maxRateExceeded
                                ? `${row.max_rate.toFixed(2)} ${t('user_keys.max_rate_exceeded_tag', '已超限')}`
                                : row.max_rate.toFixed(2),
                            }] : []),
                          ]}
                        />
                      )}
                    </div>
                  </CommonTable.Cell>
                  <CommonTable.Cell>
                    <StatusChip status={displayStatus} />
                  </CommonTable.Cell>
                  <CommonTable.Cell>
                    <MetricChips
                      className="ag-metric-chips--quota"
                      items={[
                        {
                          amount: row.used_quota,
                          color: 'warning',
                          highlightDollar: true,
                          label: t('user_keys.quota_used_short', '已使用'),
                        },
                        {
                          amount: row.quota_usd > 0 ? row.quota_usd : undefined,
                          color: 'success',
                          label: t('user_keys.quota_total_short', '总配额'),
                          value: '∞',
                        },
                      ]}
                    />
                  </CommonTable.Cell>
                  <CommonTable.Cell>
                    {hasSellRate || (row.used_quota_actual || 0) > 0 || profit !== 0 ? (
                      <MetricChips
                        className="ag-metric-chips--stack ag-metric-chips--markup"
                        items={[
                          {
                            color: 'default',
                            label: t('user_keys.sell_rate_short', '倍率'),
                            value: hasSellRate ? row.sell_rate.toFixed(2) : '—',
                          },
                          {
                            amount: row.used_quota_actual || 0,
                            color: 'default',
                            dollarTone: 'warning',
                            label: t('user_keys.cost_actual', '成本'),
                          },
                          {
                            amount: profit,
                            color: 'default',
                            dollarTone: 'success',
                            label: t('user_keys.profit', '利润'),
                          },
                        ]}
                      />
                    ) : (
                      <span className="text-text-tertiary">—</span>
                    )}
                  </CommonTable.Cell>
                  <CommonTable.Cell>
                    <MetricChips
                      className="ag-metric-chips--stack ag-metric-chips--usage"
                      items={[
                        {
                          amount: row.today_cost,
                          color: 'warning',
                          dollarTone: 'warning',
                          label: t('api_keys.today', '今日'),
                          mutedWhenZero: true,
                        },
                        {
                          amount: row.thirty_day_cost,
                          color: 'warning',
                          dollarTone: 'warning',
                          label: t('api_keys.thirty_days', '近30天'),
                          mutedWhenZero: true,
                        },
                      ]}
                    />
                  </CommonTable.Cell>
                  <CommonTable.Cell>
                    {row.expires_at
                      ? formatDate(row.expires_at)
                      : t('user_keys.never_expire')}
                  </CommonTable.Cell>
                  <CommonTable.Cell>
                    <div className="ag-table-row-actions flex items-center justify-center gap-0.5">
                      <Button
                        isIconOnly
                        size="sm"
                        variant="secondary"
                        aria-label={t('api_keys.reveal')}
                        onPress={() => revealMutation.mutate(row.id)}
                      >
                        <Eye className="w-3.5 h-3.5" />
                      </Button>
                      <Button
                        isIconOnly
                        size="sm"
                        variant="secondary"
                        aria-label={t('user_keys.use_key')}
                        onPress={() => openUseKeyModal(row)}
                      >
                        <Terminal className="w-3.5 h-3.5" />
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
                  </CommonTable.Cell>
                </CommonTable.Row>
              );
            })
          )}
        </CommonTable.Body>
      </CommonTable>

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
        <DialogTriggerShim />
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
                    <Button size="sm" variant="secondary" onPress={handleCopyRevealedKey}>
                      {revealedKeyCopied
                        ? <Check className="h-3.5 w-3.5 text-success" />
                        : <Copy className="h-3.5 w-3.5" />}
                      <span className={revealedKeyCopied ? 'text-success' : undefined}>
                        {t('common.copy')}
                      </span>
                    </Button>
                  </div>
                </div>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="primary" onPress={closeRevealedKeyModal}>
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
        onClose={closeCcsModal}
      />

      {/* 删除确认 */}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
        title={t('user_keys.delete_key')}
        description={t('user_keys.delete_confirm', { name: deleteTarget?.name })}
        loading={deleteMutation.isPending}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
      />
    </div>
  );
}
