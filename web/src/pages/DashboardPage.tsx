import { useMemo, useState, type ReactNode } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Card, ComboBox, Input, Label, ListBox, Select, Skeleton, Tabs } from '@heroui/react';
import {
  CartesianGrid,
  Cell,
  Legend,
  Line,
  LineChart,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip as RechartsTooltip,
  XAxis,
  YAxis,
} from 'recharts';
import {
  Activity,
  CalendarDays,
  Clock,
  Coins,
  Database,
  KeyRound,
  Monitor,
  RefreshCw,
  Search,
  Users,
  Zap,
} from 'lucide-react';
import { decorativePalette } from '@doudou-start/airgate-theme';
import { dashboardApi } from '../shared/api/dashboard';
import { usersApi } from '../shared/api/users';
import { queryKeys } from '../shared/queryKeys';
import { PIE_CHART_COLORS, USAGE_TOKEN_COLORS } from '../shared/constants';
import { CompactDataTable } from '../shared/components/CompactDataTable';
import { useDebouncedValue } from '../shared/hooks/useDebouncedValue';
import { CostPair, CostValue } from '../shared/components/CostValue';
import type { DashboardStatsResp, DashboardTrendResp } from '../shared/types';

const PIE_COLORS = PIE_CHART_COLORS;
const USER_COLORS = [...decorativePalette];
const TOKEN_TREND_LINE_ORDER: Array<keyof typeof USAGE_TOKEN_COLORS> = ['input', 'output', 'cacheCreation', 'cacheRead', 'cacheRatio', 'cacheCumulativeRatio'];
const TOKEN_TREND_RATIO_KEYS = new Set<keyof typeof USAGE_TOKEN_COLORS>(['cacheRatio', 'cacheCumulativeRatio']);

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
type Granularity = 'hour' | 'day';

const RANGE_PRESETS = ['today', '7d', '30d', '90d'] as const;
type MetricTone = 'blue' | 'violet' | 'emerald' | 'teal' | 'amber' | 'indigo' | 'purple' | 'rose';
type MetaTone = 'default' | 'success' | 'warning' | 'danger' | 'accent';

const METRIC_TONE_CLASSES: Record<MetricTone, string> = {
  amber: 'bg-amber-100 text-amber-600 ring-amber-200 dark:bg-amber-400/15 dark:text-amber-300 dark:ring-amber-400/25',
  blue: 'bg-blue-100 text-blue-600 ring-blue-200 dark:bg-blue-400/15 dark:text-blue-300 dark:ring-blue-400/25',
  emerald: 'bg-success-subtle text-success ring-success/25',
  indigo: 'bg-indigo-100 text-indigo-600 ring-indigo-200 dark:bg-indigo-400/15 dark:text-indigo-300 dark:ring-indigo-400/25',
  purple: 'bg-purple-100 text-purple-600 ring-purple-200 dark:bg-purple-400/15 dark:text-purple-300 dark:ring-purple-400/25',
  rose: 'bg-rose-100 text-rose-600 ring-rose-200 dark:bg-rose-400/15 dark:text-rose-300 dark:ring-rose-400/25',
  teal: 'bg-teal-100 text-teal-600 ring-teal-200 dark:bg-teal-400/15 dark:text-teal-300 dark:ring-teal-400/25',
  violet: 'bg-violet-100 text-violet-600 ring-violet-200 dark:bg-violet-400/15 dark:text-violet-300 dark:ring-violet-400/25',
};

const META_TONE_CLASSES: Record<MetaTone, string> = {
  accent: 'text-primary',
  danger: 'text-danger',
  default: 'text-text',
  success: 'text-emerald-600 dark:text-emerald-400',
  warning: 'text-amber-600 dark:text-amber-400',
};

function fmtNum(n: number | undefined | null): string {
  if (n == null) return '0';
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(2)}B`;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(2)}K`;
  return n.toLocaleString();
}

function fmtDurationMs(ms: number | undefined | null): string {
  if (ms == null || ms <= 0) return '0s';
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const seconds = ms / 1000;
  if (seconds >= 100) return `${Math.round(seconds)}s`;
  return `${seconds.toFixed(seconds >= 10 ? 1 : 2)}s`;
}

