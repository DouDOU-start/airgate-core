import type { ReactNode } from 'react';

type CostTone = 'actual' | 'channel' | 'default' | 'standard' | 'success' | 'warning';

const COST_TONE_CLASS: Record<CostTone, string> = {
  actual: 'text-warning',
  channel: 'text-danger',
  default: 'text-text',
  standard: 'text-success',
  success: 'text-success',
  warning: 'text-warning',
};

function formatCost(value: number | null | undefined, decimals?: number): string {
  const amount = value ?? 0;
  if (decimals != null) return `$${amount.toFixed(decimals)}`;
  if (amount >= 1000) return `$${(amount / 1000).toFixed(2)}K`;
  return `$${amount.toFixed(2)}`;
}

export function CostValue({
  className = '',
  decimals,
  tone = 'default',
  value,
}: {
  className?: string;
  decimals?: number;
  tone?: CostTone;
  value: number | null | undefined;
}) {
  const formatted = formatCost(value, decimals);
  const amount = formatted.startsWith('$') ? formatted.slice(1) : formatted;

  return (
    <span className={className}>
      <span className={COST_TONE_CLASS[tone]}>$</span>
      <span className="text-text">{amount}</span>
    </span>
  );
}

export function CostPair({
  actual,
  channelCost,
  className = '',
  separator = '/',
  standard,
  title,
}: {
  actual: number | null | undefined;
  /** 渠道成本（可选）：提供时展示三段 实际/成本/标准 */
  channelCost?: number | null;
  className?: string;
  separator?: ReactNode;
  standard: number | null | undefined;
  title?: string;
}) {
  return (
    <span className={`inline-flex min-w-0 items-baseline gap-1 ${className}`} title={title}>
      <CostValue value={actual} tone="actual" />
      {channelCost != null ? (
        <>
          <span className="text-text-tertiary">{separator}</span>
          <CostValue value={channelCost} tone="channel" />
        </>
      ) : null}
      <span className="text-text-tertiary">{separator}</span>
      <CostValue value={standard} tone="standard" />
    </span>
  );
}
