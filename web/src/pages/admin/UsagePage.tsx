import { useState, useMemo, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Card, ComboBox, Input, ListBox, Select, Switch, Tabs, TextField as HeroTextField } from '@heroui/react';
import {
  PieChart, Pie, Cell, ResponsiveContainer, Tooltip as RechartsTooltip,
  LineChart, Line, XAxis, YAxis, CartesianGrid, Legend,
} from 'recharts';
import { usageApi } from '../../shared/api/usage';
import { usersApi } from '../../shared/api/users';
import { usePagination } from '../../shared/hooks/usePagination';
import { usePersistentBoolean } from '../../shared/hooks/usePersistentBoolean';
import { usePlatforms } from '../../shared/hooks/usePlatforms';
import { Activity, Coins, Hash, DollarSign, Search } from 'lucide-react';
import { useUsageColumns, fmtNum, fmtCost, type UsageColumnConfig } from '../../shared/columns/usageColumns';
import type { UsageLogResp, UsageQuery, UsageTrendBucket } from '../../shared/types';
import { CompactDataTable } from '../../shared/components/CompactDataTable';
import { UsageRecordsTable } from '../../shared/components/UsageRecordsTable';
import { UsageDateRangeFilter } from '../../shared/components/UsageDateRangeFilter';

import { decorativePalette } from '@airgate/theme';

const PIE_COLORS = decorativePalette.slice(0, 10);

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
  value: string;
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

/** 格式化时间标签 */
function fmtTime(timeStr: string): string {
  if (timeStr.includes(' ')) {
    return timeStr.split(' ')[1] ?? timeStr;
  }
  const parts = timeStr.split('-');
  return `${parts[1] ?? ''}/${parts[2] ?? ''}`;
}

// 分组统计 key 映射
const groupByKeys: Record<string, string> = {
  model: 'usage.by_model',
  user: 'usage.by_user',
  account: 'usage.by_account',
  group: 'usage.by_group',
};

const groupByHeaderKeys: Record<string, string> = {
  model: 'usage.model',
  user: 'usage.user_id',
  account: 'usage.by_account',
  group: 'usage.by_group',
};

