import { lazy, memo, Suspense, useCallback, useMemo, useState, type Key } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Button, Chip, ComboBox, Input, ListBox, Tabs } from '@heroui/react';
import { usageApi } from '../../shared/api/usage';
import { usersApi } from '../../shared/api/users';
import { apikeysApi } from '../../shared/api/apikeys';
import { channelsApi } from '../../shared/api/channels';
import { usePagination } from '../../shared/hooks/usePagination';
import { useDebouncedValue } from '../../shared/hooks/useDebouncedValue';
import { useDeferredActivation } from '../../shared/hooks/useDeferredActivation';
import { queryKeys } from '../../shared/queryKeys';
import { UpstreamLogsTable } from './usage/UpstreamLogsTable';
import { Activity, Coins, Hash, Search } from 'lucide-react';
import { useUsageColumns, type UsageColumnConfig } from '../../shared/columns/usageColumns';
import type { APIKeyResp, ChannelKeyStats, UsageLogResp, UsageQuery, UsageTrendBucket } from '../../shared/types';
import type { TFunction } from 'i18next';
import { CompactDataTable } from '../../shared/components/CompactDataTable';
import { DashboardCard } from '../../shared/components/DashboardCard';
import { StatCard } from '../../shared/components/StatCard';
import { fmtNum } from '../../shared/utils/format';
import { UsageRecordsTable } from '../../shared/components/UsageRecordsTable';
import { ColumnVisibilityControl } from '../../shared/components/ColumnVisibilityControl';
import { usePersistentHiddenColumns } from '../../shared/hooks/usePersistentHiddenColumns';
import { UsageDateRangeFilter } from '../../shared/components/UsageDateRangeFilter';
import { UsageModelFilterInput } from '../../shared/components/UsageModelFilterInput';
import { PIE_CHART_COLORS } from '../../shared/constants';
import { CostValue } from '../../shared/components/CostValue';
import { AutoRefreshControl } from '../../shared/components/AutoRefreshControl';
import { ADMIN_AUTO_REFRESH_OPTIONS, usePersistentAutoRefresh } from '../../shared/hooks/usePersistentAutoRefresh';

const UsagePieChart = lazy(() =>
  import('../../shared/components/charts').then((m) => ({ default: m.UsagePieChart })),
);
const UsageTokenTrendChart = lazy(() =>
  import('./usage/UsageCharts').then((m) => ({ default: m.UsageTokenTrendChart })),
);


// 分组统计 key 映射
const groupByKeys: Record<string, string> = {
  model: 'usage.by_model',
  user: 'usage.by_user',
  channel: 'usage.by_channel',
  channel_key: 'usage.by_channel_key',
  group: 'usage.by_group',
};

const groupByHeaderKeys: Record<string, string> = {
  model: 'usage.model',
  user: 'usage.user_id',
  channel: 'usage.channel',
  channel_key: 'usage.channel_key',
  group: 'usage.by_group',
};

const ADMIN_USAGE_STATS_GROUP_BY = 'model,group,channel,channel_key,user';

// 按 key 统计的展示名：使用“渠道名称 · 密钥名称”；未命名与已删除 key 显示对应占位。
function channelKeyStatName(s: ChannelKeyStats, t: TFunction): string {
  if (s.channel_key_id <= 0) return t('usage.deleted_key');
  const label = s.name || t('channels.key_unnamed');
  return s.channel_name ? `${s.channel_name} · ${label}` : label;
}
const USAGE_PAGE_ACTIVATION_DELAY_MS = 180;
const ADMIN_USAGE_AUTO_UPDATE_STORAGE_KEY = 'airgate.admin.usage.auto_update';
const ADMIN_USAGE_HIDDEN_COLUMNS_STORAGE_KEY = 'airgate.admin.usage.hidden_columns';
// 稳定的空数组引用：避免 `trendData ?? []` 每次渲染新建数组击穿 TokenTrendCard 的 memo。
const EMPTY_TREND_DATA: UsageTrendBucket[] = [];

// ==================== 分布饼图卡片 ====================

type PieMetric = 'token' | 'cost';

interface DistributionItem {
  name: string;
  requests: number;
  tokens: number;
  totalCost: number;
  actualCost: number;
}

// memo：页面顶层 state（筛选、列显隐等）频繁变化，图表卡片数据没变时跳过 recharts 重绘。
const DistributionCard = memo(function DistributionCard({
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
    <DashboardCard title={title} extra={metricTabs}>
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
                    <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: PIE_CHART_COLORS[index % PIE_CHART_COLORS.length] }} />
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
    </DashboardCard>
  );
});

type GroupStatsRow = {
  key: string | number;
  name: string;
  requests: number;
  tokens: number;
  total_cost: number;
  actual_cost: number;
};

const GroupStatsCard = memo(function GroupStatsCard({
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
    <DashboardCard
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
                  <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: PIE_CHART_COLORS[index % PIE_CHART_COLORS.length] }} />
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
    </DashboardCard>
  );
});

// ==================== Token 使用趋势 ====================

