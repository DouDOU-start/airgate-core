import { useState, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Alert, Chip, Skeleton, Tabs } from '@heroui/react';
import { useTranslation } from 'react-i18next';
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  Clock3,
  Layers,
  RadioTower,
  Timer,
} from 'lucide-react';

import {
  channelStatusApi,
  type ChannelHealthStatus,
  type ChannelStatusGroup,
  type ChannelStatusWindow,
} from '../../shared/api/channelStatus';
import { RefreshButton } from '../../shared/components/RefreshButton';
import { queryKeys } from '../../shared/queryKeys';
import { formatDateTime } from '../../shared/utils/format';

const WINDOW_OPTIONS: Array<{ id: ChannelStatusWindow; labelKey: string }> = [
  { id: '5m', labelKey: 'channel_status.window_5m' },
  { id: '1h', labelKey: 'channel_status.window_1h' },
  { id: '6h', labelKey: 'channel_status.window_6h' },
  { id: '24h', labelKey: 'channel_status.window_24h' },
];

interface StatusMeta {
  chip: 'default' | 'success' | 'warning' | 'danger';
  dotClass: string;
  labelKey: string;
  textClass: string;
}

const STATUS_META: Record<ChannelHealthStatus, StatusMeta> = {
  idle: {
    chip: 'default',
    dotClass: 'bg-text-tertiary',
    labelKey: 'channel_status.idle',
    textClass: 'text-text-secondary',
  },
  low_sample: {
    chip: 'warning',
    dotClass: 'bg-warning',
    labelKey: 'channel_status.low_sample',
    textClass: 'text-warning',
  },
  healthy: {
    chip: 'success',
    dotClass: 'bg-success',
    labelKey: 'channel_status.healthy',
    textClass: 'text-success',
  },
  degraded: {
    chip: 'warning',
    dotClass: 'bg-warning',
    labelKey: 'channel_status.degraded',
    textClass: 'text-warning',
  },
  unhealthy: {
    chip: 'danger',
    dotClass: 'bg-danger',
    labelKey: 'channel_status.unhealthy',
    textClass: 'text-danger',
  },
};

function pct(value: number | null | undefined): string {
  if (value == null || Number.isNaN(value)) return '—';
  const digits = value >= 1 ? 0 : value >= 0.999 ? 2 : 1;
  return `${(value * 100).toFixed(digits)}%`;
}

function duration(value: number | null | undefined): string {
  if (value == null || value <= 0) return '—';
  if (value < 1000) return `${Math.round(value)}ms`;
  const seconds = value / 1000;
  return `${seconds.toFixed(seconds >= 10 ? 1 : 2)}s`;
}

function resolveOverviewStatus(
  idle: boolean,
  lowSample: boolean,
  score: number | null,
): ChannelHealthStatus {
  if (idle) return 'idle';
  if (lowSample) return 'low_sample';
  if ((score ?? 0) >= 90) return 'healthy';
  if ((score ?? 0) >= 70) return 'degraded';
  return 'unhealthy';
}

function StatusChip({ status }: { status: ChannelHealthStatus }) {
  const { t } = useTranslation();
  const meta = STATUS_META[status];
  return (
    <Chip color={meta.chip} size="sm" variant="soft">
      <span className="inline-flex items-center gap-1.5">
        <span className={`h-1.5 w-1.5 rounded-full ${meta.dotClass}`} />
        {t(meta.labelKey)}
      </span>
    </Chip>
  );
}

function MetricCell({
  detail,
  icon,
  label,
  value,
  valueClass = 'text-text',
}: {
  detail: string;
  icon: ReactNode;
  label: string;
  value: string;
  valueClass?: string;
}) {
  return (
    <div className="flex min-h-28 min-w-0 flex-col justify-center bg-surface px-4 py-3.5 2xl:px-5">
      <div className="flex min-w-0 items-center gap-2 text-xs text-text-tertiary">
        {icon}
        <span className="truncate">{label}</span>
      </div>
      <div className={`mt-2 truncate font-mono text-xl font-semibold tabular-nums ${valueClass}`}>{value}</div>
      <p className="mt-1 truncate text-[11px] text-text-tertiary" title={detail}>{detail}</p>
    </div>
  );
}

