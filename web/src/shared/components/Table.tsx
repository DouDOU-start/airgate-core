import { useMemo, type ReactNode, type CSSProperties } from 'react';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { EmptyState } from './EmptyState';
import { useIsMobile } from '../hooks/useMediaQuery';

export interface Column<T> {
  key: string;
  title: ReactNode;
  render?: (row: T) => ReactNode;
  width?: string;
  fixed?: 'left' | 'right';
  align?: 'left' | 'center' | 'right';
  /** Hide this column in mobile card view */
  hideOnMobile?: boolean;
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
  /** 是否启用多选 */
  selectable?: boolean;
  /** 当前选中的 rowKey 列表（受控） */
  selectedKeys?: (string | number)[];
  /** 选中项变化回调 */
  onSelectionChange?: (keys: (string | number)[]) => void;
  /** @deprecated No longer needed – kept for backward compatibility */
  separateHeader?: boolean;
}

export function Table<T extends Record<string, any>>({
  columns: userColumns,
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
  selectable = false,
  selectedKeys,
  onSelectionChange,
}: TableProps<T>) {
  const totalPages = Math.ceil(total / pageSize);
  const isMobile = useIsMobile();

  // 多选逻辑：所有可见行是否全选 / 部分选
  const pageKeys: (string | number)[] = selectable && rowKey ? data.map(rowKey) : [];
  const selectedSet = new Set<string | number>(selectedKeys ?? []);
  const selectedOnPage = pageKeys.filter((k) => selectedSet.has(k));
  const allSelected = pageKeys.length > 0 && selectedOnPage.length === pageKeys.length;
  const someSelected = selectedOnPage.length > 0 && !allSelected;

  const toggleAll = () => {
    if (!onSelectionChange || !rowKey) return;
    const next = new Set(selectedKeys ?? []);
    if (allSelected) {
      pageKeys.forEach((k) => next.delete(k));
    } else {
      pageKeys.forEach((k) => next.add(k));
    }
    onSelectionChange(Array.from(next));
  };

  const toggleRow = (key: string | number) => {
    if (!onSelectionChange) return;
    const next = new Set(selectedKeys ?? []);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    onSelectionChange(Array.from(next));
  };

  // 生成受控勾选框元素（表头 + 每行）
  const headerCheckbox = (
    <input
      type="checkbox"
      aria-label="select all"
      className="cursor-pointer w-4 h-4"
      style={{ accentColor: 'var(--ag-primary)' }}
      checked={allSelected}
      ref={(el) => { if (el) el.indeterminate = someSelected; }}
      onChange={toggleAll}
      onClick={(e) => e.stopPropagation()}
    />
  );

  const selectionColumn: Column<T> | null = selectable && rowKey
    ? {
        key: '__selection__',
        width: '44px',
        fixed: 'left',
        align: 'center',
        hideOnMobile: true,
        title: headerCheckbox,
        render: (row) => {
          const key = rowKey(row);
          return (
            <input
              type="checkbox"
              aria-label="select row"
              className="cursor-pointer w-4 h-4"
      style={{ accentColor: 'var(--ag-primary)' }}
              checked={selectedSet.has(key)}
              onChange={() => toggleRow(key)}
              onClick={(e) => e.stopPropagation()}
            />
          );
        },
      }
    : null;

  const columns = selectionColumn ? [selectionColumn, ...userColumns] : userColumns;

  // 计算固定列的粘性偏移（left/right 累加），不含背景色 —— 背景色交给 className 处理，
  // 以便 group-hover 能覆盖。parseInt 忽略 "120px" 里的 px 后缀。
  const fixedStyles = useMemo(() => {
    const styles: Record<string, CSSProperties> = {};
    let leftOffset = 0;
    for (const col of columns) {
      if (col.fixed === 'left') {
        styles[col.key] = { position: 'sticky', left: leftOffset };
        leftOffset += parseInt(col.width || '0', 10);
      }
    }
    let rightOffset = 0;
    for (let i = columns.length - 1; i >= 0; i--) {
      const col = columns[i]!;
      if (col.fixed === 'right') {
        styles[col.key] = { position: 'sticky', right: rightOffset };
        rightOffset += parseInt(col.width || '0', 10);
      }
    }
    return styles;
  }, [columns]);

  const thBaseClass = 'px-5 py-3 text-xs font-semibold text-text-tertiary uppercase tracking-wider whitespace-nowrap bg-bg-elevated';
  const tdBaseClass = 'px-5 py-3 text-sm text-text-secondary whitespace-nowrap align-middle';

  // --- Pagination (shared between desktop & mobile) ---
  const pagination = onPageChange && (
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
  );

  // --- Loading ---
  if (loading) {
    if (isMobile) {
      return (
        <div className="space-y-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <div key={i} className="border border-glass-border bg-bg-elevated shadow-sm rounded-xl p-4 space-y-3">
              {Array.from({ length: 4 }).map((_, j) => (
                <div key={j} className="flex items-center justify-between">
                  <div className="h-3 ag-shimmer rounded w-16" style={{ animationDelay: `${(i * 4 + j) * 80}ms` }} />
                  <div className="h-3 ag-shimmer rounded w-24" style={{ animationDelay: `${(i * 4 + j) * 80 + 40}ms` }} />
                </div>
              ))}
            </div>
          ))}
        </div>
      );
    }
    return (
      <div className="border border-glass-border bg-bg-elevated shadow-sm rounded-xl overflow-hidden">
        <div className="flex gap-4 px-4 py-3 border-b border-border bg-black/[0.03]">
          {columns.map((col) => (
            <div key={col.key} className="h-4 ag-shimmer rounded w-20" style={{ flex: col.width ? `0 0 ${col.width}` : '1' }} />
          ))}
        </div>
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

  // --- Empty ---
  if (data.length === 0) {
    return (
      <div className="border border-glass-border bg-bg-elevated shadow-sm rounded-xl">
        <EmptyState />
      </div>
    );
  }

  // --- Mobile card view ---
  if (isMobile) {
    const mobileColumns = columns.filter((col) => !col.hideOnMobile);
    const actionCol = mobileColumns.find((col) => col.key === 'actions');
    const fieldCols = mobileColumns.filter((col) => col.key !== 'actions');
    // First column as card header, rest as detail rows
    const [headerCol, ...detailCols] = fieldCols;

    return (
      <div className="space-y-4">
        <div className="space-y-3">
          {data.map((row, i) => (
            <div
              key={rowKey ? rowKey(row) : i}
              className="border border-glass-border bg-bg-elevated shadow-sm rounded-xl overflow-hidden"
            >
              {/* Card header: first column + actions */}
              {headerCol && (
                <div className="flex items-center justify-between gap-2 px-4 pt-3 pb-2">
                  <div className="text-sm text-text font-medium min-w-0 flex-1">
                    {headerCol.render ? headerCol.render(row) : String(row[headerCol.key] ?? '')}
                  </div>
                  {actionCol && (
                    <div className="flex items-center shrink-0">
                      {actionCol.render ? actionCol.render(row) : null}
                    </div>
                  )}
                </div>
              )}
              {/* Detail rows */}
              {detailCols.length > 0 && (
                <div className="px-4 pb-3 pt-1 space-y-0.5">
                  {detailCols.map((col) => (
                    <div key={col.key} className="flex items-center justify-between py-1 min-h-[28px]">
                      <span className="text-[11px] text-text-tertiary shrink-0 mr-3">{col.title}</span>
                      <div className="text-xs text-text-secondary">
                        {col.render ? col.render(row) : String(row[col.key] ?? '')}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
        {pagination}
      </div>
    );
  }

  // --- Desktop table view ---
  //
  // 结构：单一 <table> 放在单一 overflow:auto 容器内。
  //  - <thead> 的每个 <th> 用 `sticky top-0` 纵向钉顶
  //  - fixed:left/right 的列再叠加 `position:sticky; left/right:X` 横向钉边
  //  - 两个 sticky 复合生效 → 左上/右上角的 <th> 同时钉顶 + 钉边
  //  - 使用 border-separate + border-spacing:0；border-collapse 下 sticky 在 cell 上
  //    在部分浏览器（尤其 Safari）有残留/抖动 bug
  //  - 固定列的背景走 Tailwind class 而非 inline style，这样 group-hover 能正确覆盖 hover 态

  const colGroup = (
    <colgroup>
      {columns.map((col) => <col key={col.key} style={{ width: col.width }} />)}
    </colgroup>
  );

  const tableEl = (
    <table
      className="w-full border-separate"
      style={{ borderSpacing: 0, minWidth: 'max-content' }}
    >
      {colGroup}
      <thead>
        <tr>
          {columns.map((col) => {
            const align = col.align || 'center';
            const textAlign = align === 'left' ? 'text-left' : align === 'right' ? 'text-right' : 'text-center';
            const isFixed = !!col.fixed;
            return (
              <th
                key={col.key}
                className={`${thBaseClass} ${textAlign} sticky top-0`}
                style={{
                  ...(fixedStyles[col.key] || {}),
                  // 固定列交叉点需要更高 z 以盖住普通表头和 body 固定单元格
                  zIndex: isFixed ? 30 : 20,
                  boxShadow: '0 1px 0 var(--ag-border)',
                }}
              >
                {col.title}
              </th>
            );
          })}
        </tr>
      </thead>
      <tbody>
        {data.map((row, i) => {
          const isLast = i === data.length - 1;
          return (
            <tr
              key={rowKey ? rowKey(row) : i}
              className="group transition-colors"
            >
              {columns.map((col) => {
                const align = col.align || 'center';
                const justify = align === 'left' ? 'justify-start' : align === 'right' ? 'justify-end' : 'justify-center';
                const isFixed = !!col.fixed;
                // 固定列自带背景色，并随行一起 group-hover。普通列的 hover 也走 group-hover。
                // border-separate 下 tr 的 border 不生效，改为放在 td 底部。
                const bgClass = isFixed
                  ? 'bg-bg-elevated group-hover:bg-bg-hover'
                  : 'group-hover:bg-bg-hover';
                const borderClass = isLast ? '' : 'border-b border-border-subtle';
                return (
                  <td
                    key={col.key}
                    className={`${tdBaseClass} ${borderClass} ${bgClass}`}
                    style={fixedStyles[col.key] ? { ...fixedStyles[col.key], zIndex: 10 } : undefined}
                  >
                    <div className={`flex items-center ${justify}`}>
                      {col.render ? col.render(row) : String(row[col.key] ?? '')}
                    </div>
                  </td>
                );
              })}
            </tr>
          );
        })}
      </tbody>
    </table>
  );

  return (
    <div className="space-y-4">
      <div
        className="border border-glass-border bg-bg-elevated shadow-sm rounded-xl overflow-auto"
        style={autoHeight ? undefined : { maxHeight: '494px' }}
      >
        {tableEl}
      </div>

      {/* 分页 */}
      {pagination}
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
