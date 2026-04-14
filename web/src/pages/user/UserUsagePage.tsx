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
import { useAuth } from '../../app/providers/AuthProvider';
import { useToast } from '../../shared/components/Toast';
import { Activity, Hash, DollarSign, Coins, Search, RefreshCw, Key, Clock, Gauge, Percent, Upload } from 'lucide-react';
import { useUsageColumns, fmtNum } from '../../shared/columns/usageColumns';
import { getSessionAPIKey } from '../../shared/api/client';
import { CcsImportModal } from './userkeys/CcsImportModal';
import type { UsageQuery } from '../../shared/types';

function APIKeyInfoBar() {
  const { t } = useTranslation();
  const { user } = useAuth();
  const { toast } = useToast();
  const [ccsOpen, setCcsOpen] = useState(false);
  if (!user?.api_key_id) return null;

  const quota = user.api_key_quota_usd ?? 0;
  const used = user.api_key_used_quota ?? 0;
  const expiresAt = user.api_key_expires_at;
  const pct = quota > 0 ? Math.min((used / quota) * 100, 100) : 0;

  // 原文 Key 仅在 API Key 登录当次会话内通过 sessionStorage 暂存；刷新页面后丢失，
  // 此时按钮会提示用户重新登录。
  const sessionKey = getSessionAPIKey();
  const platform = user.api_key_platform || '';
  const canImportCcs = !!sessionKey;

  function handleImportCcs() {
    if (!sessionKey) {
      toast('error', t('user_keys.ccs_session_expired'));
      return;
    }
    setCcsOpen(true);
  }

  // 后端已经把"销售倍率优先、否则分组倍率"折算成单一字段 api_key_rate，
  // 前端拿不到原始来源，避免通过 DevTools 推断 reseller 定价模型。
  const effectiveRate = user.api_key_rate ?? 0;

  // 到期时间格式化
  let expiresLabel = '';
  let expiresWarning = false;
  if (expiresAt) {
    const d = new Date(expiresAt);
    const now = new Date();
    const diffDays = Math.ceil((d.getTime() - now.getTime()) / 86400000);
    expiresLabel = d.toLocaleDateString();
    expiresWarning = diffDays <= 7;
  }

  return (
    <div className="flex items-center gap-4 mb-5 px-4 py-3 rounded-xl border border-glass-border bg-bg-elevated text-sm flex-wrap">
      <div className="flex items-center gap-2 text-text-secondary">
        <Key className="w-4 h-4 text-primary" />
        <span className="font-medium text-text">{user.api_key_name}</span>
      </div>

      {quota > 0 && (
        <div className="flex items-center gap-2">
          <Gauge className="w-3.5 h-3.5 text-text-tertiary" />
          <span className="text-text-tertiary">{t('auth.apikey_quota')}:</span>
          <span className={pct >= 90 ? 'text-danger font-medium' : 'text-text-secondary'}>
            ${used.toFixed(4)} / ${quota.toFixed(2)}
          </span>
          <div className="w-20 h-1.5 rounded-full bg-bg-hover overflow-hidden">
            <div
              className="h-full rounded-full transition-all"
              style={{
                width: `${pct}%`,
                background: pct >= 90 ? 'var(--ag-danger)' : pct >= 70 ? 'var(--ag-warning)' : 'var(--ag-primary)',
              }}
            />
          </div>
        </div>
      )}

      {quota === 0 && (
        <div className="flex items-center gap-2 text-text-tertiary">
          <Gauge className="w-3.5 h-3.5" />
          <span>{t('auth.apikey_quota')}: {t('auth.apikey_unlimited')}</span>
        </div>
      )}

      {expiresAt && (
        <div className="flex items-center gap-2">
          <Clock className="w-3.5 h-3.5 text-text-tertiary" />
          <span className="text-text-tertiary">{t('auth.apikey_expires')}:</span>
          <span className={expiresWarning ? 'text-warning font-medium' : 'text-text-secondary'}>
            {expiresLabel}
          </span>
        </div>
      )}

      {!expiresAt && (
        <div className="flex items-center gap-2 text-text-tertiary">
          <Clock className="w-3.5 h-3.5" />
          <span>{t('auth.apikey_expires')}: {t('auth.apikey_never')}</span>
        </div>
      )}

      {effectiveRate > 0 && (
        <div className="flex items-center gap-2">
          <Percent className="w-3.5 h-3.5 text-text-tertiary" />
          <span className="text-text-tertiary">{t('auth.apikey_rate', '倍率')}:</span>
          <span className="text-text-secondary font-mono">{effectiveRate.toFixed(2)}x</span>
        </div>
      )}

      <button
        type="button"
        onClick={handleImportCcs}
        disabled={!canImportCcs}
        title={!canImportCcs ? t('user_keys.ccs_session_expired') : undefined}
        className="ml-auto inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-glass-border bg-surface text-text-secondary hover:text-text hover:bg-bg-hover disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
      >
        <Upload className="w-3.5 h-3.5" />
        <span>{t('user_keys.import_ccs')}</span>
      </button>

      <CcsImportModal
        open={ccsOpen}
        ccsKeyValue={sessionKey}
        ccsPlatform={platform}
        onClose={() => setCcsOpen(false)}
      />
    </div>
  );
}

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

  const { data, isLoading, refetch, isFetching } = useQuery({
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

  const { user } = useAuth();
  const customerScope = !!user?.api_key_id;
  const columns = useUsageColumns({ customerScope });

  return (
    <div>
      {/* API Key 登录信息 */}
      <APIKeyInfoBar />

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
        <button
          onClick={() => refetch()}
          className="flex items-center justify-center w-9 h-9 rounded-[10px] text-text-tertiary hover:text-text-secondary hover:bg-bg-hover transition-colors"
          title={t('common.refresh')}
        >
          <RefreshCw className={`w-4 h-4${isFetching ? ' animate-spin' : ''}`} />
        </button>
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
