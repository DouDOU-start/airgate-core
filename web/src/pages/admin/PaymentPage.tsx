import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import {
  Button, Card, Checkbox, Chip, EmptyState, Input, Label, ListBox,
  Modal, Select, Spinner, TextArea, TextField as HeroTextField, Tabs, useOverlayState,
} from '@heroui/react';
import {
  Activity, CheckCircle, Clock, Coins, Pencil, Plus, ReceiptText,
  Save, Search, Settings2, Trash2, TrendingUp,
} from 'lucide-react';
import { paymentApi } from '../../shared/api/payment';
import { settingsApi } from '../../shared/api/settings';
import { queryKeys } from '../../shared/queryKeys';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { usePagination } from '../../shared/hooks/usePagination';
import { useDebouncedValue } from '../../shared/hooks/useDebouncedValue';
import { useToast } from '../../shared/ui';
import { DEFAULT_PAGE_SIZE } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { formatDateTime } from '../../shared/utils/format';
import { CommonTable } from '../../shared/components/CommonTable';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { DialogTriggerShim } from '../../shared/components/DialogTriggerShim';
import { NativeSwitch } from '../../shared/components/NativeSwitch';
import { ConfirmDialog } from '../../shared/components/ConfirmDialog';
import { StatCard } from '../../shared/components/StatCard';
import { RefreshButton } from '../../shared/components/RefreshButton';
import type {
  PaymentOrderStatus, PaymentProviderItem, PaymentProviderKindMeta,
  SettingItem, UpsertPaymentProviderReq,
} from '../../shared/types';

// ==================== 常量 ====================

type TabKey = 'orders' | 'config';

// 充值参数 settings 键（group="payment"）
const PAYMENT_SETTING_FIELDS: { key: string; labelKey: string; hintKey?: string; type: 'text' | 'number' }[] = [
  { key: 'payment.callback_base_url', labelKey: 'payment.callback_base_url', hintKey: 'payment.callback_base_url_hint', type: 'text' },
  { key: 'payment.min_amount', labelKey: 'payment.min_amount', type: 'number' },
  { key: 'payment.max_amount', labelKey: 'payment.max_amount', type: 'number' },
  { key: 'payment.daily_limit', labelKey: 'payment.daily_limit', type: 'number' },
  { key: 'payment.order_expire_minutes', labelKey: 'payment.order_expire_minutes', type: 'number' },
];

// 订单状态徽章配色
const STATUS_CHIP_COLORS: Record<PaymentOrderStatus, 'warning' | 'success' | 'default'> = {
  pending: 'warning',
  paid: 'success',
  expired: 'default',
};

// ==================== 小组件 ====================

function OrderStatusChip({ status }: { status: PaymentOrderStatus }) {
  const { t } = useTranslation();
  return (
    <Chip color={STATUS_CHIP_COLORS[status] ?? 'default'} size="sm" variant="soft">
      {t(`payment.status_${status}`, status)}
    </Chip>
  );
}

// ==================== 订单总览 Tab ====================

