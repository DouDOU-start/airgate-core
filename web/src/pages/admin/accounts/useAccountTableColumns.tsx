import { useMemo, useRef, type MouseEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { useQueryClient } from '@tanstack/react-query';
import { RefreshCw } from 'lucide-react';
import { getPluginAccountIdentity } from '../../../app/plugin-frontend-registry';
import { accountsApi } from '../../../shared/api/accounts';
import { queryKeys } from '../../../shared/queryKeys';
import type { AccountResp } from '../../../shared/types';
import { PlatformIcon, useToast } from '../../../shared/ui';
import {
  AccountCapacityChip,
  AccountRowActions,
  AccountSchedulingSwitch,
  AccountStatusCell,
  useUsageResetClock,
  type AccountTableColumn,
  type AccountUsageCredits,
  type AccountUsageData,
  type AccountUsageTodayStats,
  type AccountUsageWindow,
} from './AccountPageSupport';

type QuotaRefreshResult = Awaited<ReturnType<typeof accountsApi.refreshQuota>>;

type UseAccountTableColumnsArgs = {
  applyQuotaRefreshResult: (id: number, result: QuotaRefreshResult) => void;
  groupMap: Map<number, string>;
  onClearRateLimitMarkers: (id: number) => void;
  onDeleteAccount: (row: AccountResp) => void;
  onEditAccount: (row: AccountResp) => void;
  onRefreshQuota: (id: number) => void;
  onStatsAccount: (id: number) => void;
  onTestAccount: (row: AccountResp) => void;
  onToggleScheduling: (id: number) => void;
  platformFilter: string;
  platformName: (platform: string) => string;
  platformsKey: string;
  usageData: AccountUsageData | undefined;
};

export function useAccountTableColumns({
  applyQuotaRefreshResult,
  groupMap,
  onClearRateLimitMarkers,
  onDeleteAccount,
  onEditAccount,
  onRefreshQuota,
  onStatsAccount,
  onTestAccount,
  onToggleScheduling,
  platformFilter,
  platformName,
  platformsKey,
  usageData,
}: UseAccountTableColumnsArgs): AccountTableColumn[] {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const usageDataRef = useRef(usageData);
  usageDataRef.current = usageData;
  const resetNow = useUsageResetClock(Boolean(usageData?.accounts));

  const accountActionLabels = useMemo(() => ({
    actions: t('common.actions'),
    clearCooldowns: t('accounts.clear_family_cooldowns'),
    delete: t('common.delete'),
    edit: t('common.edit'),
    more: t('common.more'),
    refreshQuota: t('accounts.refresh_quota'),
    stats: t('accounts.view_stats'),
    test: t('accounts.test_connection'),
  }), [t]);

  return useMemo<AccountTableColumn[]>(() => [
    {
      key: 'name',
      title: t('common.name'),
      width: '132px',
      mobileWidth: '112px',
      render: (row) => {
        const email = row.credentials?.email;
        return (
          <div className="flex w-full min-w-0 flex-col items-center text-center">
            <span style={{ color: 'var(--ag-text)' }} className="max-w-full truncate font-medium" title={row.name}>
              {row.name}
            </span>
            {email && (
              <span className="max-w-full truncate text-[11px]" style={{ color: 'var(--ag-text-tertiary)' }} title={email}>
                {email}
              </span>
            )}
          </div>
        );
      },
    },
    {
      key: 'platform',
      title: t('accounts.platform_type'),
      width: '96px',
      mobileWidth: '84px',
      render: (row) => {
        const PluginAccountIdentity = getPluginAccountIdentity(row.platform);
        return (
          <div className="flex w-full min-w-0 flex-col items-center gap-1 text-center">
            <span className="inline-flex max-w-full min-w-0 items-center justify-center gap-1">
              <PlatformIcon platform={row.platform} className="w-3.5 h-3.5" />
              <span className="min-w-0 truncate">{platformName(row.platform)}</span>
            </span>
            {PluginAccountIdentity ? (
              <PluginAccountIdentity
                accountId={row.id}
                accountType={row.type}
                context={{ account: row, credentials: row.credentials }}
              />
            ) : (
              <div className="flex max-w-full items-center justify-center gap-1">
                {row.type && (
                  <span className="truncate rounded px-1 py-0 text-[10px]" style={{ background: 'var(--ag-bg-surface)', border: '1px solid var(--ag-glass-border)', color: 'var(--ag-text-secondary)' }}>
                    {{ oauth: 'OAuth', session_key: 'Session Key', apikey: 'API Key' }[row.type] ?? row.type}
                  </span>
                )}
              </div>
            )}
          </div>
        );
      },
    },
    {
      key: 'groups',
      title: t('accounts.groups'),
      width: '92px',
      mobileWidth: '80px',
      align: 'center',
      render: (row) => {
        if (!row.group_ids || row.group_ids.length === 0) {
          return <span style={{ color: 'var(--ag-text-tertiary)' }}>-</span>;
        }
        const groupNames = row.group_ids.map((gid) => groupMap.get(gid) ?? `#${gid}`);
        const visibleGroups = groupNames.length > 3 ? groupNames.slice(0, 2) : groupNames.slice(0, 3);
        const hiddenCount = Math.max(0, groupNames.length - visibleGroups.length);
        return (
          <div className="ag-account-group-list" title={groupNames.join('\n')}>
            {visibleGroups.map((name) => (
              <span
                key={name}
                className="ag-account-group-chip"
                style={{ background: 'var(--ag-bg-surface)', border: '1px solid var(--ag-glass-border)', color: 'var(--ag-text-secondary)' }}
              >
                {name}
              </span>
            ))}
            {hiddenCount > 0 ? (
              <span
                className="ag-account-group-chip ag-account-group-chip--more"
                style={{ background: 'var(--ag-bg-surface)', border: '1px solid var(--ag-glass-border)', color: 'var(--ag-text-secondary)' }}
              >
                +{hiddenCount}
              </span>
            ) : null}
          </div>
        );
      },
    },
    {
      key: 'capacity',
      title: t('accounts.capacity'),
      width: '84px',
      mobileWidth: '68px',
      align: 'center',
      render: (row) => {
        const current = row.current_concurrency || 0;
        const max = row.max_concurrency;
        return <AccountCapacityChip current={current} max={max} />;
      },
    },
    {
      key: 'status',
      title: t('common.status'),
      width: '84px',
      mobileWidth: '76px',
      align: 'center',
      render: (row) => <AccountStatusCell row={row} />,
    },
    {
      key: 'scheduling',
      title: t('accounts.scheduling'),
      width: '80px',
      mobileWidth: '72px',
      align: 'center',
      render: (row) => (
        <AccountSchedulingSwitch
          ariaLabel={t('accounts.scheduling')}
          isSelected={row.state !== 'disabled'}
          rowId={row.id}
          onToggle={onToggleScheduling}
        />
      ),
    },
    {
      key: 'rate_multiplier',
      title: t('accounts.rate_multiplier'),
      width: '80px',
      mobileWidth: '72px',
      align: 'center',
      render: (row) => (
        <span className="font-mono" style={{ color: 'var(--ag-primary)' }}>
          {row.rate_multiplier}x
        </span>
      ),
    },
    {
      key: 'usage_window',
      title: t('accounts.usage_window'),
      width: '364px',
      mobileWidth: '364px',
      maxWidth: '364px',
      align: 'center',
      render: (row: AccountResp) => {
        const usage = usageDataRef.current?.accounts?.[String(row.id)];

        const handleRefreshClick = async (event: MouseEvent<HTMLElement>) => {
          event.stopPropagation();
          const target = event.currentTarget as HTMLElement;
          target.style.opacity = '0.5';
          target.style.pointerEvents = 'none';
          try {
            const result = await accountsApi.refreshQuota(row.id);
            applyQuotaRefreshResult(row.id, result);
            queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
            queryClient.invalidateQueries({ queryKey: queryKeys.accountUsage(platformFilter) });
            toast('success', t('accounts.refresh_usage_success', '用量刷新成功'));
          } catch (err) {
            const message = err instanceof Error && err.message ? err.message : t('accounts.refresh_usage_failed', '用量刷新失败');
            toast('error', message);
          }
          target.style.opacity = '1';
          target.style.pointerEvents = '';
        };

        if (!usage) {
          return (
            <div
              className="flex items-center gap-1 cursor-pointer rounded px-1 py-0.5 transition-colors hover:bg-[var(--ag-glass-border)]"
              title={t('accounts.refresh_usage', '点击刷新用量')}
              onClick={handleRefreshClick}
            >
              <span style={{ color: 'var(--ag-text-tertiary)' }}>-</span>
              <RefreshCw size={11} style={{ color: 'var(--ag-text-tertiary)' }} />
            </div>
          );
        }

        type UsageWindowRow = { id: string; window?: AccountUsageWindow };
        const windows: AccountUsageWindow[] = usage.windows || [];
        const credits: AccountUsageCredits | null = usage.credits || null;
        const todayStats: AccountUsageTodayStats | null = usage.today_stats || null;

        const formatCompact = (num: number, allowBillions = true) => {
          if (!num) return '0';
          const abs = Math.abs(num);
          if (allowBillions && abs >= 1_000_000_000) return `${(num / 1_000_000_000).toFixed(1)}B`;
          if (abs >= 1_000_000) return `${(num / 1_000_000).toFixed(1)}M`;
          if (abs >= 1_000) return `${(num / 1_000).toFixed(1)}K`;
          return String(num);
        };

        const hasTodayStats = todayStats != null;
        const canRefresh = row.type !== 'apikey';
        if (windows.length === 0 && !credits && !hasTodayStats) {
          return (
            <div
              className={
                canRefresh
                  ? 'flex items-center gap-1 cursor-pointer rounded px-1 py-0.5 transition-colors hover:bg-[var(--ag-glass-border)]'
                  : 'flex items-center gap-1 rounded px-1 py-0.5'
              }
              title={canRefresh ? t('accounts.refresh_usage', '点击刷新用量') : undefined}
              onClick={canRefresh ? handleRefreshClick : undefined}
            >
              <span style={{ color: 'var(--ag-text-tertiary)' }}>-</span>
              {canRefresh && <RefreshCw size={11} style={{ color: 'var(--ag-text-tertiary)' }} />}
            </div>
          );
        }

        const getResetSeconds = (w: AccountUsageWindow) => {
          if (w.reset_at) {
            const delta = Date.parse(w.reset_at) - resetNow;
            if (Number.isFinite(delta)) return Math.max(0, Math.floor(delta / 1000));
          }
          if (typeof w.reset_seconds === 'number') return w.reset_seconds;
          if (typeof w.reset_after_seconds === 'number') return w.reset_after_seconds;
          return 0;
        };

        const formatReset = (seconds: number) => {
          if (!seconds || seconds <= 0) return '-';
          const d = Math.floor(seconds / 86400);
          const h = Math.floor((seconds % 86400) / 3600);
          const m = Math.floor((seconds % 3600) / 60);
          if (d > 0) return h > 0 ? `${d}d${h}h` : `${d}d`;
          if (h > 0) return m > 0 ? `${h}h${m}m` : `${h}h`;
          return `${m}m`;
        };

        const usageColor = (pct: number) => {
          if (pct < 50) return 'var(--ag-success)';
          if (pct < 80) return 'var(--ag-warning)';
          return 'var(--ag-danger)';
        };

        const normalizeWindowToken = (value?: string) => value?.trim().toLowerCase().replace(/_/g, '-') || '';
        const getWindowSlot = (w: AccountUsageWindow) => {
          const key = w.key || '';
          const label = w.label || '';
          const normalizedKey = normalizeWindowToken(key);
          const normalizedLabel = normalizeWindowToken(label);
          const slot = normalizeWindowToken(w.slot)
            || (normalizedKey.includes(':7d') || normalizedKey === '7d' || normalizedLabel.startsWith('7d') ? '7d'
              : normalizedKey === 'monthly' || normalizedKey.includes('monthly') || normalizedLabel.includes('monthly') ? 'monthly'
                : '5h');
          const group = w.group?.trim()
            || (key.startsWith('model:') ? key.replace(/^model:(5h|7d):/, 'model:') : 'base');
          return { group, slot };
        };
        const buildWindowRows = (items: AccountUsageWindow[]): UsageWindowRow[] => {
          const groups: Array<{ id: string; five?: AccountUsageWindow; seven?: AccountUsageWindow; other: AccountUsageWindow[] }> = [];
          const groupMap = new Map<string, { id: string; five?: AccountUsageWindow; seven?: AccountUsageWindow; other: AccountUsageWindow[] }>();

          for (const item of items) {
            const { group, slot } = getWindowSlot(item);
            let bucket = groupMap.get(group);
            if (!bucket) {
              bucket = { id: group, other: [] };
              groupMap.set(group, bucket);
              groups.push(bucket);
            }
            if (slot === '7d') bucket.seven = item;
            else if (slot === '5h') bucket.five = item;
            else bucket.other.push(item);
          }

          return groups.flatMap((group) => {
            const rows: UsageWindowRow[] = [];
            if (group.five || group.seven) {
              if (group.five) {
                rows.push({ id: `${group.id}:5h`, window: group.five });
              } else {
                rows.push({ id: `${group.id}:5h-placeholder` });
              }
              if (group.seven) {
                rows.push({ id: `${group.id}:7d`, window: group.seven });
              } else {
                rows.push({ id: `${group.id}:7d-placeholder` });
              }
            }
            for (const window of group.other) {
              const { slot } = getWindowSlot(window);
              rows.push({ id: `${group.id}:${window.key || slot}:${rows.length}`, window });
            }
            return rows;
          });
        };
        const windowRows = buildWindowRows(windows);

        const badgeStyle = { background: 'var(--ag-bg-surface)', border: '1px solid var(--ag-glass-border)' };
        const todayImageCount = row.platform === 'openai' ? (row.today_image_count ?? 0) : 0;
        const showImageCount = row.platform === 'openai';
        const accessRequestsText = formatCompact(todayStats?.requests ?? 0, false);
        const accessImageText = formatCompact(todayImageCount, false);
        const accessText = showImageCount ? `${accessRequestsText}/${accessImageText}` : accessRequestsText;
        const hideAccessLabel = showImageCount && accessText.length > '100/100'.length;
        const accessLabel = showImageCount
          ? (
            <span className="inline-flex min-w-0 items-center">
              <span className="truncate">{t('accounts.today_access_count', '访问')}</span>
              <span aria-hidden="true" className="px-px text-text">/</span>
              <span>{t('accounts.image_count_inline_label', '图').trim()}</span>
            </span>
          )
          : t('accounts.today_access_count', '访问');
        const accessValue = showImageCount
          ? (
            <span className="inline-flex min-w-0 items-center justify-end">
              <span>{accessRequestsText}</span>
              <span aria-hidden="true" className="px-px text-text">/</span>
              <span className="text-text">{accessImageText}</span>
            </span>
          )
          : accessText;
        const todayMetricClass = 'ag-account-usage-metric';
        const todayMetricStyle = (color: string, foreground = color) => ({
          background: `color-mix(in srgb, ${color} 10%, transparent)`,
          borderColor: `color-mix(in srgb, ${color} 22%, var(--ag-border))`,
          color: foreground,
        });
        const todayMetricColumnClass = 'ag-account-usage-metrics';
        const todayMetricChips = hasTodayStats && todayStats ? (
          <div
            className={todayMetricColumnClass}
            title={t('accounts.today_stats_tooltip', '今日账号消耗（本地时区自然日）')}
          >
            <span
              className={todayMetricClass}
              style={todayMetricStyle('var(--ag-info)')}
              title={showImageCount ? t('accounts.image_count_tooltip', '今日生图请求数（gpt-image 系列）') : undefined}
            >
              {hideAccessLabel ? null : (
                <span className="ag-account-usage-metric-label text-text-secondary">{accessLabel}</span>
              )}
              <span className={`ag-account-usage-metric-value ${hideAccessLabel ? 'ag-account-usage-metric-value--solo' : ''}`}>{accessValue}</span>
            </span>
            <span className={todayMetricClass} style={todayMetricStyle('var(--ag-primary)')}>
              <span className="ag-account-usage-metric-label text-text-secondary">Token</span>
              <span className="ag-account-usage-metric-value">{formatCompact(todayStats.tokens)}</span>
            </span>
            <span
              className={todayMetricClass}
              style={todayMetricStyle('var(--ag-warning)')}
              title={t('accounts.window_user_cost', '用户消耗（平台计费）')}
            >
              <span className="ag-account-usage-metric-label text-text-secondary">{t('accounts.user_cost_short', '消费')}</span>
              <span className="ag-account-usage-metric-value">
                <span style={{ color: 'var(--ag-warning)' }}>$</span>
                <span className="text-text">{todayStats.user_cost.toFixed(2)}</span>
              </span>
            </span>
            <span
              className={todayMetricClass}
              style={todayMetricStyle('var(--ag-success)', 'var(--ag-success-foreground)')}
              title={t('accounts.window_account_cost', '账号成本（上游计费）')}
            >
              <span className="ag-account-usage-metric-label text-text-secondary">{t('accounts.account_cost_short', '成本')}</span>
              <span className="ag-account-usage-metric-value">
                <span style={{ color: 'var(--ag-success)' }}>$</span>
                <span className="text-text">{todayStats.account_cost.toFixed(2)}</span>
              </span>
            </span>
          </div>
        ) : null;

        return (
          <div
            className={
              canRefresh
                ? 'ag-account-usage-cell ag-account-usage-cell--refreshable'
                : 'ag-account-usage-cell'
            }
            style={{ fontFamily: 'var(--ag-font-mono)' }}
            title={canRefresh ? t('accounts.refresh_usage', '点击刷新用量') : undefined}
            onClick={canRefresh ? handleRefreshClick : undefined}
          >
            <div className={todayMetricChips ? 'ag-account-usage-layout' : 'ag-account-usage-layout ag-account-usage-layout--centered'}>
              <div className="ag-account-usage-windows">
                {windowRows.map((item) => {
                  const w = item.window;
                  if (!w) {
                    return <div key={item.id} className="h-5" aria-hidden="true" />;
                  }
                  const percent = Math.round(w.used_percent);
                  const barPercent = Math.max(0, Math.min(100, percent));
                  const color = usageColor(w.used_percent);
                  const resetText = formatReset(getResetSeconds(w));
                  const { slot } = getWindowSlot(w);
                  const displayLabel = w.display_label?.trim() || slot;
                  return (
                    <div key={item.id} className="ag-account-usage-window-row">
                      <span className="ag-account-usage-window-label text-text-secondary" style={badgeStyle} title={w.label}>
                        {displayLabel}
                      </span>
                      <div className="ag-account-usage-bar" style={{ background: 'var(--ag-glass-border)' }}>
                        <div
                          className="h-full rounded-full"
                          style={{ width: `${barPercent}%`, background: color }}
                        />
                      </div>
                      <span className="ag-account-usage-percent" style={{ color }}>
                        {percent}%
                      </span>
                      <span className="ag-account-usage-reset" title={resetText}>
                        {resetText}
                      </span>
                    </div>
                  );
                })}
                {credits && (
                  <div className="flex h-5 items-center gap-1">
                    <span className="inline-flex items-center justify-center px-1 py-0 rounded text-[10px] font-medium" style={badgeStyle}>
                      $
                    </span>
                    <span style={{ color: credits.unlimited ? 'var(--ag-success)' : credits.balance > 0 ? 'var(--ag-text)' : 'var(--ag-danger)' }}>
                      {credits.unlimited ? '∞' : `$${Number(credits.balance).toFixed(2)}`}
                    </span>
                  </div>
                )}
              </div>
              {todayMetricChips && (
                <>
                  <span aria-hidden="true" />
                  {todayMetricChips}
                </>
              )}
            </div>
          </div>
        );
      },
    },
    {
      key: 'last_used_at',
      title: t('accounts.last_used'),
      width: '120px',
      mobileWidth: '88px',
      align: 'center',
      render: (row) => {
        if (!row.last_used_at) {
          return <span style={{ color: 'var(--ag-text-tertiary)' }}>-</span>;
        }
        const diff = Date.now() - new Date(row.last_used_at).getTime();
        const seconds = Math.floor(diff / 1000);
        const minutes = Math.floor(seconds / 60);
        const hours = Math.floor(minutes / 60);
        const days = Math.floor(hours / 24);
        let relative: string;
        if (seconds < 60) relative = t('accounts.just_now');
        else if (minutes < 60) relative = t('accounts.minutes_ago', { n: minutes });
        else if (hours < 24) relative = t('accounts.hours_ago', { n: hours });
        else relative = t('accounts.days_ago', { n: days });
        return (
          <span className="text-xs" style={{ color: 'var(--ag-text-secondary)' }} title={new Date(row.last_used_at).toLocaleString()}>
            {relative}
          </span>
        );
      },
    },
    {
      key: 'actions',
      title: t('common.actions'),
      width: '116px',
      mobileWidth: '96px',
      align: 'center',
      render: (row) => (
        <AccountRowActions
          row={row}
          labels={accountActionLabels}
          onEdit={onEditAccount}
          onDelete={onDeleteAccount}
          onTest={onTestAccount}
          onStats={onStatsAccount}
          onRefreshQuota={onRefreshQuota}
          onClearCooldowns={onClearRateLimitMarkers}
        />
      ),
    },
  ], [
    accountActionLabels,
    applyQuotaRefreshResult,
    groupMap,
    onClearRateLimitMarkers,
    onDeleteAccount,
    onEditAccount,
    onRefreshQuota,
    onStatsAccount,
    onTestAccount,
    onToggleScheduling,
    platformFilter,
    platformName,
    platformsKey,
    queryClient,
    resetNow,
    t,
    toast,
    usageData?.accounts,
  ]);
}
