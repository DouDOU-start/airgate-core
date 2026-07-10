import { useState, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Tabs } from '@heroui/react';
import {
  ResponsiveContainer, Tooltip as RechartsTooltip,
  LineChart, Line, XAxis, YAxis, CartesianGrid, Legend,
} from 'recharts';
import {
  Wallet, Zap, Activity, Coins,
  LineChart as LineChartIcon, PieChart as PieChartIcon,
} from 'lucide-react';
import { useAuth } from '../../app/providers/AuthProvider';
import { usageApi } from '../../shared/api/usage';
import { queryKeys } from '../../shared/queryKeys';
import { ChartEmptyState } from '../../shared/components/ChartEmptyState';
import { ChartLineTooltip, UsagePieChart } from '../../shared/components/charts';
import { CompactDataTable } from '../../shared/components/CompactDataTable';
import { CostValue } from '../../shared/components/CostValue';
import { DashboardCard } from '../../shared/components/DashboardCard';
import { StatCard } from '../../shared/components/StatCard';
import { PIE_CHART_COLORS, RANGE_PRESETS, USAGE_TOKEN_COLORS, type RangePreset } from '../../shared/constants';
import { fmtNum, fmtTrendTime } from '../../shared/utils/format';

/** 用户概览趋势图只展示三条线（无 cacheCreation/比率线） */
const TOKEN_TREND_LINE_ORDER = ['input', 'output', 'cacheRead'] as const;
type TokenTrendKey = typeof TOKEN_TREND_LINE_ORDER[number];

