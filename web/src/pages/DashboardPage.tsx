import { useMemo, useState, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Card, Label, ListBox, Select, Skeleton, Tabs } from '@heroui/react';
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
  UserRound,
  Users,
  Zap,
} from 'lucide-react';
import { decorativePalette } from '@airgate/theme';
import { dashboardApi } from '../shared/api/dashboard';
import { usersApi } from '../shared/api/users';
import { queryKeys } from '../shared/queryKeys';
import { FETCH_ALL_PARAMS } from '../shared/constants';
import { CompactDataTable } from '../shared/components/CompactDataTable';
import type { DashboardStatsResp, DashboardTrendResp } from '../shared/types';

const PIE_COLORS = decorativePalette.slice(0, 10);
const USER_COLORS = [...decorativePalette];

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
  emerald: 'bg-emerald-100 text-emerald-600 ring-emerald-200 dark:bg-emerald-400/15 dark:text-emerald-300 dark:ring-emerald-400/25',
  indigo: 'bg-indigo-100 text-indigo-600 ring-indigo-200 dark:bg-indigo-400/15 dark:text-indigo-300 dark:ring-indigo-400/25',
  purple: 'bg-purple-100 text-purple-600 ring-purple-200 dark:bg-purple-400/15 dark:text-purple-300 dark:ring-purple-400/25',
  rose: 'bg-rose-100 text-rose-600 ring-rose-200 dark:bg-rose-400/15 dark:text-rose-300 dark:ring-rose-400/25',
  teal: 'bg-teal-100 text-teal-600 ring-teal-200 dark:bg-teal-400/15 dark:text-teal-300 dark:ring-teal-400/25',
  violet: 'bg-violet-100 text-violet-600 ring-violet-200 dark:bg-violet-400/15 dark:text-violet-300 dark:ring-violet-400/25',
};

