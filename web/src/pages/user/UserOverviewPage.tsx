import { useState, useMemo, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Card, Tabs } from '@heroui/react';
import {
  PieChart, Pie, Cell, ResponsiveContainer, Tooltip as RechartsTooltip,
  LineChart, Line, XAxis, YAxis, CartesianGrid,
} from 'recharts';
import {
  Wallet, Zap, Activity, Coins,
} from 'lucide-react';
import { useAuth } from '../../app/providers/AuthProvider';
import { usageApi } from '../../shared/api/usage';
import { queryKeys } from '../../shared/queryKeys';
import { CompactDataTable } from '../../shared/components/CompactDataTable';

import { decorativePalette } from '@airgate/theme';

const PIE_COLORS = decorativePalette.slice(0, 10);

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

function SectionCard({ children, title }: { children: ReactNode; title: string }) {
  return (
    <Card>
      <Card.Header>
        <Card.Title>{title}</Card.Title>
      </Card.Header>
      <Card.Content>{children}</Card.Content>
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
    <Card className="relative overflow-hidden">
      <div
        className="absolute inset-x-0 top-0 h-px opacity-70"
        style={{ background: `linear-gradient(90deg, transparent, ${accentColor}, transparent)` }}
      />
      <Card.Content>
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0 space-y-2">
            <p className="text-xs font-medium uppercase text-text-tertiary">{title}</p>
            <p className="font-mono text-2xl font-bold">{value}</p>
          </div>
          <div
            className="flex h-10 w-10 shrink-0 items-center justify-center rounded-sm"
            style={{ background: `color-mix(in srgb, ${accentColor} 12%, transparent)`, color: accentColor }}
          >
            {icon}
          </div>
        </div>
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

function fmtCost(n: number): string {
  if (n >= 1000) return `$${(n / 1000).toFixed(2)}K`;
  return `$${n.toFixed(4)}`;
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
      cached: b.cache_read,
    })),
    [trend],
  );

  return (
    <div>
      {/* 账户信息 */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
        <StatCard
          title={t('user_overview.balance')}
          value={`$${(user?.balance ?? 0).toFixed(2)}`}
          icon={<Wallet className="w-5 h-5" />}
          accentColor="var(--ag-primary)"
        />
        <StatCard
          title={t('user_overview.max_concurrency')}
          value={String(user?.max_concurrency ?? 0)}
          icon={<Zap className="w-5 h-5" />}
          accentColor="var(--ag-info)"
        />
        <StatCard
          title={t('usage.total_requests')}
          value={(stats?.total_requests ?? 0).toLocaleString()}
          icon={<Activity className="w-5 h-5" />}
          accentColor="var(--ag-warning)"
        />
        <StatCard
          title={t('usage.actual_cost')}
          value={fmtCost(stats?.total_actual_cost ?? 0)}
          icon={<Coins className="w-5 h-5" />}
          accentColor="var(--ag-success)"
        />
      </div>

      {/* 时间范围选择 */}
      <div className="flex items-center gap-2 mb-4">
        <span className="text-xs text-text-tertiary">{t('dashboard.time_range')}</span>
        <Tabs
          selectedKey={range}
          onSelectionChange={(key) => setRange(key as RangePreset)}
          variant="secondary"
        >
          <Tabs.List>
            {(['today', '7d', '30d', '90d'] as const).map((r) => (
              <Tabs.Tab key={r} id={r}>
                {t(`dashboard.range_${r}`)}
              </Tabs.Tab>
            ))}
          </Tabs.List>
        </Tabs>
      </div>

      {/* 模型分布 + Token 趋势 */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 mb-4">
        {/* 模型分布饼图 */}
        <SectionCard title={t('dashboard.model_distribution')}>
          {models.length > 0 ? (
            <div className="ag-distribution-card-body flex flex-col gap-3 xl:flex-row">
              <div className="ag-distribution-chart-frame">
                <PieChart width={176} height={176}>
                  <Pie data={models.map((m) => ({ name: m.model, value: m.tokens }))} cx="50%" cy="50%" innerRadius={35} outerRadius={65} dataKey="value" minAngle={3} stroke="var(--ag-bg-elevated)" strokeWidth={1}>
                    {models.map((_, i) => <Cell key={i} fill={PIE_COLORS[i % PIE_COLORS.length]} />)}
                  </Pie>
                  <RechartsTooltip
                    animationDuration={0}
                    content={<PieNameTooltip />}
                    cursor={false}
                    isAnimationActive={false}
                  />
                </PieChart>
              </div>
              <div className="ag-distribution-table-scroll">
                <CompactDataTable
                  ariaLabel={t('dashboard.model_distribution')}
                  className="ag-compact-data-table--dense"
                  emptyText={t('common.no_data')}
                  minWidth={380}
                  rowKey={(row) => row.model}
                  rows={models}
                  columns={[
                    {
                      key: 'model',
                      title: t('usage.model'),
                      width: '30%',
                      render: (row, index) => (
                        <>
                          <span className="w-2 h-2 shrink-0 rounded-full" style={{ backgroundColor: PIE_COLORS[index % PIE_COLORS.length] }} />
                          <span className="min-w-0 truncate font-medium text-text" title={row.model}>{row.model}</span>
                        </>
                      ),
                    },
                    {
                      align: 'end',
                      key: 'requests',
                      title: t('dashboard.requests'),
                      width: '22%',
                      render: (row) => <span className="truncate text-text-secondary">{row.requests}</span>,
                    },
                    {
                      align: 'end',
                      key: 'tokens',
                      title: 'TOKEN',
                      width: '24%',
                      render: (row) => <span className="truncate font-mono text-text-secondary">{fmtNum(row.tokens)}</span>,
                    },
                    {
                      align: 'end',
                      key: 'cost',
                      title: t('usage.cost'),
                      width: '24%',
                      render: (row) => <span className="truncate font-mono text-primary">{fmtCost(row.actual_cost)}</span>,
                    },
                  ]}
                />
              </div>
            </div>
          ) : (
            <div className="flex items-center justify-center h-44 text-text-tertiary text-sm">{t('common.no_data')}</div>
          )}
        </SectionCard>

        {/* Token 趋势 */}
        <SectionCard title={t('dashboard.token_trend')}>
          {trendData.length > 0 ? (
            <ResponsiveContainer width="100%" height={220}>
              <LineChart data={trendData} margin={{ top: 5, right: 10, left: 0, bottom: 5 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="var(--ag-border-subtle)" />
                <XAxis
                  dataKey="time"
                  tick={{ fontSize: 10, fill: 'var(--ag-text-tertiary)' }}
                  axisLine={{ stroke: 'var(--ag-border)' }}
                  tickLine={false}
                />
                <YAxis
                  tick={{ fontSize: 10, fill: 'var(--ag-text-tertiary)' }}
                  axisLine={false}
                  tickLine={false}
                  tickFormatter={(v: number) => fmtNum(v)}
                />
                <RechartsTooltip
                  contentStyle={{ background: 'var(--ag-bg-elevated)', border: '1px solid var(--ag-border)', borderRadius: 8, fontSize: 12 }}
                  formatter={(value, name) => [fmtNum(Number(value)), name === 'input' ? t('usage.input') : name === 'output' ? t('usage.output') : t('usage.cache_read')]}
                />
                <Line type="monotone" dataKey="input" stroke="#3b82f6" strokeWidth={2} dot={false} />
                <Line type="monotone" dataKey="output" stroke="#10b981" strokeWidth={2} dot={false} />
                <Line type="monotone" dataKey="cached" stroke="#8b5cf6" strokeWidth={2} dot={false} />
              </LineChart>
            </ResponsiveContainer>
          ) : (
            <div className="flex items-center justify-center h-44 text-text-tertiary text-sm">{t('common.no_data')}</div>
          )}
          <div className="flex items-center justify-center gap-4 mt-2 text-[11px] text-text-tertiary">
            <span className="flex items-center gap-1"><span className="w-2.5 h-0.5 rounded bg-blue-500" /> {t('usage.input')}</span>
            <span className="flex items-center gap-1"><span className="w-2.5 h-0.5 rounded bg-emerald-500" /> {t('usage.output')}</span>
            <span className="flex items-center gap-1"><span className="w-2.5 h-0.5 rounded bg-violet-500" /> {t('usage.cache_read')}</span>
          </div>
        </SectionCard>
      </div>
    </div>
  );
}
