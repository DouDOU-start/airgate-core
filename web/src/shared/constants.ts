/** 默认分页大小 */
export const DEFAULT_PAGE_SIZE = 20;

/** 全量拉取参数（用于下拉选择等场景） */
export const FETCH_ALL_PARAMS = { page: 1, page_size: 100 } as const;

/** 使用记录 Token 指标色，表格与趋势图共用。
    有机花园谱：每个指标一个独立色调，克制但可辨，跟随主题明暗自适应。 */
export const USAGE_TOKEN_COLORS = {
  input: 'var(--ag-tone-blue)',
  output: 'var(--ag-tone-teal)',
  cacheCreation: 'var(--ag-tone-amber)',
  cacheRead: 'var(--ag-muted)',
  cacheRatio: 'var(--ag-tone-violet)',
  cacheCumulativeRatio: 'var(--success)',
} as const;

/** 饼图调色板：有机花园谱 —— 苔藓绿领衔，陶土橙、鼠尾草蓝等辅色轮转 */
export const PIE_CHART_COLORS = [
  'var(--ag-tone-emerald)',
  'var(--ag-tone-blue)',
  'var(--ag-secondary)',
  'var(--ag-tone-violet)',
  'var(--ag-tone-teal)',
  'var(--ag-tone-amber)',
  'var(--ag-tone-indigo)',
  'var(--ag-tone-rose)',
  'var(--ag-tone-purple)',
  'var(--ag-muted)',
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
