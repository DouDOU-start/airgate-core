import { useState, type ReactNode } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Alert, Chip, Skeleton, Tabs } from '@heroui/react';
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  Clock3,
  FolderTree,
  HeartPulse,
  KeyRound,
  ShieldAlert,
  Timer,
} from 'lucide-react';

import { healthMonitorApi, type HealthmonEntity, type HealthmonWindow } from '../../shared/api/healthMonitor';
import { queryKeys } from '../../shared/queryKeys';
import { CommonTable } from '../../shared/components/CommonTable';
import { NativeSwitch } from '../../shared/components/NativeSwitch';
import { RefreshButton } from '../../shared/components/RefreshButton';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { formatDateTime, fmtNum } from '../../shared/utils/format';

type EntityScope = 'group' | 'channel_key';

const LAST_ERROR_COLUMN_WIDTH = 320;

interface SummaryMetric {
  detail: ReactNode;
  icon: ReactNode;
  key: string;
  label: string;
  value: ReactNode;
  valueClass?: string;
}

const WINDOW_OPTIONS: Array<{ id: HealthmonWindow; labelKey: string; shortLabelKey: string }> = [
  { id: '5m', labelKey: 'ops_health.window_5m', shortLabelKey: 'ops_health.window_5m_short' },
  { id: '1h', labelKey: 'ops_health.window_1h', shortLabelKey: 'ops_health.window_1h_short' },
  { id: '6h', labelKey: 'ops_health.window_6h', shortLabelKey: 'ops_health.window_6h_short' },
  { id: '24h', labelKey: 'ops_health.window_24h', shortLabelKey: 'ops_health.window_24h_short' },
];

function pct(rate: number | undefined | null): string {
  if (rate == null || Number.isNaN(rate)) return '—';
  const digits = rate >= 1 ? 0 : rate >= 0.999 ? 2 : 1;
  return `${(rate * 100).toFixed(digits)}%`;
}

function ms(value: number | undefined | null): string {
  if (value == null || value <= 0) return '—';
  if (value < 1000) return `${Math.round(value)}ms`;
  const seconds = value / 1000;
  return `${seconds.toFixed(seconds >= 10 ? 1 : 2)}s`;
}

function scoreTone(score: number | null | undefined, idle: boolean, lowSample: boolean): string {
  if (idle) return 'text-text';
  if (lowSample) return 'text-warning';
  if (score == null) return 'text-text-tertiary';
  if (score >= 90) return 'text-success';
  if (score >= 70) return 'text-warning';
  return 'text-danger';
}

function entityBadge(entity: HealthmonEntity, t: (key: string, opts?: Record<string, unknown>) => string) {
  if (entity.sample.idle) {
    return <Chip color="default" size="sm" variant="soft">{t('ops_health.idle')}</Chip>;
  }
  if (entity.sample.low_sample) {
    return <Chip color="warning" size="sm" variant="soft">{t('ops_health.low_sample')}</Chip>;
  }
  const color = entity.error_rate >= 0.05 ? 'danger' : entity.error_rate > 0 ? 'warning' : 'success';
  return (
    <Chip color={color} size="sm" variant="soft">
      {pct(entity.success_rate)}
    </Chip>
  );
}

function schedulerStatusLabel(
  status: string,
  t: (key: string, opts?: Record<string, unknown>) => string,
): string {
  if (status === 'enabled') return t('status.enabled');
  if (status === 'disabled') return t('status.disabled');
  return status;
}

