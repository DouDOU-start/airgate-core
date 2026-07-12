import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Modal, Skeleton, Tabs, useOverlayState } from '@heroui/react';
import { BarChart3, PieChart as PieChartIcon } from 'lucide-react';
import {
  Bar,
  CartesianGrid,
  ComposedChart,
  Line,
  ResponsiveContainer,
  Tooltip as RechartsTooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { dashboardApi } from '../../../shared/api/dashboard';
import { queryKeys } from '../../../shared/queryKeys';
import { ChartEmptyState } from '../../../shared/components/ChartEmptyState';
import { CompactDataTable } from '../../../shared/components/CompactDataTable';
import { CostValue } from '../../../shared/components/CostValue';
import { UsagePieChart } from '../../../shared/components/charts';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { PIE_CHART_COLORS } from '../../../shared/constants';
import { fmtNum, fmtTrendTime } from '../../../shared/utils/format';
import type { ChannelKeyResp, DashboardTimeBucket } from '../../../shared/types';

/** 统计范围预设：今日按小时聚合，其余按天聚合 */
const RANGE_PRESETS = ['today', '7d', '30d', '90d'] as const;
type RangePreset = (typeof RANGE_PRESETS)[number];

// 图表配色与列表金额 chips 对齐：成本 = warning 琥珀，收益 = success 绿，请求数 = 蓝
const COST_BAR_COLOR = '#f59e0b';
const REVENUE_BAR_COLOR = '#10b981';
const REQUESTS_LINE_COLOR = '#0ea5e9';

type CostBucketDatum = {
  time: string;
  rawTime: string;
  cost: number;
  revenue: number;
  requests: number;
};

function toCostBuckets(buckets: DashboardTimeBucket[]): CostBucketDatum[] {
  return buckets.map((item) => ({
    time: fmtTrendTime(item.time),
    rawTime: item.time,
    cost: item.channel_cost ?? 0,
    revenue: item.actual_cost,
    requests: item.requests ?? 0,
  }));
}

// 每日/每小时消耗图：成本与收益双柱 + 请求数折线（右轴）
function ChannelCostTrendChart({ data }: { data: CostBucketDatum[] }) {
  const { t } = useTranslation();

  return (
    <ResponsiveContainer width="100%" height="100%" debounce={80} initialDimension={{ width: 640, height: 240 }}>
      <ComposedChart data={data} margin={{ top: 4, right: 4, left: -12, bottom: 0 }}>
        <CartesianGrid stroke="var(--ag-border-subtle)" vertical={false} />
        <XAxis
          dataKey="time"
          tick={{ fontSize: 11, fill: 'var(--ag-text-tertiary)' }}
          axisLine={false}
          tickLine={false}
        />
        <YAxis
          yAxisId="money"
          tick={{ fontSize: 11, fill: 'var(--ag-text-tertiary)' }}
          axisLine={false}
          tickLine={false}
          tickFormatter={(v: number) => `$${v >= 100 ? Math.round(v) : v.toFixed(2)}`}
        />
        <YAxis
          yAxisId="requests"
          orientation="right"
          tick={{ fontSize: 11, fill: 'var(--ag-text-tertiary)' }}
          axisLine={false}
          tickLine={false}
          tickFormatter={(v: number) => fmtNum(v)}
          width={40}
        />
        <RechartsTooltip
          animationDuration={0}
          cursor={{ fill: 'var(--ag-border-subtle)', opacity: 0.4 }}
          content={({ active, payload }) => {
            if (!active || !payload?.length) return null;
            const d = payload[0]?.payload as CostBucketDatum | undefined;
            if (!d) return null;
            return (
              <div className="rounded-lg border border-border bg-bg-elevated p-3 text-xs shadow-lg">
                <div className="mb-2 font-semibold text-text">{d.rawTime}</div>
                <div className="flex items-center gap-2 py-0.5">
                  <span className="h-2.5 w-2.5 rounded-sm" style={{ background: COST_BAR_COLOR }} />
                  <span className="text-text-secondary">{t('channels.stats_cost')}:</span>
                  <CostValue className="ml-auto font-mono" decimals={4} tone="warning" value={d.cost} />
                </div>
                <div className="flex items-center gap-2 py-0.5">
                  <span className="h-2.5 w-2.5 rounded-sm" style={{ background: REVENUE_BAR_COLOR }} />
                  <span className="text-text-secondary">{t('channels.stats_revenue')}:</span>
                  <CostValue className="ml-auto font-mono" decimals={4} tone="success" value={d.revenue} />
                </div>
                <div className="flex items-center gap-2 py-0.5">
                  <span className="h-0 w-2.5 border-t-2" style={{ borderColor: REQUESTS_LINE_COLOR }} />
                  <span className="text-text-secondary">{t('channels.stats_requests')}:</span>
                  <span className="ml-auto font-mono text-text">{fmtNum(d.requests)}</span>
                </div>
              </div>
            );
          }}
        />
        <Bar yAxisId="money" dataKey="cost" fill={COST_BAR_COLOR} radius={[2, 2, 0, 0]} isAnimationActive={false} />
        <Bar yAxisId="money" dataKey="revenue" fill={REVENUE_BAR_COLOR} radius={[2, 2, 0, 0]} isAnimationActive={false} />
        <Line yAxisId="requests" type="monotone" dataKey="requests" stroke={REQUESTS_LINE_COLOR} strokeWidth={2} dot={false} isAnimationActive={false} />
      </ComposedChart>
    </ResponsiveContainer>
  );
}

/**
 * 密钥端点消耗统计弹窗：按 key 过滤的仪表盘趋势数据
 * （每日/每小时消耗柱状图 + 模型分布饼图与明细表）。
 */
export function ChannelStatsModal({
  channelKey,
  onClose,
}: {
  channelKey: ChannelKeyResp | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [range, setRange] = useState<RangePreset>('7d');

  const trendParams = useMemo(() => ({
    range,
    granularity: range === 'today' ? ('hour' as const) : ('day' as const),
    channel_key_id: channelKey?.id ?? 0,
  }), [range, channelKey?.id]);

  const { data: trend, isLoading } = useQuery({
    queryKey: queryKeys.dashboardTrend('channel-key', trendParams),
    queryFn: () => dashboardApi.trend(trendParams),
    enabled: !!channelKey,
    placeholderData: keepPreviousData,
  });

  const costBuckets = useMemo(() => toCostBuckets(trend?.token_trend ?? []), [trend]);
  const models = trend?.model_distribution ?? [];
  const modelPieData = useMemo(
    () => models.map((item) => ({ name: item.model, value: item.requests })),
    [models],
  );

  const summary = useMemo(() => costBuckets.reduce(
    (acc, item) => ({
      requests: acc.requests + item.requests,
      cost: acc.cost + item.cost,
      revenue: acc.revenue + item.revenue,
    }),
    { requests: 0, cost: 0, revenue: 0 },
  ), [costBuckets]);
  const hasUsage = summary.requests > 0;

  const dialogState = useOverlayState({
    isOpen: !!channelKey,
    onOpenChange: (open) => {
      if (!open) onClose();
    },
  });

  return (
    <Modal state={dialogState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="lg">
          <Modal.Dialog className="ag-elevation-modal">
            <Modal.Header>
              <Modal.Heading>{t('channels.stats_modal_title', { name: channelKey?.name || channelKey?.api_key_hint || '' })}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="mb-4 flex flex-wrap items-center gap-3">
                <Tabs
                  className="ag-segmented-tabs ag-segmented-tabs-compact"
                  selectedKey={range}
                  onSelectionChange={(key) => setRange(key as RangePreset)}
                >
                  <Tabs.List>
                    {RANGE_PRESETS.map((item) => (
                      <Tabs.Tab id={item} key={item}>
                        <Tabs.Indicator />
                        <span>{t(`dashboard.range_${item}`)}</span>
                      </Tabs.Tab>
                    ))}
                  </Tabs.List>
                </Tabs>
                <div className="ml-auto flex items-center gap-4 text-xs text-text-secondary">
                  <span>
                    {t('channels.stats_requests')}: <span className="font-mono text-text">{fmtNum(summary.requests)}</span>
                  </span>
                  <span>
                    {t('channels.stats_cost')}: <CostValue className="font-mono" decimals={4} tone="warning" value={summary.cost} />
                  </span>
                  <span>
                    {t('channels.stats_revenue')}: <CostValue className="font-mono" decimals={4} tone="success" value={summary.revenue} />
                  </span>
                </div>
              </div>

              {isLoading ? (
                <div className="space-y-3">
                  <Skeleton className="h-60 w-full rounded-[var(--radius)]" />
                  <Skeleton className="h-44 w-full rounded-[var(--radius)]" />
                </div>
              ) : (
                <div className="space-y-5">
                  <section>
                    <h4 className="mb-2 text-sm font-semibold text-text">{t('channels.stats_daily_title')}</h4>
                    {hasUsage ? (
                      <div className="h-60">
                        <ChannelCostTrendChart data={costBuckets} />
                      </div>
                    ) : (
                      <ChartEmptyState className="min-h-40" icon={<BarChart3 className="h-5 w-5" />} />
                    )}
                  </section>

                  <section>
                    <h4 className="mb-2 text-sm font-semibold text-text">{t('channels.stats_model_dist_title')}</h4>
                    {models.length === 0 ? (
                      <ChartEmptyState className="min-h-40" icon={<PieChartIcon className="h-5 w-5" />} />
                    ) : (
                      <div className="grid items-start gap-3 sm:grid-cols-[176px_minmax(0,1fr)]">
                        <div className="justify-self-center">
                          <UsagePieChart data={modelPieData} />
                        </div>
                        <div className="min-w-0 overflow-x-auto">
                          <CompactDataTable
                            ariaLabel={t('channels.stats_model_dist_title')}
                            className="ag-compact-data-table--dense"
                            emptyText={t('common.no_data')}
                            minWidth={420}
                            rowKey={(row) => row.model}
                            rows={models}
                            columns={[
                              {
                                key: 'model',
                                title: t('dashboard.model'),
                                width: '36%',
                                render: (row, index) => (
                                  <>
                                    <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: PIE_CHART_COLORS[index % PIE_CHART_COLORS.length] }} />
                                    <span className="min-w-0 truncate font-medium text-text" title={row.model}>{row.model}</span>
                                  </>
                                ),
                              },
                              {
                                align: 'end',
                                key: 'requests',
                                title: t('channels.stats_requests'),
                                width: '16%',
                                render: (row) => <span className="truncate font-mono text-text">{row.requests.toLocaleString()}</span>,
                              },
                              {
                                align: 'end',
                                key: 'tokens',
                                title: t('dashboard.tokens'),
                                width: '16%',
                                render: (row) => <span className="truncate font-mono text-text">{fmtNum(row.tokens)}</span>,
                              },
                              {
                                align: 'end',
                                key: 'cost',
                                title: t('channels.stats_cost'),
                                width: '16%',
                                render: (row) => <CostValue className="truncate font-mono" tone="warning" value={row.channel_cost ?? 0} />,
                              },
                              {
                                align: 'end',
                                key: 'revenue',
                                title: t('channels.stats_revenue'),
                                width: '16%',
                                render: (row) => <CostValue className="truncate font-mono" tone="success" value={row.actual_cost} />,
                              },
                            ]}
                          />
                        </div>
                      </div>
                    )}
                  </section>
                </div>
              )}
            </Modal.Body>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
