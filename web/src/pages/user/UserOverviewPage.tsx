import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import {
  Wallet, Zap,
  Activity, Hash, DollarSign, Coins, Key,
} from 'lucide-react';
import { useAuth } from '../../app/providers/AuthProvider';
import { usageApi } from '../../shared/api/usage';
import { apikeysApi } from '../../shared/api/apikeys';
import { queryKeys } from '../../shared/queryKeys';
import { Card, StatCard } from '../../shared/components/Card';

export default function UserOverviewPage() {
  const { t } = useTranslation();
  const { user } = useAuth();
  const navigate = useNavigate();

  // 使用统计
  const { data: stats } = useQuery({
    queryKey: queryKeys.userUsageStats({}),
    queryFn: () => usageApi.userStats({}),
  });

  // API 密钥列表
  const { data: keysData } = useQuery({
    queryKey: queryKeys.userKeys({ page: 1, page_size: 1 }),
    queryFn: () => apikeysApi.list({ page: 1, page_size: 1 }),
  });

  const enabledKeys = keysData?.total ?? 0;

  return (
    <div>
      {/* 用户信息卡片 */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mb-6">
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
      </div>

      {/* 使用统计 */}
      <Card title={t('user_overview.recent_usage')}>
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
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
      </Card>

      {/* 密钥概览 */}
      <div className="mt-4">
        <Card
          title={t('user_overview.my_keys')}
          extra={
            <button
              className="text-xs text-primary hover:underline cursor-pointer"
              onClick={() => navigate({ to: '/keys' })}
            >
              {t('common.view_all')}
            </button>
          }
        >
          <div className="flex items-center gap-3 py-2">
            <Key className="w-5 h-5 text-text-tertiary" />
            <span className="text-sm text-text-secondary">
              {enabledKeys > 0
                ? t('user_overview.enabled_keys', { count: enabledKeys })
                : t('user_overview.keys_empty')}
            </span>
          </div>
        </Card>
      </div>
    </div>
  );
}

function fmtNum(n: number): string {
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(2)}B`;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(2)}K`;
  return n.toLocaleString();
}
