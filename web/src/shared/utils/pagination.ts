export const DEFAULT_PAGINATION_PAGE_SIZE_OPTIONS = [10, 20, 50, 100] as const;

export type PaginationItem = number | '...';

export function getTotalPages(total: number, pageSize: number): number {
  return Math.max(1, Math.ceil(total / pageSize));
}

export function getPaginationItems(current: number, total: number): PaginationItem[] {
  if (total <= 7) return Array.from({ length: total }, (_, index) => index + 1);

  const pages: PaginationItem[] = [1];
  if (current > 3) pages.push('...');

  for (let index = Math.max(2, current - 1); index <= Math.min(total - 1, current + 1); index += 1) {
    pages.push(index);
  }

  if (current < total - 2) pages.push('...');
  pages.push(total);
  return pages;
}