const META_TONE_CLASSES: Record<MetaTone, string> = {
  accent: 'text-primary',
  danger: 'text-danger',
  default: 'text-text-tertiary',
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

function fmtCost(n: number | undefined | null): string {
  if (n == null) return '$0.00';
  if (n >= 1000) return `$${(n / 1000).toFixed(2)}K`;
  return `$${n.toFixed(2)}`;
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
  meta: string;
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
  return (
    <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4 2xl:gap-4">
      <MetricCard
        icon={<KeyRound className="h-5 w-5" />}
        tone="blue"
        metaTone="success"
        title={t('dashboard.api_keys')}
        value={stats.total_api_keys}
        meta={t('dashboard.api_keys_enabled', { count: stats.enabled_api_keys })}
      />
      <MetricCard
        icon={<Monitor className="h-5 w-5" />}
        tone="violet"
        metaTone={stats.error_accounts > 0 ? 'danger' : 'success'}
        title={t('dashboard.accounts')}
        value={stats.total_accounts}
        meta={t('dashboard.accounts_status', { enabled: stats.enabled_accounts, errors: stats.error_accounts })}
      />
      <MetricCard
        icon={<Activity className="h-5 w-5" />}
        tone="emerald"
        title={t('dashboard.today_requests')}
        value={fmtNum(stats.today_requests)}
        meta={t('dashboard.alltime_requests', { count: fmtNum(stats.alltime_requests) } as Record<string, string>)}
      />
      <MetricCard
        icon={<Users className="h-5 w-5" />}
        tone="teal"
        metaTone="success"
        title={t('dashboard.users')}
        value={t('dashboard.new_users', { count: stats.new_users_today })}
        meta={t('dashboard.total_count', { count: stats.total_users })}
      />
      <MetricCard
        icon={<Coins className="h-5 w-5" />}
        tone="amber"
        metaTone="warning"
        title={t('dashboard.today_tokens')}
        value={fmtNum(stats.today_tokens)}
        meta={`${fmtCost(stats.today_cost)} / ${fmtCost(stats.today_standard_cost)}`}
      />
      <MetricCard
        icon={<Database className="h-5 w-5" />}
        tone="indigo"
        metaTone="success"
        title={t('dashboard.total_tokens')}
        value={fmtNum(stats.alltime_tokens)}
        meta={`${fmtCost(stats.alltime_cost)} / ${fmtCost(stats.alltime_standard_cost)}`}
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
        value={`${((stats.avg_duration_ms ?? 0) / 1000).toFixed(2)}s`}
        meta={t('dashboard.active_users', { count: stats.active_users })}
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
            <span className="text-text-tertiary">{item.name ?? item.dataKey}</span>
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
    cachedInput: t('dashboard.cached_input'),
    input: t('dashboard.input'),
    output: t('dashboard.output'),
  };
  return (
    <div className="rounded-[var(--radius)] border border-border bg-surface px-3 py-2 text-xs text-text shadow-lg">
      <div className="mb-1 font-medium">{label}</div>
      <div className="space-y-1">
        {payload.map((item) => (
          <div key={item.dataKey} className="flex items-center gap-2">
            <span className="h-2 w-2 rounded-full" style={{ background: item.color }} />
            <span className="text-text-tertiary">{labels[item.dataKey ?? ''] ?? item.dataKey}</span>
            <span className="font-mono">{fmtNum(Number(item.value ?? 0))}</span>
          </div>
        ))}
      </div>
      <div className="mt-2 border-t border-border pt-2 text-text-tertiary">
        {t('dashboard.actual')}: <span className="text-warning">{fmtCost(datum?.actualCost)}</span>
        {' / '}
        {t('dashboard.standard')}: {fmtCost(datum?.standardCost)}
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
              <Pie data={activePieData} cx="50%" cy="50%" dataKey="value" innerRadius={42} minAngle={3} outerRadius={68} stroke="var(--ag-surface)" strokeWidth={2}>
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
            <div className="flex h-44 w-44 items-center justify-center text-xs text-text-tertiary">{t('common.no_data')}</div>
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
                width: tab === 'model' ? '30%' : '34%',
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
                title: t('dashboard.requests'),
                width: tab === 'model' ? '16%' : '15%',
                render: (row) => <span className="truncate font-mono text-text-secondary">{row.requests.toLocaleString()}</span>,
              },
              {
                align: 'end',
                key: 'tokens',
                title: t('dashboard.tokens'),
                width: tab === 'model' ? '18%' : '17%',
                render: (row) => <span className="truncate font-mono text-text-secondary">{fmtNum(row.tokens)}</span>,
              },
              {
                align: 'end',
                key: 'actual',
                title: t('dashboard.actual'),
                width: '18%',
                render: (row) => <span className="truncate font-mono text-warning">{fmtCost(row.actualCost)}</span>,
              },
              {
                align: 'end',
                key: 'standard',
                title: t('dashboard.standard'),
                width: tab === 'model' ? '18%' : '16%',
                render: (row) => <span className="truncate font-mono text-text-secondary">{fmtCost(row.standardCost)}</span>,
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
  const chartData = useMemo(
    () => (trend.token_trend ?? []).map((item) => ({
      actualCost: item.actual_cost,
      cachedInput: item.cached_input,
      input: item.input_tokens,
      output: item.output_tokens,
      standardCost: item.standard_cost,
      time: fmtTime(item.time),
    })),
    [trend],
  );

  return (
    <DashboardCard>
      {chartData.length > 0 ? (
        <div className="h-[248px] 2xl:h-[288px]">
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={chartData} margin={{ bottom: 0, left: -18, right: 8, top: 4 }}>
              <CartesianGrid stroke="var(--ag-border-subtle)" vertical={false} />
              <XAxis axisLine={false} dataKey="time" tick={{ fill: 'var(--ag-text-tertiary)', fontSize: 11 }} tickLine={false} />
              <YAxis axisLine={false} tick={{ fill: 'var(--ag-text-tertiary)', fontSize: 11 }} tickFormatter={fmtNum} tickLine={false} />
              <RechartsTooltip content={<TokenTrendTooltip />} />
              <Legend iconSize={8} iconType="circle" wrapperStyle={{ color: 'var(--ag-text-tertiary)', fontSize: 11 }} />
              <Line dataKey="input" dot={false} name={t('dashboard.input')} stroke="#3b82f6" strokeWidth={2.5} type="monotone" />
              <Line dataKey="output" dot={false} name={t('dashboard.output')} stroke="#10b981" strokeWidth={2.5} type="monotone" />
              <Line dataKey="cachedInput" dot={false} name={t('dashboard.cached_input')} stroke="#8b5cf6" strokeWidth={2.5} type="monotone" />
            </LineChart>
          </ResponsiveContainer>
        </div>
      ) : (
        <div className="flex h-[248px] items-center justify-center text-sm text-text-tertiary 2xl:h-[288px]">{t('common.no_data')}</div>
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
        <div className="h-[268px] 2xl:h-[320px]">
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={chartData} margin={{ bottom: 0, left: -18, right: 8, top: 4 }}>
              <CartesianGrid stroke="var(--ag-border-subtle)" vertical={false} />
              <XAxis axisLine={false} dataKey="time" tick={{ fill: 'var(--ag-text-tertiary)', fontSize: 11 }} tickLine={false} />
              <YAxis axisLine={false} tick={{ fill: 'var(--ag-text-tertiary)', fontSize: 11 }} tickFormatter={fmtNum} tickLine={false} />
              <RechartsTooltip content={<ChartTooltip />} />
              <Legend iconSize={8} iconType="circle" wrapperStyle={{ color: 'var(--ag-text-tertiary)', fontSize: 11 }} />
              {topUsers.map((user, index) => (
                <Line key={user.user_id} dataKey={user.email} dot={false} stroke={USER_COLORS[index % USER_COLORS.length]} strokeWidth={2.5} type="monotone" />
              ))}
            </LineChart>
          </ResponsiveContainer>
        </div>
      ) : (
        <div className="flex h-[268px] items-center justify-center text-sm text-text-tertiary 2xl:h-[320px]">{t('common.no_data')}</div>
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

  const { data: usersData } = useQuery({
    queryKey: queryKeys.usersAll(),
    queryFn: () => usersApi.list(FETCH_ALL_PARAMS),
  });

  const userOptions = [
    { id: '', label: t('dashboard.all_users') },
    ...(usersData?.list ?? []).map((item) => ({ id: String(item.id), label: item.email })),
  ];
  const selectedUserLabel = userOptions.find((item) => item.id === String(selectedUserId ?? ''))?.label ?? t('dashboard.all_users');
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
          <span className="shrink-0 text-sm font-semibold text-text-tertiary">{t('dashboard.time_range')}</span>
          <Tabs className="ag-segmented-tabs" selectedKey={range} onSelectionChange={(key) => setRange(key as RangePreset)}>
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
          <Button isIconOnly aria-label={t('common.refresh', 'Refresh')} variant="secondary" onPress={refresh}>
            <RefreshCw className={`h-4 w-4 ${statsQuery.isFetching || trendQuery.isFetching ? 'animate-spin' : ''}`} />
          </Button>
        </div>

        <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-end">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
            <span className="shrink-0 text-sm font-semibold text-text-tertiary">{t('dashboard.filter_user')}</span>
            <div className="w-full sm:w-64">
              <Select
                fullWidth
                selectedKey={selectedUserId ? String(selectedUserId) : ''}
                onSelectionChange={(key) => setSelectedUserId(key ? Number(key) : undefined)}
              >
                <Label className="sr-only">{t('dashboard.filter_user')}</Label>
                <Select.Trigger>
                  <UserRound className="mr-2 h-4 w-4 text-text-tertiary" />
                  <Select.Value>{selectedUserLabel}</Select.Value>
                  <Select.Indicator />
                </Select.Trigger>
                <Select.Popover>
                  <ListBox items={userOptions}>
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

          <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
            <span className="shrink-0 text-sm font-semibold text-text-tertiary">{t('dashboard.granularity')}</span>
            <div className="w-full sm:w-40">
              <Select
                fullWidth
                isDisabled={range === 'today'}
                selectedKey={selectedGranularity}
                onSelectionChange={(key) => setGranularity(key as Granularity)}
              >
                <Label className="sr-only">{t('dashboard.granularity')}</Label>
                <Select.Trigger>
                  <CalendarDays className="mr-2 h-4 w-4 text-text-tertiary" />
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

      {trendQuery.isLoading ? (
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
