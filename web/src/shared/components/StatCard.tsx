import type { CSSProperties, ReactNode } from 'react';
import { Card } from '@heroui/react';

/** 指标卡图标底色的预设色调：有机花园调色板，每个色调映射到 theme-vars.css 里的 --ag-tone-* */
export type MetricTone = 'blue' | 'violet' | 'emerald' | 'teal' | 'amber' | 'indigo' | 'purple' | 'rose';

export const METRIC_TONE_VARS: Record<MetricTone, string> = {
  amber: '--ag-tone-amber',
  blue: '--ag-tone-blue',
  emerald: '--ag-tone-emerald',
  indigo: '--ag-tone-indigo',
  purple: '--ag-tone-purple',
  rose: '--ag-tone-rose',
  teal: '--ag-tone-teal',
  violet: '--ag-tone-violet',
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
  const toneColor = accentColor ?? (tone ? `var(${METRIC_TONE_VARS[tone]})` : undefined);
  const iconStyle: CSSProperties | undefined = toneColor
    ? {
        background: `color-mix(in oklab, ${toneColor} 14%, transparent)`,
        color: toneColor,
        '--tw-ring-color': `color-mix(in oklab, ${toneColor} 26%, transparent)`,
      } as CSSProperties
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
          className="flex h-10 w-10 shrink-0 items-center justify-center rounded-[var(--field-radius)] ring-1 ring-border shadow-sm 2xl:h-11 2xl:w-11"
          style={iconStyle}
        >
          {icon}
        </span>
      </Card.Content>
    </Card>
  );
}
