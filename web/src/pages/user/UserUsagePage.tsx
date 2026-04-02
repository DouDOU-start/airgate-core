import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { usageApi } from '../../shared/api/usage';
import { queryKeys } from '../../shared/queryKeys';
import { usePagination } from '../../shared/hooks/usePagination';
import { Table } from '../../shared/components/Table';
import { Input, Select } from '../../shared/components/Input';
import { DatePicker } from '../../shared/components/DatePicker';
import { StatCard } from '../../shared/components/Card';
import { usePlatforms } from '../../shared/hooks/usePlatforms';
import { Activity, Hash, DollarSign, Coins, Search } from 'lucide-react';
import { useUsageColumns, fmtNum } from '../../shared/columns/usageColumns';
import type { UsageQuery } from '../../shared/types';

export default function UserUsagePage() {
  const { t } = useTranslation();
  const { page, setPage, pageSize, setPageSize } = usePagination(20);
  const [filters, setFilters] = useState<Partial<UsageQuery>>({});

  const queryParams: UsageQuery = {
    page,
    page_size: pageSize,
    ...filters,
  };

  const { platforms, platformName } = usePlatforms();

  const { data, isLoading } = useQuery({
    queryKey: queryKeys.userUsage(queryParams),
    queryFn: () => usageApi.list(queryParams),
  });

  // 聚合统计（跟随筛选条件，独立于分页）
  const { data: stats } = useQuery({
    queryKey: queryKeys.userUsageStats(filters),
    queryFn: () => usageApi.userStats(filters),
  });

  function updateFilter(key: string, value: string) {
    setFilters((prev) => ({ ...prev, [key]: value || undefined }));
    setPage(1);
  }

  const list = data?.list ?? [];
  const total = data?.total ?? 0;

  const columns = useUsageColumns();

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
      </div>

      {/* 概览统计 */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
        <StatCard
          title={t('usage.total_requests')}
          value={(stats?.total_requests ?? 0).toLocaleString()}
          icon={<Activity className="w-5 h-5" />}
          accentColor="var(--ag-primary)"
        />
        <StatCard
          title={t('usage.total_tokens')}
          value={fmtNum(stats?.total_tokens ?? 0)}
          icon={<Hash className="w-5 h-5" />}
          accentColor="var(--ag-info)"
        />
        <StatCard
          title={t('usage.total_cost')}
          value={`$${(stats?.total_cost ?? 0).toFixed(4)}`}
          icon={<DollarSign className="w-5 h-5" />}
          accentColor="var(--ag-warning)"
        />
        <StatCard
          title={t('usage.actual_cost')}
          value={`$${(stats?.total_actual_cost ?? 0).toFixed(4)}`}
          icon={<Coins className="w-5 h-5" />}
          accentColor="var(--ag-success)"
        />
      </div>

      {/* 使用记录表格 */}
      <Table
        columns={columns}
        data={list}
        loading={isLoading}
        rowKey={(row) => row.id as number}
        page={page}
        pageSize={pageSize}
        total={total}
        onPageChange={setPage}
        onPageSizeChange={setPageSize}
        separateHeader
      />
    </div>
  );
}
