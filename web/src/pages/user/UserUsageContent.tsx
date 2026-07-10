import { useCallback, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Button, Card, ListBox, Meter, Select, Tabs } from '@heroui/react';
import { usageApi } from '../../shared/api/usage';
import { apikeysApi } from '../../shared/api/apikeys';
import { queryKeys } from '../../shared/queryKeys';
import { usePagination } from '../../shared/hooks/usePagination';
import { useAuth } from '../../app/providers/AuthProvider';
import { useToast } from '../../shared/ui';
import { Activity, Hash, Coins, Clock, Gauge, Percent, Upload } from 'lucide-react';
import type { UsageQuery } from '../../shared/types';
import { useUsageColumns, type UsageColumnConfig, type UsageRow } from '../../shared/columns/usageColumns';
import { getSessionAPIKey } from '../../shared/api/client';
import { CcsImportModal } from './userkeys/CcsImportModal';
import { fmtNum, fmtRate, formatDate } from '../../shared/utils/format';
import { StatCard } from '../../shared/components/StatCard';
import { UsageRecordsTable } from '../../shared/components/UsageRecordsTable';
import { UserUpstreamLogsTable } from './UserUpstreamLogsTable';
import { UsageDateRangeFilter } from '../../shared/components/UsageDateRangeFilter';
import { UsageModelFilterInput } from '../../shared/components/UsageModelFilterInput';
import { CostValue } from '../../shared/components/CostValue';
import { AutoRefreshControl } from '../../shared/components/AutoRefreshControl';
import { FETCH_ALL_PARAMS } from '../../shared/constants';
import { USER_AUTO_REFRESH_OPTIONS, usePersistentAutoRefresh } from '../../shared/hooks/usePersistentAutoRefresh';

const USER_USAGE_AUTO_UPDATE_STORAGE_KEY = 'airgate.user.usage.auto_update';

function APIKeyInfoBar() {
  const { t } = useTranslation();
  const { user } = useAuth();
  const { toast } = useToast();
  const [ccsOpen, setCcsOpen] = useState(false);
  if (!user?.api_key_id) return null;

  const quota = user.api_key_quota_usd ?? 0;
  const used = user.api_key_used_quota ?? 0;
  const expiresAt = user.api_key_expires_at;
  const pct = quota > 0 ? Math.min((used / quota) * 100, 100) : 0;

  // 原文 Key 仅在 API Key 登录当次会话内保存在内存变量中；刷新页面后丢失，
  // 此时按钮会提示用户重新登录。
  const sessionKey = getSessionAPIKey();
  const canImportCcs = !!sessionKey;

  function handleImportCcs() {
    if (!sessionKey) {
      toast('error', t('user_keys.ccs_session_expired'));
      return;
    }
    setCcsOpen(true);
  }

  // 后端已经把"销售倍率优先、否则分组倍率"折算成单一字段 api_key_rate，
  // 前端拿不到原始来源，避免通过 DevTools 推断 reseller 定价模型。
  const effectiveRate = user.api_key_rate ?? 0;

  // 到期时间格式化
  let expiresLabel = '';
  let expiresWarning = false;
  if (expiresAt) {
    const d = new Date(expiresAt);
    const now = new Date();
    const diffDays = Math.ceil((d.getTime() - now.getTime()) / 86400000);
    expiresLabel = formatDate(d);
    expiresWarning = diffDays <= 7;
  }

  return (
    <Card className="mb-5">
      <Card.Content className="flex items-center gap-4 px-4 py-3 text-sm flex-wrap">
        {quota > 0 && (
          <div className="flex items-center gap-2">
            <Gauge className="w-3.5 h-3.5 text-text-tertiary" />
            <span className="text-text-tertiary">{t('auth.apikey_quota')}:</span>
            <span className={pct >= 90 ? 'text-danger font-medium' : 'text-text-secondary'}>
              ${used.toFixed(4)} / ${quota.toFixed(2)}
            </span>
            <Meter
              aria-label={t('auth.apikey_quota')}
              className="w-20"
              color={pct >= 90 ? 'danger' : pct >= 70 ? 'warning' : 'accent'}
              maxValue={100}
              minValue={0}
              size="sm"
              value={pct}
            >
              <Meter.Track>
                <Meter.Fill />
              </Meter.Track>
            </Meter>
          </div>
        )}

        {quota === 0 && (
          <div className="flex items-center gap-2 text-text-tertiary">
            <Gauge className="w-3.5 h-3.5" />
            <span>{t('auth.apikey_quota')}: {t('auth.apikey_unlimited')}</span>
          </div>
        )}

        {expiresAt && (
          <div className="flex items-center gap-2">
            <Clock className="w-3.5 h-3.5 text-text-tertiary" />
            <span className="text-text-tertiary">{t('auth.apikey_expires')}:</span>
            <span className={expiresWarning ? 'text-warning font-medium' : 'text-text-secondary'}>
              {expiresLabel}
            </span>
          </div>
        )}

        {!expiresAt && (
          <div className="flex items-center gap-2 text-text-tertiary">
            <Clock className="w-3.5 h-3.5" />
            <span>{t('auth.apikey_expires')}: {t('auth.apikey_never')}</span>
          </div>
        )}

        {effectiveRate > 0 && (
          <div className="flex items-center gap-2">
            <Percent className="w-3.5 h-3.5 text-text-tertiary" />
            <span className="text-text-tertiary">{t('auth.apikey_rate', '倍率')}:</span>
            <span className="text-text-secondary font-mono">{fmtRate(effectiveRate)}</span>
          </div>
        )}

        <div className="ml-auto flex items-center gap-2">
          {/* 原文 Key 只存内存，页面刷新后丢失；禁用时给出可见解释而不是只变灰 */}
          {!canImportCcs && (
            <span className="text-xs text-text-tertiary">{t('user_keys.ccs_relogin_hint')}</span>
          )}
          <Button
            type="button"
            onPress={handleImportCcs}
            isDisabled={!canImportCcs}
            size="sm"
            variant="outline"
          >
            <Upload className="w-3.5 h-3.5" />
            <span>{t('user_keys.import_ccs')}</span>
          </Button>
        </div>

        <CcsImportModal
          open={ccsOpen}
          ccsKeyValue={sessionKey}
          onClose={() => setCcsOpen(false)}
        />
      </Card.Content>
    </Card>
  );
}

