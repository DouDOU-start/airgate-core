import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import type { CSSProperties, ReactNode } from 'react';
import { Chip, Tooltip } from '@heroui/react';
import { BookOpen, Sparkles } from 'lucide-react';
import type { UsageLogResp, CustomerUsageLogResp } from '../types';

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

function RichTooltip({
  children,
  content,
  placement = 'right',
}: {
  children: ReactNode;
  content: ReactNode;
  placement?: 'left' | 'right';
}) {
  return (
    <Tooltip delay={0} closeDelay={0}>
      <Tooltip.Trigger className="flex h-[var(--ag-usage-table-row-height)] w-full cursor-default items-center justify-end rounded-[var(--radius)] px-2.5 py-1 text-right transition-colors hover:bg-bg-hover focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary">
        {children}
      </Tooltip.Trigger>
      <Tooltip.Content
        className="w-[min(21rem,calc(100vw-2rem))] border border-border bg-surface p-0 shadow-lg"
        placement={placement}
      >
        {content}
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
  label,
  tone,
  value,
}: {
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
    <div className="flex items-center justify-between gap-3 rounded-[var(--radius)] bg-surface px-2 py-1 text-xs">
      <span className="min-w-0 truncate text-text-tertiary">{label}</span>
      <span className={`min-w-0 max-w-[12rem] truncate text-right font-mono font-medium ${toneClass}`}>{value}</span>
    </div>
  );
}

function TooltipDivider() {
  return <div className="my-0.5 border-t border-border" />;
}

const reasoningEffortColorMap: Record<string, string> = {
  auto: 'var(--ag-success)',
  minimal: 'var(--ag-muted)',
  low: 'var(--ag-muted)',
  medium: 'var(--ag-primary)',
  high: 'var(--ag-warning)',
  xhigh: 'var(--ag-danger)',
  extrahigh: 'var(--ag-danger)',
  max: 'var(--ag-danger)',
};

function getReasoningEffortStyle(effort: string): CSSProperties {
  const normalized = effort.trim().toLowerCase().replace(/[\s_-]/g, '');
  const color = reasoningEffortColorMap[normalized] ?? 'var(--ag-primary)';

  return {
    background: `color-mix(in srgb, ${color} 14%, transparent)`,
    borderColor: `color-mix(in srgb, ${color} 28%, transparent)`,
    color,
  };
}