function OrdersTab() {
  const { t } = useTranslation();

  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE, 'admin.payment');
  const [email, setEmail] = useState('');
  const debouncedEmail = useDebouncedValue(email.trim(), 250);
  const [statusFilter, setStatusFilter] = useState('');

  const listQuery = useMemo(() => ({
    page,
    page_size: pageSize,
    email: debouncedEmail || undefined,
    status: statusFilter || undefined,
  }), [page, pageSize, debouncedEmail, statusFilter]);

  const { data, isFetching, isLoading, refetch } = useQuery({
    queryKey: queryKeys.adminPaymentOrders(listQuery),
    queryFn: () => paymentApi.adminListOrders(listQuery),
    placeholderData: keepPreviousData,
  });

  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const stats = data?.stats;
  const totalPages = getTotalPages(total, pageSize);

  const statusOptions = [
    { id: '', label: t('payment.all_statuses') },
    { id: 'pending', label: t('payment.status_pending') },
    { id: 'paid', label: t('payment.status_paid') },
    { id: 'expired', label: t('payment.status_expired') },
  ];
  const selectedStatusLabel = statusOptions.find((item) => item.id === statusFilter)?.label ?? t('payment.all_statuses');

  return (
    <div>
      {/* 统计卡 */}
      {stats ? (
        <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-5">
          <StatCard
            accentColor="var(--ag-primary)"
            icon={<ReceiptText className="h-5 w-5" />}
            title={t('payment.stat_total')}
            value={stats.total.toLocaleString()}
          />
          <StatCard
            accentColor="var(--success)"
            icon={<CheckCircle className="h-5 w-5" />}
            title={t('payment.stat_paid')}
            value={stats.paid.toLocaleString()}
          />
          <StatCard
            accentColor="var(--ag-warning)"
            icon={<Clock className="h-5 w-5" />}
            title={t('payment.stat_pending')}
            value={stats.pending.toLocaleString()}
          />
          <StatCard
            accentColor="var(--ag-info)"
            icon={<Coins className="h-5 w-5" />}
            title={t('payment.stat_total_amount')}
            value={`$${stats.total_amount.toFixed(2)}`}
          />
          <StatCard
            accentColor="var(--ag-success)"
            icon={<TrendingUp className="h-5 w-5" />}
            title={t('payment.stat_today_amount')}
            value={`$${stats.today_amount.toFixed(2)}`}
          />
        </div>
      ) : null}

      {/* 筛选 + 工具栏 */}
      <div className="mb-5 flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-center">
        <div className="relative w-full sm:w-64">
          <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
          <Input
            aria-label={t('common.search')}
            className="pl-9"
            placeholder={t('payment.search_email_placeholder')}
            value={email}
            onChange={(event) => {
              setEmail(event.target.value);
              setPage(1);
            }}
          />
        </div>
        <div className="w-full sm:w-40">
          <Select
            aria-label={t('common.status')}
            fullWidth
            selectedKey={statusFilter}
            onSelectionChange={(key) => {
              setStatusFilter(key == null ? '' : String(key));
              setPage(1);
            }}
          >
            <Select.Trigger>
              <Select.Value>{selectedStatusLabel}</Select.Value>
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox items={statusOptions}>
                {(item) => (
                  <ListBox.Item id={item.id} textValue={item.label}>
                    {item.label}
                  </ListBox.Item>
                )}
              </ListBox>
            </Select.Popover>
          </Select>
        </div>
        <div className="ag-mobile-actions ml-auto flex items-center gap-2">
          <RefreshButton
            ariaLabel={t('common.refresh', 'Refresh')}
            isRefreshing={isFetching}
            onRefresh={refetch}
            size="md"
          />
        </div>
      </div>

      {/* 订单表格 */}
      <CommonTable
        ariaLabel={t('payment.tab_orders')}
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
        minWidth={920}
      >
        <CommonTable.Header>
          <CommonTable.Column id="created_at">{t('payment.order_time')}</CommonTable.Column>
          <CommonTable.Column id="out_trade_no">{t('payment.order_no')}</CommonTable.Column>
          <CommonTable.Column id="user_email">{t('payment.col_user_email')}</CommonTable.Column>
          <CommonTable.Column id="method">{t('payment.order_method')}</CommonTable.Column>
          <CommonTable.Column id="provider_id">{t('payment.col_provider')}</CommonTable.Column>
          <CommonTable.Column id="amount">{t('payment.order_amount')}</CommonTable.Column>
          <CommonTable.Column id="status">{t('payment.order_status')}</CommonTable.Column>
        </CommonTable.Header>
        <CommonTable.Body>
          {isLoading ? (
            <TableLoadingRow colSpan={7} />
          ) : rows.length === 0 ? (
            <CommonTable.Row id="empty">
              <CommonTable.Cell colSpan={7}>
                <EmptyState>
                  <div className="text-sm text-default-500">{t('common.no_data')}</div>
                </EmptyState>
              </CommonTable.Cell>
            </CommonTable.Row>
          ) : (
            rows.map((row) => (
              <CommonTable.Row id={row.out_trade_no} key={row.out_trade_no}>
                <CommonTable.Cell>{formatDateTime(row.created_at)}</CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono text-xs text-text-secondary">{row.out_trade_no}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>{row.user_email || `#${row.user_id}`}</CommonTable.Cell>
                <CommonTable.Cell>{t(`payment.method_${row.method}`, row.method)}</CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono text-xs text-text-secondary">{row.provider_id}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono">${row.amount.toFixed(2)}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <OrderStatusChip status={row.status} />
                </CommonTable.Cell>
              </CommonTable.Row>
            ))
          )}
        </CommonTable.Body>
      </CommonTable>
    </div>
  );
}

