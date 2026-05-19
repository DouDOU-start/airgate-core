import { useState, useMemo, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Card, Tabs } from '@heroui/react';
import {
  PieChart, Pie, Cell, ResponsiveContainer, Tooltip as RechartsTooltip,
  LineChart, Line, XAxis, YAxis, CartesianGrid, Legend,
} from 'recharts';
import {
  Wallet, Zap, Activity, Coins,
} from 'lucide-react';
import { useAuth } from '../../app/providers/AuthProvider';
import { usageApi } from '../../shared/api/usage';
import { queryKeys } from '../../shared/queryKeys';
import { CompactDataTable } from '../../shared/components/CompactDataTable';
import { CostValue } from '../../shared/components/CostValue';
import { PIE_CHART_COLORS, USAGE_TOKEN_COLORS } from '../../shared/constants';

const PIE_COLORS = PIE_CHART_COLORS;
const TOKEN_TREND_LINE_ORDER = ['input', 'output', 'cacheRead'] as const;

type PieTooltipPayload = Array<{
  name?: unknown;
  payload?: {
    name?: unknown;
  };
}>;

function PieNameTooltip({
  active,
  payload,
}: {
  active?: boolean;
  payload?: PieTooltipPayload;
}) {
  const name = payload?.[0]?.payload?.name ?? payload?.[0]?.name;
  if (!active || name == null || name === '') return null;

  return (
    <div className="max-w-56 truncate rounded-[var(--radius)] border border-border bg-surface px-2.5 py-1.5 text-xs font-medium text-text shadow-lg">
      {String(name)}
    </div>
  );
}

type RangePreset = 'today' | '7d' | '30d' | '90d';
type MetricTone = 'blue' | 'emerald' | 'amber' | 'indigo';
type TokenTrendKey = typeof TOKEN_TREND_LINE_ORDER[number];

const RANGE_PRESETS = ['today', '7d', '30d', '90d'] as const;
const METRIC_TONE_CLASSES: Record<MetricTone, string> = {
  amber: 'bg-amber-100 text-amber-600 ring-amber-200 dark:bg-amber-400/15 dark:text-amber-300 dark:ring-amber-400/25',
  blue: 'bg-blue-100 text-blue-600 ring-blue-200 dark:bg-blue-400/15 dark:text-blue-300 dark:ring-blue-400/25',
  emerald: 'bg-success-subtle text-success ring-success/25',
  indigo: 'bg-indigo-100 text-indigo-600 ring-indigo-200 dark:bg-indigo-400/15 dark:text-indigo-300 dark:ring-indigo-400/25',
};

function DashboardCard({ children, title }: { children: ReactNode; title: string }) {
  return (
    <Card className="ag-dashboard-panel">
      <div className="flex items-center justify-between gap-3 p-3 pb-2 2xl:p-4 2xl:pb-2">
        <h3 className="text-base font-semibold leading-none text-text">{title}</h3>
      </div>
      <Card.Content className="px-3 pb-3 2xl:px-4 2xl:pb-4">{children}</Card.Content>
    </Card>
  );
}

