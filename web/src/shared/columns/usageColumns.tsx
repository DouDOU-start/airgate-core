import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { useMemo, useState, useSyncExternalStore, type CSSProperties, type ReactNode } from 'react';
import { Tooltip } from '@heroui/react';
import { ArrowDown, ArrowUp, BookOpen, Sparkles } from 'lucide-react';
import {
  getPluginUsageCostDetail,
  getPluginUsageMetricDetail,
  getPluginUsageModelMeta,
  getUsageCostDetailVersion,
  getUsageMetricDetailVersion,
  getUsageModelMetaVersion,
  subscribeUsageCostDetailChange,
  subscribeUsageMetricDetailChange,
  subscribeUsageModelMetaChange,
} from '../../app/plugin-frontend-registry';
import type { UsageLogResp, CustomerUsageLogResp, UsageAttribute, UsageMetric } from '../types';
import { USAGE_TOKEN_COLORS } from '../constants';
import { CostValue } from '../components/CostValue';

/**
 * 列定义统一使用一个宽松的行类型：管理端拿到的是 UsageLogResp，
 * 而 end customer（API Key 登录）拿到的是 CustomerUsageLogResp（无 input_cost / actual_cost 等字段）。
 * customerScope 列不会读取那些缺失字段。
 */
export type UsageRow = UsageLogResp | CustomerUsageLogResp;

export interface UsageColumnConfig<T extends UsageRow = UsageRow> {
  key: string;
  title: ReactNode;
  width?: string;
  hideOnMobile?: boolean;
  render: (row: T) => ReactNode;
}

const RICH_TOOLTIP_TRIGGER_CLASS = 'flex h-full w-full cursor-default items-center justify-center rounded-[var(--radius)] px-1.5 py-0 text-center transition-colors hover:bg-bg-hover focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary';
const RICH_TOOLTIP_OPEN_DELAY_MS = 140;

function RichTooltip({
  children,
  content,
  placement = 'right',
}: {
  children: ReactNode;
  content: () => ReactNode;
  placement?: 'left' | 'right';
}) {
  const [isOpen, setIsOpen] = useState(false);

  return (
    <Tooltip delay={RICH_TOOLTIP_OPEN_DELAY_MS} closeDelay={0} onOpenChange={setIsOpen}>
      <Tooltip.Trigger className={RICH_TOOLTIP_TRIGGER_CLASS}>
        {children}
      </Tooltip.Trigger>
      <Tooltip.Content
        className="w-[min(21rem,calc(100vw-2rem))] border border-border bg-surface p-0 shadow-lg"
        placement={placement}
      >
        {isOpen ? content() : null}
      </Tooltip.Content>
    </Tooltip>
  );
}

function TooltipPanel({
  children,
  subtitle,
  title,
}: {
  children: ReactNode;
  subtitle?: ReactNode;
  title: ReactNode;
}) {
  return (
    <div className="overflow-hidden rounded-[var(--radius)]">
      <div className="border-b border-border bg-default px-2.5 py-1.5">
        <div className="text-sm font-semibold leading-none text-text">{title}</div>
        {subtitle ? <div className="mt-1 truncate text-xs text-text-tertiary">{subtitle}</div> : null}
      </div>
      <div className="space-y-0.5 p-2">{children}</div>
    </div>
  );
}

function TooltipRow({
  color,
  label,
  tone,
  value,
}: {
  color?: string;
  label: ReactNode;
  tone?: 'accent' | 'info' | 'strong' | 'success' | 'warning';
  value: ReactNode;
}) {
  const toneClass = tone === 'success'
    ? 'text-success'
    : tone === 'warning'
      ? 'text-warning'
      : tone === 'info'
        ? 'text-info'
        : tone === 'accent'
          ? 'text-primary'
          : tone === 'strong'
            ? 'text-text'
            : 'text-text-secondary';

  return (
    <div className="grid grid-cols-[minmax(0,1fr)_minmax(7rem,max-content)] items-center gap-3 rounded-[var(--radius)] bg-surface px-2 py-1 text-xs">
      <span className="min-w-0 truncate text-text-tertiary">{label}</span>
      <span
        className={`min-w-0 max-w-[12rem] justify-self-end truncate text-right font-mono font-medium ${toneClass}`}
        style={color ? { color } : undefined}
      >
        {value}
      </span>
    </div>
  );
}

function TooltipDivider() {
  return <div className="my-0.5 border-t border-border" />;
}