function rangeToDate(range: RangePreset): { start_date: string; end_date: string } {
  const now = new Date();
  const end = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`;
  const d = new Date();
  switch (range) {
    case 'today': break;
    case '7d': d.setDate(d.getDate() - 6); break;
    case '30d': d.setDate(d.getDate() - 29); break;
    case '90d': d.setDate(d.getDate() - 89); break;
  }
  const start = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  return { start_date: start, end_date: end };
}

export default function UserOverviewPage() {
  const { t } = useTranslation();
  const { user } = useAuth();
  const [range, setRange] = useState<RangePreset>('today');

  const dateRange = useMemo(() => rangeToDate(range), [range]);
  const granularity = range === 'today' ? 'hour' : 'day';

  // 统计数据（按时间范围）
  const { data: stats } = useQuery({
    queryKey: queryKeys.userUsageStats(dateRange),
    queryFn: () => usageApi.userStats(dateRange),
  });

  // 趋势数据
  const { data: trend } = useQuery({
    queryKey: queryKeys.userTrend(dateRange, granularity),
    queryFn: () => usageApi.userTrend({ granularity, ...dateRange }),
  });

  const models = stats?.by_model ?? [];

  const trendData = useMemo(
    () => (trend ?? []).map((b) => ({
      time: fmtTrendTime(b.time),
      input: b.input_tokens,
      output: b.output_tokens,
      cacheRead: b.cache_read,
    })),
    [trend],
  );
  const tokenTrendLabels: Record<TokenTrendKey, string> = {
    cacheRead: t('usage.cache_read'),
    input: t('usage.input'),
    output: t('usage.output'),
  };

  return (
    <div className="space-y-5 2xl:space-y-6">
      {/* 账户信息 */}
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4 2xl:gap-4">
        <StatCard
          title={t('user_overview.balance')}
          value={`$${(user?.balance ?? 0).toFixed(2)}`}
          icon={<Wallet className="w-5 h-5" />}
          tone="blue"
        />
        <StatCard
          title={t('user_overview.max_concurrency')}
          value={String(user?.max_concurrency ?? 0)}
          icon={<Zap className="w-5 h-5" />}
          tone="indigo"
        />
        <StatCard
          title={t('usage.total_requests')}
          value={(stats?.total_requests ?? 0).toLocaleString()}
          icon={<Activity className="w-5 h-5" />}
          tone="emerald"
        />
        <StatCard
          title={t('user_overview.spent')}
          value={<CostValue value={stats?.total_actual_cost ?? 0} decimals={4} tone="actual" />}
          icon={<Coins className="w-5 h-5" />}
          tone="amber"
        />
      </div>

      {/* 时间范围选择 */}
      <div className="ag-dashboard-toolbar flex flex-col gap-3 p-4 2xl:p-5 sm:flex-row sm:items-center">
        <span className="shrink-0 text-sm font-semibold text-text">{t('dashboard.time_range')}</span>
        <Tabs
          className="ag-segmented-tabs ag-segmented-tabs-compact"
          selectedKey={range}
          onSelectionChange={(key) => setRange(key as RangePreset)}
        >
          <Tabs.List>
            {RANGE_PRESETS.map((r, index) => (
              <Tabs.Tab key={r} id={r}>
                {index > 0 ? <Tabs.Separator /> : null}
                <Tabs.Indicator />
                <span>{t(`dashboard.range_${r}`)}</span>
              </Tabs.Tab>
            ))}
          </Tabs.List>
        </Tabs>
      </div>

      {/* 模型分布 + Token 趋势 */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {/* 模型分布饼图 */}
        <DashboardCard title={t('dashboard.model_distribution')}>
          {models.length === 0 ? (
            <ChartEmptyState className="min-h-44" icon={<PieChartIcon className="h-5 w-5" />} />
          ) : (
          <div className="ag-distribution-card-body grid items-start gap-3 2xl:grid-cols-[176px_minmax(0,1fr)]">
            <div className="ag-distribution-chart-frame">
              <UsagePieChart data={models.map((m) => ({ name: m.model, value: m.tokens }))} />
            </div>
            <div className="ag-distribution-table-scroll">
              <CompactDataTable
                ariaLabel={t('dashboard.model_distribution')}
                className="ag-compact-data-table--dense"
                emptyText={t('common.no_data')}
                minWidth={480}
                rowKey={(row) => row.model}
                rows={models}
                columns={[
                  {
                    key: 'model',
                    title: t('usage.model'),
                    width: '32%',
                    render: (row, index) => (
                      <>
                        <span className="shrink-0 font-mono text-[11px] font-semibold text-text">#{index + 1}</span>
                        <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: PIE_CHART_COLORS[index % PIE_CHART_COLORS.length] }} />
                        <span className="min-w-0 truncate font-medium text-text" title={row.model}>{row.model}</span>
                      </>
                    ),
                  },
                  {
                    align: 'end',
                    key: 'requests',
                    title: t('dashboard.requests'),
                    width: '20%',
                    render: (row) => <span className="truncate font-mono text-text">{row.requests.toLocaleString()}</span>,
                  },
                  {
                    align: 'end',
                    key: 'tokens',
                    title: t('dashboard.tokens'),
                    width: '24%',
                    render: (row) => <span className="truncate font-mono text-text">{fmtNum(row.tokens)}</span>,
                  },
                  {
                    align: 'end',
                    key: 'cost',
                    title: t('usage.cost'),
                    width: '24%',
                    render: (row) => <CostValue className="truncate font-mono" value={row.actual_cost} decimals={4} tone="actual" />,
                  },
                ]}
              />
            </div>
          </div>
          )}
        </DashboardCard>

        {/* Token 趋势 */}
        <DashboardCard title={t('dashboard.token_trend')}>
          {trendData.length > 0 ? (
            <div className="h-[248px] w-full min-w-0 2xl:h-[288px]">
              <ResponsiveContainer width="100%" height="100%" debounce={80} initialDimension={{ width: 600, height: 248 }}>
                <LineChart data={trendData} margin={{ bottom: 0, left: -18, right: 4, top: 4 }}>
                  <CartesianGrid stroke="var(--ag-border-subtle)" vertical={false} />
                  <XAxis axisLine={false} dataKey="time" tick={{ fill: 'var(--ag-text)', fontSize: 11 }} tickLine={false} />
                  <YAxis axisLine={false} tick={{ fill: 'var(--ag-text)', fontSize: 11 }} tickFormatter={(v: number) => fmtNum(v)} tickLine={false} />
                  <RechartsTooltip content={<ChartLineTooltip order={TOKEN_TREND_LINE_ORDER} />} />
                  <Legend
                    height={24}
                    content={() => (
                      <div className="flex flex-wrap items-center justify-center gap-x-4 gap-y-1 pt-1 text-[11px] text-text">
                        {TOKEN_TREND_LINE_ORDER.map((key) => (
                          <span key={key} className="inline-flex items-center gap-1.5">
                            <span className="h-2 w-2 rounded-full" style={{ background: USAGE_TOKEN_COLORS[key] }} />
                            <span>{tokenTrendLabels[key]}</span>
                          </span>
                        ))}
                      </div>
                    )}
                  />
                  <Line type="monotone" dataKey="input" name={tokenTrendLabels.input} stroke={USAGE_TOKEN_COLORS.input} strokeWidth={2.5} dot={false} isAnimationActive={false} />
                  <Line type="monotone" dataKey="output" name={tokenTrendLabels.output} stroke={USAGE_TOKEN_COLORS.output} strokeWidth={2.5} dot={false} isAnimationActive={false} />
                  <Line type="monotone" dataKey="cacheRead" name={tokenTrendLabels.cacheRead} stroke={USAGE_TOKEN_COLORS.cacheRead} strokeWidth={2.5} dot={false} isAnimationActive={false} />
                </LineChart>
              </ResponsiveContainer>
            </div>
          ) : (
            <ChartEmptyState className="h-[248px] 2xl:h-[288px]" icon={<LineChartIcon className="h-5 w-5" />} />
          )}
        </DashboardCard>
      </div>
    </div>
  );
}