const TokenTrendCard = memo(function TokenTrendCard({
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
      <DashboardCard title={t('usage.token_trend')} extra={granularityTabs}>
        <div className="flex h-[248px] items-center justify-center text-sm text-text-tertiary 2xl:h-[288px]">
          {t('common.no_data')}
        </div>
      </DashboardCard>
    );
  }

  return (
    <DashboardCard
      title={t('usage.token_trend')}
      extra={granularityTabs}
    >
      <div className="h-[248px] 2xl:h-[288px]">
        <Suspense fallback={<div className="h-full w-full" />}>
          <UsageTokenTrendChart data={data} lineLabels={lineLabels} />
        </Suspense>
      </div>
    </DashboardCard>
  );
});

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

  // 渠道 + 渠道下 Key 级联筛选（可输入搜索）：渠道一次拉全，keys 内嵌于渠道响应，无需二次请求。
  const { data: channelsData } = useQuery({
    queryKey: queryKeys.channels('usage-filter'),
    queryFn: () => channelsApi.list({ page: 1, page_size: 100 }),
    enabled: pageActive,
  });
  const channelList = channelsData?.list ?? [];
  const selectedChannel = channelList.find((ch) => ch.id === filters.channel_id);

  // 渠道搜索：客户端按名称过滤（渠道数量有限，无需服务端搜索）。
  // 关键：输入框既显示已选渠道名、又当搜索词用。当输入等于已选渠道名时视为「仅展示选中项」
  // 而非搜索，返回全量列表——否则选中后重新展开只剩当前渠道，无法切换到其它渠道。
  const [channelKeyword, setChannelKeyword] = useState('');
  const channelOptions = channelList.map((ch) => ({
    id: String(ch.id),
    label: ch.name,
    textValue: ch.name,
  }));
  const trimmedChannelKeyword = channelKeyword.trim();
  const channelSearchActive = trimmedChannelKeyword.length > 0
    && trimmedChannelKeyword !== (selectedChannel?.name ?? '').trim();
  const visibleChannelOptions = channelSearchActive
    ? channelOptions.filter((o) => o.label.toLowerCase().includes(trimmedChannelKeyword.toLowerCase()))
    : channelOptions;

  // 渠道下 Key 搜索：选项级联自选中渠道的内嵌 keys；名称为空时用尾 4 位 hint 兜底。
  const [channelKeyKeyword, setChannelKeyKeyword] = useState('');
  const channelKeyOptions = (selectedChannel?.keys ?? []).map((key) => ({
    id: String(key.id),
    label: key.name || key.api_key_hint || `#${key.id}`,
    textValue: key.name || key.api_key_hint || String(key.id),
  }));
  const selectedChannelKeyLabel = channelKeyOptions.find(
    (o) => o.id === (filters.channel_key_id ? String(filters.channel_key_id) : ''),
  )?.label ?? '';
  const trimmedChannelKeyKeyword = channelKeyKeyword.trim();
  const channelKeySearchActive = trimmedChannelKeyKeyword.length > 0
    && trimmedChannelKeyKeyword !== selectedChannelKeyLabel.trim();
  const visibleChannelKeyOptions = channelKeySearchActive
    ? channelKeyOptions.filter((o) => o.label.toLowerCase().includes(trimmedChannelKeyKeyword.toLowerCase()))
    : channelKeyOptions;

  // 清空渠道筛选（连带清空 Key 筛选与两处搜索词）。
  function clearChannelFilter() {
    setChannelKeyword('');
    setChannelKeyKeyword('');
    setFilters((prev) => ({ ...prev, channel_id: undefined, channel_key_id: undefined }));
    setPage(1);
  }

  // 选中渠道：写 channel_id 并清空已选 Key（避免残留跨渠道 key 过滤）。
  function handleChannelSelect(key: Key | null) {
    const value = key == null ? '' : String(key);
    if (!value) {
      clearChannelFilter();
      return;
    }
    setChannelKeyword(channelOptions.find((o) => o.id === value)?.label ?? '');
    setChannelKeyKeyword('');
    setFilters((prev) => ({ ...prev, channel_id: Number(value), channel_key_id: undefined }));
    setPage(1);
  }

  function handleChannelKeySelect(key: Key | null) {
    const value = key == null ? '' : String(key);
    setChannelKeyKeyword(value ? (channelKeyOptions.find((o) => o.id === value)?.label ?? '') : '');
    setFilters((prev) => ({ ...prev, channel_key_id: value ? Number(value) : undefined }));
    setPage(1);
  }

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
      channel: activeStats.by_channel?.map((s) => ({ key: s.channel_id, name: s.name || (s.channel_id > 0 ? `#${s.channel_id}` : t('usage.deleted_channel')), requests: s.requests, tokens: s.tokens, total_cost: s.total_cost, actual_cost: s.actual_cost })) ?? [],
      channel_key: activeStats.by_channel_key?.map((s) => ({ key: s.channel_key_id, name: channelKeyStatName(s, t), requests: s.requests, tokens: s.tokens, total_cost: s.total_cost, actual_cost: s.actual_cost })) ?? [],
      group: activeStats.by_group?.map((s) => ({ key: s.group_id, name: s.name || `#${s.group_id}`, requests: s.requests, tokens: s.tokens, total_cost: s.total_cost, actual_cost: s.actual_cost })) ?? [],
      model: activeStats.by_model?.map((s) => ({ key: s.model, name: s.model, requests: s.requests, tokens: s.tokens, total_cost: s.total_cost, actual_cost: s.actual_cost })) ?? [],
      user: activeStats.by_user?.map((s) => ({ key: s.user_id, name: s.email, requests: s.requests, tokens: s.tokens, total_cost: s.total_cost, actual_cost: s.actual_cost })) ?? [],
    };
    return dataMap[statsGroupBy] ?? [];
  }, [activeStats, statsGroupBy, t]);

  const sharedColumns = useUsageColumns();

  const columns = useMemo(() => {
    const adminColumns: UsageColumnConfig<UsageLogResp>[] = [
      {
        key: 'user_id',
        title: t('common.user'),
        width: '160px',
        // 管理端列多需横向滚动，用户列吸附在最左侧保持可见。
        stickyLeft: true,
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
    const timingColumns = sharedColumns.filter((column) => column.key === 'latency');
    const sharedColumnsAfterModel = sharedColumns
      .slice(modelIdx + 1)
      .filter((column) => column.key !== 'latency' && column.key !== 'stream');
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
      width: '220px',
      hideOnMobile: true,
      render: (row) => {
        const channelName = row.channel_name || '-';
        const keyName = row.channel_key_name
          || (row.channel_key_id ? t('channels.key_unnamed') : t('usage.deleted_key'));
        const name = channelName === '-' ? channelName : `${channelName} · ${keyName}`;
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

  // 列显隐：吸附的用户列锁定不可隐藏，其余列可按需关闭并持久化。
  const [hiddenColumnKeys, setHiddenColumnKeys] = usePersistentHiddenColumns(ADMIN_USAGE_HIDDEN_COLUMNS_STORAGE_KEY);
  const columnPickerItems = useMemo(
    () => columns.map((column) => ({ key: column.key, label: column.title, locked: column.stickyLeft })),
    [columns],
  );
  const visibleColumns = useMemo(
    () => columns.filter((column) => column.stickyLeft || !hiddenColumnKeys.has(column.key)),
    [columns, hiddenColumnKeys],
  );
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
              data={trendData ?? EMPTY_TREND_DATA}
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
        <div className="w-full sm:w-44">
          <ComboBox
            aria-label={t('usage.search_channel')}
            allowsEmptyCollection
            fullWidth
            inputValue={channelKeyword}
            items={visibleChannelOptions}
            menuTrigger="focus"
            selectedKey={filters.channel_id ? String(filters.channel_id) : null}
            onInputChange={(value) => {
              setChannelKeyword(value);
              if (!value) {
                clearChannelFilter();
              }
            }}
            onSelectionChange={handleChannelSelect}
          >
            <ComboBox.InputGroup className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
              <Input className="pl-9" placeholder={t('usage.search_channel')} />
            </ComboBox.InputGroup>
            <ComboBox.Popover>
              <ListBox
                items={visibleChannelOptions}
                renderEmptyState={() => (
                  <div className="px-3 py-6 text-center text-xs text-text-tertiary">
                    {channelList.length === 0 ? t('common.no_data') : t('usage.search_channel')}
                  </div>
                )}
              >
                {(item) => (
                  <ListBox.Item id={item.id} textValue={item.textValue}>
                    {item.label}
                  </ListBox.Item>
                )}
              </ListBox>
            </ComboBox.Popover>
          </ComboBox>
        </div>
        <div className="w-full sm:w-44">
          <ComboBox
            aria-label={t('usage.search_channel_key')}
            allowsEmptyCollection
            fullWidth
            isDisabled={!filters.channel_id}
            inputValue={channelKeyKeyword}
            items={visibleChannelKeyOptions}
            menuTrigger="focus"
            selectedKey={filters.channel_key_id ? String(filters.channel_key_id) : null}
            onInputChange={(value) => {
              setChannelKeyKeyword(value);
              if (!value) {
                handleChannelKeySelect(null);
              }
            }}
            onSelectionChange={handleChannelKeySelect}
          >
            <ComboBox.InputGroup className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
              <Input className="pl-9" placeholder={t('usage.search_channel_key')} />
            </ComboBox.InputGroup>
            <ComboBox.Popover>
              <ListBox
                items={visibleChannelKeyOptions}
                renderEmptyState={() => (
                  <div className="px-3 py-6 text-center text-xs text-text-tertiary">
                    {t('common.no_data')}
                  </div>
                )}
              >
                {(item) => (
                  <ListBox.Item id={item.id} textValue={item.textValue}>
                    {item.label}
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
      <div className="mb-3 flex items-center justify-between gap-3">
        <Tabs
          className="ag-segmented-tabs ag-segmented-tabs-compact"
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
        {recordsTab === 'usage' && (
          <ColumnVisibilityControl
            hiddenKeys={hiddenColumnKeys}
            items={columnPickerItems}
            onChange={setHiddenColumnKeys}
          />
        )}
      </div>

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
        columns={visibleColumns}
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
          : t('usage.empty_description')}
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
