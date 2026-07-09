import { lazy, Suspense, useCallback, useMemo, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Button, Card, Chip, ComboBox, Input, ListBox, Tabs } from '@heroui/react';
import { usageApi } from '../../shared/api/usage';
import { usersApi } from '../../shared/api/users';
import { apikeysApi } from '../../shared/api/apikeys';
import { usePagination } from '../../shared/hooks/usePagination';
import { useDebouncedValue } from '../../shared/hooks/useDebouncedValue';
import { useDeferredActivation } from '../../shared/hooks/useDeferredActivation';
import { queryKeys } from '../../shared/queryKeys';
import { UpstreamLogsTable } from './usage/UpstreamLogsTable';
import { Activity, Coins, Hash, Search } from 'lucide-react';
import { useUsageColumns, fmtNum, type UsageColumnConfig } from '../../shared/columns/usageColumns';
import type { APIKeyResp, UsageLogResp, UsageQuery, UsageTrendBucket } from '../../shared/types';
import { CompactDataTable } from '../../shared/components/CompactDataTable';
import { UsageRecordsTable } from '../../shared/components/UsageRecordsTable';
import { UsageDateRangeFilter } from '../../shared/components/UsageDateRangeFilter';
import { UsageModelFilterInput } from '../../shared/components/UsageModelFilterInput';
import { PIE_CHART_COLORS } from '../../shared/constants';
import { CostValue } from '../../shared/components/CostValue';
import { AutoRefreshControl } from '../../shared/components/AutoRefreshControl';
import { ADMIN_AUTO_REFRESH_OPTIONS, usePersistentAutoRefresh } from '../../shared/hooks/usePersistentAutoRefresh';

const UsagePieChart = lazy(() =>
  import('./usage/UsageCharts').then((m) => ({ default: m.UsagePieChart })),
);
const UsageTokenTrendChart = lazy(() =>
  import('./usage/UsageCharts').then((m) => ({ default: m.UsageTokenTrendChart })),
);

const PIE_COLORS = PIE_CHART_COLORS;

function SectionCard({
  children,
  extra,
  title,
}: {
  children: ReactNode;
  extra?: ReactNode;
  title: string;
}) {
  return (
    <Card className="ag-dashboard-panel">
      <div
        className="flex min-w-0 items-center justify-between gap-3 p-3 pb-2 2xl:p-4 2xl:pb-2"
      >
        <h3 className="min-w-0 truncate text-base font-semibold leading-none text-text">{title}</h3>
        {extra ? (
          <div className="min-w-0 shrink">{extra}</div>
        ) : null}
      </div>
      <Card.Content className="px-3 pb-3 2xl:px-4 2xl:pb-4">{children}</Card.Content>
    </Card>
  );
}

function StatCard({
  accentColor,
  icon,
  title,
  value,
}: {
  accentColor: string;
  icon: ReactNode;
  title: string;
  value: ReactNode;
}) {
  return (
    <Card className="ag-dashboard-metric min-h-[72px] 2xl:min-h-[78px]">
      <Card.Content className="ag-dashboard-metric-content p-3 2xl:p-3.5">
        <div className="ag-dashboard-metric-copy">
          <div className="truncate text-sm font-semibold tracking-normal text-text-tertiary">{title}</div>
          <div className="mt-1 flex min-w-0 items-baseline gap-2">
            <div className="min-w-0 truncate font-mono text-[22px] font-semibold leading-none text-text 2xl:text-2xl">{value}</div>
          </div>
        </div>
        <div
          className="flex h-10 w-10 shrink-0 items-center justify-center rounded-[var(--field-radius)] ring-1 shadow-sm 2xl:h-11 2xl:w-11"
          style={{
            background: `color-mix(in srgb, ${accentColor} 14%, transparent)`,
            color: accentColor,
            borderColor: `color-mix(in srgb, ${accentColor} 24%, transparent)`,
          }}
        >
          {icon}
        </div>
      </Card.Content>
    </Card>
  );
}

// 分组统计 key 映射
const groupByKeys: Record<string, string> = {
  model: 'usage.by_model',
  user: 'usage.by_user',
  channel: 'usage.by_channel',
  group: 'usage.by_group',
};

const groupByHeaderKeys: Record<string, string> = {
  model: 'usage.model',
  user: 'usage.user_id',
  channel: 'usage.channel',
  group: 'usage.by_group',
};

