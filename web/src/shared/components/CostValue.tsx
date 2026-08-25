import type { ReactNode } from 'react';

type CostTone = 'actual' | 'default' | 'standard' | 'success' | 'warning';

const COST_TONE_CLASS: Record<CostTone, string> = {
  actual: 'text-warning',
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
  className = '',
  separator = '/',
  standard,
}: {
  actual: number | null | undefined;
  className?: string;
  separator?: ReactNode;
  standard: number | null | undefined;
}) {
  return (
    <span className={`inline-flex min-w-0 items-baseline gap-1 ${className}`}>
      <CostValue value={actual} tone="actual" />
      <span className="text-text-tertiary">{separator}</span>
      <CostValue value={standard} tone="standard" />
    </span>
  );
}