const MODEL_META_IMAGE_COLOR = 'rgb(148,163,184)';
const META_CHIP_LOW_COLOR = 'rgb(34,197,94)';
const META_CHIP_MEDIUM_COLOR = 'rgb(59,130,246)';
const META_CHIP_HIGH_COLOR = 'rgb(249,115,22)';
const META_CHIP_XHIGH_COLOR = 'rgb(239,68,68)';
const META_CHIP_SERVICE_TIER_COLOR = 'rgb(168,85,247)';

const META_CHIP_EFFORT_COLORS: Record<string, string> = {
  low: META_CHIP_LOW_COLOR,
  medium: META_CHIP_MEDIUM_COLOR,
  high: META_CHIP_HIGH_COLOR,
  xhigh: META_CHIP_XHIGH_COLOR,
};

const MODEL_META_SLOT_WIDTH_CLASS = 'w-[5.5rem]';

function MetaChip({
  color,
  dotColor,
  label,
}: {
  color: string;
  dotColor?: string;
  label: string;
}) {
  return (
    <span
      className={`${MODEL_META_SLOT_WIDTH_CLASS} ${dotColor ? 'ag-usage-image-size-chip' : ''} inline-flex h-4 shrink-0 items-center justify-center truncate rounded px-1.5 text-[12px] font-semibold leading-none whitespace-nowrap`}
      style={{
        background: `color-mix(in srgb, ${color} 18%, transparent)`,
        boxShadow: `inset 0 0 0 1px color-mix(in srgb, ${color} 34%, transparent)`,
        color,
      }}
      title={label}
    >
      {dotColor ? (
        <span
          className="ag-usage-image-size-dot"
          aria-hidden="true"
          style={{ backgroundColor: dotColor }}
        />
      ) : null}
      {label}
    </span>
  );
}

function getImageSizeDotColor(imageSize: string): string {
  const normalized = imageSize.trim().toLowerCase();
  if (normalized.includes('4k')) return META_CHIP_HIGH_COLOR;
  if (normalized.includes('2k')) return META_CHIP_MEDIUM_COLOR;
  if (normalized.includes('1k')) return META_CHIP_LOW_COLOR;

  const dimensions = normalized.match(/\d+(?:\.\d+)?/g)?.map(Number).filter(Number.isFinite) ?? [];
  const maxDimension = Math.max(0, ...dimensions);
  if (maxDimension > 2048) return META_CHIP_HIGH_COLOR;
  if (maxDimension > 1536) return META_CHIP_MEDIUM_COLOR;
  return META_CHIP_LOW_COLOR;
}

function serviceTierMetaLabel(serviceTier: string): string {
  const normalized = serviceTier.trim().toLowerCase();
  if (normalized === 'fast' || normalized === 'priority' || normalized === 'scale') return 'fast';
  return serviceTier;
}

const HEROUI_BLUE = 'oklch(62.04% 0.1950 253.83)';

const STREAM_CHIP_STYLE: CSSProperties = {
  background: `color-mix(in srgb, ${HEROUI_BLUE} 18%, transparent)`,
  boxShadow: `inset 0 0 0 1px color-mix(in srgb, ${HEROUI_BLUE} 34%, transparent)`,
  color: HEROUI_BLUE,
};

/** 单行 token 数据行：固定宽度图标 + 右对齐等宽数字 */
function TokenRow({
  color,
  icon,
  value,
}: {
  color: string;
  icon: ReactNode;
  value: string;
}) {
  return (
    <div className="grid grid-cols-[1rem_minmax(0,1fr)] items-center gap-1">
      <span
        className="flex h-4 w-4 shrink-0 items-center justify-center rounded-[var(--radius)] leading-none"
        style={{
          background: `color-mix(in srgb, ${color} 18%, transparent)`,
          color,
        }}
      >
        <span className="flex h-3 w-3 shrink-0 items-center justify-center">{icon}</span>
      </span>
      <span
        className="w-[3.5rem] justify-self-center truncate text-center font-mono text-xs font-semibold tabular-nums leading-none"
        style={{ color }}
      >
        {value}
      </span>
    </div>
  );
}

