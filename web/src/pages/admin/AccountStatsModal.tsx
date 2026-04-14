import { useState, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip as RechartsTooltip,
  ResponsiveContainer, PieChart, Pie, Cell,
} from 'recharts';
import {
  DollarSign, Activity, TrendingUp, Clock, Calendar,
  Cpu, Zap,
} from 'lucide-react';
import { Modal } from '../../shared/components/Modal';
import { StatusBadge } from '../../shared/components/Badge';
import { PlatformIcon } from '../../shared/components/PlatformIcon';
import { DatePicker } from '../../shared/components/DatePicker';
import { accountsApi, type AccountStatsResp } from '../../shared/api/accounts';

import { decorativePalette } from '@airgate/theme';

const PIE_COLORS = decorativePalette.slice(0, 10);

// 预设时间范围
type RangePreset = '7d' | '30d' | '90d' | 'custom';

// 按浏览器本地时区拼出 YYYY-MM-DD（不要用 toISOString，那是 UTC，会跨日）。
function localDateStr(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

function getPresetDates(preset: RangePreset): { start_date?: string; end_date?: string } {
  if (preset === 'custom') return {};
  const now = new Date();
  const end = localDateStr(now);
  const days = preset === '7d' ? 7 : preset === '90d' ? 90 : 30;
  const start = new Date(now);
  start.setDate(start.getDate() - (days - 1));
  return { start_date: localDateStr(start), end_date: end };
}

// 格式化数字
function fmtNum(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(2)}K`;
  return n.toLocaleString();
}

// 格式化费用
function fmtCost(n: number, decimals = 4): string {
  return `$${n.toFixed(decimals)}`;
}

// 格式化日期为 MM/DD
function fmtDate(dateStr: string): string {
  const parts = dateStr.split('-');
  return `${parts[1]}/${parts[2]}`;
}

export function AccountStatsModal({
  accountId,
  onClose,
}: {
  accountId: number;
  onClose: () => void;
}) {
  const { t } = useTranslation();

  // 时间范围状态
  const [preset, setPreset] = useState<RangePreset>('30d');
  const [customStart, setCustomStart] = useState('');
  const [customEnd, setCustomEnd] = useState('');

  const queryParams = useMemo(() => {
    if (preset === 'custom' && customStart) {
      return { start_date: customStart, end_date: customEnd || undefined };
    }
    return getPresetDates(preset);
  }, [preset, customStart, customEnd]);

  const { data, isLoading } = useQuery({
    queryKey: ['account-stats', accountId, queryParams],
    queryFn: () => accountsApi.stats(accountId, queryParams),
  });

  return (
    <Modal open onClose={onClose} title={t('accounts.view_stats')} width="880px">
      {/* 时间范围选择器 */}
      <div className="flex items-center gap-2 mb-4 flex-wrap">
        {(['7d', '30d', '90d', 'custom'] as const).map((p) => (
          <button
            key={p}
            className={`px-3 py-1.5 text-xs rounded-md font-medium transition-all cursor-pointer ${
              preset === p
                ? 'bg-primary-subtle text-primary shadow-[0_0_8px_var(--ag-primary-glow)]'
                : 'text-text-secondary hover:bg-bg-hover hover:text-text border border-border-subtle'
            }`}
            onClick={() => setPreset(p)}
          >
            {t(`accounts.stats_range_${p}`)}
          </button>
        ))}
        {preset === 'custom' && (
          <div className="flex items-center gap-2 ml-2">
            <DatePicker
              value={customStart}
              onChange={setCustomStart}
              placeholder={t('accounts.stats_start_date')}
            />
            <span className="text-text-tertiary text-xs">—</span>
            <DatePicker
              value={customEnd}
              onChange={setCustomEnd}
              placeholder={t('accounts.stats_end_date')}
            />
          </div>
        )}
      </div>

      {isLoading ? (
        <div className="flex items-center justify-center py-16 text-text-tertiary text-sm">
          {t('common.loading')}
        </div>
      ) : data ? (
        <StatsContent data={data} />
      ) : null}
    </Modal>
  );
}

function StatsContent({ data }: { data: AccountStatsResp }) {
  const { t } = useTranslation();
  const range = data.range;

  // 计算活跃天数和日均
  // 注意：所有"账号计费"相关数字都用 account_cost（base × account_rate），
  // 而不是 total_cost（base 原价）。这样 reseller 配置 account_rate 才能真正反映"我用这个账号的实际花费"。
  const activeDays = data.active_days || 1;
  const dailyAvgCost = range.account_cost / activeDays;
  const dailyAvgRequests = range.count / activeDays;

  // Token 总量
  const totalTokens = range.input_tokens + range.output_tokens;
  const dailyAvgTokens = totalTokens / activeDays;

  // 时间范围描述
  const rangeLabel = `${data.start_date} ~ ${data.end_date}`;

  return (
    <div className="space-y-5">
      {/* 头部信息 */}
      <div className="flex items-center gap-3 p-4 rounded-lg bg-gradient-to-r from-primary-subtle/50 to-transparent border border-border-subtle">
        <div className="flex items-center justify-center w-10 h-10 rounded-lg bg-primary-subtle">
          <PlatformIcon platform={data.platform} className="w-5 h-5" />
        </div>
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="font-semibold text-sm text-text truncate">{data.name}</span>
          </div>
          <span className="text-xs text-text-tertiary">
            {rangeLabel} · {t('accounts.stats_range_summary', { days: data.total_days, active: activeDays })}
          </span>
        </div>
        <StatusBadge status={data.status} />
      </div>

      {/* 顶部 4 个统计卡片 */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <MiniStatCard
          label={t('accounts.stats_range_cost')}
          value={fmtCost(range.account_cost, 2)}
          sub={`${t('accounts.stats_actual')}: ${fmtCost(range.actual_cost, 2)}`}
          icon={<DollarSign className="w-4 h-4" />}
          color="var(--ag-warning)"
        />
        <MiniStatCard
          label={t('accounts.stats_range_requests')}
          value={fmtNum(range.count)}
          sub={t('accounts.stats_total_calls')}
          icon={<Activity className="w-4 h-4" />}
          color="var(--ag-info)"
        />
        <MiniStatCard
          label={t('accounts.stats_daily_cost')}
          value={fmtCost(dailyAvgCost, 2)}
          sub={t('accounts.stats_based_on_days', { days: activeDays })}
          icon={<TrendingUp className="w-4 h-4" />}
          color="var(--ag-success)"
        />
        <MiniStatCard
          label={t('accounts.stats_daily_requests')}
          value={fmtNum(Math.round(dailyAvgRequests))}
          sub={t('accounts.stats_avg_daily')}
          icon={<Zap className="w-4 h-4" />}
          color="var(--ag-danger)"
        />
      </div>

      {/* 中间 3 个信息卡片 */}
      <div className="grid grid-cols-3 gap-3">
        {/* 今日概览 */}
        <InfoCard title={t('accounts.stats_today')} icon={<Clock className="w-4 h-4" />} color="var(--ag-info)">
          <InfoRow label={t('accounts.stats_cost')} value={fmtCost(data.today.account_cost)} />
          <InfoRow label={t('accounts.stats_actual_cost')} value={fmtCost(data.today.actual_cost)} />
          <InfoRow label={t('accounts.stats_requests')} value={data.today.count.toLocaleString()} />
          <InfoRow label="Token" value={fmtNum(data.today.input_tokens + data.today.output_tokens)} />
        </InfoCard>

        {/* 最高费用日 */}
        <InfoCard title={t('accounts.stats_peak_cost_day')} icon={<DollarSign className="w-4 h-4" />} color="var(--ag-warning)">
          <InfoRow label={t('accounts.stats_date')} value={data.peak_cost_day.date ? fmtDate(data.peak_cost_day.date) : '-'} />
          <InfoRow label={t('accounts.stats_cost')} value={fmtCost(data.peak_cost_day.account_cost)} highlight />
          <InfoRow label={t('accounts.stats_actual_cost')} value={fmtCost(data.peak_cost_day.actual_cost)} />
          <InfoRow label={t('accounts.stats_requests')} value={fmtNum(data.peak_cost_day.count)} />
        </InfoCard>

        {/* 最高请求日 */}
        <InfoCard title={t('accounts.stats_peak_request_day')} icon={<Activity className="w-4 h-4" />} color="var(--ag-success)">
          <InfoRow label={t('accounts.stats_date')} value={data.peak_request_day.date ? fmtDate(data.peak_request_day.date) : '-'} />
          <InfoRow label={t('accounts.stats_requests')} value={fmtNum(data.peak_request_day.count)} highlight />
          <InfoRow label={t('accounts.stats_cost')} value={fmtCost(data.peak_request_day.account_cost)} />
          <InfoRow label={t('accounts.stats_actual_cost')} value={fmtCost(data.peak_request_day.actual_cost)} />
        </InfoCard>
      </div>

      {/* 下方 3 个信息卡片 */}
      <div className="grid grid-cols-3 gap-3">
        {/* 累计 Token */}
        <InfoCard title={t('accounts.stats_total_tokens')} icon={<Cpu className="w-4 h-4" />} color="var(--ag-primary)">
          <InfoRow label={t('accounts.stats_range_total')} value={fmtNum(totalTokens)} />
          <InfoRow label={t('accounts.stats_daily_avg_token')} value={fmtNum(Math.round(dailyAvgTokens))} />
        </InfoCard>

        {/* 性能 */}
        <InfoCard title={t('accounts.stats_performance')} icon={<Zap className="w-4 h-4" />} color="var(--ag-warning)">
          <InfoRow label={t('accounts.stats_avg_response')} value={`${(data.avg_duration_ms / 1000).toFixed(2)}s`} />
          <InfoRow label={t('accounts.stats_active_days')} value={`${data.active_days} / ${data.total_days}`} />
        </InfoCard>

        {/* 最近统计 */}
        <InfoCard title={t('accounts.stats_recent')} icon={<Calendar className="w-4 h-4" />} color="var(--ag-info)">
          <InfoRow label={t('accounts.stats_today_requests')} value={data.today.count.toLocaleString()} />
          <InfoRow label={t('accounts.stats_today_tokens')} value={fmtNum(data.today.input_tokens + data.today.output_tokens)} />
          <InfoRow label={t('accounts.stats_today_cost')} value={fmtCost(data.today.account_cost)} />
        </InfoCard>
      </div>

      {/* 费用与请求趋势 */}
      <TrendChart data={data} />

      {/* 模型分布 */}
      {data.models && data.models.length > 0 && <ModelDistribution data={data} />}
    </div>
  );
}

// ==================== 迷你统计卡片 ====================

function MiniStatCard({
  label, value, sub, icon, color,
}: {
  label: string; value: string; sub: string; icon: React.ReactNode; color: string;
}) {
  return (
    <div className="relative overflow-hidden rounded-lg border border-border-subtle p-3.5 transition-all hover:border-border hover:shadow-sm">
      <div className="absolute top-0 left-0 right-0 h-px opacity-40" style={{ background: `linear-gradient(90deg, transparent, ${color}, transparent)` }} />
      <div className="flex items-start justify-between mb-2">
        <span className="text-[11px] text-text-tertiary font-medium">{label}</span>
        <div className="flex items-center justify-center w-7 h-7 rounded-md" style={{ background: `color-mix(in srgb, ${color} 12%, transparent)`, color }}>
          {icon}
        </div>
      </div>
      <div className="text-xl font-bold text-text font-mono">{value}</div>
      <div className="text-[10px] text-text-tertiary mt-1">{sub}</div>
    </div>
  );
}

// ==================== 信息卡片 ====================

function InfoCard({
  title, icon, color, children,
}: {
  title: string; icon: React.ReactNode; color: string; children: React.ReactNode;
}) {
  return (
    <div className="rounded-lg border border-border-subtle p-3.5 space-y-2">
      <div className="flex items-center gap-1.5">
        <div className="flex items-center justify-center w-5 h-5 rounded" style={{ color }}>{icon}</div>
        <span className="text-xs font-semibold text-text">{title}</span>
      </div>
      <div className="space-y-1.5">{children}</div>
    </div>
  );
}

function InfoRow({ label, value, highlight }: { label: string; value: string; highlight?: boolean }) {
  return (
    <div className="flex items-center justify-between text-xs">
      <span className="text-text-tertiary">{label}</span>
      <span className={`font-mono ${highlight ? 'text-warning font-semibold' : 'text-text-secondary'}`}>{value}</span>
    </div>
  );
}

// ==================== 趋势图 ====================

function TrendChart({ data }: { data: AccountStatsResp }) {
  const { t } = useTranslation();

  const chartData = useMemo(() =>
    (data.daily_trend ?? []).map((d) => ({
      date: fmtDate(d.date),
      // 趋势图的"账号计费"线读 account_cost（含 account_rate），匹配卡片数字
      totalCost: Number(d.account_cost.toFixed(4)),
      actualCost: Number(d.actual_cost.toFixed(4)),
      count: d.count,
    })),
    [data.daily_trend],
  );

  if (chartData.length === 0) return null;

  return (
    <div className="rounded-lg border border-border-subtle p-4">
      <h4 className="text-xs font-semibold text-text mb-3">{t('accounts.stats_trend_title')}</h4>
      <ResponsiveContainer width="100%" height={260}>
        <LineChart data={chartData} margin={{ top: 5, right: 20, left: 10, bottom: 5 }}>
          <CartesianGrid strokeDasharray="3 3" stroke="var(--ag-border-subtle)" />
          <XAxis
            dataKey="date"
            tick={{ fontSize: 10, fill: 'var(--ag-text-tertiary)' }}
            axisLine={{ stroke: 'var(--ag-border)' }}
            tickLine={false}
          />
          <YAxis
            yAxisId="cost"
            tick={{ fontSize: 10, fill: 'var(--ag-text-tertiary)' }}
            axisLine={false}
            tickLine={false}
            tickFormatter={(v: number) => `$${v}`}
          />
          <YAxis
            yAxisId="count"
            orientation="right"
            tick={{ fontSize: 10, fill: 'var(--ag-text-tertiary)' }}
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
            itemStyle={{ padding: '2px 0' }}
            formatter={(value, name) => {
              const v = Number(value);
              if (name === 'count') return [fmtNum(v), t('accounts.stats_requests')];
              return [`$${v.toFixed(4)}`, name === 'totalCost' ? t('accounts.stats_total_cost_label') : t('accounts.stats_actual_cost_label')];
            }}
          />
          <Line yAxisId="cost" type="monotone" dataKey="totalCost" stroke="#3b82f6" strokeWidth={2} dot={false} name="totalCost" />
          <Line yAxisId="cost" type="monotone" dataKey="actualCost" stroke="#10b981" strokeWidth={2} dot={false} name="actualCost" />
          <Line yAxisId="count" type="monotone" dataKey="count" stroke="#f59e0b" strokeWidth={2} dot={false} name="count" />
        </LineChart>
      </ResponsiveContainer>
      <div className="flex items-center justify-center gap-4 mt-2">
        <LegendDot color="#3b82f6" label={`${t('accounts.stats_total_cost_label')} (USD)`} />
        <LegendDot color="#10b981" label={`${t('accounts.stats_actual_cost_label')} (USD)`} />
        <LegendDot color="#f59e0b" label={t('accounts.stats_requests')} />
      </div>
    </div>
  );
}

function LegendDot({ color, label }: { color: string; label: string }) {
  return (
    <div className="flex items-center gap-1.5 text-[11px] text-text-tertiary">
      <div className="w-2.5 h-2.5 rounded-full" style={{ background: color }} />
      {label}
    </div>
  );
}

// ==================== 模型分布 ====================

function ModelDistribution({ data }: { data: AccountStatsResp }) {
  const { t } = useTranslation();
  const models = data.models ?? [];

  const pieData = useMemo(() =>
    models.map((m) => ({ name: m.model, value: m.count })),
    [models],
  );

  return (
    <div className="rounded-lg border border-border-subtle p-4">
      <h4 className="text-xs font-semibold text-text mb-3">{t('accounts.stats_model_distribution')}</h4>
      <div className="flex gap-4">
        {/* 饼图 */}
        <div className="w-48 h-48 flex-shrink-0">
          <ResponsiveContainer width="100%" height="100%">
            <PieChart>
              <Pie
                data={pieData}
                cx="50%"
                cy="50%"
                innerRadius={35}
                outerRadius={70}
                dataKey="value"
                stroke="var(--ag-bg-elevated)"
                strokeWidth={1}
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
              />
            </PieChart>
          </ResponsiveContainer>
        </div>

        {/* 模型表格 */}
        <div className="flex-1 overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-border-subtle">
                <th className="text-left py-2 pr-3 text-[10px] font-semibold text-text-tertiary uppercase tracking-wider">{t('accounts.stats_model')}</th>
                <th className="text-right py-2 px-3 text-[10px] font-semibold text-text-tertiary uppercase tracking-wider">{t('accounts.stats_requests')}</th>
                <th className="text-right py-2 px-3 text-[10px] font-semibold text-text-tertiary uppercase tracking-wider">Token</th>
                <th className="text-right py-2 px-3 text-[10px] font-semibold text-text-tertiary uppercase tracking-wider">{t('accounts.stats_actual')}</th>
                <th className="text-right py-2 pl-3 text-[10px] font-semibold text-text-tertiary uppercase tracking-wider">{t('accounts.stats_standard')}</th>
              </tr>
            </thead>
            <tbody>
              {models.map((m, i) => (
                <tr key={m.model} className="border-b border-border-subtle last:border-0 hover:bg-bg-hover transition-colors">
                  <td className="py-2 pr-3 text-text font-medium flex items-center gap-1.5">
                    <div className="w-2 h-2 rounded-full flex-shrink-0" style={{ background: PIE_COLORS[i % PIE_COLORS.length] }} />
                    <span className="truncate max-w-[180px]">{m.model}</span>
                  </td>
                  <td className="py-2 px-3 text-right text-text-secondary font-mono">{m.count.toLocaleString()}</td>
                  <td className="py-2 px-3 text-right text-text-secondary font-mono">{fmtNum(m.input_tokens + m.output_tokens)}</td>
                  <td className="py-2 px-3 text-right font-mono text-warning">{fmtCost(m.actual_cost, 2)}</td>
                  <td className="py-2 pl-3 text-right text-text-secondary font-mono">{fmtCost(m.total_cost, 2)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
