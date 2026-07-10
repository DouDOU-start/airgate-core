import type { CSSProperties, ReactNode } from 'react';
import { Card } from '@heroui/react';

/** 指标卡图标底色的预设色调（tailwind 类，带暗色适配） */
export type MetricTone = 'blue' | 'violet' | 'emerald' | 'teal' | 'amber' | 'indigo' | 'purple' | 'rose';

export const METRIC_TONE_CLASSES: Record<MetricTone, string> = {
  amber: 'bg-amber-100 text-amber-600 ring-amber-200 dark:bg-amber-400/15 dark:text-amber-300 dark:ring-amber-400/25',
  blue: 'bg-blue-100 text-blue-600 ring-blue-200 dark:bg-blue-400/15 dark:text-blue-300 dark:ring-blue-400/25',
  emerald: 'bg-success-subtle text-success ring-success/25',
  indigo: 'bg-indigo-100 text-indigo-600 ring-indigo-200 dark:bg-indigo-400/15 dark:text-indigo-300 dark:ring-indigo-400/25',
  purple: 'bg-purple-100 text-purple-600 ring-purple-200 dark:bg-purple-400/15 dark:text-purple-300 dark:ring-purple-400/25',
  rose: 'bg-rose-100 text-rose-600 ring-rose-200 dark:bg-rose-400/15 dark:text-rose-300 dark:ring-rose-400/25',
  teal: 'bg-teal-100 text-teal-600 ring-teal-200 dark:bg-teal-400/15 dark:text-teal-300 dark:ring-teal-400/25',
  violet: 'bg-violet-100 text-violet-600 ring-violet-200 dark:bg-violet-400/15 dark:text-violet-300 dark:ring-violet-400/25',
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
          <div className="truncate text-sm font-semibold tracking-normal text-text-tertiary">{title}</div>
          <div className="mt-1 flex min-w-0 items-baseline gap-2">
            <div className="flex min-w-0 items-baseline truncate font-mono text-[22px] font-semibold leading-none text-text 2xl:text-2xl">
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