function StatCard({
  icon,
  tone,
  title,
  value,
}: {
  icon: ReactNode;
  tone: MetricTone;
  title: string;
  value: ReactNode;
}) {
  return (
    <Card className="ag-dashboard-metric min-h-[72px] 2xl:min-h-[78px]">
      <Card.Content className="ag-dashboard-metric-content p-3 2xl:p-3.5">
        <div className="ag-dashboard-metric-copy">
          <div className="truncate text-sm font-semibold tracking-normal text-text-tertiary">{title}</div>
          <div className="mt-1 flex min-w-0 items-baseline gap-2">
            <div className="flex min-w-0 items-baseline font-mono text-[22px] font-semibold leading-none text-text 2xl:text-2xl">
              {value}
            </div>
          </div>
        </div>
        <span className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-[var(--field-radius)] ring-1 shadow-sm 2xl:h-11 2xl:w-11 ${METRIC_TONE_CLASSES[tone]}`}>
          {icon}
        </span>
      </Card.Content>
    </Card>
  );
}

function fmtNum(n: number | undefined | null): string {
  if (n == null) return '0';
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(2)}B`;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(2)}K`;
  return n.toLocaleString();
}

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

/** 格式化趋势图时间标签：含小时取 HH:00，纯日期取 MM/DD。
 *
 * 后端从 v1 起会按调用方时区（client.ts 自动附带的 tz 参数）格式化桶 key，
 * 因此 timeStr 已经是用户本地时区下的字符串，前端只需直接截取，不要再做时区换算。
 */
function fmtTime(timeStr: string): string {
  if (timeStr.includes(' ')) {
    const time = timeStr.split(' ')[1] ?? '';
    return time.slice(0, 5) || timeStr;
  }
  const parts = timeStr.split('-');
  if (parts.length === 3) {
    return `${parts[1]}/${parts[2]}`;
  }
  return timeStr;
}

function TokenTrendTooltip({
  active,
  label,
  payload,
}: {
  active?: boolean;
  label?: string;
  payload?: Array<{ color?: string; dataKey?: string; name?: string; value?: number }>;
}) {
  const { t } = useTranslation();
  if (!active || !payload?.length) return null;

  const labels: Record<TokenTrendKey, string> = {
    cacheRead: t('usage.cache_read'),
    input: t('usage.input'),
    output: t('usage.output'),
  };
  const orderedPayload = [...payload].sort((a, b) => {
    const aIndex = TOKEN_TREND_LINE_ORDER.indexOf(a.dataKey as TokenTrendKey);
    const bIndex = TOKEN_TREND_LINE_ORDER.indexOf(b.dataKey as TokenTrendKey);
    return (aIndex < 0 ? TOKEN_TREND_LINE_ORDER.length : aIndex) - (bIndex < 0 ? TOKEN_TREND_LINE_ORDER.length : bIndex);
  });

  return (
    <div className="rounded-[var(--radius)] border border-border bg-surface px-3 py-2 text-xs text-text shadow-lg">
      <div className="mb-1 font-medium">{label}</div>
      <div className="space-y-1">
        {orderedPayload.map((item) => {
          const key = item.dataKey as TokenTrendKey;
          return (
            <div key={item.dataKey} className="flex items-center gap-2">
              <span className="h-2 w-2 rounded-full" style={{ background: item.color }} />
              <span className="text-text">{labels[key] ?? item.name ?? item.dataKey}</span>
              <span className="font-mono">{fmtNum(Number(item.value ?? 0))}</span>
            </div>
          );
        })}
      </div>
    </div>
  );
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
    queryKey: ['user-trend', dateRange, granularity],
    queryFn: () => usageApi.userTrend({ granularity, ...dateRange }),
  });

  const models = stats?.by_model ?? [];

  const trendData = useMemo(
    () => (trend ?? []).map((b) => ({
      time: fmtTime(b.time),
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
          title={t('usage.actual_cost')}
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
          <div className="ag-distribution-card-body grid items-start gap-3 2xl:grid-cols-[176px_minmax(0,1fr)]">
            <div className="ag-distribution-chart-frame">
              {models.length > 0 ? (
                <PieChart width={176} height={176}>
                  <Pie data={models.map((m) => ({ name: m.model, value: m.tokens }))} cx="50%" cy="50%" innerRadius={42} outerRadius={68} dataKey="value" isAnimationActive={false} minAngle={3} stroke="var(--ag-surface)" strokeWidth={2}>
                    {models.map((_, i) => <Cell key={i} fill={PIE_COLORS[i % PIE_COLORS.length]} />)}
                  </Pie>
                  <RechartsTooltip
                    animationDuration={0}
                    content={<PieNameTooltip />}
                    cursor={false}
                    isAnimationActive={false}
                  />
                </PieChart>
              ) : (
                <div className="flex h-44 w-44 items-center justify-center text-xs text-text">{t('common.no_data')}</div>
              )}
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
                        <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: PIE_COLORS[index % PIE_COLORS.length] }} />
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
                  <RechartsTooltip content={<TokenTrendTooltip />} />
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
            <div className="flex h-[248px] items-center justify-center text-sm text-text 2xl:h-[288px]">{t('common.no_data')}</div>
          )}
        </DashboardCard>
      </div>
    </div>
  );
}
