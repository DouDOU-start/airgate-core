import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { usageApi } from '../../shared/api/usage';
import { usePagination } from '../../shared/hooks/usePagination';
import { Table, type Column } from '../../shared/components/Table';
import { Input, Select } from '../../shared/components/Input';
import { DatePicker } from '../../shared/components/DatePicker';
import { Card, StatCard } from '../../shared/components/Card';
import { Badge } from '../../shared/components/Badge';
import { usePlatforms } from '../../shared/hooks/usePlatforms';
import { Activity, Coins, Hash, DollarSign, Search } from 'lucide-react';
import type { UsageLogResp, UsageQuery } from '../../shared/types';

/** 大数字友好显示：33518599 → "33.52M"，1234 → "1,234" */
function formatLargeNumber(n: number): string {
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(2)}B`;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 10_000) return `${(n / 1_000).toFixed(1)}K`;
  return n.toLocaleString();
}

// 分组统计 key 映射
const groupByKeys: Record<string, string> = {
  model: 'usage.by_model',
  user: 'usage.by_user',
  account: 'usage.by_account',
  group: 'usage.by_group',
};

export default function UsagePage() {
  const { t } = useTranslation();
  const { page, setPage, pageSize, setPageSize } = usePagination(20);
  const [filters, setFilters] = useState<Partial<UsageQuery>>({});
  const [statsGroupBy, setStatsGroupBy] = useState<string>('model');
  const { platforms, platformName } = usePlatforms();

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
  const { data: stats } = useQuery({
    queryKey: ['admin-usage-stats', statsGroupBy, filters.start_date, filters.end_date],
    queryFn: () =>
      usageApi.stats({
        group_by: statsGroupBy,
        start_date: filters.start_date,
        end_date: filters.end_date,
      }),
  });

  function updateFilter(key: string, value: string) {
    setFilters((prev) => ({ ...prev, [key]: value || undefined }));
    setPage(1);
  }

  // 从分组标签中提取表头名（去掉"按"前缀的部分）
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
          #{row.user_id}
        </span>
      ),
    },
    {
      key: 'model',
      title: t('usage.model'),
      render: (row) => <span className="text-text">{row.model}</span>,
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
        <div className="w-36">
          <Input
            type="number"
            placeholder={t('usage.user_id')}
            value={filters.user_id ?? ''}
            onChange={(e) => updateFilter('user_id', e.target.value)}
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
              value={formatLargeNumber(stats.total_tokens)}
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
                    {statsGroupBy === 'model' && (
                      <th className="text-left py-2.5 pr-4 text-[10px] font-semibold text-text-tertiary uppercase tracking-widest">
                        {t('usage.tokens')}
                      </th>
                    )}
                    <th className="text-left py-2.5 text-[10px] font-semibold text-text-tertiary uppercase tracking-widest">
                      {t('usage.cost')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {(() => {
                    const rowClass = "border-b border-border-subtle last:border-0 transition-colors hover:bg-bg-hover";
                    const dataMap: Record<string, { items: Array<{ key: string | number; name: string; requests: number; tokens?: number; total_cost: number }> }> = {
                      model: { items: stats.by_model?.map((s) => ({ key: s.model, name: s.model, requests: s.requests, tokens: s.tokens, total_cost: s.total_cost })) ?? [] },
                      user: { items: stats.by_user?.map((s) => ({ key: s.user_id, name: s.email, requests: s.requests, total_cost: s.total_cost })) ?? [] },
                      account: { items: stats.by_account?.map((s) => ({ key: s.account_id, name: s.name, requests: s.requests, total_cost: s.total_cost })) ?? [] },
                      group: { items: stats.by_group?.map((s) => ({ key: s.group_id, name: s.name, requests: s.requests, total_cost: s.total_cost })) ?? [] },
                    };
                    return dataMap[statsGroupBy]?.items.map((row) => (
                      <tr key={row.key} className={rowClass}>
                        <td className="py-2.5 pr-4 font-medium text-text">{row.name}</td>
                        <td className="py-2.5 pr-4 text-text-secondary font-mono">{row.requests.toLocaleString()}</td>
                        {statsGroupBy === 'model' && (
                          <td className="py-2.5 pr-4 text-text-secondary font-mono">{row.tokens?.toLocaleString()}</td>
                        )}
                        <td className="py-2.5 text-text-secondary font-mono">${row.total_cost.toFixed(4)}</td>
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
