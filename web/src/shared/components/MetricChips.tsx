import { Chip } from '@heroui/react';
import type { ReactNode } from 'react';

type MetricChipColor = 'default' | 'warning' | 'success' | 'accent' | 'danger';

export type MetricChipItem = {
  amount?: number;
  color: MetricChipColor;
  decimals?: number;
  dollarTone?: MetricChipColor;
  highlightDollar?: boolean;
  label: string;
  /** 显式弱化（灰底）：value 型指标空闲态使用，与 mutedWhenZero 的零值弱化同款式 */
  muted?: boolean;
  mutedWhenZero?: boolean;
  value?: string;
  /** 富文本展示（如带删除线的原倍率）；提供时优先于 value 渲染，title 仍取 value */
  valueNode?: ReactNode;
};

function formatMoneyAmount(value: number, decimals = 4) {
  return (Number.isFinite(value) ? value : 0).toFixed(decimals);
}

function formatMetricTitleValue(item: MetricChipItem) {
  if (item.amount != null) return `$${formatMoneyAmount(item.amount, item.decimals)}`;
  return item.value ?? '';
}

function MetricChip({ amount, color, decimals, dollarTone, highlightDollar, label, muted, mutedWhenZero, value, valueNode }: MetricChipItem) {
  const amountText = amount == null ? null : formatMoneyAmount(amount, decimals);
  const isMutedZero = muted || (mutedWhenZero && amount === 0);
  const chipClassName = [
    'ag-metric-chip',
    isMutedZero ? 'ag-metric-chip--zero' : '',
  ].filter(Boolean).join(' ');
  const effectiveDollarTone = dollarTone ?? (highlightDollar ? 'warning' : undefined);
  const dollarClassName = [
    'ag-metric-dollar',
    effectiveDollarTone ? `ag-metric-dollar--${effectiveDollarTone}` : '',
  ].filter(Boolean).join(' ');

  return (
    <Chip className={chipClassName} color={isMutedZero ? 'default' : color} size="sm" variant="soft">
      <span className="ag-metric-chip-label">{label}</span>
      <span className="ag-metric-chip-value">
        {amountText == null ? (
          valueNode != null ? valueNode : value === '∞' ? <span className="ag-metric-infinity">{value}</span> : value
        ) : (
          <>
            <span className={dollarClassName}>$</span>
            <span>{amountText}</span>
          </>
        )}
      </span>
    </Chip>
  );
}

export function MetricChips({
  className,
  items,
}: {
  className?: string;
  items: MetricChipItem[];
}) {
  const title = items
    .map((item) => `${item.label} ${formatMetricTitleValue(item)}`)
    .join(' / ');

  return (
    <div className={`ag-metric-chips ${className ?? ''}`} title={title}>
      {items.map((item, idx) => (
        <MetricChip key={`${idx}-${item.label}`} {...item} />
      ))}
    </div>
  );
}
