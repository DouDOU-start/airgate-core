import { PieChart, Pie, Cell, Tooltip as RechartsTooltip } from 'recharts';
import { PIE_CHART_COLORS } from '../constants';
import { fmtNum } from '../utils/format';

type PieTooltipPayload = Array<{
  name?: unknown;
  payload?: {
    name?: unknown;
  };
}>;

/** 饼图悬浮提示：只显示扇区名称 */
export function PieNameTooltip({
  active,
  payload,
}: {
  active?: boolean;
  payload?: PieTooltipPayload;
}) {
  const name = payload?.[0]?.payload?.name ?? payload?.[0]?.name;
  if (!active || name == null || name === '') return null;

  return (
    <div className="max-w-56 truncate rounded-[var(--radius)] border border-border bg-surface px-2.5 py-1.5 text-xs font-medium text-text shadow-lg">
      {String(name)}
    </div>
  );
}

export interface UsagePieChartItem {
  name: string;
  value: number;
}

/** 176×176 环形分布图，仪表盘/使用统计共用 */
export function UsagePieChart({ data }: { data: UsagePieChartItem[] }) {
  return (
    <PieChart width={176} height={176}>
      <Pie
        data={data}
        cx="50%"
        cy="50%"
        innerRadius={42}
        outerRadius={68}
        dataKey="value"
        isAnimationActive={false}
        minAngle={3}
        stroke="var(--ag-surface)"
        strokeWidth={2}
      >
        {data.map((_, i) => (
          <Cell key={i} fill={PIE_CHART_COLORS[i % PIE_CHART_COLORS.length]} />
        ))}
      </Pie>
      <RechartsTooltip
        animationDuration={0}
        content={<PieNameTooltip />}
        cursor={false}
        isAnimationActive={false}
      />
    </PieChart>
  );
}

/**
 * 折线图悬浮提示：色点 + 系列名（取 Line 的 name，缺省回退 dataKey）+ 数值。
 * order 可选，按给定 dataKey 顺序排序系列；未列出的排在末尾。
 */
export function ChartLineTooltip({
  active,
  label,
  order,
  payload,
}: {
  active?: boolean;
  label?: string;
  order?: readonly string[];
  payload?: Array<{ color?: string; dataKey?: string; name?: string; value?: number }>;
}) {
  if (!active || !payload?.length) return null;

  const items = order
    ? [...payload].sort((a, b) => {
        const aIndex = order.indexOf(String(a.dataKey));
        const bIndex = order.indexOf(String(b.dataKey));
        return (aIndex < 0 ? order.length : aIndex) - (bIndex < 0 ? order.length : bIndex);
      })
    : payload;

  return (
    <div className="rounded-[var(--radius)] border border-border bg-surface px-3 py-2 text-xs text-text shadow-lg">
      <div className="mb-1 font-medium">{label}</div>
      <div className="space-y-1">
        {items.map((item) => (
          <div key={`${item.dataKey}-${item.name}`} className="flex items-center gap-2">
            <span className="h-2 w-2 rounded-full" style={{ background: item.color }} />
            <span className="text-text">{item.name ?? item.dataKey}</span>
            <span className="font-mono">{fmtNum(Number(item.value ?? 0))}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
