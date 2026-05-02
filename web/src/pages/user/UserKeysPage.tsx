import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apikeysApi } from '../../shared/api/apikeys';
import { usePagination } from '../../shared/hooks/usePagination';
import { groupsApi } from '../../shared/api/groups';
import { useToast } from '../../shared/components/Toast';
import { Table, type Column } from '../../shared/components/Table';
import { Button } from '../../shared/components/Button';
import { ConfirmModal } from '../../shared/components/Modal';
import { StatusBadge } from '../../shared/components/Badge';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../shared/queryKeys';
import { DEFAULT_PAGE_SIZE, FETCH_ALL_PARAMS } from '../../shared/constants';
import { useDropdownMenu } from '../../shared/hooks/useDropdownMenu';
import { DropdownMenu, type DropdownMenuItem } from '../../shared/components/DropdownMenu';
import { KeyRevealModal } from '../../shared/components/KeyRevealModal';
import {
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

  // 更多菜单
  const { menu: moreMenu, menuRef: moreMenuRef, open: openMoreMenu, close: closeMoreMenu } = useDropdownMenu();

  // 密钥列表
  const { data, isLoading, refetch } = useQuery({
    queryKey: queryKeys.userKeys(page, pageSize),
    queryFn: () => apikeysApi.list({ page, page_size: pageSize }),
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

  const columns: Column<APIKeyResp>[] = [
    { key: 'name', title: t('common.name') },
    {
      key: 'key_prefix',
      title: t('user_keys.title'),
      hideOnMobile: true,
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
      align: 'center',
      render: (row) => {
        if (row.group_id == null) return t('user_keys.group_unbound');
        const group = groupMap.get(row.group_id);
        const name = group?.name || `#${row.group_id}`;
        const hasSellRate = row.sell_rate != null && row.sell_rate > 0;

        // 用户在当前分组上是否有"用户专属倍率"覆盖
        // 优先级：UserGroupRates[groupID] > GroupRateMultiplier
        // 与后端 billing/rate.go:ResolveBillingRate 的解析规则保持一致
        const userOverride = user?.group_rates?.[row.group_id];
        const hasOverride =
          typeof userOverride === 'number' &&
          Number.isFinite(userOverride) &&
          userOverride > 0 &&
          group != null &&
          userOverride !== group.rate_multiplier;

        return (
          <div className="space-y-0.5 text-center">
            <div>{name}</div>
            {group && (
              <div className="font-mono text-xs text-text-tertiary">
                {t('user_keys.group_rate_short', '分组倍率')}:{' '}
                {hasOverride ? (
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
        );
      },
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
      key: 'markup',
      title: t('user_keys.markup_title', '销售/成本'),
      hideOnMobile: true,
      render: (row) => {
        // sell_rate=0 时账面 == 真实成本，没必要展示 markup
        if (!row.sell_rate || row.sell_rate <= 0) {
          return <span className="text-text-tertiary text-xs">—</span>;
        }
        const profit = (row.used_quota || 0) - (row.used_quota_actual || 0);
        return (
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
        );
      },
    },
    {
      key: 'usage',
      title: t('api_keys.usage'),
      render: (row) => (
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
      ),
    },
    {
      key: 'expires_at',
      title: t('user_keys.expires_at'),
      hideOnMobile: true,
      render: (row) =>
        row.expires_at
          ? new Date(row.expires_at).toLocaleDateString('zh-CN')
          : t('user_keys.never_expire'),
    },
    {
      key: 'status',
      title: t('common.status'),
      render: (row) => {
        // 前端判断：过期时间已过则显示为 expired
        const isExpired = row.expires_at && new Date(row.expires_at) < new Date();
        const displayStatus = isExpired ? 'expired' : row.status;
        return <StatusBadge status={displayStatus} />;
      },
    },
    {
      key: 'actions',
      title: t('common.actions'),
      width: '120px',
      render: (row) => (
        <div className="flex items-center justify-center gap-0.5">
          <button
            className="p-1.5 rounded hover:bg-bg-hover transition-colors cursor-pointer"
            style={{ color: 'var(--ag-text-secondary)' }}
            title={t('api_keys.reveal')}
            onClick={() => revealMutation.mutate(row.id)}
          >
            <Eye className="w-3.5 h-3.5" />
          </button>
          <button
            className="p-1.5 rounded hover:bg-bg-hover transition-colors cursor-pointer"
            style={{ color: 'var(--ag-text-secondary)' }}
            title={t('user_keys.use_key')}
            onClick={() => openUseKeyModal(row)}
          >
            <Terminal className="w-3.5 h-3.5" />
          </button>
          <button
            className="p-1.5 rounded hover:bg-bg-hover transition-colors cursor-pointer"
            style={{ color: 'var(--ag-text-secondary)' }}
            title={t('common.more')}
            onClick={(e) => {
              e.stopPropagation();
              if (moreMenu?.id === row.id) {
                closeMoreMenu();
              } else {
                openMoreMenu(row.id, e);
              }
            }}
          >
            <MoreHorizontal className="w-3.5 h-3.5" />
          </button>
        </div>
      ),
    },
  ];

  const saving = createMutation.isPending || updateMutation.isPending;

  // Build dropdown menu items for the active row
  const moreMenuRow = moreMenu ? data?.list?.find((k) => k.id === moreMenu.id) : null;
  const moreMenuItems: DropdownMenuItem[] = moreMenuRow
    ? [
        {
          icon: <Upload className="w-3.5 h-3.5" style={{ color: 'var(--ag-text-tertiary)' }} />,
          label: t('user_keys.import_ccs'),
          onClick: () => openCcsModal(moreMenuRow),
        },
        {
          icon: moreMenuRow.status === 'active'
            ? <Ban className="w-3.5 h-3.5" />
            : <CheckCircle className="w-3.5 h-3.5" />,
          label: moreMenuRow.status === 'active' ? t('user_keys.disable') : t('user_keys.enable'),
          onClick: () =>
            toggleStatusMutation.mutate({
              id: moreMenuRow.id,
              status: moreMenuRow.status === 'active' ? 'disabled' : 'active',
            }),
        },
        {
          icon: <Pencil className="w-3.5 h-3.5" />,
          label: t('common.edit'),
          onClick: () => openEdit(moreMenuRow),
        },
        {
          icon: <Trash2 className="w-3.5 h-3.5" />,
          label: t('common.delete'),
          onClick: () => setDeleteTarget(moreMenuRow),
          danger: true,
          divider: true,
        },
      ]
    : [];

  return (
    <div className="p-6">
      <div className="flex justify-end mb-5">
        <div className="flex items-center gap-2 ml-auto">
          <button
            onClick={() => refetch()}
            className="flex items-center justify-center w-9 h-9 rounded-[10px] text-text-tertiary hover:text-text-secondary hover:bg-bg-hover transition-colors"
          >
            <RefreshCw className="w-4 h-4" />
          </button>
          <Button
            onClick={openCreate}
            icon={<Plus className="w-4 h-4" />}
            disabled={!hasAvailableGroups}
            title={!hasAvailableGroups ? t('user_keys.no_groups_available') : undefined}
          >
            {hasAvailableGroups ? t('user_keys.create') : t('user_keys.create_disabled_no_groups')}
          </Button>
        </div>
      </div>

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

      {/* 更多操作下拉菜单 */}
      {moreMenu && moreMenuRow && (
        <DropdownMenu
          ref={moreMenuRef}
          items={moreMenuItems}
          position={{ top: moreMenu.top, left: moreMenu.left }}
          onClose={closeMoreMenu}
        />
      )}

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
      <KeyRevealModal
        open={!!revealedKey}
        keyValue={revealedKey || ''}
        title={t('api_keys.reveal')}
        warningText={t('api_keys.key_reveal_warning')}
        onClose={() => setRevealedKey(null)}
      />

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