/** 大数字友好显示：33518599 -> "33.52M"，1234 -> "1,234" */
export function fmtNum(n: number): string {
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(2)}B`;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 10_000) return `${(n / 1_000).toFixed(1)}K`;
  return n.toLocaleString();
}

/** 格式化费用 */
export function fmtCost(n: number): string {
  if (n >= 1000) return `$${(n / 1000).toFixed(2)}K`;
  return `$${n.toFixed(2)}`;
}

function normalizeUsageKey(value?: string): string {
  return (value || '').trim().toLowerCase().replace(/[\s-]+/g, '_');
}

function normalizeMetricKey(metric: Pick<UsageMetric, 'key' | 'kind' | 'label'>): string {
  return normalizeUsageKey(metric.key || metric.kind || metric.label);
}

function metricNumber(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

function metricMatches(metric: UsageMetric, keys: string[]) {
  const key = normalizeMetricKey(metric);
  return keys.includes(key);
}

function metricValue(metrics: UsageMetric[], keys: string[]): number | undefined {
  const item = metrics.find((metric) => metricMatches(metric, keys));
  return item ? metricNumber(item.value) : undefined;
}

function firstText(...values: unknown[]): string | undefined {
  for (const value of values) {
    if (typeof value !== 'string') continue;
    const text = value.trim();
    if (text) return text;
  }
  return undefined;
}

function usageAttributeValue(attributes: UsageAttribute[], keys: string[]): string | undefined {
  const normalizedKeys = new Set(keys.map(normalizeUsageKey));
  const item = attributes.find((attr) => (
    normalizedKeys.has(normalizeUsageKey(attr.key || attr.kind || attr.label))
  ));
  return firstText(item?.value);
}

function usageMetadataValue(metadata: Record<string, string>, keys: string[]): string | undefined {
  const normalizedKeys = new Set(keys.map(normalizeUsageKey));
  for (const [key, value] of Object.entries(metadata)) {
    if (!normalizedKeys.has(normalizeUsageKey(key))) continue;
    const text = firstText(value);
    if (text) return text;
  }
  return undefined;
}

function isTotalMetric(metric: UsageMetric) {
  return metricMatches(metric, ['total_tokens', 'total_token', 'total']);
}

function formatMetricValue(metric: UsageMetric): string {
  const value = metricNumber(metric.value);
  const formatted = Number.isInteger(value)
    ? value.toLocaleString()
    : value.toLocaleString(undefined, { maximumFractionDigits: 4 });
  return metric.unit ? `${formatted} ${metric.unit}` : formatted;
}

function metricColor(metric: UsageMetric, index: number): string | undefined {
  const key = normalizeMetricKey(metric);
  if (key.includes('input') && !key.includes('cached')) return USAGE_TOKEN_COLORS.input;
  if (key.includes('output')) return USAGE_TOKEN_COLORS.output;
  if (key.includes('cache_read') || key.includes('cached_input')) return USAGE_TOKEN_COLORS.cacheRead;
  if (key.includes('cache_creation')) return USAGE_TOKEN_COLORS.cacheCreation;
  if (metric.kind === 'image') return 'var(--ag-success)';
  return [USAGE_TOKEN_COLORS.input, USAGE_TOKEN_COLORS.output, USAGE_TOKEN_COLORS.cacheRead, USAGE_TOKEN_COLORS.cacheCreation][index % 4];
}

function legacyMetrics(row: UsageRow): UsageMetric[] {
  const cacheCreation = (row as UsageLogResp).cache_creation_tokens ?? 0;
  return [
    { key: 'input_tokens', label: '输入 Token', kind: 'token', unit: 'token', value: row.input_tokens },
    { key: 'output_tokens', label: '输出 Token', kind: 'token', unit: 'token', value: row.output_tokens },
    { key: 'cached_input_tokens', label: '缓存读取 Token', kind: 'token', unit: 'token', value: row.cached_input_tokens },
    { key: 'cache_creation_tokens', label: '缓存写入 Token', kind: 'token', unit: 'token', value: cacheCreation },
  ].filter((metric) => metric.value > 0 || metric.key === 'input_tokens' || metric.key === 'output_tokens');
}

function rowMetrics(row: UsageRow): UsageMetric[] {
  const metrics = row.usage_metrics ?? [];
  if (metrics.length > 0) return metrics;
  return legacyMetrics(row);
}

function buildUsageRecordContext(row: UsageRow, customerScope: boolean) {
  const usageCostDetails = !customerScope && 'usage_cost_details' in row
    ? (row.usage_cost_details ?? [])
    : [];
  const usageAttributes = row.usage_attributes ?? [];
  const usageMetrics = row.usage_metrics ?? [];
  const usageMetadata = row.usage_metadata ?? {};
  const imageSize = firstText(
    row.image_size,
    usageAttributeValue(usageAttributes, ['image_size', 'resolution', 'size']),
    usageMetadataValue(usageMetadata, ['image_size', 'resolution', 'size']),
  );
  const serviceTier = firstText(
    row.service_tier,
    usageAttributeValue(usageAttributes, ['service_tier', 'tier']),
    usageMetadataValue(usageMetadata, ['service_tier', 'tier']),
  );
  const reasoningEffort = firstText(
    (row as Partial<UsageLogResp>).reasoning_effort,
    usageAttributeValue(usageAttributes, ['reasoning_effort', 'reasoning']),
    usageMetadataValue(usageMetadata, ['reasoning_effort', 'reasoning']),
  );
  const reasoningTokens =
    (row as Partial<UsageLogResp>).reasoning_output_tokens
    ?? metricValue(usageMetrics, ['reasoning_output_tokens', 'reasoning_tokens', 'reasoning_token']);

  const ctx: Record<string, unknown> = {
    record: row,
    customerScope,
    usageAttributes,
    usageMetrics,
    usageCostDetails,
    usageMetadata,
    usage_attributes: usageAttributes,
    usage_metrics: usageMetrics,
    usage_cost_details: usageCostDetails,
    usage_metadata: usageMetadata,
    // 常用的行级别字段做扁平化，方便插件扩展渲染器直接取值。
    model: row.model,
    platform: row.platform,
    service_tier: serviceTier,
    image_size: imageSize,
    endpoint: row.endpoint,
    stream: row.stream,
    created_at: row.created_at,
  };

  if (reasoningEffort) ctx.reasoning_effort = reasoningEffort;
  if (typeof reasoningTokens === 'number' && reasoningTokens > 0) {
    ctx.reasoning_output_tokens = reasoningTokens;
  }

  return ctx;
}

function buildCostDetailContext(row: UsageLogResp, adminView: boolean) {
  const ctx = buildUsageRecordContext(row, false);
  ctx.adminView = adminView;
  return ctx;
}

function GenericMetricDetail({ row, t }: { row: UsageRow; t: TFunction }) {
  const allMetrics = rowMetrics(row);
  const hasSDKMetrics = (row.usage_metrics?.length ?? 0) > 0;
  const metrics = allMetrics.filter((metric) => (
    !isTotalMetric(metric) && (metricNumber(metric.value) > 0 || !hasSDKMetrics)
  ));
  const totalMetric = allMetrics.find(isTotalMetric);
  const tokenTotal =
    totalMetric?.value
    ?? row.input_tokens + row.output_tokens + row.cached_input_tokens + ((row as UsageLogResp).cache_creation_tokens ?? 0);
  const shouldShowTokenTotal = !!totalMetric || tokenTotal > 0 || metrics.some((metric) => metric.kind === 'token');

  return (
    <TooltipPanel title={t('usage.metric_detail', '计量明细')} subtitle={row.model}>
      {metrics.map((metric, index) => (
        <TooltipRow
          key={metric.key || `${metric.label}:${index}`}
          label={metric.label || metric.key || t('usage.metric', '计量')}
          value={formatMetricValue(metric)}
          color={metricColor(metric, index)}
        />
      ))}
      {shouldShowTokenTotal && (
        <>
          <TooltipDivider />
          <TooltipRow label={t('usage.total_tokens')} value={Number(tokenTotal).toLocaleString()} tone="strong" />
        </>
      )}
    </TooltipPanel>
  );
}

/** Reseller / admin 视角的成本列：包含完整的成本拆分与倍率信息 */
function buildResellerCostColumn(t: TFunction, adminView: boolean): UsageColumnConfig<UsageRow> {
  return {
    key: 'cost',
    title: t('usage.cost'),
    width: '140px',
    render: (raw) => {
      const row = raw as UsageLogResp;
      const PluginUsageCostDetail = getPluginUsageCostDetail(row.platform);
      return (
        <RichTooltip
          placement="right"
          content={() => (
            PluginUsageCostDetail ? (
              <PluginUsageCostDetail
                recordId={row.id}
                context={buildCostDetailContext(row, adminView)}
              />
            ) : (
              <TooltipPanel title={t('usage.cost_detail')} subtitle={row.model}>
                <TooltipRow label={t('usage.input_cost')} value={`$${row.input_cost.toFixed(6)}`} />
                <TooltipRow label={t('usage.output_cost')} value={`$${row.output_cost.toFixed(6)}`} />
                {row.input_price > 0 && (
                  <TooltipRow label={t('usage.input_unit_price')} value={`$${row.input_price.toFixed(4)} / 1M Token`} />
                )}
                {row.output_price > 0 && (
                  <TooltipRow label={t('usage.output_unit_price')} value={`$${row.output_price.toFixed(4)} / 1M Token`} />
                )}
                {row.cached_input_cost > 0 && (
                  <TooltipRow label={t('usage.cached_input_cost')} value={`$${row.cached_input_cost.toFixed(6)}`} />
                )}
                <TooltipDivider />
                {row.service_tier && (
                  <TooltipRow label={t('usage.service_tier')} value={<span className="capitalize">{row.service_tier}</span>} />
                )}
                <TooltipRow label={t('usage.rate_multiplier')} value={`${row.rate_multiplier.toFixed(2)}x`} />
                {adminView && row.account_rate_multiplier > 0 && (
                  <TooltipRow label={t('usage.account_rate', '账号倍率')} value={`${row.account_rate_multiplier.toFixed(2)}x`} />
                )}
                {row.sell_rate > 0 && (
                  <TooltipRow label={t('usage.sell_rate', '销售倍率')} value={`${row.sell_rate.toFixed(2)}x`} />
                )}
                <TooltipDivider />
                <TooltipRow label={t('usage.original_cost')} value={<CostValue value={row.total_cost} decimals={6} tone="standard" />} />
                {adminView && (
                  <TooltipRow label={t('usage.account_cost', '账号计费')} value={<CostValue value={row.account_cost} decimals={6} />} />
                )}
                <TooltipRow label={t('usage.user_charged', '用户扣费')} value={<CostValue value={row.actual_cost} decimals={6} tone="actual" />} />
                {row.sell_rate > 0 && row.billed_cost !== row.actual_cost && (
                  <>
                    <TooltipRow label={t('usage.billed_cost', '客户账面')} value={<CostValue value={row.billed_cost} decimals={6} />} />
                    <TooltipRow label={t('usage.profit', '利润')} value={<CostValue value={row.billed_cost - row.actual_cost} decimals={6} tone="success" />} />
                  </>
                )}
              </TooltipPanel>
            )
          )}
        >
          <div className="flex w-full flex-col items-center font-mono text-center text-xs">
            {row.sell_rate > 0 && row.billed_cost !== row.actual_cost ? (
              <div className="text-[15px] font-semibold leading-none text-text">
                <CostValue value={row.billed_cost} decimals={6} tone="warning" />
              </div>
            ) : (
              <div className="text-[15px] font-semibold leading-none text-text">
                <CostValue value={row.actual_cost} decimals={6} tone="warning" />
              </div>
            )}
          </div>
        </RichTooltip>
      );
    },
  };
}

/** End customer 视角的成本列：只展示后端剥离过的 cost 字段 */
function buildCustomerCostColumn(t: TFunction): UsageColumnConfig<UsageRow> {
  return {
    key: 'cost',
    title: t('usage.cost'),
    width: '140px',
    render: (raw) => {
      const cost = (raw as CustomerUsageLogResp).cost ?? 0;
      return (
        <RichTooltip
          placement="right"
          content={() => (
            <TooltipPanel title={t('usage.cost_detail')} subtitle={raw.model}>
              <TooltipRow label={t('usage.cost')} value={<CostValue value={cost} decimals={6} tone="actual" />} tone="strong" />
            </TooltipPanel>
          )}
        >
          <div className="flex w-full flex-col items-center font-mono text-center text-xs">
            <div className="text-[15px] font-semibold leading-none text-text">
              <CostValue value={cost} decimals={6} tone="warning" />
            </div>
          </div>
        </RichTooltip>
      );
    },
  };
}

/**
 * 使用记录表格的共享列定义。
 * 管理端和用户端共用，管理端额外在前面插入 user / api_key / account 列。
 *
 * customerScope=true 时切换为 end customer 视角的成本列，避免读取后端剥离过的字段。
 */
export function useUsageColumns(opts?: { customerScope?: boolean; adminView?: boolean }): UsageColumnConfig<UsageRow>[] {
  const { t } = useTranslation();
  const customerScope = opts?.customerScope ?? false;
  const adminView = opts?.adminView ?? true;
  const metricDetailVersion = useSyncExternalStore(subscribeUsageMetricDetailChange, getUsageMetricDetailVersion);
  const costDetailVersion = useSyncExternalStore(subscribeUsageCostDetailChange, getUsageCostDetailVersion);
  const modelMetaVersion = useSyncExternalStore(subscribeUsageModelMetaChange, getUsageModelMetaVersion);

  return useMemo(() => {
    const costColumn = customerScope ? buildCustomerCostColumn(t) : buildResellerCostColumn(t, adminView);

    return [
    {
      key: 'created_at',
      title: t('usage.time'),
      width: '142px',
      render: (row) => {
        const date = new Date(row.created_at);
        const timeLabel = date.toLocaleTimeString('zh-CN', { hour12: false });
        const dateLabel = date.toLocaleDateString('zh-CN');
        const fullLabel = `${dateLabel} ${timeLabel}`;

        return (
          <div className="flex min-w-0 items-center gap-1.5 font-mono text-xs" title={fullLabel}>
            <span className="shrink-0 font-mono text-[13px] font-medium text-text">
              {timeLabel}
            </span>
            <span className="hidden shrink-0 text-text-tertiary xl:inline">
              {dateLabel}
            </span>
          </div>
        );
      },
    },
    {
      key: 'model',
      title: t('usage.model'),
      width: '220px',
      render: (row) => {
        const PluginUsageModelMeta = getPluginUsageModelMeta(row.platform);
        const metaContext = buildUsageRecordContext(row, customerScope);
        const fallbackMeta = (() => {
          if (PluginUsageModelMeta) return null;
          const imageSize = typeof metaContext.image_size === 'string' ? metaContext.image_size : '';
          if (imageSize) {
            return (
              <MetaChip
                color={MODEL_META_IMAGE_COLOR}
                dotColor={getImageSizeDotColor(imageSize)}
                label={imageSize}
              />
            );
          }

          const reasoningEffort = typeof metaContext.reasoning_effort === 'string' ? metaContext.reasoning_effort : '';
          if (reasoningEffort) {
            return (
              <MetaChip
                color={META_CHIP_EFFORT_COLORS[reasoningEffort.toLowerCase()] ?? 'rgb(148,163,184)'}
                label={reasoningEffort}
              />
            );
          }

          const serviceTier = typeof metaContext.service_tier === 'string' ? metaContext.service_tier : '';
          if (!serviceTier) return null;
          return (
            <MetaChip
              color={META_CHIP_SERVICE_TIER_COLOR}
              label={serviceTierMetaLabel(serviceTier)}
            />
          );
        })();

        return (
          <div className="grid w-full min-w-0 grid-cols-[5.5rem_minmax(0,1fr)] items-center gap-2 text-left">
            <div className={`ag-usage-model-meta-slot ${MODEL_META_SLOT_WIDTH_CLASS} flex h-4 shrink-0 items-center justify-center overflow-hidden`}>
              {PluginUsageModelMeta ? (
                <PluginUsageModelMeta
                  recordId={row.id}
                  context={metaContext}
                />
              ) : fallbackMeta}
            </div>
            <span className="min-w-0 truncate text-sm font-medium leading-none text-text" title={row.model}>
              {row.model}
            </span>
          </div>
        );
      },
    },
    {
      key: 'tokens',
      title: t('usage.metrics', '计量'),
      width: '220px',
      render: (row) => {
        const metrics = rowMetrics(row);
        const PluginUsageMetricDetail = getPluginUsageMetricDetail(row.platform);
        const inputTokens = metricValue(metrics, ['input_tokens', 'input_token', 'prompt_tokens', 'prompt_token']) ?? row.input_tokens;
        const outputTokens = metricValue(metrics, ['output_tokens', 'output_token', 'completion_tokens', 'completion_token']) ?? row.output_tokens;
        const cacheReadTokens = metricValue(metrics, ['cached_input_tokens', 'cached_input_token', 'cache_read_tokens', 'cache_read_token']) ?? row.cached_input_tokens;
        const cacheCreationTokens = metricValue(metrics, ['cache_creation_tokens', 'cache_creation_token']) ?? ((row as UsageLogResp).cache_creation_tokens ?? 0);
        const total =
          metricValue(metrics, ['total_tokens', 'total_token'])
          ?? inputTokens + outputTokens + cacheReadTokens + cacheCreationTokens;
        const hasCacheRead = cacheReadTokens > 0;
        const hasCacheWrite = cacheCreationTokens > 0;
        const tokenSummaryVisible = inputTokens > 0 || outputTokens > 0 || hasCacheRead || hasCacheWrite || total > 0;
        const primaryMetric = metrics.find((metric) => metricNumber(metric.value) > 0 && !isTotalMetric(metric));
        return (
          <RichTooltip
            placement="left"
            content={() => (
              PluginUsageMetricDetail ? (
                <PluginUsageMetricDetail
                  recordId={row.id}
                  context={buildUsageRecordContext(row, customerScope)}
                />
              ) : (
                <GenericMetricDetail row={row} t={t} />
              )
            )}
          >
            {tokenSummaryVisible ? (
              <div className="mx-auto grid h-full max-h-[var(--ag-usage-table-row-height)] grid-cols-[minmax(0,8.75rem)_4.75rem] items-center justify-center gap-2 overflow-visible px-1">
                <div className="grid min-w-0 grid-cols-2 gap-x-2 gap-y-px">
                  <TokenRow
                    color={USAGE_TOKEN_COLORS.input}
                    icon={<ArrowDown className="h-3 w-3 shrink-0" />}
                    value={fmtNum(inputTokens)}
                  />
                  <TokenRow
                    color={USAGE_TOKEN_COLORS.output}
                    icon={<ArrowUp className="h-3 w-3 shrink-0" />}
                    value={fmtNum(outputTokens)}
                  />
                  {(hasCacheRead || hasCacheWrite) ? (
                    <>
                      {hasCacheRead ? (
                        <TokenRow
                          color={USAGE_TOKEN_COLORS.cacheRead}
                          icon={<BookOpen className="h-3 w-3 shrink-0" />}
                          value={fmtNum(cacheReadTokens)}
                        />
                      ) : <div />}
                      {hasCacheWrite ? (
                        <TokenRow
                          color={USAGE_TOKEN_COLORS.cacheCreation}
                          icon={<Sparkles className="h-3 w-3 shrink-0" />}
                          value={fmtNum(cacheCreationTokens)}
                        />
                      ) : <div />}
                    </>
                  ) : null}
                </div>
                <div className="w-[4.75rem] text-center font-mono text-base font-semibold tabular-nums leading-none text-text">
                  {fmtNum(total)}
                </div>
              </div>
            ) : (
              <div className="flex h-full min-w-0 flex-col items-center justify-center px-2 text-center">
                <span className="max-w-full truncate text-[11px] leading-none text-text-tertiary" title={primaryMetric?.label || primaryMetric?.key}>
                  {primaryMetric?.label || primaryMetric?.key || '-'}
                </span>
                <span className="mt-1 max-w-full truncate font-mono text-sm font-semibold leading-none text-text">
                  {primaryMetric ? formatMetricValue(primaryMetric) : '-'}
                </span>
              </div>
            )}
          </RichTooltip>
        );
      },
    },
    costColumn,
    {
      key: 'stream',
      title: t('usage.type'),
      width: '72px',
      hideOnMobile: true,
      render: (row) => (
        <span
          className="inline-flex h-6 min-w-0 items-center justify-center rounded-[var(--radius)] px-1.5 text-[13px] font-medium leading-none text-text-secondary"
          style={row.stream ? STREAM_CHIP_STYLE : undefined}
        >
          {row.stream ? t('usage.type_stream') : t('usage.type_sync')}
        </span>
      ),
    },
    {
      key: 'first_token_ms',
      title: t('usage.first_token'),
      width: '78px',
      hideOnMobile: true,
      render: (row) => (
        <span className="block text-center font-mono text-[13px] text-text-secondary">
          {row.first_token_ms > 0 ? (row.first_token_ms >= 1000 ? `${(row.first_token_ms / 1000).toFixed(2)}s` : `${row.first_token_ms}ms`) : '-'}
        </span>
      ),
    },
    {
      key: 'duration_ms',
      title: t('usage.duration'),
      width: '76px',
      hideOnMobile: true,
      render: (row) => (
        <span className="block text-center font-mono text-[13px] text-text-secondary">
          {row.duration_ms >= 1000 ? `${(row.duration_ms / 1000).toFixed(2)}s` : `${row.duration_ms}ms`}
        </span>
      ),
    },
    ];
  }, [adminView, costDetailVersion, customerScope, metricDetailVersion, modelMetaVersion, t]);
}