function OverviewSkeleton() {
  return (
    <div className="grid grid-cols-2 gap-px overflow-hidden rounded-[var(--radius)] border border-border bg-border shadow-sm xl:grid-cols-5">
      {Array.from({ length: 5 }, (_, index) => (
        <div className={`min-h-28 bg-surface p-4 ${index === 0 ? 'col-span-2 xl:col-span-1' : ''}`} key={index}>
          <Skeleton className="h-4 w-24 rounded" />
          <Skeleton className="mt-4 h-7 w-20 rounded" />
          <Skeleton className="mt-2 h-3 w-32 rounded" />
        </div>
      ))}
    </div>
  );
}

function GroupStatusRow({ group }: { group: ChannelStatusGroup }) {
  const { t } = useTranslation();
  const status = group.status || resolveOverviewStatus(
    group.sample.idle,
    group.sample.low_sample,
    group.health_score,
  );
  const meta = STATUS_META[status];
  const successWidth = group.sample.idle
    ? 0
    : Math.max(0, Math.min(100, group.success_rate * 100));

  return (
    <div className="grid min-h-[76px] grid-cols-2 items-center gap-x-4 gap-y-3 border-t border-separator px-4 py-3 first:border-t-0 md:grid-cols-[minmax(220px,1.4fr)_minmax(180px,1fr)_110px_110px_110px] 2xl:px-5">
      <div className="col-span-2 flex min-w-0 items-center justify-between gap-3 md:col-span-1 md:justify-center">
        <div className="flex min-w-0 items-center gap-3">
          <span
            className="inline-flex h-[26px] w-[26px] shrink-0 items-center justify-center rounded-lg border border-border bg-surface-secondary text-text-secondary"
            aria-hidden
          >
            <Layers className="h-4 w-4" />
          </span>
          <div className="min-w-0 truncate text-sm font-medium text-text" title={group.name}>
            {group.name}
          </div>
        </div>
        <span className="md:hidden"><StatusChip status={status} /></span>
      </div>

      <div className="col-span-2 flex min-w-0 items-center gap-3 md:col-span-1">
        <div className="h-2 min-w-0 flex-1 overflow-hidden rounded-full bg-surface-secondary ring-1 ring-inset ring-border">
          <div
            className={`h-full rounded-full ${meta.dotClass}`}
            style={{ width: `${successWidth}%` }}
          />
        </div>
        <span className={`w-12 shrink-0 text-right font-mono text-xs tabular-nums ${meta.textClass}`}>
          {group.sample.idle ? '—' : pct(group.success_rate)}
        </span>
      </div>

      <div className="min-w-0 md:text-center">
        <div className="text-[10px] text-text-tertiary md:hidden">{t('channel_status.avg_ttft')}</div>
        <div className="mt-0.5 font-mono text-xs tabular-nums text-text md:mt-0">
          {group.sample.idle ? '—' : duration(group.ttft.avg_ms)}
        </div>
      </div>

      <div className="min-w-0 text-right md:text-center">
        <div className="text-[10px] text-text-tertiary md:hidden">{t('channel_status.avg_duration')}</div>
        <div className="mt-0.5 font-mono text-xs tabular-nums text-text md:mt-0">
          {group.sample.idle ? '—' : duration(group.latency.avg_ms)}
        </div>
      </div>

      <div className="hidden md:flex md:justify-center"><StatusChip status={status} /></div>
    </div>
  );
}

function GroupRowsSkeleton() {
  return (
    <div className="divide-y divide-separator">
      {Array.from({ length: 3 }, (_, index) => (
        <div className="grid min-h-[76px] grid-cols-2 items-center gap-x-4 gap-y-3 px-4 py-3 md:grid-cols-[minmax(220px,1.4fr)_minmax(180px,1fr)_110px_110px_110px]" key={index}>
          <Skeleton className="col-span-2 h-8 w-44 max-w-full rounded md:col-span-1 md:justify-self-center" />
          <Skeleton className="col-span-2 h-2 w-full rounded-full md:col-span-1" />
          <Skeleton className="h-4 w-14 rounded md:justify-self-center" />
          <Skeleton className="h-4 w-14 justify-self-end rounded md:justify-self-center" />
          <Skeleton className="hidden h-6 w-16 justify-self-center rounded-full md:block" />
        </div>
      ))}
    </div>
  );
}

