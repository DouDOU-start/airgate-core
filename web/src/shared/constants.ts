/** 默认分页大小 */
export const DEFAULT_PAGE_SIZE = 20;

/** 分页大小选项 */
export const PAGE_SIZE_OPTIONS = [20, 50, 100] as const;

/** 全量拉取参数（用于下拉选择等场景） */
export const FETCH_ALL_PARAMS = { page: 1, page_size: 100 } as const;

/** 头像颜色池 */
export const AVATAR_COLORS = [
  '#10b981', '#6366f1', '#f59e0b', '#ef4444', '#8b5cf6',
  '#ec4899', '#14b8a6', '#f97316', '#06b6d4', '#84cc16',
] as const;
