import { useState, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
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
import { Card, StatCard } from '../../shared/components/Card';

import { decorativePalette } from '@airgate/theme';

const PIE_COLORS = decorativePalette.slice(0, 10);

type RangePreset = 'today' | '7d' | '30d' | '90d';

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

/** 将后端 UTC 时间字符串转为本地时区显示 */
function fmtTime(timeStr: string): string {
  // "2026-04-03 09:00" (UTC) 或 "2026-04-03" (日期)
  const utcStr = timeStr.includes(' ') ? timeStr.replace(' ', 'T') + ':00Z' : timeStr + 'T00:00:00Z';
  const d = new Date(utcStr);
  if (isNaN(d.getTime())) return timeStr;
  if (timeStr.includes(' ')) {
    return `${String(d.getHours()).padStart(2, '0')}:00`;
  }
  return `${String(d.getMonth() + 1).padStart(2, '0')}/${String(d.getDate()).padStart(2, '0')}`;
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
        {(['today', '7d', '30d', '90d'] as const).map((r) => (
          <button
            key={r}
            onClick={() => setRange(r)}
            className={`px-3 py-1 text-xs rounded-lg font-medium transition-all cursor-pointer ${
              range === r
                ? 'bg-primary-subtle text-primary'
                : 'text-text-tertiary hover:text-text hover:bg-bg-hover'
            }`}
          >
            {t(`dashboard.range_${r}`)}
          </button>
        ))}
      </div>

      {/* 模型分布 + Token 趋势 */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 mb-4">
        {/* 模型分布饼图 */}
        <Card title={t('dashboard.model_distribution')}>
          {models.length > 0 ? (
            <div className="flex flex-col sm:flex-row gap-4">
              <div className="w-44 h-44 flex-shrink-0 mx-auto sm:mx-0">
                <ResponsiveContainer width="100%" height="100%">
                  <PieChart>
                    <Pie data={models.map((m) => ({ name: m.model, value: m.tokens }))} cx="50%" cy="50%" innerRadius={35} outerRadius={65} paddingAngle={2} dataKey="value">
                      {models.map((_, i) => <Cell key={i} fill={PIE_COLORS[i % PIE_COLORS.length]} />)}
                    </Pie>
                    <RechartsTooltip
                      contentStyle={{ background: 'var(--ag-bg-elevated)', border: '1px solid var(--ag-border)', borderRadius: 8, fontSize: 12 }}
                      formatter={(value) => [fmtNum(Number(value)), 'Token']}
                    />
                  </PieChart>
                </ResponsiveContainer>
              </div>
              <div className="flex-1 overflow-auto">
                <table className="w-full text-xs">
                  <thead>
                    <tr className="text-text-tertiary">
                      <th className="text-left font-medium pb-2">{t('usage.model')}</th>
                      <th className="text-right font-medium pb-2">{t('dashboard.requests')}</th>
                      <th className="text-right font-medium pb-2">TOKEN</th>
                      <th className="text-right font-medium pb-2">{t('usage.cost')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {models.map((m, i) => (
                      <tr key={m.model} className="border-t border-border-subtle">
                        <td className="py-1.5 flex items-center gap-1.5">
                          <span className="w-2 h-2 rounded-full flex-shrink-0" style={{ backgroundColor: PIE_COLORS[i % PIE_COLORS.length] }} />
                          <span className="text-text truncate max-w-[120px]">{m.model}</span>
                        </td>
                        <td className="text-right text-text-secondary py-1.5">{m.requests}</td>
                        <td className="text-right text-text-secondary py-1.5 font-mono">{fmtNum(m.tokens)}</td>
                        <td className="text-right py-1.5 font-mono" style={{ color: 'var(--ag-primary)' }}>{fmtCost(m.actual_cost)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          ) : (
            <div className="flex items-center justify-center h-44 text-text-tertiary text-sm">{t('common.no_data')}</div>
          )}
        </Card>

        {/* Token 趋势 */}
        <Card title={t('dashboard.token_trend')}>
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
        </Card>
      </div>
    </div>
  );
}