function OverviewSkeleton() {
  return (
    <div className="grid gap-3 xl:grid-cols-[minmax(250px,0.85fr)_minmax(0,3fr)]">
      <div className="min-h-52 rounded-[var(--radius)] border border-border bg-surface p-5 shadow-sm">
        <div className="flex items-center justify-between gap-4">
          <Skeleton className="h-8 w-32" />
          <Skeleton className="h-6 w-16" />
        </div>
        <Skeleton className="mt-7 h-9 w-28" />
        <Skeleton className="mt-3 h-4 w-44" />
        <Skeleton className="mt-7 h-11 w-full" />
      </div>
      <div className="grid grid-cols-2 gap-px overflow-hidden rounded-[var(--radius)] border border-border bg-border shadow-sm md:grid-cols-3">
        {Array.from({ length: 6 }).map((_, index) => (
          <div className="min-h-24 bg-surface p-4" key={index}>
            <Skeleton className="h-4 w-24" />
            <Skeleton className="mt-3 h-6 w-20" />
            <Skeleton className="mt-2 h-3 w-32 max-w-full" />
          </div>
        ))}
      </div>
    </div>
  );
}

function SummaryMetricCell({ metric }: { metric: SummaryMetric }) {
  return (
    <div className="flex min-h-24 min-w-0 flex-col justify-center bg-surface px-4 py-3.5 2xl:min-h-28 2xl:px-5">
      <div className="flex min-w-0 items-center gap-2 text-xs text-text-tertiary">
        <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-[var(--ag-radius-sm)] bg-surface-secondary">
          {metric.icon}
        </span>
        <span className="truncate">{metric.label}</span>
      </div>
      <div className={`mt-2 truncate font-mono text-xl font-semibold leading-none tabular-nums ${metric.valueClass ?? 'text-text'}`}>
        {metric.value}
      </div>
      <div className="mt-1.5 truncate text-[11px] text-text-tertiary" title={typeof metric.detail === 'string' ? metric.detail : undefined}>
        {metric.detail}
      </div>
    </div>
  );
}