export default function UserUsageContent() {
  const { t } = useTranslation();
  const { user } = useAuth();
  const customerScope = !!user?.api_key_id;
  const { page, setPage, pageSize, setPageSize } = usePagination(20, 'user.usage');
  const [filters, setFilters] = useState<Partial<UsageQuery>>({});
  // 记录区 Tab：消费记录 | 失败请求（共享筛选；失败请求由后端脱敏）。
  const [recordsTab, setRecordsTab] = useState<'usage' | 'upstream'>('usage');
  const [autoRefresh, setAutoRefresh] = usePersistentAutoRefresh(USER_USAGE_AUTO_UPDATE_STORAGE_KEY, 0, USER_AUTO_REFRESH_OPTIONS);
  const autoRefreshEnabled = autoRefresh > 0;
  const autoRefreshLabel = `${t('usage.auto_update')} `;
  const autoRefreshOffLabel = t('usage.auto_update_off');

  const handleModelChange = useCallback((model: string) => {
    const nextModel = model || undefined;
    setPage(1);
    setFilters((prev) => (prev.model === nextModel ? prev : { ...prev, model: nextModel }));
  }, [setPage]);

  const queryParams = useMemo<UsageQuery>(() => ({
    page,
    page_size: pageSize,
    ...filters,
  }), [filters, page, pageSize]);

  const { data: apiKeysData } = useQuery({
    queryKey: queryKeys.userKeys('usage-filter'),
    queryFn: () => apikeysApi.list(FETCH_ALL_PARAMS),
    enabled: !customerScope,
  });
  const apiKeyOptions = [
    { id: '', label: t('common.all') },
    ...(apiKeysData?.list ?? []).map((key) => ({ id: String(key.id), label: key.name })),
  ];
  const selectedApiKeyLabel = apiKeyOptions.find((item) => item.id === String(filters.api_key_id ?? ''))?.label ?? t('common.all');

  const {
    data,
    dataUpdatedAt,
    isFetching: isUsageFetching,
    isLoading,
    isPlaceholderData,
    refetch: refetchUsage,
  } = useQuery({
    queryKey: queryKeys.userUsage(queryParams),
    queryFn: ({ signal }) => usageApi.list(queryParams, { signal }),
    meta: { globalLoading: false },
    refetchOnReconnect: autoRefreshEnabled,
    refetchOnWindowFocus: autoRefreshEnabled,
    placeholderData: keepPreviousData,
  });

  // 聚合统计（跟随筛选条件，独立于分页）
  const { data: stats, isFetching: isStatsFetching, refetch: refetchStats } = useQuery({
    queryKey: queryKeys.userUsageStats(filters),
    queryFn: ({ signal }) => usageApi.userStats(filters, { signal }),
    meta: { globalLoading: false },
    refetchOnReconnect: false,
    refetchOnWindowFocus: false,
  });

  const isRefreshing = isUsageFetching || isStatsFetching;
  const isUsageTableRefreshing = isUsageFetching;

  const handleManualRefresh = useCallback(() => {
    void refetchUsage({ cancelRefetch: false });
    void refetchStats({ cancelRefetch: false });
  }, [refetchStats, refetchUsage]);

  const handleAutoRefresh = useCallback(() => {
    void refetchUsage({ cancelRefetch: false });
  }, [refetchUsage]);

  function updateFilter(key: string, value: string) {
    const nextValue = key === 'api_key_id' && value ? Number(value) : value || undefined;
    setFilters((prev) => ({ ...prev, [key]: nextValue }));
    setPage(1);
  }

  const list = data?.list ?? [];
  const total = data?.total ?? 0;
  const visibleActualCost = customerScope ? (stats?.total_billed_cost ?? 0) : (stats?.total_actual_cost ?? 0);

  const sharedColumns = useUsageColumns({ customerScope, adminView: false });
  // columns 必须 memoize：UsageRecordsTable 的行组件按 columns 引用做 memo，
  // 每次渲染重建数组会击穿行级 memo，自动刷新时整表 20 行全量重渲染。
  const columns = useMemo(() => {
    const modelColumnIndex = sharedColumns.findIndex((column) => column.key === 'model');
    const timeColumnIndex = sharedColumns.findIndex((column) => column.key === 'created_at');
    const streamColumn = sharedColumns.find((column) => column.key === 'stream');
    const timingColumns = sharedColumns.filter((column) => column.key === 'first_token_ms' || column.key === 'duration_ms');
    const sharedColumnsAfterModel = sharedColumns
      .slice(modelColumnIndex + 1)
      .filter((column) => column.key !== 'first_token_ms' && column.key !== 'duration_ms' && column.key !== 'stream');
    const endpointColumn: UsageColumnConfig<UsageRow> = {
      key: 'endpoint',
      title: t('usage.endpoint', '端点'),
      width: '180px',
      hideOnMobile: true,
      render: (row) => {
        const endpoint = 'endpoint' in row && row.endpoint ? row.endpoint : '-';

        return (
          <span className="block truncate font-mono text-xs leading-tight text-text-secondary" title={endpoint}>
            {endpoint}
          </span>
        );
      },
    };
    const apiKeyColumn: UsageColumnConfig<UsageRow> = {
      key: 'api_key',
      title: 'API Key',
      width: '96px',
      hideOnMobile: true,
      render: (row) => {
        if ('api_key_deleted' in row && row.api_key_deleted) {
          return <span className="block max-w-full truncate text-[13px] text-text-tertiary">{t('usage.api_key_deleted')}</span>;
        }

        const name = 'api_key_name' in row && row.api_key_name ? row.api_key_name : '-';

        return (
          <span className="block max-w-full truncate text-xs text-text-secondary" title={name}>{name}</span>
        );
      },
    };

    return modelColumnIndex >= 0
      ? [
          ...sharedColumns.slice(0, timeColumnIndex + 1),
          ...(customerScope ? [] : [apiKeyColumn]),
          ...sharedColumns.slice(timeColumnIndex + 1, modelColumnIndex + 1),
          ...(streamColumn ? [streamColumn] : []),
          ...timingColumns,
          ...sharedColumnsAfterModel,
          endpointColumn,
        ]
      : [
          ...sharedColumns,
          endpointColumn,
          ...(customerScope ? [] : [apiKeyColumn]),
        ];
  }, [sharedColumns, customerScope, t]);

  return (
    <div>
      {/* API Key 登录信息 */}
      <APIKeyInfoBar />

      {/* 概览统计 */}
      <div className="mb-6 grid grid-cols-1 gap-3 md:grid-cols-3 xl:grid-cols-3 2xl:gap-4">
        <StatCard
          title={t('usage.total_requests')}
          value={(stats?.total_requests ?? 0).toLocaleString()}
          icon={<Activity className="w-5 h-5" />}
          accentColor="var(--ag-primary)"
        />
        <StatCard
          title={t('usage.total_tokens')}
          value={fmtNum(stats?.total_tokens ?? 0)}
          icon={<Hash className="w-5 h-5" />}
          accentColor="var(--ag-info)"
        />
        <StatCard
          title={t('usage.cost')}
          value={customerScope ? (
            // end customer 只能看到自己的账面消费，不暴露标准价
            <CostValue value={visibleActualCost} decimals={4} tone="actual" />
          ) : (
            <span
              className="inline-flex min-w-0 items-baseline gap-1.5"
              title={`${t('usage.actual_cost')} / ${t('usage.standard_cost')}`}
            >
              <CostValue value={visibleActualCost} decimals={4} tone="actual" />
              <span className="text-sm text-text-tertiary">/</span>
              <span className="text-sm opacity-70">
                <CostValue value={stats?.total_cost ?? 0} decimals={4} tone="standard" />
              </span>
            </span>
          )}
          icon={<Coins className="w-5 h-5" />}
          accentColor="var(--ag-warning)"
        />
      </div>

      {/* 筛选栏 */}
      <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-3 mb-5 flex-wrap">
        <div className="w-full sm:w-64">
          <UsageDateRangeFilter
            clearLabel={t('common.clear')}
            endDate={filters.end_date}
            label={t('usage.time_range')}
            startDate={filters.start_date}
            onChange={(startDate, endDate) => {
              setPage(1);
              setFilters((prev) => ({ ...prev, start_date: startDate, end_date: endDate }));
            }}
          />
        </div>
        {!customerScope && (
          <div className="w-full sm:w-48">
            <Select
              aria-label="API Key"
              fullWidth
              selectedKey={String(filters.api_key_id ?? '')}
              onSelectionChange={(key) => updateFilter('api_key_id', key == null ? '' : String(key))}
            >
              <Select.Trigger>
                <Select.Value>
                  {filters.api_key_id ? selectedApiKeyLabel : (
                    <span className="text-text-tertiary">API Key</span>
                  )}
                </Select.Value>
                <Select.Indicator />
              </Select.Trigger>
              <Select.Popover>
                <ListBox items={apiKeyOptions}>
                  {(item) => (
                    <ListBox.Item id={item.id} textValue={item.label}>
                      {item.label}
                    </ListBox.Item>
                  )}
                </ListBox>
              </Select.Popover>
            </Select>
          </div>
        )}
        <div className="w-full sm:w-48">
          <UsageModelFilterInput
            ariaLabel={t('usage.model', 'Model')}
            placeholder={t('usage.model_placeholder')}
            value={filters.model ?? ''}
            onModelChange={handleModelChange}
          />
        </div>
        <AutoRefreshControl
          value={autoRefresh}
          options={USER_AUTO_REFRESH_OPTIONS}
          label={autoRefreshLabel}
          offLabel={autoRefreshOffLabel}
          ariaLabel={t('usage.auto_update')}
          refreshAriaLabel={t('common.refresh', 'Refresh')}
          onChange={setAutoRefresh}
          onAutoRefresh={handleAutoRefresh}
          onRefresh={handleManualRefresh}
          isAutoRefreshing={isUsageTableRefreshing}
          isRefreshing={isRefreshing}
        />
      </div>

      {/* 记录区：消费记录 | 失败请求 */}
      <Tabs
        className="ag-segmented-tabs ag-segmented-tabs-compact mb-3"
        selectedKey={recordsTab}
        onSelectionChange={(key) => setRecordsTab(key as 'usage' | 'upstream')}
      >
        <Tabs.List>
          <Tabs.Tab id="usage">
            <Tabs.Indicator />
            {t('usage.records_tab_usage')}
          </Tabs.Tab>
          <Tabs.Tab id="upstream">
            <Tabs.Indicator />
            {t('usage.records_tab_upstream')}
          </Tabs.Tab>
        </Tabs.List>
      </Tabs>

      {recordsTab === 'upstream' && (
        <UserUpstreamLogsTable
          filters={{
            start_date: filters.start_date,
            end_date: filters.end_date,
            model: filters.model,
            api_key_id: filters.api_key_id,
          }}
        />
      )}

      {recordsTab === 'usage' && (
      <UsageRecordsTable
        ariaLabel={t('usage.title', 'Usage')}
        columns={columns}
        dataVersion={dataUpdatedAt}
        emptyAction={filters.start_date || filters.end_date ? (
          <Button
            size="sm"
            variant="secondary"
            onPress={() => {
              setPage(1);
              setFilters((prev) => ({ ...prev, start_date: undefined, end_date: undefined }));
            }}
          >
            {t('usage.view_all_time')}
          </Button>
        ) : undefined}
        emptyDescription={filters.start_date || filters.end_date
          ? t('usage.empty_in_range')
          : t('usage.empty_description')}
        emptyTitle={t('common.no_data')}
        highlightNewRows={autoRefreshEnabled && page === 1}
        highlightResetKey={JSON.stringify({ ...filters, page, pageSize })}
        isLoading={isLoading}
        page={page}
        pageSize={pageSize}
        rows={list}
        setPage={setPage}
        setPageSize={setPageSize}
        suppressHighlight={isPlaceholderData}
        total={total}
      />
      )}
    </div>
  );
}
