import { useRef, type ReactNode } from 'react';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { EmptyState } from './EmptyState';

export interface Column<T> {
  key: string;
  title: string;
  render?: (row: T) => ReactNode;
  width?: string;
  fixed?: 'left' | 'right';
}

export interface TableProps<T> {
  columns: Column<T>[];
  data: T[];
  loading?: boolean;
  rowKey?: (row: T) => string | number;
  page?: number;
  pageSize?: number;
  total?: number;
  onPageChange?: (page: number) => void;
  pageSizeOptions?: number[];
  onPageSizeChange?: (size: number) => void;
  autoHeight?: boolean;
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function Table<T extends Record<string, any>>({
  columns,
  data,
  loading = false,
  rowKey,
  page = 1,
  pageSize = 20,
  total = 0,
  onPageChange,
  pageSizeOptions = [10, 20, 50, 100],
  onPageSizeChange,
  autoHeight = false,
}: TableProps<T>) {
  const headerRef = useRef<HTMLDivElement>(null);
  const totalPages = Math.ceil(total / pageSize);

  if (loading) {
    return (
      <div className="border border-glass-border bg-bg-elevated shadow-sm rounded-xl overflow-hidden">
        {/* 表头骨架 */}
        <div className="flex gap-4 px-4 py-3 border-b border-border bg-black/[0.03]">
          {columns.map((col) => (
            <div key={col.key} className="h-4 ag-shimmer rounded w-20" style={{ flex: col.width ? `0 0 ${col.width}` : '1' }} />
          ))}
        </div>
        {/* 行骨架 */}
        {Array.from({ length: 5 }).map((_, i) => (
          <div key={i} className="flex gap-4 px-4 py-3.5 border-b border-border-subtle">
            {columns.map((col) => (
              <div key={col.key} className="h-4 ag-shimmer rounded w-24" style={{ flex: col.width ? `0 0 ${col.width}` : '1', animationDelay: `${i * 100}ms` }} />
            ))}
          </div>
        ))}
      </div>
    );
  }

  if (data.length === 0) {
    return (
      <div className="border border-glass-border bg-bg-elevated shadow-sm rounded-xl">
        <EmptyState />
      </div>
    );
  }

  const tableBody = (
    <tbody>
      {data.map((row, i) => (
        <tr
          key={rowKey ? rowKey(row) : i}
          className="border-b border-border-subtle last:border-0 transition-colors hover:bg-bg-hover"
        >
          {columns.map((col) => (
            <td
              key={col.key}
              className="px-4 py-3 text-sm text-text-secondary whitespace-nowrap"
            >
              <div className="flex items-center justify-center">
                {col.render ? col.render(row) : String(row[col.key] ?? '')}
              </div>
            </td>
          ))}
        </tr>
      ))}
    </tbody>
  );

  const colGroup = (
    <colgroup>
      {columns.map((col) => (
        <col key={col.key} style={{ width: col.width }} />
      ))}
    </colgroup>
  );

  return (
    <div className="space-y-4">
      {autoHeight ? (
        <div className="border border-glass-border bg-bg-elevated shadow-sm rounded-xl overflow-x-auto">
          <table className="w-full min-w-max">
            {colGroup}
            <thead className="border-b border-border bg-black/[0.03]">
              <tr>
                {columns.map((col) => (
                  <th
                    key={col.key}
                    className="px-4 py-3 text-center text-[10px] font-semibold text-text-tertiary uppercase tracking-widest"
                  >
                    {col.title}
                  </th>
                ))}
              </tr>
            </thead>
            {tableBody}
          </table>
        </div>
      ) : (
        <div className="border border-glass-border bg-bg-elevated shadow-sm rounded-xl overflow-hidden flex flex-col" style={{ height: '494px' }}>
          {/* 固定表头 — 仅水平跟随滚动 */}
          <div className="flex-shrink-0 overflow-hidden border-b border-border" ref={headerRef}>
            <table className="w-full min-w-max">
              {colGroup}
              <thead>
                <tr>
                  {columns.map((col) => (
                    <th
                      key={col.key}
                      className="px-4 py-3 text-center text-[10px] font-semibold text-text-tertiary uppercase tracking-widest bg-bg-elevated"
                    >
                      {col.title}
                    </th>
                  ))}
                </tr>
              </thead>
            </table>
          </div>
          {/* 可滚动表体 */}
          <div className="flex-1 overflow-auto" onScroll={(e) => {
            if (headerRef.current) {
              headerRef.current.scrollLeft = (e.target as HTMLDivElement).scrollLeft;
            }
          }}>
            <table className="w-full min-w-max">
              {colGroup}
              {tableBody}
            </table>
          </div>
        </div>
      )}

      {/* 分页 */}
      {onPageChange && (
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="text-xs text-text-tertiary font-mono">
              共 {total} 条 · 第 {page}/{totalPages} 页
            </span>
            {onPageSizeChange && (
              <select
                value={pageSize}
                onChange={(e) => { onPageSizeChange(Number(e.target.value)); onPageChange(1); }}
                className="text-xs text-text-secondary bg-transparent border border-glass-border rounded px-1.5 py-0.5 cursor-pointer hover:border-primary transition-colors outline-none"
              >
                {pageSizeOptions.map((s) => (
                  <option key={s} value={s}>{s} 条/页</option>
                ))}
              </select>
            )}
          </div>
          <div className="flex items-center gap-1">
            <button
              className="flex items-center justify-center w-8 h-8 rounded-sm text-text-secondary hover:bg-bg-hover disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
              disabled={page <= 1}
              onClick={() => onPageChange(page - 1)}
            >
              <ChevronLeft className="w-4 h-4" />
            </button>
            {generatePageNumbers(page, totalPages).map((p, i) =>
              p === '...' ? (
                <span key={`ellipsis-${i}`} className="w-8 text-center text-text-tertiary text-xs">
                  ···
                </span>
              ) : (
                <button
                  key={p}
                  className={`flex items-center justify-center w-8 h-8 rounded-sm text-xs font-medium transition-all ${
                    p === page
                      ? 'bg-primary text-text-inverse shadow-md'
                      : 'text-text-secondary hover:bg-bg-hover'
                  }`}
                  onClick={() => onPageChange(p as number)}
                >
                  {p}
                </button>
              ),
            )}
            <button
              className="flex items-center justify-center w-8 h-8 rounded-sm text-text-secondary hover:bg-bg-hover disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
              disabled={page >= totalPages}
              onClick={() => onPageChange(page + 1)}
            >
              <ChevronRight className="w-4 h-4" />
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

/** 生成分页页码 */
function generatePageNumbers(current: number, total: number): (number | string)[] {
  if (total <= 7) return Array.from({ length: total }, (_, i) => i + 1);
  const pages: (number | string)[] = [1];
  if (current > 3) pages.push('...');
  for (let i = Math.max(2, current - 1); i <= Math.min(total - 1, current + 1); i++) {
    pages.push(i);
  }
  if (current < total - 2) pages.push('...');
  pages.push(total);
  return pages;
}