export default function ChannelStatusPage() {
  const { t } = useTranslation();
  const [window, setWindow] = useState<ChannelStatusWindow>('1h');

  const overviewQuery = useQuery({
    queryKey: queryKeys.channelStatusOverview(window),
    queryFn: () => channelStatusApi.overview({ window }),
    refetchInterval: 60_000,
  });
  const groupsQuery = useQuery({
    queryKey: queryKeys.channelStatusGroups(window),
    queryFn: () => channelStatusApi.groups({ window }),
    refetchInterval: 60_000,
  });

  const overview = overviewQuery.data;
  const groups = groupsQuery.data ?? [];
  const overviewStatus = overview
    ? resolveOverviewStatus(overview.sample.idle, overview.sample.low_sample, overview.health_score)
    : null;
  const overviewMeta = overviewStatus ? STATUS_META[overviewStatus] : null;
  const selectedWindowLabel = WINDOW_OPTIONS.find((item) => item.id === window)?.labelKey
    ?? 'channel_status.window_1h';

  const refresh = () => {
    void overviewQuery.refetch();
    void groupsQuery.refetch();
  };

  return (
    <div className="space-y-5 2xl:space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <p className="min-w-0 truncate text-xs text-text-tertiary">
          {overview?.updated_at
            ? t('channel_status.updated_at', { time: formatDateTime(overview.updated_at) })
            : t('channel_status.updating')}
        </p>

        <div className="flex w-full min-w-0 flex-wrap items-center justify-end gap-2 sm:w-auto sm:flex-nowrap">
          <Tabs
            className="ag-ops-health-window-tabs ag-segmented-tabs ag-segmented-tabs-compact min-w-0 max-w-full flex-1 sm:flex-none"
            selectedKey={window}
            onSelectionChange={(key) => {
              if (key != null) setWindow(String(key) as ChannelStatusWindow);
            }}
          >
            <Tabs.List aria-label={t('channel_status.window')}>
              {WINDOW_OPTIONS.map((item, index) => (
                <Tabs.Tab id={item.id} key={item.id}>
                  {index > 0 ? <Tabs.Separator /> : null}
                  <Tabs.Indicator />
                  <span>{t(item.labelKey)}</span>
                </Tabs.Tab>
              ))}
            </Tabs.List>
          </Tabs>
          <RefreshButton
            ariaLabel={t('common.refresh')}
            isRefreshing={overviewQuery.isFetching || groupsQuery.isFetching}
            onRefresh={refresh}
          />
        </div>
      </div>

      {overviewQuery.error || groupsQuery.error ? (
        <Alert status="danger">
          {t('channel_status.load_failed', {
            error: (overviewQuery.error ?? groupsQuery.error) instanceof Error
              ? (overviewQuery.error ?? groupsQuery.error as Error).message
              : '',
          })}
        </Alert>
      ) : null}

      {overviewQuery.isLoading && !overview ? (
        <OverviewSkeleton />
      ) : overview && overviewStatus && overviewMeta ? (
        <section
          aria-label={t('channel_status.overview')}
          className="grid grid-cols-2 gap-px overflow-hidden rounded-[var(--radius)] border border-border bg-border shadow-sm xl:grid-cols-5"
        >
          <div className="col-span-2 flex min-h-28 min-w-0 flex-col justify-center bg-surface px-4 py-3.5 xl:col-span-1 2xl:px-5">
            <div className="flex items-center gap-2 text-xs text-text-tertiary">
              <RadioTower className="h-3.5 w-3.5 text-primary" />
              <span>{t('channel_status.overall_status')}</span>
            </div>
            <div className={`mt-2 flex min-w-0 items-baseline gap-2 ${overviewMeta.textClass}`}>
              <span className="truncate text-xl font-semibold">{t(overviewMeta.labelKey)}</span>
              {!overview.sample.idle && !overview.sample.low_sample && overview.health_score != null ? (
                <span className="font-mono text-xs tabular-nums text-text-tertiary">{overview.health_score}/100</span>
              ) : null}
            </div>
            <p className="mt-1 truncate text-[11px] text-text-tertiary">
              {overview.sample.idle
                ? t('channel_status.no_requests', { window: t(selectedWindowLabel) })
                : t('channel_status.selected_window', { window: t(selectedWindowLabel) })}
            </p>
          </div>

          <MetricCell
            detail={t('channel_status.selected_window', { window: t(selectedWindowLabel) })}
            icon={<CheckCircle2 className="h-3.5 w-3.5 text-success" />}
            label={t('channel_status.success_rate')}
            value={overview.sample.idle ? '—' : pct(overview.success_rate)}
            valueClass={overview.sample.idle ? 'text-text-tertiary' : 'text-success'}
          />
          <MetricCell
            detail={t('channel_status.selected_window', { window: t(selectedWindowLabel) })}
            icon={<AlertTriangle className={`h-3.5 w-3.5 ${overview.error_rate > 0 ? 'text-danger' : 'text-text-tertiary'}`} />}
            label={t('channel_status.error_rate')}
            value={overview.sample.idle ? '—' : pct(overview.error_rate)}
            valueClass={overview.sample.idle
              ? 'text-text-tertiary'
              : overview.error_rate > 0.05
                ? 'text-danger'
                : overview.error_rate > 0
                  ? 'text-warning'
                  : 'text-success'}
          />
          <MetricCell
            detail={t('channel_status.success_samples_only')}
            icon={<Timer className="h-3.5 w-3.5 text-info" />}
            label={t('channel_status.avg_ttft')}
            value={overview.sample.idle ? '—' : duration(overview.ttft.avg_ms)}
          />
          <MetricCell
            detail={t('channel_status.success_samples_only')}
            icon={<Clock3 className="h-3.5 w-3.5 text-[var(--ag-tone-violet)]" />}
            label={t('channel_status.avg_duration')}
            value={overview.sample.idle ? '—' : duration(overview.latency.avg_ms)}
          />
        </section>
      ) : null}

      <section className="space-y-3" aria-label={t('channel_status.group_status')}>
        <div className="flex min-w-0 items-center justify-between gap-3">
          <div className="min-w-0">
            <h2 className="truncate text-sm font-semibold text-text">{t('channel_status.group_status')}</h2>
            <p className="mt-0.5 text-xs text-text-tertiary">{t('channel_status.group_count', { count: groups.length })}</p>
          </div>
          <div className="hidden flex-wrap items-center justify-end gap-3 text-[11px] text-text-tertiary sm:flex">
            {(['healthy', 'degraded', 'unhealthy', 'low_sample', 'idle'] as ChannelHealthStatus[]).map((status) => (
              <span className="inline-flex items-center gap-1.5" key={status}>
                <span className={`h-2 w-2 rounded-full ${STATUS_META[status].dotClass}`} />
                {t(STATUS_META[status].labelKey)}
              </span>
            ))}
          </div>
        </div>

        <div className="overflow-hidden rounded-[var(--radius)] border border-border bg-surface shadow-sm">
          <div className="hidden min-h-9 grid-cols-[minmax(220px,1.4fr)_minmax(180px,1fr)_110px_110px_110px] items-center gap-4 bg-surface-secondary px-4 text-[11px] font-medium text-text-tertiary md:grid 2xl:px-5">
            <span className="text-center">{t('channel_status.group')}</span>
            <span className="text-center">{t('channel_status.success_rate')}</span>
            <span className="text-center">{t('channel_status.avg_ttft')}</span>
            <span className="text-center">{t('channel_status.avg_duration')}</span>
            <span className="text-center">{t('channel_status.status')}</span>
          </div>

          {groupsQuery.isLoading && groups.length === 0 ? (
            <GroupRowsSkeleton />
          ) : groups.length > 0 ? (
            groups.map((group) => <GroupStatusRow group={group} key={group.id} />)
          ) : (
            <div className="flex min-h-40 flex-col items-center justify-center gap-2 px-4 py-8 text-center text-sm text-text-tertiary">
              <Activity className="h-5 w-5" />
              <span>{t('channel_status.empty')}</span>
            </div>
          )}
        </div>
      </section>
    </div>
  );
}