function getImageSizeStyle(): CSSProperties {
  const color = 'var(--ag-success)';

  return {
    background: `color-mix(in srgb, ${color} 14%, transparent)`,
    borderColor: `color-mix(in srgb, ${color} 28%, transparent)`,
    color,
  };
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

/** Reseller / admin 视角的成本列：包含完整的成本拆分与倍率信息 */
function buildResellerCostColumn(t: TFunction): UsageColumnConfig<UsageRow> {
  return {
    key: 'cost',
    title: t('usage.cost'),
    width: '140px',
    render: (raw) => {
      const row = raw as UsageLogResp;
      return (
        <RichTooltip
          placement="right"
          content={
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
              {row.account_rate_multiplier > 0 && row.account_rate_multiplier !== 1 && (
                <TooltipRow label={t('usage.account_rate', '账号倍率')} value={`${row.account_rate_multiplier.toFixed(2)}x`} />
              )}
              {row.sell_rate > 0 && (
                <TooltipRow label={t('usage.sell_rate', '销售倍率')} value={`${row.sell_rate.toFixed(2)}x`} />
              )}
              <TooltipDivider />
              <TooltipRow label={t('usage.original_cost')} value={`$${row.total_cost.toFixed(6)}`} />
              {row.account_cost !== row.total_cost && (
                <TooltipRow label={t('usage.account_cost', '账号计费')} value={`$${row.account_cost.toFixed(6)}`} />
              )}
              <TooltipRow label={t('usage.user_charged', '用户扣费')} value={`$${row.actual_cost.toFixed(6)}`} tone="warning" />
              {row.sell_rate > 0 && row.billed_cost !== row.actual_cost && (
                <>
                  <TooltipRow label={t('usage.billed_cost', '客户账面')} value={`$${row.billed_cost.toFixed(6)}`} />
                  <TooltipRow label={t('usage.profit', '利润')} value={`$${(row.billed_cost - row.actual_cost).toFixed(6)}`} tone="success" />
                </>
              )}
            </TooltipPanel>
          }
        >
          <div className="flex w-full flex-col items-end font-mono text-xs text-right">
            {row.sell_rate > 0 && row.billed_cost !== row.actual_cost ? (
              <>
                <div className="text-sm font-semibold leading-none text-text">${row.billed_cost.toFixed(6)}</div>
                <div className="mt-1 text-[11px] text-text-tertiary">
                  {t('usage.cost_actual_short', '成本')} ${row.actual_cost.toFixed(6)}
                </div>
              </>
            ) : (
              <div className="text-sm font-semibold leading-none text-text">${row.actual_cost.toFixed(6)}</div>
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
          content={
            <TooltipPanel title={t('usage.cost_detail')} subtitle={raw.model}>
              <TooltipRow label={t('usage.cost')} value={`$${cost.toFixed(6)}`} tone="strong" />
            </TooltipPanel>
          }
        >
          <div className="flex w-full flex-col items-end font-mono text-xs text-right">
            <div className="text-sm font-semibold leading-none text-text">${cost.toFixed(6)}</div>
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
export function useUsageColumns(opts?: { customerScope?: boolean }): UsageColumnConfig<UsageRow>[] {
  const { t } = useTranslation();
  const customerScope = opts?.customerScope ?? false;

  const costColumn = customerScope ? buildCustomerCostColumn(t) : buildResellerCostColumn(t);

  return [
    {
      key: 'created_at',
      title: t('usage.time'),
      width: '142px',
      render: (row) => {
        const date = new Date(row.created_at);

        return (
          <div className="flex min-w-0 items-center gap-1.5 font-mono text-[11px]">
            <span className="font-mono text-xs font-medium text-text">
              {date.toLocaleTimeString('zh-CN', { hour12: false })}
            </span>
            <span className="text-text-tertiary">
              {date.toLocaleDateString('zh-CN')}
            </span>
          </div>
        );
      },
    },
    {
      key: 'model',
      title: t('usage.model'),
      width: '210px',
      render: (row) => {
        const reasoningEffort = (row as UsageLogResp).reasoning_effort;

        return (
          <div className="flex min-w-0 flex-col gap-1">
            <div className="flex min-w-0 items-center gap-2">
              <span className="block max-w-full truncate text-sm font-medium text-text" title={row.model}>
                {row.model}
              </span>
            </div>
            {(reasoningEffort || row.image_size) ? (
              <div className="flex min-w-0 items-center gap-1.5">
                {reasoningEffort ? (
                  <span
                    className="truncate rounded-[var(--radius)] border px-1.5 py-0.5 text-[11px] font-medium"
                    style={getReasoningEffortStyle(reasoningEffort)}
                  >
                    {reasoningEffort}
                  </span>
                ) : null}
                {row.image_size ? (
                  <span
                    className="truncate rounded-[var(--radius)] border px-1.5 py-0.5 font-mono text-[11px] font-medium"
                    style={getImageSizeStyle()}
                  >
                    {row.image_size}
                  </span>
                ) : null}
              </div>
            ) : null}
          </div>
        );
      },
    },
    {
      key: 'tokens',
      title: 'TOKEN',
      width: '136px',
      render: (row) => {
        // 注意：cached_input_tokens 表示 cache read；cache_creation_tokens 为 5m+1h 之和，
        // 两者与 input/output 互斥计入 total_tokens。
        // CustomerUsageLogResp 不下发 cache_creation_* 字段，这里用 ?? 0 做兼容。
        const cacheCreation = (row as UsageLogResp).cache_creation_tokens ?? 0;
        const cacheCreation5m = (row as UsageLogResp).cache_creation_5m_tokens ?? 0;
        const cacheCreation1h = (row as UsageLogResp).cache_creation_1h_tokens ?? 0;
        const total =
          row.input_tokens + row.output_tokens + row.cached_input_tokens + cacheCreation;
        return (
          <RichTooltip
            placement="left"
            content={
              <TooltipPanel title={`Token ${t('usage.detail')}`} subtitle={row.model}>
                <TooltipRow label={t('usage.input_tokens')} value={row.input_tokens.toLocaleString()} tone="success" />
                <TooltipRow label={t('usage.output_tokens')} value={row.output_tokens.toLocaleString()} tone="info" />
                {row.cached_input_tokens > 0 && (
                  <TooltipRow label={t('usage.cache_read')} value={row.cached_input_tokens.toLocaleString()} />
                )}
                {cacheCreation > 0 && (
                  <TooltipRow label={t('usage.cache_creation')} value={cacheCreation.toLocaleString()} tone="warning" />
                )}
                {cacheCreation5m > 0 && (
                  <TooltipRow label={t('usage.cache_creation_5m')} value={cacheCreation5m.toLocaleString()} tone="warning" />
                )}
                {cacheCreation1h > 0 && (
                  <TooltipRow label={t('usage.cache_creation_1h')} value={cacheCreation1h.toLocaleString()} tone="warning" />
                )}
                <TooltipDivider />
                <TooltipRow label={t('usage.total_tokens')} value={total.toLocaleString()} tone="strong" />
              </TooltipPanel>
            }
          >
            <div className="flex w-full flex-col items-end gap-1">
              <div className="font-mono text-sm font-semibold leading-none text-text">
                {fmtNum(total)}
              </div>
              <div className="flex items-center gap-1.5 font-mono text-[11px] leading-none">
                <span className="inline-flex items-center gap-0.5 rounded px-1 py-0.5 text-emerald-600 dark:text-emerald-400" style={{ background: 'color-mix(in srgb, #10b981 12%, transparent)' }}>
                  <span className="text-[10px] font-semibold opacity-70">in</span>
                  <span className="tabular-nums">{fmtNum(row.input_tokens)}</span>
                </span>
                <span className="inline-flex items-center gap-0.5 rounded px-1 py-0.5 text-sky-600 dark:text-sky-400" style={{ background: 'color-mix(in srgb, #0ea5e9 12%, transparent)' }}>
                  <span className="text-[10px] font-semibold opacity-70">out</span>
                  <span className="tabular-nums">{fmtNum(row.output_tokens)}</span>
                </span>
              </div>
              {(row.cached_input_tokens > 0 || cacheCreation > 0) && (
                <div className="flex items-center gap-1.5 font-mono text-[11px] leading-none">
                  {row.cached_input_tokens > 0 && (
                    <span
                      className="inline-flex items-center gap-0.5 rounded px-1 py-0.5 text-text-tertiary"
                      style={{ background: 'color-mix(in srgb, currentColor 10%, transparent)' }}
                      title={t('usage.cache_read')}
                    >
                      <BookOpen className="h-2.5 w-2.5 shrink-0" />
                      <span className="tabular-nums">{fmtNum(row.cached_input_tokens)}</span>
                    </span>
                  )}
                  {cacheCreation > 0 && (
                    <span
                      className="inline-flex items-center gap-0.5 rounded px-1 py-0.5 text-amber-600 dark:text-amber-400"
                      style={{ background: 'color-mix(in srgb, #f59e0b 12%, transparent)' }}
                      title={t('usage.cache_creation')}
                    >
                      <Sparkles className="h-2.5 w-2.5 shrink-0" />
                      <span className="tabular-nums">{fmtNum(cacheCreation)}</span>
                    </span>
                  )}
                </div>
              )}
            </div>
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
        <Chip className="px-1.5 text-xs" color={row.stream ? 'accent' : 'default'} size="sm" variant="soft">
          {row.stream ? t('usage.type_stream') : t('usage.type_sync')}
        </Chip>
      ),
    },
    {
      key: 'first_token_ms',
      title: t('usage.first_token'),
      width: '78px',
      hideOnMobile: true,
      render: (row) => (
        <span className="block text-right font-mono text-xs text-text-secondary">
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
        <span className="block text-right font-mono text-xs text-text-secondary">
          {row.duration_ms >= 1000 ? `${(row.duration_ms / 1000).toFixed(2)}s` : `${row.duration_ms}ms`}
        </span>
      ),
    },
  ];
}
