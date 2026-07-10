import type { CSSProperties, ReactNode } from 'react';
import { Card } from '@heroui/react';

/** 指标卡图标底色的预设色调（tailwind 类，带暗色适配） */
export type MetricTone = 'blue' | 'violet' | 'emerald' | 'teal' | 'amber' | 'indigo' | 'purple' | 'rose';

/* Monolith：黑白系统里图标底一律中性，彩色只留给语义状态。
   保留 tone API 以免改动所有调用点，但所有色调映射到同一套中性样式。 */
const NEUTRAL_TONE = 'bg-surface-secondary text-text-secondary ring-border';

export const METRIC_TONE_CLASSES: Record<MetricTone, string> = {
  amber: NEUTRAL_TONE,
  blue: NEUTRAL_TONE,
  emerald: NEUTRAL_TONE,
  indigo: NEUTRAL_TONE,
  purple: NEUTRAL_TONE,
  rose: NEUTRAL_TONE,
  teal: NEUTRAL_TONE,
  violet: NEUTRAL_TONE,
};

/**
 * 仪表盘指标卡：标题 + 数值 + 右侧图标。
 * 图标底色二选一：tone 用预设色调，accentColor 用任意 CSS 颜色（color-mix 调透明度）。
 */
export function StatCard({
  accentColor,
  icon,
  title,
  tone,
  value,
}: {
  accentColor?: string;
  icon: ReactNode;
  title: string;
  tone?: MetricTone;
  value: ReactNode;
}) {
  const accentStyle: CSSProperties | undefined = accentColor
    ? {
        background: `color-mix(in srgb, ${accentColor} 14%, transparent)`,
        color: accentColor,
        borderColor: `color-mix(in srgb, ${accentColor} 24%, transparent)`,
      }
    : undefined;

  return (
    <Card className="ag-dashboard-metric min-h-[72px] 2xl:min-h-[78px]">
      <Card.Content className="ag-dashboard-metric-content p-3 2xl:p-3.5">
        <div className="ag-dashboard-metric-copy">
          <div className="ag-kicker truncate">{title}</div>
          <div className="mt-1.5 flex min-w-0 items-baseline gap-2">
            <div className="ag-display flex min-w-0 items-baseline truncate text-[22px] leading-none tabular-nums text-text 2xl:text-2xl">
              {value}
            </div>
          </div>
        </div>
        <span
          className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-[var(--field-radius)] ring-1 shadow-sm 2xl:h-11 2xl:w-11 ${tone ? METRIC_TONE_CLASSES[tone] : ''}`}
          style={accentStyle}
        >
          {icon}
        </span>
      </Card.Content>
    </Card>
  );
}