function fmtTime(timeStr: string): string {
  if (timeStr.includes(' ')) {
    const time = timeStr.split(' ')[1] ?? '';
    return time.slice(0, 5) || timeStr;
  }
  const parts = timeStr.split('-');
  if (parts.length === 3) return `${parts[1]}/${parts[2]}`;
  return timeStr;
}

function DashboardCard({
  children,
  extra,
  title,
}: {
  children: ReactNode;
  extra?: ReactNode;
  title?: string;
}) {
  const hasHeader = Boolean(title || extra);

  return (
    <Card className="ag-dashboard-panel">
      {hasHeader ? (
        <div
          className={`flex items-center gap-3 p-3 pb-2 2xl:p-4 2xl:pb-2 ${title ? 'justify-between' : 'justify-end'}`}
        >
          {title ? <h3 className="text-base font-semibold leading-none text-text">{title}</h3> : null}
          {extra ? (
            <div className="shrink-0">{extra}</div>
          ) : null}
        </div>
      ) : null}
      <Card.Content className={hasHeader ? 'px-3 pb-3 2xl:px-4 2xl:pb-4' : 'p-3 2xl:p-4'}>{children}</Card.Content>
    </Card>
  );
}

function MetricCard({
  icon,
  meta,
  metaTone = 'default',
  title,
  tone,
  value,
  valueSuffix,
}: {
  icon: ReactNode;
  meta: ReactNode;
  metaTone?: MetaTone;
  title: string;
  tone: MetricTone;
  value: ReactNode;
  valueSuffix?: string;
}) {
  return (
    <Card className="ag-dashboard-metric min-h-[72px] 2xl:min-h-[78px]">
      <Card.Content className="ag-dashboard-metric-content p-3 2xl:p-3.5">
        <div className="ag-dashboard-metric-copy">
          <div className="truncate text-sm font-semibold tracking-normal text-text-tertiary">{title}</div>
          <div className="mt-1 flex min-w-0 items-baseline gap-2">
            <div className="flex min-w-0 items-baseline font-mono text-[22px] font-semibold leading-none text-text 2xl:text-2xl">
              {value}
              {valueSuffix ? <span className="ml-1.5 text-sm font-medium text-text-tertiary">{valueSuffix}</span> : null}
            </div>
            <div className={`min-w-0 truncate text-xs font-semibold ${META_TONE_CLASSES[metaTone]}`}>{meta}</div>
          </div>
        </div>
        <span className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-[var(--field-radius)] ring-1 shadow-sm 2xl:h-11 2xl:w-11 ${METRIC_TONE_CLASSES[tone]}`}>
          {icon}
        </span>
      </Card.Content>
    </Card>
  );
}

function StatsSkeleton() {
  return (
    <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4 2xl:gap-4">
      {Array.from({ length: 8 }).map((_, index) => (
        <Card className="ag-dashboard-metric min-h-[72px] 2xl:min-h-[78px]" key={index}>
          <Card.Content className="ag-dashboard-metric-content p-3 2xl:p-3.5">
            <div className="ag-dashboard-metric-copy space-y-2">
              <Skeleton className="h-3 w-24" />
              <div className="flex items-baseline gap-2">
                <Skeleton className="h-6 w-24" />
                <Skeleton className="h-3 w-32" />
              </div>
            </div>
            <Skeleton className="h-10 w-10 shrink-0 rounded-[var(--field-radius)]" />
          </Card.Content>
        </Card>
      ))}
    </div>
  );
}

function StatsCards({ stats }: { stats: DashboardStatsResp }) {
  const { t } = useTranslation();
  const todayImageRequests = stats.today_image_requests ?? 0;
  const todayTextRequests = Math.max(0, (stats.today_requests ?? 0) - todayImageRequests);
  return (
    <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4 2xl:gap-4">
      <MetricCard
        icon={<KeyRound className="h-5 w-5" />}
        tone="blue"
        title={t('dashboard.api_keys')}
        value={stats.total_api_keys}
        meta={t('dashboard.api_keys_enabled', { count: stats.enabled_api_keys })}
      />
      <MetricCard
        icon={<Monitor className="h-5 w-5" />}
        tone="violet"
        title={t('dashboard.accounts')}
        value={stats.total_accounts}
        meta={t('dashboard.accounts_status', { enabled: stats.enabled_accounts, errors: stats.error_accounts })}
      />
      <MetricCard
        icon={<Activity className="h-5 w-5" />}
        tone="emerald"
        title={t('dashboard.today_requests')}
        value={`${fmtNum(todayTextRequests)}/${fmtNum(todayImageRequests)}`}
        valueSuffix={t('dashboard.image_suffix')}
        meta={t('dashboard.alltime_requests', { count: fmtNum(stats.alltime_requests) } as Record<string, string>)}
      />
      <MetricCard
        icon={<Users className="h-5 w-5" />}
        tone="teal"
        title={t('dashboard.users')}
        value={t('dashboard.new_users', { count: stats.new_users_today })}
        meta={`${t('dashboard.total_count', { count: stats.total_users })}  ${t('dashboard.active_users_label', { count: stats.active_users })}`}
      />
      <MetricCard
        icon={<Coins className="h-5 w-5" />}
        tone="amber"
        metaTone="warning"
        title={t('dashboard.today_tokens')}
        value={fmtNum(stats.today_tokens)}
        meta={<CostPair actual={stats.today_cost} standard={stats.today_standard_cost} />}
      />
      <MetricCard
        icon={<Database className="h-5 w-5" />}
        tone="indigo"
        metaTone="success"
        title={t('dashboard.total_tokens')}
        value={fmtNum(stats.alltime_tokens)}
        meta={<CostPair actual={stats.alltime_cost} standard={stats.alltime_standard_cost} />}
      />
      <MetricCard
        icon={<Zap className="h-5 w-5" />}
        tone="purple"
        metaTone="accent"
        title={t('dashboard.performance')}
        value={Math.round(stats.rpm ?? 0)}
        valueSuffix={t('dashboard.rpm')}
        meta={`${fmtNum(stats.tpm ?? 0)} ${t('dashboard.tpm')}`}
      />
      <MetricCard
        icon={<Clock className="h-5 w-5" />}
        tone="rose"
        title={t('dashboard.avg_response')}
        value={`${fmtDurationMs(stats.avg_first_token_ms)}/${fmtDurationMs(stats.avg_duration_ms)}`}
        meta={
          (stats.avg_image_duration_ms ?? 0) > 0
            ? t('dashboard.image_response_time', { time: fmtDurationMs(stats.avg_image_duration_ms) })
            : undefined
        }
      />
    </div>
  );
}

function ChartTooltip({
  active,
  label,
  payload,
}: {
  active?: boolean;
  label?: string;
  payload?: Array<{ color?: string; dataKey?: string; name?: string; payload?: Record<string, unknown>; value?: number }>;
}) {
  if (!active || !payload?.length) return null;
  return (
    <div className="rounded-[var(--radius)] border border-border bg-surface px-3 py-2 text-xs text-text shadow-lg">
      <div className="mb-1 font-medium">{label}</div>
      <div className="space-y-1">
        {payload.map((item) => (
          <div key={`${item.dataKey}-${item.name}`} className="flex items-center gap-2">
            <span className="h-2 w-2 rounded-full" style={{ background: item.color }} />
            <span className="text-text">{item.name ?? item.dataKey}</span>
            <span className="font-mono">{fmtNum(Number(item.value ?? 0))}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function TokenTrendTooltip({
  active,
  label,
  payload,
}: {
  active?: boolean;
  label?: string;
  payload?: Array<{ color?: string; dataKey?: string; payload?: { actualCost?: number; standardCost?: number }; value?: number }>;
}) {
  const { t } = useTranslation();
  if (!active || !payload?.length) return null;
  const datum = payload[0]?.payload;
  const labels: Record<string, string> = {
    input: t('dashboard.input'),
    output: t('dashboard.output'),
    cacheCreation: t('dashboard.cache_creation'),
    cacheRead: t('dashboard.cache_read'),
    cacheRatio: t('dashboard.cache_ratio'),
    cacheCumulativeRatio: t('dashboard.cache_cumulative_ratio'),
  };
  const orderedPayload = [...payload].sort((a, b) => {
    const aIndex = TOKEN_TREND_LINE_ORDER.indexOf(String(a.dataKey) as keyof typeof USAGE_TOKEN_COLORS);
    const bIndex = TOKEN_TREND_LINE_ORDER.indexOf(String(b.dataKey) as keyof typeof USAGE_TOKEN_COLORS);
    return (aIndex < 0 ? TOKEN_TREND_LINE_ORDER.length : aIndex) - (bIndex < 0 ? TOKEN_TREND_LINE_ORDER.length : bIndex);
  });

  return (
    <div className="rounded-[var(--radius)] border border-border bg-surface px-3 py-2 text-xs text-text shadow-lg">
      <div className="mb-1 font-medium">{label}</div>
      <div className="space-y-1">
        {orderedPayload.map((item) => (
          <div key={item.dataKey} className="flex items-center gap-2">
            <span className="h-2 w-2 rounded-full" style={{ background: item.color }} />
            <span className="text-text">{labels[item.dataKey ?? ''] ?? item.dataKey}</span>
            <span className="font-mono">
              {TOKEN_TREND_RATIO_KEYS.has(item.dataKey as keyof typeof USAGE_TOKEN_COLORS) ? `${Number(item.value ?? 0).toFixed(1)}%` : fmtNum(Number(item.value ?? 0))}
            </span>
          </div>
        ))}
      </div>
      <div className="mt-2 border-t border-border pt-2 text-text">
        {t('dashboard.actual')}: <CostValue className="font-mono" value={datum?.actualCost} tone="actual" />
        {' / '}
        {t('dashboard.standard')}: <CostValue className="font-mono" value={datum?.standardCost} tone="standard" />
      </div>
    </div>
  );
}

type DashboardDistributionTableRow = {
  actualCost: number;
  key: string | number;
  name: string;
  requests: number;
  standardCost: number;
  tokens: number;
};

const DASHBOARD_DISTRIBUTION_COLUMN_WIDTHS = {
  name: '32%',
  requests: '16%',
  tokens: '18%',
  actual: '17%',
  standard: '17%',
} as const;

function ModelDistributionCard({ trend }: { trend: DashboardTrendResp }) {
  const { t } = useTranslation();
  const [tab, setTab] = useState<'model' | 'user'>('model');
  const models = trend.model_distribution ?? [];
  const users = trend.user_ranking ?? [];
  const modelPieData = useMemo(() => models.map((item) => ({ name: item.model, value: item.requests })), [models]);
  const userPieData = useMemo(() => users.map((item) => ({ name: item.email, value: item.tokens })), [users]);
  const activePieData = tab === 'model' ? modelPieData : userPieData;
  const activeTitle = tab === 'model' ? t('dashboard.model_distribution') : t('dashboard.user_ranking');
  const tableRows: DashboardDistributionTableRow[] = useMemo(
    () => (
      tab === 'model'
        ? models.map((item, index) => ({
            actualCost: item.actual_cost,
            key: item.model || index,
            name: item.model,
            requests: item.requests,
            standardCost: item.standard_cost,
            tokens: item.tokens,
          }))
        : users.map((item, index) => ({
            actualCost: item.actual_cost,
            key: item.user_id || index,
            name: item.email,
            requests: item.requests,
            standardCost: item.standard_cost,
            tokens: item.tokens,
          }))
    ),
    [models, tab, users],
  );
  const firstColumnTitle = tab === 'model' ? t('dashboard.model') : t('dashboard.email');
  const distributionTabs = (
    <Tabs className="ag-segmented-tabs ag-segmented-tabs-compact" selectedKey={tab} onSelectionChange={(key) => setTab(key as 'model' | 'user')}>
      <Tabs.List>
        <Tabs.Tab id="model">
          <Tabs.Indicator />
          <span>{t('dashboard.model_distribution')}</span>
        </Tabs.Tab>
        <Tabs.Tab id="user">
          <Tabs.Separator />
          <Tabs.Indicator />
          <span>{t('dashboard.user_ranking')}</span>
        </Tabs.Tab>
      </Tabs.List>
    </Tabs>
  );

  return (
    <DashboardCard title={activeTitle} extra={distributionTabs}>
      <div className="ag-distribution-card-body grid items-start gap-3 2xl:grid-cols-[176px_minmax(0,1fr)]">
        <div className="ag-distribution-chart-frame">
          {activePieData.length > 0 ? (
            <PieChart width={176} height={176}>
              <Pie data={activePieData} cx="50%" cy="50%" dataKey="value" innerRadius={42} isAnimationActive={false} minAngle={3} outerRadius={68} stroke="var(--ag-surface)" strokeWidth={2}>
                {activePieData.map((_, index) => (
                  <Cell key={index} fill={PIE_COLORS[index % PIE_COLORS.length]} />
                ))}
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
            ariaLabel={activeTitle}
            className="ag-compact-data-table--dense"
            emptyText={t('common.no_data')}
            minWidth={480}
            rowKey={(row) => row.key}
            rows={tableRows}
            columns={[
              {
                key: 'name',
                title: firstColumnTitle,
                width: DASHBOARD_DISTRIBUTION_COLUMN_WIDTHS.name,
                render: (row, index) => (
                  <>
                    <span className="shrink-0 font-mono text-[11px] font-semibold text-text">#{index + 1}</span>
                    <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: PIE_COLORS[index % PIE_COLORS.length] }} />
                    <span className="min-w-0 truncate font-medium text-text" title={row.name}>{row.name}</span>
                  </>
                ),
              },
              {
                align: 'end',
                key: 'requests',
                title: t('dashboard.requests'),
                width: DASHBOARD_DISTRIBUTION_COLUMN_WIDTHS.requests,
                render: (row) => <span className="truncate font-mono text-text">{row.requests.toLocaleString()}</span>,
              },
              {
                align: 'end',
                key: 'tokens',
                title: t('dashboard.tokens'),
                width: DASHBOARD_DISTRIBUTION_COLUMN_WIDTHS.tokens,
                render: (row) => <span className="truncate font-mono text-text">{fmtNum(row.tokens)}</span>,
              },
              {
                align: 'end',
                key: 'actual',
                title: t('dashboard.actual'),
                width: DASHBOARD_DISTRIBUTION_COLUMN_WIDTHS.actual,
                render: (row) => <CostValue className="truncate font-mono" value={row.actualCost} tone="actual" />,
              },
              {
                align: 'end',
                key: 'standard',
                title: t('dashboard.standard'),
                width: DASHBOARD_DISTRIBUTION_COLUMN_WIDTHS.standard,
                render: (row) => <CostValue className="truncate font-mono" value={row.standardCost} tone="standard" />,
              },
            ]}
          />
        </div>
      </div>
    </DashboardCard>
  );
}

function TokenTrendCard({ trend }: { trend: DashboardTrendResp }) {
  const { t } = useTranslation();
  const lineLabels: Record<string, string> = {
    input: t('dashboard.input'),
    output: t('dashboard.output'),
    cacheCreation: t('dashboard.cache_creation'),
    cacheRead: t('dashboard.cache_read'),
    cacheRatio: t('dashboard.cache_ratio'),
    cacheCumulativeRatio: t('dashboard.cache_cumulative_ratio'),
  };
  const chartData = useMemo(() => {
    let cumulativeCache = 0;
    let cumulativeTotal = 0;

    return (trend.token_trend ?? []).map((item) => {
      const cacheRead = item.cache_read ?? item.cached_input ?? 0;
      const cacheCreation = item.cache_creation ?? 0;
      const cacheTokens = cacheRead + cacheCreation;
      const totalTokens = item.input_tokens + item.output_tokens + cacheTokens;
      cumulativeCache += cacheTokens;
      cumulativeTotal += totalTokens;
      const cacheRatio = totalTokens > 0
        ? Math.min(100, Math.max(0, (cacheTokens / totalTokens) * 100))
        : 0;
      const cacheCumulativeRatio = cumulativeTotal > 0
        ? Math.min(100, Math.max(0, (cumulativeCache / cumulativeTotal) * 100))
        : 0;

      return {
        actualCost: item.actual_cost,
        cacheCreation,
        cacheCumulativeRatio,
        cacheRatio,
        cacheRead,
        input: item.input_tokens,
        output: item.output_tokens,
        standardCost: item.standard_cost,
        time: fmtTime(item.time),
      };
    });
  }, [trend]);

  return (
    <DashboardCard title={t('dashboard.token_trend')}>
      {chartData.length > 0 ? (
        <div className="h-[248px] w-full min-w-0 2xl:h-[288px]">
          <ResponsiveContainer width="100%" height="100%" debounce={80} initialDimension={{ width: 600, height: 248 }}>
            <LineChart data={chartData} margin={{ bottom: 0, left: -18, right: 4, top: 4 }}>
              <CartesianGrid stroke="var(--ag-border-subtle)" vertical={false} />
              <XAxis axisLine={false} dataKey="time" tick={{ fill: 'var(--ag-text)', fontSize: 11 }} tickLine={false} />
              <YAxis yAxisId="tokens" axisLine={false} tick={{ fill: 'var(--ag-text)', fontSize: 11 }} tickFormatter={fmtNum} tickLine={false} />
              <YAxis
                yAxisId="ratio"
                axisLine={false}
                domain={[0, 100]}
                orientation="right"
                tick={{ fill: 'var(--ag-text)', fontSize: 11 }}
                tickFormatter={(value: number) => `${Math.round(value)}%`}
                tickLine={false}
                width={32}
              />
              <RechartsTooltip content={<TokenTrendTooltip />} />
              <Legend
                height={24}
                content={() => (
                  <div className="flex flex-wrap items-center justify-center gap-x-4 gap-y-1 pt-1 text-[11px] text-text">
                    {TOKEN_TREND_LINE_ORDER.map((key) => (
                      <span key={key} className="inline-flex items-center gap-1.5">
                        {TOKEN_TREND_RATIO_KEYS.has(key) ? (
                          <span className="h-0 w-4 border-t-2 border-dashed" style={{ borderColor: USAGE_TOKEN_COLORS[key] }} />
                        ) : (
                          <span className="h-2 w-2 rounded-full" style={{ background: USAGE_TOKEN_COLORS[key] }} />
                        )}
                        <span>{lineLabels[key]}</span>
                      </span>
                    ))}
                  </div>
                )}
              />
              <Line yAxisId="tokens" dataKey="input" dot={false} isAnimationActive={false} name={lineLabels.input} stroke={USAGE_TOKEN_COLORS.input} strokeWidth={2.5} type="monotone" />
              <Line yAxisId="tokens" dataKey="output" dot={false} isAnimationActive={false} name={lineLabels.output} stroke={USAGE_TOKEN_COLORS.output} strokeWidth={2.5} type="monotone" />
              <Line yAxisId="tokens" dataKey="cacheCreation" dot={false} isAnimationActive={false} name={lineLabels.cacheCreation} stroke={USAGE_TOKEN_COLORS.cacheCreation} strokeWidth={2.5} type="monotone" />
              <Line yAxisId="tokens" dataKey="cacheRead" dot={false} isAnimationActive={false} name={lineLabels.cacheRead} stroke={USAGE_TOKEN_COLORS.cacheRead} strokeWidth={2.5} type="monotone" />
              <Line yAxisId="ratio" dataKey="cacheRatio" dot={false} isAnimationActive={false} name={lineLabels.cacheRatio} stroke={USAGE_TOKEN_COLORS.cacheRatio} strokeDasharray="5 5" strokeWidth={2} type="monotone" />
              <Line yAxisId="ratio" dataKey="cacheCumulativeRatio" dot={false} isAnimationActive={false} name={lineLabels.cacheCumulativeRatio} stroke={USAGE_TOKEN_COLORS.cacheCumulativeRatio} strokeDasharray="5 5" strokeWidth={2} type="monotone" />
            </LineChart>
          </ResponsiveContainer>
        </div>
      ) : (
        <div className="flex h-[248px] items-center justify-center text-sm text-text 2xl:h-[288px]">{t('common.no_data')}</div>
      )}
    </DashboardCard>
  );
}

function TopUsersCard({ trend }: { trend: DashboardTrendResp }) {
  const { t } = useTranslation();
  const topUsers = trend.top_users ?? [];
  const chartData = useMemo(() => {
    if (topUsers.length === 0) return [];
    const timeSet = new Set<string>();
    topUsers.forEach((user) => user.trend.forEach((point) => timeSet.add(point.time)));
    return Array.from(timeSet).sort().map((time) => {
      const row: Record<string, number | string> = { time: fmtTime(time) };
      topUsers.forEach((user) => {
        row[user.email] = user.trend.find((point) => point.time === time)?.tokens ?? 0;
      });
      return row;
    });
  }, [topUsers]);

  return (
    <DashboardCard title={t('dashboard.top_users')}>
      {topUsers.length > 0 ? (
        <div className="h-[268px] w-full min-w-0 2xl:h-[320px]">
          <ResponsiveContainer width="100%" height="100%" debounce={80} initialDimension={{ width: 1200, height: 268 }}>
            <LineChart data={chartData} margin={{ bottom: 0, left: -18, right: 8, top: 4 }}>
              <CartesianGrid stroke="var(--ag-border-subtle)" vertical={false} />
              <XAxis axisLine={false} dataKey="time" tick={{ fill: 'var(--ag-text)', fontSize: 11 }} tickLine={false} />
              <YAxis axisLine={false} tick={{ fill: 'var(--ag-text)', fontSize: 11 }} tickFormatter={fmtNum} tickLine={false} />
              <RechartsTooltip content={<ChartTooltip />} />
              <Legend iconSize={8} iconType="circle" wrapperStyle={{ color: 'var(--ag-text)', fontSize: 11 }} />
              {topUsers.map((user, index) => (
                <Line key={user.user_id} dataKey={user.email} dot={false} isAnimationActive={false} stroke={USER_COLORS[index % USER_COLORS.length]} strokeWidth={2.5} type="monotone" />
              ))}
            </LineChart>
          </ResponsiveContainer>
        </div>
      ) : (
        <div className="flex h-[268px] items-center justify-center text-sm text-text 2xl:h-[320px]">{t('common.no_data')}</div>
      )}
    </DashboardCard>
  );
}

function TrendCharts({ trend }: { trend: DashboardTrendResp }) {
  return (
    <div className="ag-dashboard-trends space-y-4 2xl:space-y-5">
      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
        <ModelDistributionCard trend={trend} />
        <TokenTrendCard trend={trend} />
      </div>
      <TopUsersCard trend={trend} />
    </div>
  );
}

export default function DashboardPage() {
  const { t } = useTranslation();
  const [range, setRange] = useState<RangePreset>('today');
  const [granularity, setGranularity] = useState<Granularity>('day');
  const [selectedUserId, setSelectedUserId] = useState<number | undefined>();
  const [userKeyword, setUserKeyword] = useState('');
  const debouncedUserKeyword = useDebouncedValue(userKeyword.trim(), 250);
  const [selectedUserLabel, setSelectedUserLabel] = useState('');

  const { data: usersData } = useQuery({
    queryKey: queryKeys.users('dashboard-filter-search', debouncedUserKeyword),
    queryFn: () => usersApi.list({ page: 1, page_size: 20, keyword: debouncedUserKeyword }),
    enabled: debouncedUserKeyword.length > 0,
  });

  const userOptions = (usersData?.list ?? []).map((user) => ({
    id: String(user.id),
    label: user.username || user.email,
    description: user.username ? user.email : undefined,
    textValue: `${user.username || ''} ${user.email}`,
  }));
  const visibleUserOptions = (() => {
    const selectedId = selectedUserId ? String(selectedUserId) : '';
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
  const granularityOptions = [
    { id: 'day', label: t('dashboard.granularity_day') },
    { id: 'hour', label: t('dashboard.granularity_hour') },
  ];
  const selectedGranularity = range === 'today' ? 'hour' : granularity;
  const selectedGranularityLabel = granularityOptions.find((item) => item.id === selectedGranularity)?.label ?? '';
  const userFilter = selectedUserId ? { user_id: selectedUserId } : undefined;

  const statsQuery = useQuery({
    queryKey: queryKeys.dashboard(selectedUserId),
    queryFn: () => dashboardApi.stats(userFilter),
  });

  const trendParams = useMemo(() => ({
    range,
    granularity: range === 'today' ? 'hour' as const : granularity,
    ...(selectedUserId ? { user_id: selectedUserId } : {}),
  }), [range, granularity, selectedUserId]);

  const trendQuery = useQuery({
    queryKey: queryKeys.dashboardTrend(trendParams),
    queryFn: () => dashboardApi.trend(trendParams),
    placeholderData: keepPreviousData,
  });

  const refresh = () => {
    statsQuery.refetch();
    trendQuery.refetch();
  };

  return (
    <div className="space-y-5 2xl:space-y-6">
      {statsQuery.error ? (
        <Alert status="danger">
          {t('dashboard.load_failed', { error: statsQuery.error instanceof Error ? statsQuery.error.message : '' })}
        </Alert>
      ) : null}

      {statsQuery.isLoading ? <StatsSkeleton /> : statsQuery.data ? <StatsCards stats={statsQuery.data} /> : null}

      <div className="ag-dashboard-toolbar flex flex-col gap-3 p-4 2xl:p-5 xl:flex-row xl:items-center xl:justify-between">
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <span className="shrink-0 text-sm font-semibold text-text">{t('dashboard.time_range')}</span>
          <Tabs className="ag-segmented-tabs ag-segmented-tabs-compact" selectedKey={range} onSelectionChange={(key) => setRange(key as RangePreset)}>
            <Tabs.List>
              {RANGE_PRESETS.map((item, index) => (
                <Tabs.Tab id={item} key={item}>
                  {index > 0 ? <Tabs.Separator /> : null}
                  <Tabs.Indicator />
                  <span>{t(`dashboard.range_${item}`)}</span>
                </Tabs.Tab>
              ))}
            </Tabs.List>
          </Tabs>
          <Button isIconOnly aria-label={t('common.refresh', 'Refresh')} size="sm" variant="ghost" onPress={refresh}>
            <RefreshCw className={`h-4 w-4 ${statsQuery.isFetching || trendQuery.isFetching ? 'animate-spin' : ''}`} />
          </Button>
        </div>

        <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-end">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
            <span className="shrink-0 text-sm font-semibold text-text">{t('dashboard.filter_user')}</span>
            <div className="w-full sm:w-48">
              <ComboBox
                aria-label={t('dashboard.filter_user')}
                allowsEmptyCollection
                fullWidth
                inputValue={userKeyword}
                items={visibleUserOptions}
                menuTrigger="focus"
                selectedKey={selectedUserId ? String(selectedUserId) : null}
                onInputChange={(value) => {
                  setUserKeyword(value);
                  if (!value.trim()) {
                    setSelectedUserId(undefined);
                    setSelectedUserLabel('');
                    return;
                  }
                  if (selectedUserId && value !== selectedUserLabel) {
                    setSelectedUserId(undefined);
                    setSelectedUserLabel('');
                  }
                }}
                onSelectionChange={(key) => {
                  const value = key == null ? '' : String(key);
                  if (!value) {
                    setSelectedUserId(undefined);
                    setSelectedUserLabel('');
                    setUserKeyword('');
                    return;
                  }
                  const option = visibleUserOptions.find((item) => item.id === value);
                  const label = option?.label ? String(option.label) : '';
                  setSelectedUserId(Number(value));
                  setSelectedUserLabel(label);
                  setUserKeyword(label);
                }}
              >
                <ComboBox.InputGroup className="relative">
                  <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                  <Input className="pl-9" placeholder={t('dashboard.all_users')} />
                </ComboBox.InputGroup>
                <ComboBox.Popover>
                  <ListBox
                    items={visibleUserOptions}
                    renderEmptyState={() => (
                      <div className="px-3 py-6 text-center text-xs text-text-tertiary">
                        {userKeyword.trim() ? t('common.no_data') : t('dashboard.filter_user')}
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
          </div>

          <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
            <span className="shrink-0 text-sm font-semibold text-text">{t('dashboard.granularity')}</span>
            <div className="w-full sm:w-48">
              <Select
                fullWidth
                isDisabled={range === 'today'}
                selectedKey={selectedGranularity}
                onSelectionChange={(key) => setGranularity(key as Granularity)}
              >
                <Label className="sr-only">{t('dashboard.granularity')}</Label>
                <Select.Trigger>
                  <CalendarDays className="mr-2 h-4 w-4 text-text" />
                  <Select.Value>{selectedGranularityLabel}</Select.Value>
                  <Select.Indicator />
                </Select.Trigger>
                <Select.Popover>
                  <ListBox items={granularityOptions}>
                    {(item) => (
                      <ListBox.Item id={item.id} textValue={item.label}>
                        {item.label}
                      </ListBox.Item>
                    )}
                  </ListBox>
                </Select.Popover>
              </Select>
            </div>
          </div>
        </div>
      </div>

      {trendQuery.isLoading && !trendQuery.data ? (
        <div className="ag-dashboard-trends space-y-4 2xl:space-y-5">
          <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
            {Array.from({ length: 2 }).map((_, index) => (
              <Card className="ag-dashboard-panel" key={index}>
                <Card.Content>
                  <Skeleton className="h-[280px] w-full 2xl:h-[320px]" />
                </Card.Content>
              </Card>
            ))}
          </div>
          <Card className="ag-dashboard-panel">
            <Card.Content>
              <Skeleton className="h-[300px] w-full 2xl:h-[360px]" />
            </Card.Content>
          </Card>
        </div>
      ) : trendQuery.data ? (
        <TrendCharts trend={trendQuery.data} />
      ) : null}
    </div>
  );
}