function EntityTable({
  scope,
  entities,
  loading,
  onlyUnhealthy,
}: {
  scope: EntityScope;
  entities: HealthmonEntity[];
  loading: boolean;
  onlyUnhealthy: boolean;
}) {
  const { t } = useTranslation();
  const isGroup = scope === 'group';
  // group: name + metrics + last_error; key: name + channel + status + metrics + last_error
  const colSpan = isGroup ? 8 : 10;

  return (
    <CommonTable
      ariaLabel={isGroup ? t('ops_health.group_table') : t('ops_health.entity_table')}
      className="ag-ops-health-table"
      contentStyle={{ tableLayout: 'fixed' }}
      mobileLayout="cards"
      minWidth={isGroup ? 1120 : 1380}
    >
      <CommonTable.Header>
        <CommonTable.Column id="name">
          {isGroup ? t('ops_health.col_group') : t('ops_health.col_key')}
        </CommonTable.Column>
        {!isGroup ? (
          <CommonTable.Column id="channel" style={{ width: 140 }}>
            {t('ops_health.col_channel')}
          </CommonTable.Column>
        ) : null}
        {!isGroup ? (
          <CommonTable.Column id="status" style={{ width: 120 }}>{t('ops_health.col_status')}</CommonTable.Column>
        ) : null}
        <CommonTable.Column id="requests" style={{ width: 100 }}>
          {t('ops_health.col_requests')}
        </CommonTable.Column>
        <CommonTable.Column id="traffic" style={{ width: 110 }}>
          {t('ops_health.col_success')}
        </CommonTable.Column>
        <CommonTable.Column id="error" style={{ width: 90 }}>
          {t('ops_health.col_error')}
        </CommonTable.Column>
        <CommonTable.Column id="ttft_avg" style={{ width: 110 }}>
          {t('ops_health.ttft_avg')}
        </CommonTable.Column>
        <CommonTable.Column id="dur_avg" style={{ width: 110 }}>
          {t('ops_health.duration_avg')}
        </CommonTable.Column>
        <CommonTable.Column id="score" style={{ width: 90 }}>
          {t('ops_health.health_score')}
        </CommonTable.Column>
        <CommonTable.Column id="last" style={{ width: LAST_ERROR_COLUMN_WIDTH }}>
          {t('ops_health.col_last_error')}
        </CommonTable.Column>
      </CommonTable.Header>
      <CommonTable.Body>
        {loading && entities.length === 0 ? (
          <TableLoadingRow colSpan={colSpan} />
        ) : entities.length === 0 ? (
          <CommonTable.Row id="empty">
            <CommonTable.Cell colSpan={colSpan}>
              <div className="empty-state flex-col gap-2 px-4 py-12 text-center text-sm text-text-tertiary">
                <Activity className="h-5 w-5" />
                <span>{onlyUnhealthy ? t('ops_health.empty_unhealthy') : t('ops_health.empty')}</span>
              </div>
            </CommonTable.Cell>
          </CommonTable.Row>
        ) : (
          entities.map((entity) => (
            <CommonTable.Row id={`${scope}-${entity.id}`} key={`${scope}-${entity.id}`}>
              <CommonTable.Cell>
                <div className="min-w-0">
                  <div className="truncate font-medium text-text" title={entity.name || `#${entity.id}`}>
                    {entity.name || (isGroup
                      ? t('ops_health.unnamed_group', { id: entity.id })
                      : t('ops_health.unnamed_key', { id: entity.id }))}
                  </div>
                  {!isGroup && entity.type ? (
                    <div className="mt-0.5 text-[11px] text-text-tertiary">{entity.type}</div>
                  ) : null}
                </div>
              </CommonTable.Cell>
              {!isGroup ? (
                <CommonTable.Cell>
                  <span
                    className="block truncate text-sm text-text-secondary"
                    title={entity.channel_name}
                  >
                    {entity.channel_name || (entity.channel_id ? `ch#${entity.channel_id}` : '—')}
                  </span>
                </CommonTable.Cell>
              ) : null}
              {!isGroup ? (
                <CommonTable.Cell>
                  <div className="flex flex-col items-center gap-1">
                    {entity.sched_status ? (
                      <Chip color={entity.sched_status === 'enabled' ? 'success' : 'default'} size="sm" variant="soft">
                        {schedulerStatusLabel(entity.sched_status, t)}
                      </Chip>
                    ) : null}
                    {entity.health_status && entity.health_status !== 'healthy' ? (
                      <Chip color="warning" size="sm" variant="soft">{entity.health_status}</Chip>
                    ) : null}
                  </div>
                </CommonTable.Cell>
              ) : null}
              <CommonTable.Cell>
                <span className="font-mono text-xs tabular-nums">{fmtNum(entity.sample.n)}</span>
              </CommonTable.Cell>
              <CommonTable.Cell>{entityBadge(entity, t)}</CommonTable.Cell>
              <CommonTable.Cell>
                <span className={`font-mono text-xs tabular-nums ${entity.error_rate >= 0.05 ? 'text-danger' : ''}`}>
                  {entity.sample.idle ? '—' : pct(entity.error_rate)}
                </span>
              </CommonTable.Cell>
              <CommonTable.Cell>
                <div className="font-mono text-xs tabular-nums leading-tight">
                  <div>{entity.sample.idle ? '—' : ms(entity.ttft.avg_ms)}</div>
                  <div className="mt-0.5 text-[10px] text-text-tertiary">
                    {t('ops_health.p95')} {entity.sample.idle ? '—' : ms(entity.ttft.p95_ms)}
                  </div>
                </div>
              </CommonTable.Cell>
              <CommonTable.Cell>
                <div className="font-mono text-xs tabular-nums leading-tight">
                  <div>{entity.sample.idle ? '—' : ms(entity.latency.avg_ms)}</div>
                  <div className="mt-0.5 text-[10px] text-text-tertiary">
                    {t('ops_health.p95')} {entity.sample.idle ? '—' : ms(entity.latency.p95_ms)}
                  </div>
                </div>
              </CommonTable.Cell>
              <CommonTable.Cell>
                <span className={`font-mono text-xs tabular-nums ${scoreTone(entity.health_score, entity.sample.idle, entity.sample.low_sample)}`}>
                  {entity.sample.idle
                    ? t('ops_health.idle')
                    : entity.sample.low_sample
                      ? t('ops_health.low_sample')
                      : (entity.health_score ?? '—')}
                </span>
              </CommonTable.Cell>
              <CommonTable.Cell
                className="overflow-hidden"
                style={{ width: LAST_ERROR_COLUMN_WIDTH, maxWidth: LAST_ERROR_COLUMN_WIDTH }}
              >
                {entity.last_error ? (
                  <div className="mx-auto w-full min-w-0 max-w-[20rem] overflow-hidden">
                    <div className="block truncate text-xs text-text-secondary" title={entity.last_error.message}>
                      {entity.last_error.message || entity.last_error.phase || '—'}
                    </div>
                    <div className="mt-0.5 whitespace-nowrap text-[11px] text-text-tertiary">
                      {formatDateTime(entity.last_error.at)}
                    </div>
                  </div>
                ) : (
                  <span className="text-xs text-text-tertiary">—</span>
                )}
              </CommonTable.Cell>
            </CommonTable.Row>
          ))
        )}
      </CommonTable.Body>
    </CommonTable>
  );
}