const ADMIN_USAGE_STATS_GROUP_BY = 'model,group,account,user';
const USAGE_AUTO_UPDATE_INTERVAL_MS = 1_000;
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
}: {
  title: string;
  data: DistributionItem[];
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
      <div className="ag-distribution-card-body grid items-start gap-3 lg:grid-cols-[176px_minmax(0,1fr)]">
        <div className="ag-distribution-chart-frame">
          <PieChart width={176} height={176}>
            <Pie
              data={pieData}
              cx="50%"
              cy="50%"
              innerRadius={42}
              outerRadius={68}
              dataKey="value"
              minAngle={3}
              stroke="var(--ag-surface)"
              strokeWidth={2}
            >
              {pieData.map((_, i) => (
                <Cell key={i} fill={PIE_COLORS[i % PIE_COLORS.length]} />
              ))}
            </Pie>
            <RechartsTooltip
              contentStyle={{
                background: 'var(--ag-bg-elevated)',
                border: '1px solid var(--ag-border)',
                borderRadius: 8,
                fontSize: 12,
              }}
              formatter={(value) => [
                metric === 'token' ? fmtNum(Number(value)) : fmtCost(Number(value)),
              ]}
            />
          </PieChart>
        </div>

        <div className="ag-distribution-table-scroll">
          <CompactDataTable
            ariaLabel={title}
            className="ag-compact-data-table--dense"
            emptyText={t('common.no_data')}
            minWidth={620}
            rowKey={(row) => row.name}
            rows={data}
            columns={[
              {
                key: 'name',
                title,
                width: '42%',
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
                width: '14%',
                render: (item) => <span className="truncate font-mono text-text-secondary">{item.requests.toLocaleString()}</span>,
              },
              {
                align: 'end',
                key: 'tokens',
                title: t('usage.tokens'),
                width: '16%',
                render: (item) => <span className="truncate font-mono text-text-secondary">{fmtNum(item.tokens)}</span>,
              },
              {
                align: 'end',
                key: 'actualCost',
                title: t('usage.actual_cost'),
                width: '14%',
                render: (item) => <span className="truncate font-mono text-warning">{fmtCost(item.actualCost)}</span>,
              },
              {
                align: 'end',
                key: 'totalCost',
                title: t('usage.standard_cost'),
                width: '14%',
                render: (item) => <span className="truncate font-mono text-text-secondary">{fmtCost(item.totalCost)}</span>,
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
          minWidth={620}
          rowKey={(row) => row.key}
          rows={rows}
          columns={[
            {
              key: 'name',
              title: t(groupByHeaderKeys[activeKey] ?? 'usage.model'),
              width: '42%',
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
              width: '14%',
              render: (row) => <span className="truncate font-mono text-text-secondary">{row.requests.toLocaleString()}</span>,
            },
            {
              align: 'end',
              key: 'tokens',
              title: t('usage.tokens'),
              width: '16%',
              render: (row) => <span className="truncate font-mono text-text-secondary">{fmtNum(row.tokens)}</span>,
            },
            {
              align: 'end',
              key: 'actualCost',
              title: t('usage.actual_cost'),
              width: '14%',
              render: (row) => <span className="truncate font-mono text-warning">{fmtCost(row.actual_cost)}</span>,
            },
            {
              align: 'end',
              key: 'totalCost',
              title: t('usage.standard_cost'),
              width: '14%',
              render: (row) => <span className="truncate font-mono text-text-secondary">{fmtCost(row.total_cost)}</span>,
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

  const chartData = useMemo(
    () => data.map((d) => ({
      time: fmtTime(d.time),
      rawTime: d.time,
      input: d.input_tokens,
      output: d.output_tokens,
      cacheCreation: d.cache_creation,
      cacheRead: d.cache_read,
      actualCost: d.actual_cost,
      standardCost: d.standard_cost,
    })),
    [data],
  );

  const lineLabels: Record<string, string> = {
    input: t('usage.input'),
    output: t('usage.output'),
    cacheCreation: t('usage.cache_creation'),
    cacheRead: t('usage.cache_read'),
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

  if (chartData.length === 0) {
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
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={chartData} margin={{ top: 4, right: 8, left: -18, bottom: 0 }}>
          <CartesianGrid stroke="var(--ag-border-subtle)" vertical={false} />
          <XAxis
            dataKey="time"
            tick={{ fontSize: 11, fill: 'var(--ag-text-tertiary)' }}
            axisLine={false}
            tickLine={false}
          />
          <YAxis
            tick={{ fontSize: 11, fill: 'var(--ag-text-tertiary)' }}
            axisLine={false}
            tickLine={false}
            tickFormatter={(v: number) => fmtNum(v)}
          />
          <RechartsTooltip
            contentStyle={{
              background: 'var(--ag-bg-elevated)',
              border: '1px solid var(--ag-border)',
              borderRadius: 8,
              fontSize: 12,
              padding: '8px 12px',
            }}
            labelStyle={{ color: 'var(--ag-text)', fontWeight: 600, marginBottom: 4 }}
            labelFormatter={(_label, payload) => {
              if (payload?.[0]?.payload?.rawTime) {
                return payload[0].payload.rawTime;
              }
              return _label;
            }}
            formatter={(value, name) => [fmtNum(Number(value)), lineLabels[String(name)] || String(name)]}
            itemSorter={(item) => -(item.value as number)}
            content={({ active, payload, label }) => {
              if (!active || !payload?.length) return null;
              const d = payload[0]?.payload;
              return (
                <div className="rounded-lg border border-border bg-bg-elevated p-3 text-xs shadow-lg">
                  <div className="font-semibold text-text mb-2">{d?.rawTime ?? label}</div>
                  {payload.map((entry, i) => (
                    <div key={i} className="flex items-center gap-2 py-0.5">
                      <div className="w-2.5 h-2.5 rounded-sm" style={{ background: entry.color }} />
                      <span className="text-text-secondary">{lineLabels[String(entry.dataKey)] || String(entry.dataKey)}:</span>
                      <span className="font-mono text-text ml-auto">{fmtNum(Number(entry.value))}</span>
                    </div>
                  ))}
                  <div className="border-t border-border-subtle mt-2 pt-2 text-text-secondary">
                    Actual: <span className="font-mono text-warning">{fmtCost(d?.actualCost ?? 0)}</span>
                    {' | '}
                    Standard: <span className="font-mono text-text">{fmtCost(d?.standardCost ?? 0)}</span>
                  </div>
                </div>
              );
            }}
          />
          <Legend
            iconType="circle"
            iconSize={8}
            wrapperStyle={{ fontSize: 11, color: 'var(--ag-text-tertiary)' }}
            formatter={(value: string) => lineLabels[value] || value}
          />
          <Line type="monotone" dataKey="input" stroke="#3b82f6" strokeWidth={2} dot={false} />
          <Line type="monotone" dataKey="output" stroke="#10b981" strokeWidth={2} dot={false} />
          <Line type="monotone" dataKey="cacheCreation" stroke="#f59e0b" strokeWidth={2} dot={false} />
          <Line type="monotone" dataKey="cacheRead" stroke="#8b5cf6" strokeWidth={2} dot={false} />
        </LineChart>
        </ResponsiveContainer>
      </div>
    </SectionCard>
  );
}

// ==================== 主页面 ====================

export default function UsagePage() {
  const { t } = useTranslation();
  const { page, setPage, pageSize, setPageSize } = usePagination(20);
  const [filters, setFilters] = useState<Partial<UsageQuery>>({});
  const [statsGroupBy, setStatsGroupBy] = useState<string>('model');
  const [granularity, setGranularity] = useState<string>('hour');
  const [autoRefresh, setAutoRefresh] = usePersistentBoolean(ADMIN_USAGE_AUTO_UPDATE_STORAGE_KEY, false);
  const { platforms, platformName } = usePlatforms();
  const autoRefreshInterval = autoRefresh ? USAGE_AUTO_UPDATE_INTERVAL_MS : false;

  // 用户搜索
  const [userKeyword, setUserKeyword] = useState('');
  const [selectedUserLabel, setSelectedUserLabel] = useState('');
  const { data: usersData } = useQuery({
    queryKey: ['admin-users-search', userKeyword],
    queryFn: () => usersApi.list({ page: 1, page_size: 20, keyword: userKeyword.trim() }),
    enabled: userKeyword.trim().length > 0,
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

  // 构建查询参数
  const queryParams: UsageQuery = {
    page,
    page_size: pageSize,
    ...filters,
  };

  // 使用记录列表
  const { data, isLoading, refetch: refetchUsage } = useQuery({
    queryKey: ['admin-usage', queryParams],
    queryFn: () => usageApi.adminList(queryParams),
    refetchInterval: autoRefreshInterval,
    refetchIntervalInBackground: false,
    refetchOnReconnect: autoRefresh,
    refetchOnWindowFocus: autoRefresh,
  });

  const { data: stats, refetch: refetchStats } = useQuery({
    queryKey: ['admin-usage-stats', filters.start_date, filters.end_date, filters.platform, filters.model, filters.user_id],
    queryFn: () =>
      usageApi.stats({
        group_by: ADMIN_USAGE_STATS_GROUP_BY,
        start_date: filters.start_date,
        end_date: filters.end_date,
        platform: filters.platform,
        model: filters.model,
        user_id: filters.user_id ? Number(filters.user_id) : undefined,
    }),
    refetchInterval: autoRefreshInterval,
    refetchIntervalInBackground: false,
    refetchOnReconnect: autoRefresh,
    refetchOnWindowFocus: autoRefresh,
  });

  // Token 趋势
  const { data: trendData, refetch: refetchTrend } = useQuery({
    queryKey: ['admin-usage-trend', granularity, filters.start_date, filters.end_date, filters.platform, filters.model, filters.user_id],
    queryFn: () =>
      usageApi.trend({
        granularity,
        start_date: filters.start_date,
        end_date: filters.end_date,
        platform: filters.platform,
        model: filters.model,
        user_id: filters.user_id ? Number(filters.user_id) : undefined,
    }),
    refetchInterval: autoRefreshInterval,
    refetchIntervalInBackground: false,
    refetchOnReconnect: autoRefresh,
    refetchOnWindowFocus: autoRefresh,
  });

  function handleAutoRefreshChange(enabled: boolean) {
    setAutoRefresh(enabled);
    if (enabled) {
      void refetchUsage();
      void refetchStats();
      void refetchTrend();
    }
  }

  function updateFilter(key: string, value: string) {
    setFilters((prev) => ({ ...prev, [key]: value || undefined }));
    setPage(1);
  }

  // 饼图数据
  const modelDistribution: DistributionItem[] = useMemo(
    () => (stats?.by_model ?? []).map((s) => ({
      name: s.model,
      requests: s.requests,
      tokens: s.tokens,
      totalCost: s.total_cost,
      actualCost: s.actual_cost,
    })),
    [stats?.by_model],
  );

  const groupDistribution: DistributionItem[] = useMemo(
    () => (stats?.by_group ?? []).map((s) => ({
      name: s.name || `#${s.group_id}`,
      requests: s.requests,
      tokens: s.tokens,
      totalCost: s.total_cost,
      actualCost: s.actual_cost,
    })),
    [stats?.by_group],
  );

  const groupStatsRows: GroupStatsRow[] = useMemo(() => {
    if (!stats) return [];
    const dataMap: Record<string, GroupStatsRow[]> = {
      account: stats.by_account?.map((s) => ({ key: s.account_id, name: s.name, requests: s.requests, tokens: s.tokens, total_cost: s.total_cost, actual_cost: s.actual_cost })) ?? [],
      group: stats.by_group?.map((s) => ({ key: s.group_id, name: s.name || `#${s.group_id}`, requests: s.requests, tokens: s.tokens, total_cost: s.total_cost, actual_cost: s.actual_cost })) ?? [],
      model: stats.by_model?.map((s) => ({ key: s.model, name: s.model, requests: s.requests, tokens: s.tokens, total_cost: s.total_cost, actual_cost: s.actual_cost })) ?? [],
      user: stats.by_user?.map((s) => ({ key: s.user_id, name: s.email, requests: s.requests, tokens: s.tokens, total_cost: s.total_cost, actual_cost: s.actual_cost })) ?? [],
    };
    return dataMap[statsGroupBy] ?? [];
  }, [stats, statsGroupBy]);

  const sharedColumns = useUsageColumns();

  // 管理端额外的列（用户、API Key、上游凭证），插入在共享列之前
  const adminColumns: UsageColumnConfig<UsageLogResp>[] = [
    {
      key: 'user_id',
      title: t('common.user'),
      width: '160px',
      render: (row) => {
        const label = row.user_email || `#${row.user_id}`;

        return (
          <div className="flex min-w-0 items-center gap-1.5">
            <span className="shrink-0 font-mono text-[11px] text-text-tertiary">#{row.user_id}</span>
            <span className="min-w-0 truncate text-xs font-medium text-text" title={label}>
              {label}
            </span>
          </div>
        );
      },
    },
  ];
  const platformOptions = [
    { id: '', label: t('common.all') },
    ...platforms.map((p) => ({ id: p, label: platformName(p) })),
  ];
  const selectedPlatformLabel = platformOptions.find((item) => item.id === (filters.platform || ''))?.label ?? t('common.all');

  // 插入管理列。
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
      <span className="block truncate font-mono text-[11px] leading-tight text-text-secondary" title={row.endpoint || '-'}>
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
        return <span className="block max-w-full truncate text-text-tertiary text-xs">{t('usage.api_key_deleted')}</span>;
      }
      const name = row.api_key_name || '-';
      return (
        <span className="block max-w-full truncate text-[11px] text-text-secondary" title={name}>{name}</span>
      );
    },
  };
  const accountColumn: UsageColumnConfig<UsageLogResp> = {
    key: 'account_name',
    title: t('usage.upstream_credential', '上游凭证'),
    width: '172px',
    hideOnMobile: true,
    render: (row) => {
      const label = row.account_name || '-';
      return (
        <span className="block max-w-full truncate text-[11px] text-text-secondary" title={label}>{label}</span>
      );
    },
  };
  const columns: UsageColumnConfig<UsageLogResp>[] = [
    ...adminColumns,
    ...sharedColumns.slice(0, modelIdx + 1),
    ...(streamColumn ? [streamColumn] : []),
    ...timingColumns,
    ...sharedColumnsAfterModel,
    endpointColumn,
    apiKeyColumn,
    accountColumn,
  ];
  const total = data?.total ?? 0;

  return (
    <div>
      {/* 聚合统计 */}
      {stats && (
        <div className="mb-6 space-y-4">
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4 2xl:gap-4">
            <StatCard
              title={t('usage.total_requests')}
              value={stats.total_requests.toLocaleString()}
              icon={<Activity className="w-5 h-5" />}
              accentColor="var(--ag-primary)"
            />
            <StatCard
              title={t('usage.total_tokens')}
              value={fmtNum(stats.total_tokens)}
              icon={<Hash className="w-5 h-5" />}
              accentColor="var(--ag-info)"
            />
            <StatCard
              title={t('usage.total_cost')}
              value={`$${stats.total_cost.toFixed(4)}`}
              icon={<DollarSign className="w-5 h-5" />}
              accentColor="var(--ag-warning)"
            />
            <StatCard
              title={t('usage.actual_cost')}
              value={`$${stats.total_actual_cost.toFixed(4)}`}
              icon={<Coins className="w-5 h-5" />}
              accentColor="var(--ag-success)"
            />
          </div>

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <DistributionCard
              title={t('usage.model_distribution')}
              data={modelDistribution}
            />
            <DistributionCard
              title={t('usage.group_distribution')}
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
        <div className="w-full sm:w-72">
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
        <div className="w-full sm:w-40">
          <Select
            aria-label={t('usage.platform')}
            fullWidth
            selectedKey={filters.platform || ''}
            onSelectionChange={(key) => updateFilter('platform', key == null ? '' : String(key))}
          >
            <Select.Trigger>
              <Select.Value>
                {filters.platform ? selectedPlatformLabel : (
                  <span className="text-text-tertiary">{t('usage.platform')}</span>
                )}
              </Select.Value>
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox items={platformOptions}>
                {(item) => (
                  <ListBox.Item id={item.id} textValue={item.label}>
                    {item.label}
                  </ListBox.Item>
                )}
              </ListBox>
            </Select.Popover>
          </Select>
        </div>
        <div className="w-full sm:w-40">
          <HeroTextField fullWidth>
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
              <Input
                className="pl-9"
                placeholder={t('usage.model_placeholder')}
                value={filters.model || ''}
                onChange={(e) => updateFilter('model', e.target.value)}
              />
            </div>
          </HeroTextField>
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
        <Switch
          aria-label="自动更新"
          className="shrink-0"
          isSelected={autoRefresh}
          size="sm"
          onChange={handleAutoRefreshChange}
        >
          <Switch.Control>
            <Switch.Thumb />
          </Switch.Control>
          <Switch.Content>
            <span className="text-sm text-text-secondary">自动更新</span>
          </Switch.Content>
        </Switch>
      </div>

      {/* 使用记录表格 */}
      <UsageRecordsTable
        ariaLabel={t('usage.title', 'Usage')}
        columns={columns}
        emptyDescription={t('usage.empty_description', '调整筛选条件后重试')}
        emptyTitle={t('common.no_data')}
        isLoading={isLoading}
        page={page}
        pageSize={pageSize}
        rows={data?.list ?? []}
        setPage={setPage}
        setPageSize={setPageSize}
        total={total}
      />
    </div>
  );
}