const ADMIN_USAGE_STATS_GROUP_BY = 'model,group,channel,user';
const USAGE_PAGE_ACTIVATION_DELAY_MS = 180;
const ADMIN_USAGE_AUTO_UPDATE_STORAGE_KEY = 'airgate.admin.usage.auto_update';

// ==================== 分布饼图卡片 ====================

type PieMetric = 'token' | 'cost';

interface DistributionItem {
  name: string;
  requests: number;
  tokens: number;
  totalCost: number;
  actualCost: number;
}

function DistributionCard({
  title,
  data,
  firstColumnTitle,
  firstColumnWidth = '30%',
}: {
  title: string;
  data: DistributionItem[];
  firstColumnTitle: string;
  firstColumnWidth?: string;
}) {
  const { t } = useTranslation();
  const [metric, setMetric] = useState<PieMetric>('token');

  const pieData = useMemo(
    () => data.map((d) => ({
      name: d.name,
      value: metric === 'token' ? d.tokens : d.actualCost,
    })),
    [data, metric],
  );
  const metricTabs = (
    <Tabs className="ag-segmented-tabs ag-segmented-tabs-compact" selectedKey={metric} onSelectionChange={(key) => setMetric(key as PieMetric)}>
      <Tabs.List>
        <Tabs.Tab id="token">
          <Tabs.Indicator />
          <span>{t('usage.by_token')}</span>
        </Tabs.Tab>
        <Tabs.Tab id="cost">
          <Tabs.Separator />
          <Tabs.Indicator />
          <span>{t('usage.by_actual_cost')}</span>
        </Tabs.Tab>
      </Tabs.List>
    </Tabs>
  );

  return (
    <SectionCard title={title} extra={metricTabs}>
      <div className="ag-distribution-card-body grid items-start gap-3 2xl:grid-cols-[176px_minmax(0,1fr)]">
        <div className="ag-distribution-chart-frame">
          <Suspense fallback={<div className="h-[176px] w-[176px]" />}>
            <UsagePieChart data={pieData} />
          </Suspense>
        </div>

        <div className="ag-distribution-table-scroll">
          <CompactDataTable
            ariaLabel={title}
            className="ag-compact-data-table--dense"
            emptyText={t('common.no_data')}
            minWidth={480}
            rowKey={(row) => row.name}
            rows={data}
            columns={[
              {
                key: 'name',
                title: firstColumnTitle,
                width: firstColumnWidth,
                render: (item, index) => (
                  <>
                    <span className="shrink-0 font-mono text-[11px] font-semibold text-text-tertiary">#{index + 1}</span>
                    <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: PIE_COLORS[index % PIE_COLORS.length] }} />
                    <span className="min-w-0 truncate font-medium text-text" title={item.name}>{item.name}</span>
                  </>
                ),
              },
              {
                align: 'end',
                key: 'requests',
                title: t('usage.requests'),
                width: '16%',
                render: (item) => <span className="truncate font-mono text-text-secondary">{item.requests.toLocaleString()}</span>,
              },
              {
                align: 'end',
                key: 'tokens',
                title: t('usage.tokens'),
                width: '18%',
                render: (item) => <span className="truncate font-mono text-text-secondary">{fmtNum(item.tokens)}</span>,
              },
              {
                align: 'end',
                key: 'cost',
                title: t('usage.cost'),
                width: '30%',
                render: (item) => (
                  <span
                    className="inline-flex min-w-0 items-baseline gap-1 truncate font-mono"
                    title={`${t('usage.actual_cost')} / ${t('usage.standard_cost')}`}
                  >
                    <CostValue value={item.actualCost} tone="actual" />
                    <span className="text-text-tertiary">/</span>
                    <span className="opacity-70"><CostValue value={item.totalCost} tone="standard" /></span>
                  </span>
                ),
              },
            ]}
          />
        </div>
      </div>
    </SectionCard>
  );
}

type GroupStatsRow = {
  key: string | number;
  name: string;
  requests: number;
  tokens: number;
  total_cost: number;
  actual_cost: number;
};

