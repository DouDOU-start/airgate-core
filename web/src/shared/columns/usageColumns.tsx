import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { useMemo, useState, type CSSProperties, type ReactNode } from 'react';
import { Tooltip } from '@heroui/react';
import { ArrowDown, ArrowUp, BookOpen, Sparkles } from 'lucide-react';
import type { UsageLogResp, CustomerUsageLogResp } from '../types';
import { USAGE_TOKEN_COLORS } from '../constants';
import { formatDate, formatTime } from '../utils/format';
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

const META_CHIP_SERVICE_TIER_COLOR = 'rgb(168,85,247)';

const MODEL_META_SLOT_WIDTH_CLASS = 'w-[5.5rem]';

function MetaChip({
  color,
  label,
}: {
  color: string;
  label: string;
}) {
  return (
    <span
      className={`${MODEL_META_SLOT_WIDTH_CLASS} inline-flex h-4 shrink-0 items-center justify-center truncate rounded px-1.5 text-[12px] font-semibold leading-none whitespace-nowrap`}
      style={{
        background: `color-mix(in srgb, ${color} 18%, transparent)`,
        boxShadow: `inset 0 0 0 1px color-mix(in srgb, ${color} 34%, transparent)`,
        color,
      }}
      title={label}
    >
      {label}
    </span>
  );
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


// TokenMetric 计量明细行（直接由 usage_log 的 token 列构造）。
type TokenMetric = {
  key: string;
  label: string;
  value: number;
  color: string;
};

// tokenMetrics 计量明细：输入/输出恒显示，缓存读/写仅在非零时显示；
// Claude 双档缓存写有明细时展开 5m/1h 两行。
function tokenMetrics(row: UsageRow, t: TFunction): TokenMetric[] {
  const cacheCreation = row.cache_creation_tokens ?? 0;
  const cache5m = row.cache_creation_5m_tokens ?? 0;
  const cache1h = row.cache_creation_1h_tokens ?? 0;
  const metrics: TokenMetric[] = [
    { key: 'input_tokens', label: t('usage.input_tokens'), value: row.input_tokens, color: USAGE_TOKEN_COLORS.input },
    { key: 'output_tokens', label: t('usage.output_tokens'), value: row.output_tokens, color: USAGE_TOKEN_COLORS.output },
  ];
  if (row.cached_input_tokens > 0) {
    metrics.push({ key: 'cached_input_tokens', label: t('usage.cache_read'), value: row.cached_input_tokens, color: USAGE_TOKEN_COLORS.cacheRead });
  }
  if (cache5m > 0 || cache1h > 0) {
    if (cache5m > 0) {
      metrics.push({ key: 'cache_creation_5m_tokens', label: t('usage.cache_creation_5m'), value: cache5m, color: USAGE_TOKEN_COLORS.cacheCreation });
    }
    if (cache1h > 0) {
      metrics.push({ key: 'cache_creation_1h_tokens', label: t('usage.cache_creation_1h'), value: cache1h, color: USAGE_TOKEN_COLORS.cacheCreation });
    }
  } else if (cacheCreation > 0) {
    metrics.push({ key: 'cache_creation_tokens', label: t('usage.cache_creation'), value: cacheCreation, color: USAGE_TOKEN_COLORS.cacheCreation });
  }
  return metrics;
}

function GenericMetricDetail({ row, t }: { row: UsageRow; t: TFunction }) {
  const metrics = tokenMetrics(row, t);
  const tokenTotal =
    row.input_tokens + row.output_tokens + row.cached_input_tokens + (row.cache_creation_tokens ?? 0);
  const calls = row.calls ?? 0;

  return (
    <TooltipPanel title={t('usage.metric_detail', '计量明细')} subtitle={row.model}>
      {metrics.map((metric) => (
        <TooltipRow
          key={metric.key}
          label={metric.label}
          value={metric.value.toLocaleString()}
          color={metric.color}
        />
      ))}
      {calls > 0 && (
        // 图像端点的产出张数（按次计费的计次数）；token 端点恒 0 不显示。
        <TooltipRow label={t('usage.calls', '产出张数')} value={`×${calls}`} tone="accent" />
      )}
      <TooltipDivider />
      <TooltipRow label={t('usage.total_tokens')} value={tokenTotal.toLocaleString()} tone="strong" />
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
      return (
        <RichTooltip
          placement="right"
          content={() => (
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
                {(row.calls ?? 0) > 0 && (
                  // 图像端点产出张数；按次计费时成本 = input_price × 张数。
                  <TooltipRow label={t('usage.calls', '产出张数')} value={`×${row.calls}`} />
                )}
                <TooltipDivider />
                {row.service_tier && (
                  <TooltipRow label={t('usage.service_tier')} value={<span className="capitalize">{row.service_tier}</span>} />
                )}
                <TooltipRow label={t('usage.rate_multiplier')} value={`${row.rate_multiplier.toFixed(2)}x`} />
                {adminView && row.account_rate_multiplier > 0 && (
                  <TooltipRow label={t('usage.account_rate', '渠道倍率')} value={`${row.account_rate_multiplier.toFixed(2)}x`} />
                )}
                {row.sell_rate > 0 && (
                  <TooltipRow label={t('usage.sell_rate', '销售倍率')} value={`${row.sell_rate.toFixed(2)}x`} />
                )}
                <TooltipDivider />
                <TooltipRow label={t('usage.original_cost')} value={<CostValue value={row.total_cost} decimals={6} tone="standard" />} />
                {adminView && row.account_rate_multiplier > 0 && (
                  // 渠道成本不落列，按快照现算：total × 渠道成本倍率（与原落库值精确一致）
                  <TooltipRow label={t('usage.account_cost', '渠道成本')} value={<CostValue value={row.total_cost * row.account_rate_multiplier} decimals={6} />} />
                )}
                <TooltipRow label={t('usage.user_charged', '用户扣费')} value={<CostValue value={row.actual_cost} decimals={6} tone="actual" />} />
                {row.sell_rate > 0 && row.billed_cost !== row.actual_cost && (
                  <>
                    <TooltipRow label={t('usage.billed_cost', '客户账面')} value={<CostValue value={row.billed_cost} decimals={6} />} />
                    <TooltipRow label={t('usage.profit', '利润')} value={<CostValue value={row.billed_cost - row.actual_cost} decimals={6} tone="success" />} />
                  </>
                )}
              </TooltipPanel>
          )}
        >
          <div className="flex w-full flex-col items-center font-mono text-center text-xs">
            {adminView && row.source === 'channel_test' ? (
              // 渠道测试不计用户扣费（恒为 0），费用列直接展示渠道成本（红 $ 区分口径）
              <div className="text-[15px] font-semibold leading-none text-text">
                <CostValue value={row.total_cost * row.account_rate_multiplier} decimals={6} tone="channel" />
              </div>
            ) : row.sell_rate > 0 && row.billed_cost !== row.actual_cost ? (
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
 * 管理端和用户端共用，管理端额外在前面插入 user / api_key / channel 列。
 *
 * customerScope=true 时切换为 end customer 视角的成本列，避免读取后端剥离过的字段。
 */
export function useUsageColumns(opts?: { customerScope?: boolean; adminView?: boolean }): UsageColumnConfig<UsageRow>[] {
  const { t } = useTranslation();
  const customerScope = opts?.customerScope ?? false;
  const adminView = opts?.adminView ?? true;

  return useMemo(() => {
    const costColumn = customerScope ? buildCustomerCostColumn(t) : buildResellerCostColumn(t, adminView);

    return [
    {
      key: 'created_at',
      title: t('usage.time'),
      width: '142px',
      render: (row) => {
        const date = new Date(row.created_at);
        const timeLabel = formatTime(date);
        const dateLabel = formatDate(date);
        // request_id 附在 title：与失败请求 Tab 的留痕互查（两侧都露同一 ID）。
        const fullLabel = row.request_id
          ? `${dateLabel} ${timeLabel}\nrequest_id: ${row.request_id}`
          : `${dateLabel} ${timeLabel}`;

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
        const serviceTier = (row.service_tier ?? '').trim();
        const fallbackMeta = serviceTier ? (
          <MetaChip
            color={META_CHIP_SERVICE_TIER_COLOR}
            label={serviceTierMetaLabel(serviceTier)}
          />
        ) : null;

        return (
          <div className="grid w-full min-w-0 grid-cols-[5.5rem_minmax(0,1fr)] items-center gap-2 text-left">
            <div className={`ag-usage-model-meta-slot ${MODEL_META_SLOT_WIDTH_CLASS} flex h-4 shrink-0 items-center justify-center overflow-hidden`}>
              {fallbackMeta}
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
        const inputTokens = row.input_tokens;
        const outputTokens = row.output_tokens;
        const cacheReadTokens = row.cached_input_tokens;
        const cacheCreationTokens = row.cache_creation_tokens ?? 0;
        const calls = row.calls ?? 0;
        const total = inputTokens + outputTokens + cacheReadTokens + cacheCreationTokens;
        const hasCacheRead = cacheReadTokens > 0;
        const hasCacheWrite = cacheCreationTokens > 0;
        const tokenSummaryVisible = total > 0;
        return (
          <RichTooltip
            placement="left"
            content={() => (
              <GenericMetricDetail row={row} t={t} />
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
            ) : calls > 0 ? (
              // 纯按次计费的图像请求（无 token 计量）：以产出张数代替 "-"。
              <div className="flex h-full min-w-0 items-center justify-center px-2 text-center">
                <span className="font-mono text-base font-semibold tabular-nums leading-none text-text">
                  ×{calls}
                </span>
              </div>
            ) : (
              <div className="flex h-full min-w-0 items-center justify-center px-2 text-center">
                <span className="font-mono text-sm font-semibold leading-none text-text-tertiary">-</span>
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
    // customer scope 的响应剥离了 user_agent / ip_address，不渲染客户端列
    ...(customerScope ? [] : [buildClientColumn(t)]),
    ];
  }, [adminView, customerScope, t]);
}

/** 客户端列：IP 主行 + UA 次行，悬浮展示完整 IP / User-Agent */
function buildClientColumn(t: TFunction): UsageColumnConfig<UsageRow> {
  return {
    key: 'client',
    title: t('usage.client', '客户端'),
    width: '150px',
    hideOnMobile: true,
    render: (raw) => {
      const row = raw as UsageLogResp;
      const ip = row.ip_address || '';
      const ua = row.user_agent || '';
      if (!ip && !ua) {
        return <span className="block text-center font-mono text-[13px] text-text-secondary">-</span>;
      }
      return (
        <RichTooltip
          placement="left"
          content={() => (
            <TooltipPanel title={t('usage.client', '客户端')}>
              <TooltipRow label={t('usage.client_ip', 'IP 地址')} value={ip || '-'} />
              <div className="rounded-[var(--radius)] bg-surface px-2 py-1 text-xs">
                <div className="text-text-tertiary">User-Agent</div>
                <div className="mt-0.5 break-all text-left font-mono font-medium text-text-secondary">{ua || '-'}</div>
              </div>
            </TooltipPanel>
          )}
        >
          <div className="flex w-full min-w-0 flex-col items-center text-center">
            <span className="max-w-full truncate font-mono text-xs text-text-secondary">{ip || '-'}</span>
            {ua ? (
              <span className="max-w-full truncate text-[11px] leading-tight text-text-tertiary">{ua}</span>
            ) : null}
          </div>
        </RichTooltip>
      );
    },
  };
}
