/** 默认分页大小 */
export const DEFAULT_PAGE_SIZE = 20;

/** 分页大小选项 */
export const PAGE_SIZE_OPTIONS = [20, 50, 100] as const;

/** 全量拉取参数（用于下拉选择等场景） */
export const FETCH_ALL_PARAMS = { page: 1, page_size: 100 } as const;

/** 使用记录 Token 指标色，表格与趋势图共用 */
export const USAGE_TOKEN_COLORS = {
  input: '#10b981',
  output: '#0ea5e9',
  cacheCreation: '#f59e0b',
  cacheRead: 'var(--ag-muted)',
  cacheRatio: '#c084fc',
  cacheCumulativeRatio: 'var(--success)',
} as const;

/** 饼图专用低饱和调色板，避免分布图在黑色主题下过于刺眼 */
export const PIE_CHART_COLORS = [
  'oklch(64% 0.105 253.83)',
  'oklch(66% 0.095 156)',
  'oklch(72% 0.105 76)',
  'oklch(65% 0.1 24)',
  'oklch(65% 0.095 298)',
  'oklch(68% 0.085 205)',
  'oklch(67% 0.09 338)',
  'oklch(62% 0.075 262)',
  'oklch(70% 0.085 124)',
  'oklch(66% 0.085 48)',
] as const;

/** 头像颜色池（内嵌装饰色板） */
export { decorativePalette as AVATAR_COLORS } from './utils/theme';