export default function OpsHealthPage() {
  const { t } = useTranslation();
  const [window, setWindow] = useState<HealthmonWindow>('1h');
  const [onlyUnhealthy, setOnlyUnhealthy] = useState(false);
  const [scope, setScope] = useState<EntityScope>('group');

  const overviewQuery = useQuery({
    queryKey: queryKeys.healthMonitorOverview(window),
    queryFn: () => healthMonitorApi.overview({ window }),
    refetchInterval: 60_000,
    placeholderData: keepPreviousData,
  });

  const entitiesQuery = useQuery({
    queryKey: queryKeys.healthMonitorEntities(window, scope, onlyUnhealthy),
    queryFn: () => healthMonitorApi.entities({
      window,
      scope,
      only_unhealthy: onlyUnhealthy || undefined,
    }),
    refetchInterval: 60_000,
    placeholderData: keepPreviousData,
  });

  const overview = overviewQuery.data;
  const entities = entitiesQuery.data ?? [];
  const selectedWindowLabel = WINDOW_OPTIONS.find((item) => item.id === window)?.labelKey ?? 'ops_health.window_1h';

  const status = overview
    ? overview.sample.idle
      ? { color: 'default' as const, label: t('ops_health.idle') }
      : overview.sample.low_sample
        ? { color: 'warning' as const, label: t('ops_health.low_sample') }
        : (overview.health_score ?? 0) >= 90
          ? { color: 'success' as const, label: t('ops_health.healthy') }
          : (overview.health_score ?? 0) >= 70
            ? { color: 'warning' as const, label: t('ops_health.needs_attention') }
            : { color: 'danger' as const, label: t('ops_health.unhealthy') }
    : null;

  const summaryMetrics: SummaryMetric[] = overview ? [
    {
      key: 'requests',
      icon: <Activity className="h-3.5 w-3.5 text-primary" />,
      label: t('ops_health.requests'),
      value: fmtNum(overview.sample.n),
      detail: t('ops_health.request_breakdown', { success: overview.sample.s, errors: overview.sample.e }),
    },
    {
      key: 'success',
      icon: <CheckCircle2 className="h-3.5 w-3.5 text-success" />,
      label: t('ops_health.success_rate'),
      value: overview.sample.idle ? '—' : pct(overview.success_rate),
      valueClass: overview.sample.idle ? 'text-text-tertiary' : 'text-success',
      detail: t('ops_health.sample_summary', { success: overview.sample.s, total: overview.sample.n }),
    },
    {
      key: 'error',
      icon: <AlertTriangle className={`h-3.5 w-3.5 ${overview.sample.e > 0 ? 'text-danger' : 'text-text-tertiary'}`} />,
      label: t('ops_health.error_rate_short'),
      value: overview.sample.idle ? '—' : pct(overview.error_rate),
      valueClass: overview.sample.idle
        ? 'text-text-tertiary'
        : overview.error_rate >= 0.05
          ? 'text-danger'
          : overview.error_rate > 0
            ? 'text-warning'
            : 'text-success',
      detail: t('ops_health.error_breakdown', {
        auth: overview.counts.auth,
        rateLimit: overview.counts.rate_limit,
        upstream5xx: overview.counts.upstream_5xx,
      }),
    },
    {
      key: 'ttft',
      icon: <Timer className="h-3.5 w-3.5 text-info" />,
      label: t('ops_health.ttft_avg'),
      value: overview.sample.idle ? '—' : ms(overview.ttft.avg_ms),
      detail: `${t('ops_health.p95')} ${overview.sample.idle ? '—' : ms(overview.ttft.p95_ms)}`,
    },
    {
      key: 'duration',
      icon: <Clock3 className="h-3.5 w-3.5 text-[var(--ag-tone-violet)]" />,
      label: t('ops_health.duration_avg'),
      value: overview.sample.idle ? '—' : ms(overview.latency.avg_ms),
      detail: `${t('ops_health.p95')} ${overview.sample.idle ? '—' : ms(overview.latency.p95_ms)}`,
    },
    {
      key: 'avail',
      icon: <KeyRound className="h-3.5 w-3.5 text-warning" />,
      label: t('ops_health.available_keys_short'),
      value: `${overview.availability.channel_keys_available}/${overview.availability.channel_keys_total}`,
      valueClass: overview.availability.channel_keys_available < overview.availability.channel_keys_total
        ? 'text-warning'
        : 'text-text',
      detail: t('ops_health.sched_status_hint'),
    },
  ] : [];

  const refresh = () => {
    void overviewQuery.refetch();
    void entitiesQuery.refetch();
  };

  return (
    <div className="space-y-5 2xl:space-y-6">
      <div className="flex min-w-0 items-center justify-end gap-2">
          <Tabs
            className="ag-ops-health-window-tabs ag-segmented-tabs ag-segmented-tabs-compact min-w-0"
            selectedKey={window}
            onSelectionChange={(key) => {
              if (key == null) return;
              setWindow(String(key) as HealthmonWindow);
            }}
          >
            <Tabs.List aria-label={t('ops_health.window')}>
              {WINDOW_OPTIONS.map((item, index) => (
                <Tabs.Tab id={item.id} key={item.id}>
                  {index > 0 ? <Tabs.Separator /> : null}
                  <Tabs.Indicator />
                  <span>{t(item.shortLabelKey)}</span>
                </Tabs.Tab>
              ))}
            </Tabs.List>
          </Tabs>
          <RefreshButton
            ariaLabel={t('common.refresh')}
            isRefreshing={overviewQuery.isFetching || entitiesQuery.isFetching}
            onRefresh={refresh}
          />
      </div>

      {overviewQuery.error || entitiesQuery.error ? (
        <Alert status="danger">
          {t('ops_health.load_failed', {
            error: (overviewQuery.error ?? entitiesQuery.error) instanceof Error
              ? (overviewQuery.error ?? entitiesQuery.error as Error).message
              : '',
          })}
        </Alert>
      ) : null}

      {overviewQuery.isLoading && !overview ? (
        <OverviewSkeleton />
      ) : overview ? (
        <div className="grid gap-3 xl:grid-cols-[minmax(250px,0.85fr)_minmax(0,3fr)]">
          <section className="flex min-h-52 flex-col rounded-[var(--radius)] border border-border bg-surface p-4 shadow-sm 2xl:p-5" aria-label={t('ops_health.health_score')}>
            <div className="flex items-center justify-between gap-3">
              <div className="flex min-w-0 items-center gap-2.5">
                <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-[var(--field-radius)] bg-primary-subtle text-primary">
                  <HeartPulse className="h-4 w-4" />
                </span>
                <span className="truncate text-sm font-medium text-text">{t('ops_health.health_score')}</span>
              </div>
              {status ? <Chip color={status.color} size="sm" variant="soft">{status.label}</Chip> : null}
            </div>

            <div className="mt-5">
              <div className={`flex min-w-0 items-baseline gap-1.5 ${scoreTone(overview.health_score, overview.sample.idle, overview.sample.low_sample)}`}>
                <span className="truncate text-3xl font-semibold leading-none tabular-nums">
                  {overview.sample.idle
                    ? t('ops_health.no_traffic')
                    : overview.sample.low_sample
                      ? t('ops_health.low_sample')
                      : (overview.health_score ?? '—')}
                </span>
                {!overview.sample.idle && !overview.sample.low_sample && overview.health_score != null ? (
                  <span className="text-xs font-normal text-text-tertiary">/ 100</span>
                ) : null}
              </div>
              <p className="mt-2 truncate text-xs text-text-tertiary">
                {overview.sample.idle
                  ? t('ops_health.no_requests_in_window', { window: t(selectedWindowLabel) })
                  : t('ops_health.request_breakdown', { success: overview.sample.s, errors: overview.sample.e })}
              </p>
            </div>

            <div className="mt-auto flex items-center gap-3 border-t border-separator pt-3">
              <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-[var(--ag-radius-sm)] bg-surface-secondary text-text-secondary">
                <ShieldAlert className="h-3.5 w-3.5" />
              </span>
              <div className="min-w-0">
                <div className="flex items-baseline gap-2">
                  <span className="text-xs text-text-tertiary">{t('ops_health.client_errors')}</span>
                  <span className="font-mono text-sm font-semibold tabular-nums text-text">{overview.counts.client}</span>
                </div>
                <div className="truncate text-[11px] text-text-tertiary">
                  {t('ops_health.client_breakdown', {
                    canceled: overview.counts.canceled,
                    precheck: overview.counts.precheck,
                  })}
                </div>
              </div>
            </div>
          </section>

          <section className="grid grid-cols-2 gap-px overflow-hidden rounded-[var(--radius)] border border-border bg-border shadow-sm md:grid-cols-3" aria-label={t('ops_health.metrics')}>
            {summaryMetrics.map((metric) => <SummaryMetricCell key={metric.key} metric={metric} />)}
          </section>
        </div>
      ) : null}

      <section className="space-y-3">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex min-w-0 items-center gap-2.5">
            <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-[var(--ag-radius-sm)] border border-border bg-surface text-text-secondary shadow-sm">
              {scope === 'group' ? <FolderTree className="h-4 w-4" /> : <KeyRound className="h-4 w-4" />}
            </span>
            <div className="min-w-0">
              <h2 className="truncate text-sm font-semibold text-text">
                {scope === 'group' ? t('ops_health.group_table') : t('ops_health.entity_table')}
              </h2>
              <p className="text-xs text-text-tertiary">{t('ops_health.entity_count', { count: entities.length })}</p>
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-3">
            <NativeSwitch
              ariaLabel={t('ops_health.only_unhealthy')}
              contentClassName="text-xs text-text-secondary"
              isSelected={onlyUnhealthy}
              label={t('ops_health.only_unhealthy')}
              onChange={setOnlyUnhealthy}
            />
            <Tabs
              className="ag-segmented-tabs ag-segmented-tabs-compact"
              selectedKey={scope}
              onSelectionChange={(key) => {
                if (key == null) return;
                setScope(String(key) as EntityScope);
              }}
            >
              <Tabs.List aria-label={t('ops_health.scope')}>
                <Tabs.Tab id="group">
                  <Tabs.Indicator />
                  <span className="inline-flex items-center gap-1">
                    <FolderTree className="h-3.5 w-3.5" />
                    {t('ops_health.tab_groups')}
                  </span>
                </Tabs.Tab>
                <Tabs.Tab id="channel_key">
                  <Tabs.Separator />
                  <Tabs.Indicator />
                  <span className="inline-flex items-center gap-1">
                    <KeyRound className="h-3.5 w-3.5" />
                    {t('ops_health.tab_keys')}
                  </span>
                </Tabs.Tab>
              </Tabs.List>
            </Tabs>
          </div>
        </div>

        <EntityTable
          scope={scope}
          entities={entities}
          loading={entitiesQuery.isLoading}
          onlyUnhealthy={onlyUnhealthy}
        />
      </section>
    </div>
  );
}
