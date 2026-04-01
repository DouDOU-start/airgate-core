import { useState, useCallback, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  PieChart, Pie, Cell, ResponsiveContainer, Tooltip as RechartsTooltip,
  LineChart, Line, XAxis, YAxis, CartesianGrid, Legend,
} from 'recharts';
import { usageApi } from '../../shared/api/usage';
import { usersApi } from '../../shared/api/users';
import { usePagination } from '../../shared/hooks/usePagination';
import { Table, type Column } from '../../shared/components/Table';
import { Input, Select } from '../../shared/components/Input';
import { SearchSelect, type SearchSelectOption } from '../../shared/components/SearchSelect';
import { DatePicker } from '../../shared/components/DatePicker';
import { Card, StatCard } from '../../shared/components/Card';
import { Badge } from '../../shared/components/Badge';
import { usePlatforms } from '../../shared/hooks/usePlatforms';
import { Activity, Coins, Hash, DollarSign, Search } from 'lucide-react';
import type { UsageLogResp, UsageQuery, UsageTrendBucket } from '../../shared/types';

// 饼图颜色
const PIE_COLORS = [
  '#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6',
  '#06b6d4', '#ec4899', '#84cc16', '#f97316', '#6366f1',
];

/** 大数字友好显示 */
function fmtNum(n: number): string {
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(2)}B`;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 10_000) return `${(n / 1_000).toFixed(1)}K`;
  return n.toLocaleString();
}

/** 格式化费用 */
function fmtCost(n: number): string {
  if (n >= 1000) return `$${(n / 1000).toFixed(2)}K`;
  return `$${n.toFixed(2)}`;
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

  return (
    <Card
      title={title}
      extra={
        <div className="flex gap-1">
          {(['token', 'cost'] as const).map((m) => (
            <button
              key={m}
              className={`px-2.5 py-1 text-[11px] rounded font-medium transition-all cursor-pointer ${
                metric === m
                  ? 'bg-primary-subtle text-primary'
                  : 'text-text-tertiary hover:text-text'
              }`}
              onClick={() => setMetric(m)}
            >
              {m === 'token' ? t('usage.by_token') : t('usage.by_actual_cost')}
            </button>
          ))}
        </div>
      }
    >
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
                paddingAngle={2}
                dataKey="value"
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
          </ResponsiveContainer>
        </div>

        {/* 数据表格 */}
        <div className="flex-1 overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-border-subtle">
                <th className="text-left py-2 pr-3 text-[10px] font-semibold text-text-tertiary uppercase tracking-wider">
                  {title}
                </th>
                <th className="text-right py-2 px-3 text-[10px] font-semibold text-text-tertiary uppercase tracking-wider">
                  {t('usage.requests')}
                </th>
                <th className="text-right py-2 px-3 text-[10px] font-semibold text-text-tertiary uppercase tracking-wider">
                  {t('usage.tokens')}
                </th>
                <th className="text-right py-2 px-3 text-[10px] font-semibold text-text-tertiary uppercase tracking-wider">
                  {t('usage.actual_cost')}
                </th>
                <th className="text-right py-2 pl-3 text-[10px] font-semibold text-text-tertiary uppercase tracking-wider">
                  {t('usage.standard_cost')}
                </th>
              </tr>
            </thead>
            <tbody>
              {data.map((item, i) => (
                <tr key={item.name} className="border-b border-border-subtle last:border-0 hover:bg-bg-hover transition-colors">
                  <td className="py-2 pr-3 text-text font-medium flex items-center gap-1.5">
                    <div
                      className="w-2 h-2 rounded-full flex-shrink-0"
                      style={{ background: PIE_COLORS[i % PIE_COLORS.length] }}
                    />
                    <span className="truncate max-w-[180px]">{item.name}</span>
                  </td>
                  <td className="py-2 px-3 text-right text-text-secondary font-mono">
                    {item.requests.toLocaleString()}
                  </td>
                  <td className="py-2 px-3 text-right text-text-secondary font-mono">
                    {fmtNum(item.tokens)}
                  </td>
                  <td className="py-2 px-3 text-right font-mono text-warning">
                    {fmtCost(item.actualCost)}
                  </td>
                  <td className="py-2 pl-3 text-right text-text-secondary font-mono">
                    {fmtCost(item.totalCost)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </Card>
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

  if (chartData.length === 0) {
    return (
      <Card title={t('usage.token_trend')}>
        <div className="flex items-center justify-center h-48 text-text-tertiary text-sm">
          No data
        </div>
      </Card>
    );
  }

  return (
    <Card
      title={t('usage.token_trend')}
      extra={
        <div className="flex gap-1">
          {(['hour', 'day'] as const).map((g) => (
            <button
              key={g}
              className={`px-2.5 py-1 text-[11px] rounded font-medium transition-all cursor-pointer ${
                granularity === g
                  ? 'bg-primary-subtle text-primary'
                  : 'text-text-tertiary hover:text-text'
              }`}
              onClick={() => onGranularityChange(g)}
            >
              {t(`usage.granularity_${g}`)}
            </button>
          ))}
        </div>
      }
    >
      <ResponsiveContainer width="100%" height={300}>
        <LineChart data={chartData} margin={{ top: 5, right: 20, left: 10, bottom: 5 }}>
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
    </Card>
  );
}

// ==================== 主页面 ====================

export default function UsagePage() {
  const { t } = useTranslation();
  const { page, setPage, pageSize, setPageSize } = usePagination(20);
  const [filters, setFilters] = useState<Partial<UsageQuery>>({});
  const [statsGroupBy, setStatsGroupBy] = useState<string>('model');
  const [granularity, setGranularity] = useState<string>('hour');
  const { platforms, platformName } = usePlatforms();

  // 用户搜索
  const [userKeyword, setUserKeyword] = useState('');
  const { data: usersData, isLoading: usersLoading } = useQuery({
    queryKey: ['admin-users-search', userKeyword],
    queryFn: () => usersApi.list({ page: 1, page_size: 20, keyword: userKeyword }),
    enabled: userKeyword.length > 0,
  });
  const userOptions: SearchSelectOption[] = (usersData?.list ?? []).map((u) => ({
    value: String(u.id),
    label: u.username || u.email,
    description: u.username ? u.email : undefined,
  }));
  const handleUserSearch = useCallback((kw: string) => setUserKeyword(kw), []);

  // 构建查询参数
  const queryParams: UsageQuery = {
    page,
    page_size: pageSize,
    ...filters,
  };

  // 使用记录列表
  const { data, isLoading } = useQuery({
    queryKey: ['admin-usage', queryParams],
    queryFn: () => usageApi.adminList(queryParams),
  });

  // 聚合统计
  const allGroupBy = useMemo(() => {
    const groups = new Set(['model', 'group', statsGroupBy]);
    return Array.from(groups).join(',');
  }, [statsGroupBy]);

  const { data: stats } = useQuery({
    queryKey: ['admin-usage-stats', allGroupBy, filters.start_date, filters.end_date, filters.platform, filters.model, filters.user_id],
    queryFn: () =>
      usageApi.stats({
        group_by: allGroupBy,
        start_date: filters.start_date,
        end_date: filters.end_date,
        platform: filters.platform,
        model: filters.model,
        user_id: filters.user_id ? Number(filters.user_id) : undefined,
      }),
  });

  // Token 趋势
  const { data: trendData } = useQuery({
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
  });

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

  // 分组统计表头
  const groupByHeaderKeys: Record<string, string> = {
    model: 'usage.model',
    user: 'usage.user_id',
    account: 'usage.by_account',
    group: 'usage.by_group',
  };

  const columns: Column<UsageLogResp>[] = [
    {
      key: 'created_at',
      title: t('usage.time'),
      render: (row) => (
        <span className="text-text-secondary">
          {new Date(row.created_at).toLocaleString('zh-CN')}
        </span>
      ),
    },
    {
      key: 'user_id',
      title: t('usage.user_id'),
      render: (row) => (
        <span className="text-text-tertiary font-mono">
          {row.user_email || `#${row.user_id}`}
        </span>
      ),
    },
    {
      key: 'model',
      title: t('usage.model'),
      render: (row) => {
        const keyParts: string[] = [];
        if (row.api_key_deleted) {
          keyParts.push(t('usage.api_key_deleted'));
        } else {
          if (row.api_key_hint) keyParts.push(row.api_key_hint);
          if (row.api_key_name) keyParts.push(row.api_key_name);
          if (keyParts.length === 0) keyParts.push(`API Key #${row.api_key_id}`);
        }
        return (
          <div>
            <div className="text-text">{row.model}</div>
            <div className="text-xs text-text-tertiary font-mono truncate max-w-[200px]" title={keyParts.join(' · ')}>
              {keyParts.join(' · ')}
            </div>
          </div>
        );
      },
    },
    {
      key: 'account_name',
      title: t('usage.account'),
      render: (row) => {
        const label = row.account_name || `#${row.account_id}`;
        return (
          <span className="text-text-tertiary font-mono truncate max-w-[160px] inline-block" title={label}>
            {label}
          </span>
        );
      },
    },
    {
      key: 'input_tokens',
      title: t('usage.input_tokens'),
      render: (row) => (
        <span className="font-mono">
          {row.input_tokens.toLocaleString()}
        </span>
      ),
    },
    {
      key: 'output_tokens',
      title: t('usage.output_tokens'),
      render: (row) => (
        <span className="font-mono">
          {row.output_tokens.toLocaleString()}
        </span>
      ),
    },
    {
      key: 'cached_input_tokens',
      title: t('usage.cached_input_tokens'),
      render: (row) => (
        <span className="font-mono text-text-secondary">
          {row.cached_input_tokens > 0 ? row.cached_input_tokens.toLocaleString() : '-'}
        </span>
      ),
    },
    {
      key: 'total_cost',
      title: t('usage.total_cost'),
      render: (row) => (
        <span className="font-mono">
          ${row.total_cost.toFixed(6)}
        </span>
      ),
    },
    {
      key: 'actual_cost',
      title: t('usage.actual_cost'),
      render: (row) => (
        <span className="font-mono">
          ${row.actual_cost.toFixed(6)}
        </span>
      ),
    },
    {
      key: 'stream',
      title: t('usage.stream'),
      render: (row) => (
        <Badge variant={row.stream ? 'info' : 'default'}>
          {row.stream ? t('common.yes') : t('common.no')}
        </Badge>
      ),
    },
    {
      key: 'duration_ms',
      title: t('usage.duration'),
      render: (row) => (
        <span className="font-mono">
          {row.duration_ms}ms
        </span>
      ),
    },
  ];

  return (
    <div>
      {/* 筛选栏 */}
      <div className="flex items-center gap-3 mb-5 flex-wrap">
        <div className="w-44">
          <DatePicker
            placeholder={t('usage.start_date')}
            value={filters.start_date || ''}
            onChange={(v) => updateFilter('start_date', v)}
          />
        </div>
        <div className="w-44">
          <DatePicker
            placeholder={t('usage.end_date')}
            value={filters.end_date || ''}
            onChange={(v) => updateFilter('end_date', v)}
          />
        </div>
        <div className="w-40">
          <Select
            placeholder={t('common.all')}
            value={filters.platform || ''}
            onChange={(e) => updateFilter('platform', e.target.value)}
            options={[
              { label: t('common.all'), value: '' },
              ...platforms.map((p) => ({ label: platformName(p), value: p })),
            ]}
          />
        </div>
        <div className="w-40">
          <Input
            placeholder={t('usage.model_placeholder')}
            value={filters.model || ''}
            onChange={(e) => updateFilter('model', e.target.value)}
            icon={<Search className="w-4 h-4" />}
          />
        </div>
        <div className="w-48">
          <SearchSelect
            placeholder={t('usage.search_user')}
            value={filters.user_id ? String(filters.user_id) : ''}
            onChange={(v) => updateFilter('user_id', v)}
            onSearch={handleUserSearch}
            options={userOptions}
            loading={usersLoading}
          />
        </div>
      </div>

      {/* 聚合统计 */}
      {stats && (
        <div className="mb-6 space-y-4">
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
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

          {/* 饼图区域 + Token 趋势 */}
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
            <DistributionCard
              title={t('usage.model_distribution')}
              data={modelDistribution}
            />
            <DistributionCard
              title={t('usage.group_distribution')}
              data={groupDistribution}
            />
          </div>

          {/* Token 使用趋势 */}
          <TokenTrendCard
            data={trendData ?? []}
            granularity={granularity}
            onGranularityChange={setGranularity}
          />

          {/* 分组统计切换 */}
          <Card>
            <div className="flex items-center gap-2 mb-4">
              <span className="text-xs text-text-tertiary uppercase tracking-wider">
                {t('usage.group_stats')}
              </span>
              <div className="flex gap-1 ml-2">
                {Object.entries(groupByKeys).map(([key, i18nKey]) => (
                  <button
                    key={key}
                    className={`px-3 py-1.5 text-xs rounded-md font-medium transition-all duration-200 cursor-pointer ${
                      statsGroupBy === key
                        ? 'bg-primary-subtle text-primary shadow-[0_0_8px_var(--ag-primary-glow)]'
                        : 'text-text-secondary hover:bg-bg-hover hover:text-text'
                    }`}
                    onClick={() => setStatsGroupBy(key)}
                  >
                    {t(i18nKey)}
                  </button>
                ))}
              </div>
            </div>

            {/* 统计表格 */}
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-border">
                    <th className="text-left py-2.5 pr-4 text-[10px] font-semibold text-text-tertiary uppercase tracking-widest">
                      {t(groupByHeaderKeys[statsGroupBy] ?? 'usage.model')}
                    </th>
                    <th className="text-left py-2.5 pr-4 text-[10px] font-semibold text-text-tertiary uppercase tracking-widest">
                      {t('usage.requests')}
                    </th>
                    <th className="text-left py-2.5 pr-4 text-[10px] font-semibold text-text-tertiary uppercase tracking-widest">
                      {t('usage.tokens')}
                    </th>
                    <th className="text-left py-2.5 pr-4 text-[10px] font-semibold text-text-tertiary uppercase tracking-widest">
                      {t('usage.actual_cost')}
                    </th>
                    <th className="text-left py-2.5 text-[10px] font-semibold text-text-tertiary uppercase tracking-widest">
                      {t('usage.standard_cost')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {(() => {
                    const rowClass = "border-b border-border-subtle last:border-0 transition-colors hover:bg-bg-hover";
                    const dataMap: Record<string, { items: Array<{ key: string | number; name: string; requests: number; tokens: number; total_cost: number; actual_cost: number }> }> = {
                      model: { items: stats.by_model?.map((s) => ({ key: s.model, name: s.model, requests: s.requests, tokens: s.tokens, total_cost: s.total_cost, actual_cost: s.actual_cost })) ?? [] },
                      user: { items: stats.by_user?.map((s) => ({ key: s.user_id, name: s.email, requests: s.requests, tokens: s.tokens, total_cost: s.total_cost, actual_cost: s.actual_cost })) ?? [] },
                      account: { items: stats.by_account?.map((s) => ({ key: s.account_id, name: s.name, requests: s.requests, tokens: s.tokens, total_cost: s.total_cost, actual_cost: s.actual_cost })) ?? [] },
                      group: { items: stats.by_group?.map((s) => ({ key: s.group_id, name: s.name, requests: s.requests, tokens: s.tokens, total_cost: s.total_cost, actual_cost: s.actual_cost })) ?? [] },
                    };
                    return dataMap[statsGroupBy]?.items.map((row) => (
                      <tr key={row.key} className={rowClass}>
                        <td className="py-2.5 pr-4 font-medium text-text">{row.name}</td>
                        <td className="py-2.5 pr-4 text-text-secondary font-mono">{row.requests.toLocaleString()}</td>
                        <td className="py-2.5 pr-4 text-text-secondary font-mono">{fmtNum(row.tokens)}</td>
                        <td className="py-2.5 pr-4 font-mono text-warning">{fmtCost(row.actual_cost)}</td>
                        <td className="py-2.5 text-text-secondary font-mono">{fmtCost(row.total_cost)}</td>
                      </tr>
                    ));
                  })()}
                </tbody>
              </table>
            </div>
          </Card>
        </div>
      )}

      {/* 使用记录表格 */}
      <Table
        columns={columns}
        data={data?.list ?? []}
        loading={isLoading}
        rowKey={(row) => row.id as number}
        page={page}
        pageSize={pageSize}
        total={data?.total ?? 0}
        onPageChange={setPage}
        onPageSizeChange={setPageSize}
      />
    </div>
  );
}