function GroupStatsCard({
  activeKey,
  rows,
  onActiveKeyChange,
}: {
  activeKey: string;
  rows: GroupStatsRow[];
  onActiveKeyChange: (key: string) => void;
}) {
  const { t } = useTranslation();

  return (
    <SectionCard
      title={t('usage.group_stats')}
      extra={
        <Tabs
          className="ag-segmented-tabs ag-segmented-tabs-compact ag-segmented-tabs-auto"
          selectedKey={activeKey}
          onSelectionChange={(key) => {
            const nextKey = String(key);
            if (nextKey !== activeKey) {
              onActiveKeyChange(nextKey);
            }
          }}
        >
          <Tabs.List>
            {Object.entries(groupByKeys).map(([key, i18nKey], index) => (
              <Tabs.Tab id={key} key={key}>
                {index > 0 ? <Tabs.Separator /> : null}
                <Tabs.Indicator />
                <span>{t(i18nKey)}</span>
              </Tabs.Tab>
            ))}
          </Tabs.List>
        </Tabs>
      }
    >
      <div className="h-[248px] min-w-0 overflow-auto 2xl:h-[288px]">
        <CompactDataTable
          ariaLabel={t('usage.group_stats')}
          className="ag-compact-data-table--dense"
          emptyText={t('common.no_data')}
          minWidth={520}
          rowKey={(row) => row.key}
          rows={rows}
          columns={[
            {
              key: 'name',
              title: t(groupByHeaderKeys[activeKey] ?? 'usage.model'),
              width: '30%',
              render: (row, index) => (
                <>
                  <span className="shrink-0 font-mono text-[11px] font-semibold text-text-tertiary">#{index + 1}</span>
                  <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: PIE_COLORS[index % PIE_COLORS.length] }} />
                  <span className="min-w-0 truncate font-medium text-text" title={row.name}>{row.name}</span>
                </>
              ),
            },
            {
              align: 'end',
              key: 'requests',
              title: t('usage.requests'),
              width: '16%',
              render: (row) => <span className="truncate font-mono text-text-secondary">{row.requests.toLocaleString()}</span>,
            },
            {
              align: 'end',
              key: 'tokens',
              title: t('usage.tokens'),
              width: '18%',
              render: (row) => <span className="truncate font-mono text-text-secondary">{fmtNum(row.tokens)}</span>,
            },
            {
              align: 'end',
              key: 'cost',
              title: t('usage.cost'),
              width: '30%',
              render: (row) => (
                <span
                  className="inline-flex min-w-0 items-baseline gap-1 truncate font-mono"
                  title={`${t('usage.actual_cost')} / ${t('usage.standard_cost')}`}
                >
                  <CostValue value={row.actual_cost} tone="actual" />
                  <span className="text-text-tertiary">/</span>
                  <span className="opacity-70"><CostValue value={row.total_cost} tone="standard" /></span>
                </span>
              ),
            },
          ]}
        />
      </div>
    </SectionCard>
  );
}

// ==================== Token 使用趋势 ====================

function TokenTrendCard({
  data,
  granularity,
  onGranularityChange,
}: {
  data: UsageTrendBucket[];
  granularity: string;
  onGranularityChange: (g: string) => void;
}) {
  const { t } = useTranslation();

  const lineLabels: Record<string, string> = {
    input: t('usage.input'),
    output: t('usage.output'),
    cacheCreation: t('usage.cache_creation'),
    cacheRead: t('usage.cache_read'),
    cacheRatio: t('usage.cache_ratio'),
    cacheCumulativeRatio: t('usage.cache_cumulative_ratio'),
  };
  const granularityTabs = (
    <Tabs className="ag-segmented-tabs ag-segmented-tabs-compact" selectedKey={granularity} onSelectionChange={(key) => onGranularityChange(String(key))}>
      <Tabs.List>
        {(['hour', 'day'] as const).map((g, index) => (
          <Tabs.Tab id={g} key={g}>
            {index > 0 ? <Tabs.Separator /> : null}
            <Tabs.Indicator />
            <span>{t(`usage.granularity_${g}`)}</span>
          </Tabs.Tab>
        ))}
      </Tabs.List>
    </Tabs>
  );

  if (data.length === 0) {
    return (
      <SectionCard title={t('usage.token_trend')} extra={granularityTabs}>
        <div className="flex h-[248px] items-center justify-center text-sm text-text-tertiary 2xl:h-[288px]">
          {t('common.no_data')}
        </div>
      </SectionCard>
    );
  }

  return (
    <SectionCard
      title={t('usage.token_trend')}
      extra={granularityTabs}
    >
      <div className="h-[248px] 2xl:h-[288px]">
        <Suspense fallback={<div className="h-full w-full" />}>
          <UsageTokenTrendChart data={data} lineLabels={lineLabels} />
        </Suspense>
      </div>
    </SectionCard>
  );
}