// ==================== 服务商动态表单弹窗 ====================

// 按 kind 元信息初始化配置：编辑用现值，新建 bool 默认 false、method-multi 默认全选
function buildInitialConfig(kind: PaymentProviderKindMeta, editing: PaymentProviderItem | null): Record<string, string> {
  const config: Record<string, string> = {};
  for (const field of kind.field_descriptors) {
    const existing = editing?.config?.[field.key];
    if (existing !== undefined) {
      config[field.key] = existing;
      continue;
    }
    if (field.type === 'bool') {
      config[field.key] = 'false';
    } else if (field.type === 'method-multi') {
      config[field.key] = editing ? '' : kind.supported_methods.join(',');
    } else {
      config[field.key] = '';
    }
  }
  return config;
}

function ProviderFormModal({
  editing,
  kind,
  loading,
  onClose,
  onSubmit,
}: {
  editing: PaymentProviderItem | null;
  kind: PaymentProviderKindMeta;
  loading: boolean;
  onClose: () => void;
  onSubmit: (req: UpsertPaymentProviderReq) => void;
}) {
  const { t } = useTranslation();
  const { toast } = useToast();

  const [instanceID, setInstanceID] = useState(editing?.id ?? '');
  const [enabled, setEnabled] = useState(editing?.enabled ?? true);
  const [config, setConfig] = useState<Record<string, string>>(() => buildInitialConfig(kind, editing));

  const sensitiveKeys = useMemo(() => new Set(editing?.sensitive_keys ?? []), [editing]);

  function setField(key: string, value: string) {
    setConfig((prev) => ({ ...prev, [key]: value }));
  }

  // method-multi：值为逗号分隔的 method key 字符串
  function parseMethods(raw: string): string[] {
    return raw.split(',').map((item) => item.trim()).filter(Boolean);
  }

  function toggleMethod(fieldKey: string, methodKey: string, selected: boolean) {
    const current = parseMethods(config[fieldKey] ?? '');
    const next = selected
      ? [...new Set([...current, methodKey])]
      : current.filter((item) => item !== methodKey);
    setField(fieldKey, next.join(','));
  }

  function handleSubmit() {
    // 必填校验：敏感字段编辑时留空 = 保持不变，跳过
    for (const field of kind.field_descriptors) {
      if (!field.required || field.type === 'bool') continue;
      const value = (config[field.key] ?? '').trim();
      if (value) continue;
      if (editing && sensitiveKeys.has(field.key)) continue;
      toast('error', t('payment.required_field', { label: field.label }));
      return;
    }
    onSubmit({
      id: instanceID.trim() || undefined,
      original_id: editing?.id || undefined,
      kind: kind.kind,
      enabled,
      config,
    });
  }

  const modalState = useOverlayState({
    isOpen: true,
    onOpenChange: (open) => {
      if (!open) onClose();
    },
  });

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog
            className="ag-elevation-modal"
            style={{ maxWidth: '560px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>
                {editing
                  ? `${t('payment.provider_edit_title')} · ${kind.name}`
                  : `${t('payment.provider_add_title')} · ${kind.name}`}
              </Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                {kind.description ? (
                  <p className="text-xs text-text-tertiary">{kind.description}</p>
                ) : null}

                <HeroTextField fullWidth>
                  <Label>{t('payment.provider_id_label')}</Label>
                  <Input
                    placeholder={t('payment.provider_id_hint')}
                    value={instanceID}
                    onChange={(e) => setInstanceID(e.target.value)}
                  />
                </HeroTextField>

                <NativeSwitch
                  isSelected={enabled}
                  label={<span className="text-sm text-text">{t('payment.provider_enabled_label')}</span>}
                  onChange={setEnabled}
                />

                {kind.field_descriptors.map((field) => {
                  const isSensitive = !!editing && sensitiveKeys.has(field.key);
                  const placeholder = isSensitive
                    ? t('payment.sensitive_configured')
                    : field.placeholder || undefined;
                  const value = config[field.key] ?? '';

                  if (field.type === 'bool') {
                    return (
                      <NativeSwitch
                        key={field.key}
                        isSelected={value === 'true'}
                        label={(
                          <>
                            <span className="text-sm text-text">{field.label}</span>
                            {field.description ? (
                              <span className="block text-xs text-text-tertiary">{field.description}</span>
                            ) : null}
                          </>
                        )}
                        onChange={(selected) => setField(field.key, String(selected))}
                      />
                    );
                  }

                  if (field.type === 'method-multi') {
                    const selectedMethods = parseMethods(value);
                    return (
                      <div className="space-y-1" key={field.key}>
                        <Label>{field.label}</Label>
                        {field.description ? (
                          <p className="text-xs text-text-tertiary">{field.description}</p>
                        ) : null}
                        <div className="flex flex-wrap gap-x-5 gap-y-1.5 pt-1">
                          {kind.supported_methods.map((methodKey) => (
                            <Checkbox
                              key={methodKey}
                              isSelected={selectedMethods.includes(methodKey)}
                              onChange={(selected) => toggleMethod(field.key, methodKey, selected)}
                            >
                              <Checkbox.Control>
                                <Checkbox.Indicator />
                              </Checkbox.Control>
                              <span className="text-sm text-text">
                                {t(`payment.method_${methodKey}`, methodKey)}
                              </span>
                            </Checkbox>
                          ))}
                        </div>
                      </div>
                    );
                  }

                  if (field.type === 'textarea') {
                    return (
                      <HeroTextField fullWidth isRequired={field.required} key={field.key}>
                        <Label>{field.label}</Label>
                        <TextArea
                          placeholder={placeholder}
                          rows={4}
                          value={value}
                          onChange={(e) => setField(field.key, e.target.value)}
                        />
                        {field.description ? (
                          <p className="text-xs text-text-tertiary">{field.description}</p>
                        ) : null}
                      </HeroTextField>
                    );
                  }

                  return (
                    <HeroTextField fullWidth isRequired={field.required} key={field.key}>
                      <Label>{field.label}</Label>
                      <Input
                        placeholder={placeholder}
                        type={field.type === 'password' ? 'password' : field.type === 'number' ? 'number' : 'text'}
                        value={value}
                        onChange={(e) => setField(field.key, e.target.value)}
                      />
                      {field.description ? (
                        <p className="text-xs text-text-tertiary">{field.description}</p>
                      ) : null}
                    </HeroTextField>
                  );
                })}
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              <Button isDisabled={loading} variant="primary" onPress={handleSubmit}>
                {loading ? <Spinner size="sm" /> : null}
                {t('common.save')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

// ==================== 支付配置 Tab ====================

function ConfigTab() {
  const { t } = useTranslation();

  // ----- 充值参数（settings，group="payment"） -----
  const [values, setValues] = useState<Record<string, string>>({});
  const [hasChanges, setHasChanges] = useState(false);

  const { data: settings } = useQuery({
    queryKey: queryKeys.settings(),
    queryFn: () => settingsApi.list(),
  });

  useEffect(() => {
    if (!settings) return;
    const map: Record<string, string> = {};
    for (const s of settings) {
      map[s.key] = s.value;
    }
    setValues(map);
    setHasChanges(false);
  }, [settings]);

  function set(key: string, value: string) {
    setValues((prev) => ({ ...prev, [key]: value }));
    setHasChanges(true);
  }

  function val(key: string): string {
    return values[key] ?? '';
  }

  const saveSettingsMutation = useCrudMutation({
    mutationFn: (items: SettingItem[]) => settingsApi.update({ settings: items }),
    successMessage: t('settings.save_success'),
    queryKey: queryKeys.settings(),
    onSuccess: () => setHasChanges(false),
  });

  function handleSaveSettings() {
    saveSettingsMutation.mutate(
      PAYMENT_SETTING_FIELDS.map(({ key }) => ({ key, value: values[key] ?? '', group: 'payment' })),
    );
  }

  // ----- 支付服务商 -----
  const { data: providersData, isLoading: providersLoading } = useQuery({
    queryKey: queryKeys.paymentProviders(),
    queryFn: () => paymentApi.adminListProviders(),
  });

  const providers = providersData?.providers ?? [];
  const kinds = providersData?.kinds ?? [];
  const kindMap = useMemo(() => new Map(kinds.map((k) => [k.kind, k])), [kinds]);

  // 表单弹窗目标：kind 元信息 +（编辑时）实例
  const [formTarget, setFormTarget] = useState<{ kind: PaymentProviderKindMeta; editing: PaymentProviderItem | null } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<PaymentProviderItem | null>(null);

  const upsertMutation = useCrudMutation<unknown, UpsertPaymentProviderReq>({
    mutationFn: (data) => paymentApi.adminUpsertProvider(data),
    successMessage: t('payment.provider_save_success'),
    queryKey: queryKeys.paymentProviders(),
    onSuccess: () => setFormTarget(null),
  });

  const deleteMutation = useCrudMutation<unknown, string>({
    mutationFn: (id) => paymentApi.adminDeleteProvider(id),
    successMessage: t('payment.provider_delete_success'),
    queryKey: queryKeys.paymentProviders(),
    onSuccess: () => setDeleteTarget(null),
  });

  function openEdit(item: PaymentProviderItem) {
    const kind = kindMap.get(item.kind);
    if (!kind) return;
    setFormTarget({ kind, editing: item });
  }

  return (
    <div className="flex flex-col gap-6">
      {/* 充值参数 */}
      <Card>
        <Card.Header>
          <Card.Title className="flex items-center gap-2">
            <Settings2 className="h-4 w-4 text-primary" />
            {t('payment.settings_title')}
          </Card.Title>
        </Card.Header>
        <Card.Content>
          <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
            {PAYMENT_SETTING_FIELDS.map((field) => (
              <HeroTextField
                className={field.key === 'payment.callback_base_url' ? 'col-span-1 md:col-span-2' : undefined}
                fullWidth
                key={field.key}
              >
                <Label>{t(field.labelKey)}</Label>
                <Input
                  type={field.type}
                  value={val(field.key)}
                  onChange={(e) => set(field.key, e.target.value)}
                />
                {field.hintKey ? (
                  <p className="text-xs text-text-tertiary">{t(field.hintKey)}</p>
                ) : null}
              </HeroTextField>
            ))}
          </div>
          <div className="mt-6 flex justify-end">
            <Button
              aria-busy={saveSettingsMutation.isPending}
              isDisabled={!hasChanges || saveSettingsMutation.isPending}
              onPress={handleSaveSettings}
            >
              <Save className="h-4 w-4" />
              {t('common.save')}
            </Button>
          </div>
        </Card.Content>
      </Card>

      {/* 支付服务商 */}
      <Card>
        <Card.Header>
          <Card.Title className="flex items-center gap-2">
            <Activity className="h-4 w-4 text-primary" />
            {t('payment.providers_title')}
          </Card.Title>
        </Card.Header>
        <Card.Content>
          {providersLoading ? (
            <div className="flex items-center justify-center py-10">
              <Spinner size="sm" />
            </div>
          ) : (
            <div className="space-y-4">
              {/* 按协议类型渲染添加按钮 */}
              <div className="flex flex-wrap gap-2">
                {kinds.map((kind) => (
                  <Button
                    key={kind.kind}
                    size="sm"
                    variant="secondary"
                    onPress={() => setFormTarget({ kind, editing: null })}
                  >
                    <Plus className="h-4 w-4" />
                    {t('payment.add_provider', { name: kind.name })}
                  </Button>
                ))}
              </div>

              {/* 已配置实例卡片 */}
              {providers.length === 0 ? (
                <p className="py-4 text-sm text-text-tertiary">{t('payment.no_providers')}</p>
              ) : (
                <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
                  {providers.map((item) => (
                    <div
                      className="flex items-center justify-between gap-3 rounded-[var(--radius)] border border-border bg-surface p-4"
                      key={item.id}
                    >
                      <div className="min-w-0">
                        <div className="flex min-w-0 items-center gap-2">
                          <span className="truncate font-medium text-text" title={item.id}>
                            {item.name || item.id}
                          </span>
                          <Chip color="default" size="sm" variant="soft">
                            {kindMap.get(item.kind)?.name ?? item.kind}
                          </Chip>
                        </div>
                        <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
                          {item.enabled ? (
                            item.is_running ? (
                              <Chip color="success" size="sm" variant="soft">{t('payment.provider_running')}</Chip>
                            ) : (
                              <Chip color="warning" size="sm" variant="soft">{t('payment.provider_stopped')}</Chip>
                            )
                          ) : (
                            <Chip color="default" size="sm" variant="soft">{t('payment.provider_disabled')}</Chip>
                          )}
                          {item.supported_methods.map((methodKey) => (
                            <span className="text-xs text-text-tertiary" key={methodKey}>
                              {t(`payment.method_${methodKey}`, methodKey)}
                            </span>
                          ))}
                        </div>
                      </div>
                      <div className="flex shrink-0 items-center gap-0.5">
                        <Button
                          isIconOnly
                          aria-label={t('common.edit')}
                          size="sm"
                          variant="secondary"
                          onPress={() => openEdit(item)}
                        >
                          <Pencil className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          isIconOnly
                          aria-label={t('common.delete')}
                          className="text-danger"
                          size="sm"
                          variant="danger-soft"
                          onPress={() => setDeleteTarget(item)}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </Card.Content>
      </Card>

      {/* 新增/编辑服务商弹窗（条件挂载，确保每次打开重置表单态） */}
      {formTarget && (
        <ProviderFormModal
          editing={formTarget.editing}
          kind={formTarget.kind}
          loading={upsertMutation.isPending}
          onClose={() => setFormTarget(null)}
          onSubmit={(req) => upsertMutation.mutate(req)}
        />
      )}

      {/* 删除确认 */}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
        title={t('payment.provider_delete_title')}
        description={t('payment.provider_delete_confirm', { name: deleteTarget?.name || deleteTarget?.id })}
        loading={deleteMutation.isPending}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
      />
    </div>
  );
}

// ==================== 页面入口 ====================

const TABS: { key: TabKey; labelKey: string; icon: typeof ReceiptText }[] = [
  { key: 'orders', labelKey: 'payment.tab_orders', icon: ReceiptText },
  { key: 'config', labelKey: 'payment.tab_config', icon: Settings2 },
];

export default function PaymentPage() {
  const { t } = useTranslation();
  const [activeTab, setActiveTab] = useState<TabKey>('orders');

  return (
    <div className="flex flex-col gap-6">
      <div className="w-full max-w-full overflow-x-auto hide-scrollbar pb-1">
        <Tabs
          className="ag-page-tabs whitespace-nowrap"
          selectedKey={activeTab}
          onSelectionChange={(key) => setActiveTab(key as TabKey)}
        >
          <Tabs.List>
            {TABS.map((tab, index) => {
              const Icon = tab.icon;
              return (
                <Tabs.Tab id={tab.key} key={tab.key}>
                  {index > 0 ? <Tabs.Separator /> : null}
                  <Tabs.Indicator />
                  <Icon className="h-4 w-4" />
                  <span>{t(tab.labelKey)}</span>
                </Tabs.Tab>
              );
            })}
          </Tabs.List>
        </Tabs>
      </div>

      {activeTab === 'orders' && <OrdersTab />}
      {activeTab === 'config' && <ConfigTab />}
    </div>
  );
}
