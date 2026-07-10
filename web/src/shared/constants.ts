/** 默认分页大小 */
export const DEFAULT_PAGE_SIZE = 20;

/** 全量拉取参数（用于下拉选择等场景） */
export const FETCH_ALL_PARAMS = { page: 1, page_size: 100 } as const;

/** 使用记录 Token 指标色，表格与趋势图共用。
    Monolith 谱：数据蓝领衔 + 前景灰阶，彩色只留给功能语义。 */
export const USAGE_TOKEN_COLORS = {
  input: 'var(--ag-data-blue)',
  output: 'var(--ag-text-secondary)',
  cacheCreation: 'oklch(72% 0.125 82)',
  cacheRead: 'var(--ag-muted)',
  cacheRatio: 'oklch(64% 0.11 200)',
  cacheCumulativeRatio: 'var(--success)',
} as const;

/** 饼图调色板：Monolith 谱 —— 数据蓝深浅交替 + 中性灰阶，克制但可辨 */
export const PIE_CHART_COLORS = [
  'oklch(60% 0.155 250)',
  'oklch(70% 0 0)',
  'oklch(72% 0.11 220)',
  'oklch(52% 0 0)',
  'oklch(46% 0.14 255)',
  'oklch(84% 0 0)',
  'oklch(66% 0.12 190)',
  'oklch(38% 0 0)',
  'oklch(78% 0.09 240)',
  'oklch(60% 0 0)',
] as const;

/** 仪表盘时间范围预设，管理员仪表盘与用户概览共用 */
export const RANGE_PRESETS = ['today', '7d', '30d', '90d'] as const;
export type RangePreset = typeof RANGE_PRESETS[number];

/** Token 趋势图折线顺序（图例与 tooltip 共用） */
export const TOKEN_TREND_LINE_ORDER: Array<keyof typeof USAGE_TOKEN_COLORS> = ['input', 'output', 'cacheCreation', 'cacheRead', 'cacheRatio', 'cacheCumulativeRatio'];

/** 趋势图中按百分比渲染的折线 key */
export const TOKEN_TREND_RATIO_KEYS = new Set<keyof typeof USAGE_TOKEN_COLORS>(['cacheRatio', 'cacheCumulativeRatio']);

/** 头像颜色池（内嵌装饰色板） */
export { decorativePalette as AVATAR_COLORS } from './utils/theme';