// ==================== 主页面 ====================

export default function UsagePage() {
  const { t } = useTranslation();
  const { page, setPage, pageSize, setPageSize } = usePagination(20, 'admin.usage');
  const [filters, setFilters] = useState<Partial<UsageQuery>>({});
  // 记录区 Tab：消费记录（usage_logs）| 失败请求（上游请求日志），共享筛选。
  const [recordsTab, setRecordsTab] = useState<'usage' | 'upstream'>('usage');
  const [statsGroupBy, setStatsGroupBy] = useState<string>('model');
  const [granularity, setGranularity] = useState<string>('hour');
  const [autoRefresh, setAutoRefresh] = usePersistentAutoRefresh(ADMIN_USAGE_AUTO_UPDATE_STORAGE_KEY, 0, ADMIN_AUTO_REFRESH_OPTIONS);
  const pageActive = useDeferredActivation(USAGE_PAGE_ACTIVATION_DELAY_MS);
  const autoRefreshEnabled = autoRefresh > 0;
  const autoRefreshLabel = `${t('usage.auto_update')} `;
  const autoRefreshOffLabel = t('usage.auto_update_off');

  const handleModelChange = useCallback((model: string) => {
    const nextModel = model || undefined;
    setPage(1);
    setFilters((prev) => (prev.model === nextModel ? prev : { ...prev, model: nextModel }));
  }, [setPage]);

  // 用户搜索
  const [userKeyword, setUserKeyword] = useState('');
  const debouncedUserKeyword = useDebouncedValue(userKeyword.trim(), 250);
  const [selectedUserLabel, setSelectedUserLabel] = useState('');
  const { data: usersData } = useQuery({
    queryKey: queryKeys.adminUsersSearch(debouncedUserKeyword),
    queryFn: () => usersApi.list({ page: 1, page_size: 20, keyword: debouncedUserKeyword }),
    enabled: pageActive && debouncedUserKeyword.length > 0,
  });
  const userOptions = (usersData?.list ?? []).map((u) => ({
    id: String(u.id),
    label: u.username || u.email,
    description: u.username ? u.email : undefined,
    textValue: `${u.username || ''} ${u.email}`,
  }));
  const visibleUserOptions = (() => {
    const selectedId = filters.user_id ? String(filters.user_id) : '';
    if (!selectedId || !selectedUserLabel || userOptions.some((option) => option.id === selectedId)) {
      return userOptions;
    }
    return [
      {
        id: selectedId,
        label: selectedUserLabel,
        description: undefined,
        textValue: selectedUserLabel,
      },
      ...userOptions,
    ];
  })();

  // API Key 搜索：防抖 + 服务端分页，只取前 20 条候选，避免全量加载大量 key。
  const [apiKeyKeyword, setAPIKeyKeyword] = useState('');
  const debouncedAPIKeyKeyword = useDebouncedValue(apiKeyKeyword.trim(), 250);
  const [selectedAPIKeyLabel, setSelectedAPIKeyLabel] = useState('');
  const { data: apiKeysData } = useQuery({
    queryKey: queryKeys.adminApiKeysSearch('api_key', debouncedAPIKeyKeyword),
    queryFn: ({ signal }) => apikeysApi.adminList({ page: 1, page_size: 20, keyword: debouncedAPIKeyKeyword, search_scope: 'api_key' }, { signal }),
    enabled: pageActive && debouncedAPIKeyKeyword.length > 0,
  });
  const apiKeyOptions = (apiKeysData?.list ?? []).map((key: APIKeyResp) => ({
    id: String(key.id),
    label: key.name || key.key_prefix || `#${key.id}`,
    description: [
      `#${key.id}`,
      key.key_prefix,
      key.user_id ? `User #${key.user_id}` : '',
    ].filter(Boolean).join(' · '),
    textValue: `${key.name || ''} ${key.key_prefix || ''} ${key.id || ''}`,
  }));
  const visibleAPIKeyOptions = (() => {
    const selectedId = filters.api_key_id ? String(filters.api_key_id) : '';
    if (!selectedId || !selectedAPIKeyLabel || apiKeyOptions.some((option) => option.id === selectedId)) {
      return apiKeyOptions;
    }
    return [
      {
        id: selectedId,
        label: selectedAPIKeyLabel,
        description: undefined,
        textValue: selectedAPIKeyLabel,
      },
      ...apiKeyOptions,
    ];
  })();

  // 构建查询参数
  const queryParams = useMemo<UsageQuery>(() => ({
    page,
    page_size: pageSize,
    ...filters,
  }), [filters, page, pageSize]);

  // 使用记录列表
  const {
    data,
    dataUpdatedAt,
    isFetching: isUsageFetching,
    isLoading,
    isPlaceholderData,
    refetch: refetchUsage,
  } = useQuery({
    queryKey: queryKeys.adminUsage(queryParams),
    queryFn: ({ signal }) => usageApi.adminList(queryParams, { signal }),
    meta: { globalLoading: false },
    enabled: pageActive,
    refetchOnReconnect: autoRefreshEnabled,
    refetchOnWindowFocus: autoRefreshEnabled,
    placeholderData: keepPreviousData,
  });

  const { data: stats, isFetching: isStatsFetching, refetch: refetchStats } = useQuery({
    queryKey: queryKeys.adminUsageStats(filters.start_date, filters.end_date, filters.model, filters.user_id, filters.api_key_id),
    queryFn: ({ signal }) =>
      usageApi.stats({
        group_by: ADMIN_USAGE_STATS_GROUP_BY,
        start_date: filters.start_date,
        end_date: filters.end_date,
        model: filters.model,
        user_id: filters.user_id ? Number(filters.user_id) : undefined,
        api_key_id: filters.api_key_id ? Number(filters.api_key_id) : undefined,
      }, { signal }),
    meta: { globalLoading: false },
    enabled: pageActive,
    refetchOnReconnect: false,
    refetchOnWindowFocus: false,
    placeholderData: keepPreviousData,
  });

  // Token 趋势
  const { data: trendData, isFetching: isTrendFetching, refetch: refetchTrend } = useQuery({
    queryKey: queryKeys.adminUsageTrend(granularity, filters.start_date, filters.end_date, filters.model, filters.user_id, filters.api_key_id),
    queryFn: ({ signal }) =>
      usageApi.trend({
        granularity,
        start_date: filters.start_date,
        end_date: filters.end_date,
        model: filters.model,
        user_id: filters.user_id ? Number(filters.user_id) : undefined,
        api_key_id: filters.api_key_id ? Number(filters.api_key_id) : undefined,
      }, { signal }),
    meta: { globalLoading: false },
    enabled: pageActive,
    refetchOnReconnect: false,
    refetchOnWindowFocus: false,
    placeholderData: keepPreviousData,
  });

  const isRefreshing = pageActive && (isUsageFetching || isStatsFetching || isTrendFetching);
  const isUsageTableRefreshing = pageActive && isUsageFetching;

  const handleManualRefresh = useCallback(() => {
    if (!pageActive) return;
    void refetchUsage({ cancelRefetch: false });
    void refetchStats({ cancelRefetch: false });
    void refetchTrend({ cancelRefetch: false });
  }, [pageActive, refetchStats, refetchTrend, refetchUsage]);

  const handleAutoRefresh = useCallback(() => {
    if (!pageActive) return;
    void refetchUsage({ cancelRefetch: false });
  }, [pageActive, refetchUsage]);

  function updateFilter(key: keyof UsageQuery, value: string) {
    const nextValue = (key === 'user_id' || key === 'api_key_id')
      ? (value ? Number(value) : undefined)
      : value || undefined;
    setFilters((prev) => ({ ...prev, [key]: nextValue }));
    setPage(1);
  }

  const activeStats = pageActive ? stats : undefined;

  // 饼图数据
  const modelDistribution: DistributionItem[] = useMemo(
    () => (activeStats?.by_model ?? []).map((s) => ({
      name: s.model,
      requests: s.requests,
      tokens: s.tokens,
      totalCost: s.total_cost,
      actualCost: s.actual_cost,
    })),
    [activeStats?.by_model],
  );

  const groupDistribution: DistributionItem[] = useMemo(
    () => (activeStats?.by_group ?? []).map((s) => ({
      name: s.name || `#${s.group_id}`,
      requests: s.requests,
      tokens: s.tokens,
      totalCost: s.total_cost,
      actualCost: s.actual_cost,
    })),
    [activeStats?.by_group],
  );

  const groupStatsRows: GroupStatsRow[] = useMemo(() => {
    if (!activeStats) return [];
    const dataMap: Record<string, GroupStatsRow[]> = {
      channel: activeStats.by_channel?.map((s) => ({ key: s.channel_id, name: s.name, requests: s.requests, tokens: s.tokens, total_cost: s.total_cost, actual_cost: s.actual_cost })) ?? [],
      group: activeStats.by_group?.map((s) => ({ key: s.group_id, name: s.name || `#${s.group_id}`, requests: s.requests, tokens: s.tokens, total_cost: s.total_cost, actual_cost: s.actual_cost })) ?? [],
      model: activeStats.by_model?.map((s) => ({ key: s.model, name: s.model, requests: s.requests, tokens: s.tokens, total_cost: s.total_cost, actual_cost: s.actual_cost })) ?? [],
      user: activeStats.by_user?.map((s) => ({ key: s.user_id, name: s.email, requests: s.requests, tokens: s.tokens, total_cost: s.total_cost, actual_cost: s.actual_cost })) ?? [],
    };
    return dataMap[statsGroupBy] ?? [];
  }, [activeStats, statsGroupBy]);

  const sharedColumns = useUsageColumns();

  const columns = useMemo(() => {
    const adminColumns: UsageColumnConfig<UsageLogResp>[] = [
      {
        key: 'user_id',
        title: t('common.user'),
        width: '160px',
        render: (row) => {
          // 渠道测试落账行：无用户归属，发起方标为「渠道测试」。
          if (row.source === 'channel_test') {
            return (
              <Chip color="accent" size="sm" variant="soft">
                {t('upstream_logs.source_channel_test')}
              </Chip>
            );
          }
          const fallbackLabel = row.user_deleted ? t('usage.user_deleted') : `#${row.user_id}`;
          const label = row.user_email || fallbackLabel;

          return (
            <div className="flex min-w-0 items-center gap-1.5">
              <span className="shrink-0 font-mono text-xs text-text-tertiary">{row.user_id > 0 ? `#${row.user_id}` : '-'}</span>
              <span className={`min-w-0 truncate text-[13px] font-medium ${row.user_deleted ? 'text-text-tertiary' : 'text-text'}`} title={label}>
                {label}
              </span>
            </div>
          );
        },
      },
    ];
    const modelIdx = sharedColumns.findIndex((c) => c.key === 'model');
    const streamColumn = sharedColumns.find((column) => column.key === 'stream');
    const timingColumns = sharedColumns.filter((column) => column.key === 'first_token_ms' || column.key === 'duration_ms');
    const sharedColumnsAfterModel = sharedColumns
      .slice(modelIdx + 1)
      .filter((column) => column.key !== 'first_token_ms' && column.key !== 'duration_ms' && column.key !== 'stream');
    const endpointColumn: UsageColumnConfig<UsageLogResp> = {
      key: 'endpoint',
      title: t('usage.endpoint', '端点'),
      width: '180px',
      hideOnMobile: true,
      render: (row) => (
        <span className="block truncate font-mono text-xs leading-tight text-text-secondary" title={row.endpoint || '-'}>
          {row.endpoint || '-'}
        </span>
      ),
    };
    const apiKeyColumn: UsageColumnConfig<UsageLogResp> = {
      key: 'api_key',
      title: 'API Key',
      width: '124px',
      hideOnMobile: true,
      render: (row) => {
        if (row.api_key_deleted) {
          return <span className="block max-w-full truncate text-[13px] text-text-tertiary">{t('usage.api_key_deleted')}</span>;
        }
        const name = row.api_key_name || '-';
        return (
          <span className="block max-w-full truncate text-xs text-text-secondary" title={name}>{name}</span>
        );
      },
    };
    const channelColumn: UsageColumnConfig<UsageLogResp> = {
      key: 'channel_name',
      title: t('usage.channel', '渠道'),
      width: '172px',
      hideOnMobile: true,
      render: (row) => {
        const name = row.channel_name || '-';
        return (
          <div className="flex w-full min-w-0 flex-col items-center text-center" title={name}>
            <span className="block max-w-full truncate text-xs font-medium text-text-secondary">{name}</span>
          </div>
        );
      },
    };
    return [
      ...adminColumns,
      ...sharedColumns.slice(0, modelIdx + 1),
      ...(streamColumn ? [streamColumn] : []),
      ...timingColumns,
      ...sharedColumnsAfterModel,
      endpointColumn,
      apiKeyColumn,
      channelColumn,
    ] as UsageColumnConfig<UsageLogResp>[];
  }, [sharedColumns, t]);
  const total = data?.total ?? 0;

  return (
    <div>
      {/* 聚合统计 */}
      {activeStats && (
        <div className="mb-6 space-y-4">
          <div className="grid grid-cols-1 gap-3 md:grid-cols-3 xl:grid-cols-3 2xl:gap-4">
            <StatCard
              title={t('usage.total_requests')}
              value={activeStats.total_requests.toLocaleString()}
              icon={<Activity className="w-5 h-5" />}
              accentColor="var(--ag-primary)"
            />
            <StatCard
              title={t('usage.total_tokens')}
              value={fmtNum(activeStats.total_tokens)}
              icon={<Hash className="w-5 h-5" />}
              accentColor="var(--ag-info)"
            />
            <StatCard
              title={t('usage.cost')}
              value={(
                <span
                  className="inline-flex min-w-0 items-baseline gap-1.5"
                  title={`${t('usage.actual_cost')} / ${t('usage.standard_cost')}`}
                >
                  <CostValue value={activeStats.total_actual_cost} decimals={4} tone="actual" />
                  <span className="text-sm text-text-tertiary">/</span>
                  <span className="text-sm opacity-70">
                    <CostValue value={activeStats.total_cost} decimals={4} tone="standard" />
                  </span>
                </span>
              )}
              icon={<Coins className="w-5 h-5" />}
              accentColor="var(--ag-warning)"
            />
          </div>

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <DistributionCard
              title={t('usage.model_distribution')}
              firstColumnTitle={t('usage.model')}
              firstColumnWidth="30%"
              data={modelDistribution}
            />
            <DistributionCard
              title={t('usage.group_distribution')}
              firstColumnTitle={t('groups.group')}
              firstColumnWidth="26%"
              data={groupDistribution}
            />
          </div>

          <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
            <TokenTrendCard
              data={trendData ?? []}
              granularity={granularity}
              onGranularityChange={setGranularity}
            />
            <GroupStatsCard
              activeKey={statsGroupBy}
              rows={groupStatsRows}
              onActiveKeyChange={setStatsGroupBy}
            />
          </div>
        </div>
      )}

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
        <div className="w-full sm:w-48">
          <UsageModelFilterInput
            ariaLabel={t('usage.model', 'Model')}
            placeholder={t('usage.model_placeholder')}
            value={filters.model ?? ''}
            onModelChange={handleModelChange}
          />
        </div>
        <div className="w-full sm:w-48">
          <ComboBox
            aria-label={t('usage.search_user')}
            allowsEmptyCollection
            fullWidth
            inputValue={userKeyword}
            items={visibleUserOptions}
            menuTrigger="focus"
            selectedKey={filters.user_id ? String(filters.user_id) : null}
            onInputChange={(value) => {
              setUserKeyword(value);
              if (!value) {
                setSelectedUserLabel('');
                updateFilter('user_id', '');
                return;
              }
              if (filters.user_id && value !== selectedUserLabel) {
                setSelectedUserLabel('');
                updateFilter('user_id', '');
              }
            }}
            onSelectionChange={(key) => {
              const value = key == null ? '' : String(key);
              updateFilter('user_id', value);
              const option = visibleUserOptions.find((item) => item.id === value);
              const label = option?.label ? String(option.label) : '';
              setSelectedUserLabel(label);
              setUserKeyword(label);
            }}
          >
            <ComboBox.InputGroup className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
              <Input className="pl-9" placeholder={t('usage.search_user')} />
            </ComboBox.InputGroup>
            <ComboBox.Popover>
              <ListBox
                items={visibleUserOptions}
                renderEmptyState={() => (
                  <div className="px-3 py-6 text-center text-xs text-text-tertiary">
                    {userKeyword.trim() ? t('common.no_data') : t('usage.search_user')}
                  </div>
                )}
              >
                {(item) => (
                  <ListBox.Item id={item.id} textValue={item.textValue}>
                    <div className="min-w-0">
                      <div className="truncate">{item.label}</div>
                      {item.description ? (
                        <div className="truncate text-xs text-text-tertiary">{item.description}</div>
                      ) : null}
                    </div>
                  </ListBox.Item>
                )}
              </ListBox>
            </ComboBox.Popover>
          </ComboBox>
        </div>
        <div className="w-full sm:w-48">
          <ComboBox
            aria-label={t('usage.search_api_key', '搜索 API Key')}
            allowsEmptyCollection
            fullWidth
            inputValue={apiKeyKeyword}
            items={visibleAPIKeyOptions}
            menuTrigger="focus"
            selectedKey={filters.api_key_id ? String(filters.api_key_id) : null}
            onInputChange={(value) => {
              setAPIKeyKeyword(value);
              if (!value) {
                setSelectedAPIKeyLabel('');
                updateFilter('api_key_id', '');
                return;
              }
              if (filters.api_key_id && value !== selectedAPIKeyLabel) {
                setSelectedAPIKeyLabel('');
                updateFilter('api_key_id', '');
              }
            }}
            onSelectionChange={(key) => {
              const value = key == null ? '' : String(key);
              updateFilter('api_key_id', value);
              const option = visibleAPIKeyOptions.find((item) => item.id === value);
              const label = option?.label ? String(option.label) : '';
              setSelectedAPIKeyLabel(label);
              setAPIKeyKeyword(label);
            }}
          >
            <ComboBox.InputGroup className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
              <Input className="pl-9" placeholder={t('usage.search_api_key', '搜索 API Key')} />
            </ComboBox.InputGroup>
            <ComboBox.Popover>
              <ListBox
                items={visibleAPIKeyOptions}
                renderEmptyState={() => (
                  <div className="px-3 py-6 text-center text-xs text-text-tertiary">
                    {apiKeyKeyword.trim() ? t('common.no_data') : t('usage.search_api_key', '搜索 API Key')}
                  </div>
                )}
              >
                {(item) => (
                  <ListBox.Item id={item.id} textValue={item.textValue}>
                    <div className="min-w-0">
                      <div className="truncate">{item.label}</div>
                      {item.description ? (
                        <div className="truncate text-xs text-text-tertiary">{item.description}</div>
                      ) : null}
                    </div>
                  </ListBox.Item>
                )}
              </ListBox>
            </ComboBox.Popover>
          </ComboBox>
        </div>
        <AutoRefreshControl
          value={autoRefresh}
          options={ADMIN_AUTO_REFRESH_OPTIONS}
          label={autoRefreshLabel}
          offLabel={autoRefreshOffLabel}
          ariaLabel={t('usage.auto_update')}
          refreshAriaLabel={t('common.refresh', 'Refresh')}
          onChange={setAutoRefresh}
          onAutoRefresh={handleAutoRefresh}
          onRefresh={handleManualRefresh}
          isAutoRefreshing={isUsageTableRefreshing}
          isRefreshing={isRefreshing}
          isDisabled={!pageActive}
        />
      </div>

      {/* 记录区：消费记录 | 失败请求（切换共享筛选） */}
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
        <UpstreamLogsTable
          filters={{
            start_date: filters.start_date,
            end_date: filters.end_date,
            model: filters.model,
            user_id: filters.user_id,
            api_key_id: filters.api_key_id,
          }}
        />
      )}

      {recordsTab === 'usage' && (
      <UsageRecordsTable
        ariaLabel={t('usage.title', 'Usage')}
        columns={columns}
        dataVersion={pageActive ? dataUpdatedAt : undefined}
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
          : t('usage.empty_description', '调整筛选条件后重试')}
        emptyTitle={t('common.no_data')}
        highlightNewRows={pageActive && autoRefreshEnabled && page === 1}
        highlightResetKey={JSON.stringify({ ...filters, page, pageSize })}
        isLoading={!pageActive || isLoading}
        page={page}
        pageSize={pageSize}
        rows={pageActive ? data?.list ?? [] : []}
        setPage={setPage}
        setPageSize={setPageSize}
        suppressHighlight={!pageActive || isPlaceholderData}
        total={pageActive ? total : 0}
      />
      )}
    </div>
  );
}
