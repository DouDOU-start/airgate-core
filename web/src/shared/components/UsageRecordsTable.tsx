import { memo, useCallback, useEffect, useMemo, useRef, useState, type AnimationEvent, type CSSProperties, type ReactNode } from 'react';
import { EmptyState } from '@heroui/react';
import { Inbox } from 'lucide-react';
import type { UsageColumnConfig, UsageRow } from '../columns/usageColumns';
import { getTotalPages } from '../utils/pagination';
import { TableLoadingRow } from './TableLoadingRow';
import { TablePaginationFooter } from './TablePaginationFooter';

const FULL_CELL_CONTENT_COLUMNS = new Set(['cost', 'tokens']);
const LEFT_ALIGNED_CONTENT_COLUMNS = new Set<string>(['model']);
const NEW_ROW_ANIMATION_NAME = 'ag-usage-row-new-enter';

function cx(...classes: Array<string | false | null | undefined>) {
  return classes.filter(Boolean).join(' ');
}

function parseColumnWidth(width?: string): number {
  const match = width?.match(/^(\d+(?:\.\d+)?)px$/);
  return match ? Number(match[1]) : 128;
}

function ColumnHeader({ children }: { children: ReactNode }) {
  return (
    <span className="flex h-full w-full items-center justify-center gap-2 whitespace-nowrap px-2.5 text-center text-xs font-semibold leading-none">
      {children}
    </span>
  );
}

function getColumnClassName(key: string) {
  return `ag-usage-col-${key.replace(/[^a-zA-Z0-9_-]/g, '-')}`;
}

function useNewRowMarkers<T extends UsageRow>({
  dataVersion,
  enabled,
  paused,
  resetKey,
  rows,
}: {
  dataVersion?: number;
  enabled: boolean;
  paused: boolean;
  resetKey?: string;
  rows: T[];
}) {
  const rowIds = useMemo(() => rows.map((row) => String(row.id)), [rows]);
  const previousRowIdsRef = useRef<Set<string> | null>(null);
  const previousResetKeyRef = useRef<string | undefined>(undefined);
  const [markedRowIds, setMarkedRowIds] = useState<Set<string>>(() => new Set());

  const clearMarkedRowId = useCallback((rowId: string) => {
    setMarkedRowIds((current) => {
      if (!current.has(rowId)) {
        return current;
      }
      const next = new Set(current);
      next.delete(rowId);
      return next;
    });
  }, []);

  useEffect(() => {
    const clearActiveBatch = () => {
      setMarkedRowIds((current) => (current.size === 0 ? current : new Set()));
    };

    if (paused) {
      clearActiveBatch();
      return;
    }

    const currentIds = new Set(rowIds);
    const resetChanged = previousResetKeyRef.current !== resetKey;

    if (resetChanged || !enabled || rowIds.length === 0) {
      previousResetKeyRef.current = resetKey;
      previousRowIdsRef.current = currentIds;
      clearActiveBatch();
      return;
    }

    const previousIds = previousRowIdsRef.current;
    previousResetKeyRef.current = resetKey;
    previousRowIdsRef.current = currentIds;

    if (!previousIds) {
      return;
    }

    const addedIds = rowIds.filter((id) => !previousIds.has(id));
    if (addedIds.length === 0) {
      return;
    }

    setMarkedRowIds(new Set(addedIds));
  }, [dataVersion, enabled, paused, resetKey, rowIds]);

  return { clearMarkedRowId, markedRowIds };
}

const UsageTableRow = memo(function UsageTableRow({
  columns,
  isNew,
  onNewAnimationEnd,
  row,
}: {
  columns: UsageColumnConfig[];
  isNew: boolean;
  onNewAnimationEnd: (rowId: string) => void;
  row: UsageRow;
}) {
  const rowId = String(row.id);
  const handleAnimationEnd = (event: AnimationEvent<HTMLTableRowElement>) => {
    if (event.animationName === NEW_ROW_ANIMATION_NAME) {
      onNewAnimationEnd(rowId);
    }
  };

  return (
    <tr
      data-key={rowId}
      data-slot="tr"
      className={isNew ? 'ag-usage-table-row--new' : undefined}
      onAnimationEnd={isNew ? handleAnimationEnd : undefined}
    >
      {columns.map((column) => {
        const fullCellContent = FULL_CELL_CONTENT_COLUMNS.has(column.key);
        const leftAlignedContent = LEFT_ALIGNED_CONTENT_COLUMNS.has(column.key);

        return (
          <td
            data-slot="td"
            key={column.key}
            className={cx(getColumnClassName(column.key), leftAlignedContent ? 'text-left' : 'text-center')}
          >
            <div
              className={cx(
                'flex h-[var(--ag-usage-table-row-height)] w-full items-center overflow-hidden',
                leftAlignedContent ? 'justify-start text-left' : 'justify-center text-center',
                fullCellContent ? 'px-1 py-0.5' : 'px-2.5 py-0.5',
              )}
            >
              {column.render(row)}
            </div>
          </td>
        );
      })}
    </tr>
  );
});

export function UsageRecordsTable<T extends UsageRow>({
  ariaLabel,
  columns,
  dataVersion,
  emptyDescription,
  emptyTitle,
  highlightNewRows = false,
  highlightResetKey,
  isLoading,
  suppressHighlight = false,
  page,
  pageSize,
  rows,
  setPage,
  setPageSize,
  total,
}: {
  ariaLabel: string;
  columns: UsageColumnConfig<T>[];
  dataVersion?: number;
  emptyDescription?: string;
  emptyTitle: string;
  highlightNewRows?: boolean;
  highlightResetKey?: string;
  isLoading: boolean;
  suppressHighlight?: boolean;
  page: number;
  pageSize: number;
  rows: T[];
  setPage: (page: number) => void;
  setPageSize: (pageSize: number) => void;
  total: number;
}) {
  const totalPages = getTotalPages(total, pageSize);
  const tableMinWidth = useMemo(
    () => Math.max(760, columns.reduce((sum, column) => sum + parseColumnWidth(column.width), 0) + 24),
    [columns],
  );
  const tableMobileWidthDelta = useMemo(
    () => columns.reduce((sum, column) => {
      if (column.key !== 'created_at') return sum;
      return sum + Math.max(0, parseColumnWidth(column.width) - 92);
    }, 0),
    [columns],
  );
  const tableStyle = useMemo(
    () => ({
      minWidth: tableMinWidth,
      '--ag-usage-table-min-width': `${tableMinWidth}px`,
      '--ag-usage-table-mobile-delta': `${tableMobileWidthDelta}px`,
    }) as CSSProperties,
    [tableMinWidth, tableMobileWidthDelta],
  );
  const { clearMarkedRowId, markedRowIds } = useNewRowMarkers({
    dataVersion,
    enabled: highlightNewRows,
    paused: isLoading || suppressHighlight,
    resetKey: highlightResetKey,
    rows,
  });

  const emptyState = (
    <EmptyState className="flex min-h-[220px] w-full flex-col items-center justify-center gap-3 text-center">
      <div className="flex h-11 w-11 items-center justify-center rounded-[var(--field-radius)] bg-default text-muted shadow-sm">
        <Inbox className="h-5 w-5" />
      </div>
      <div className="space-y-1">
        <div className="text-sm font-medium text-text">{emptyTitle}</div>
        {emptyDescription ? (
          <div className="text-xs text-text-tertiary">{emptyDescription}</div>
        ) : null}
      </div>
    </EmptyState>
  );

  return (
    <div className="ag-usage-records-table min-h-[240px]">
      <div className="ag-usage-table-scroll" data-slot="wrapper">
        <table
          aria-label={ariaLabel}
          className="ag-usage-table"
          data-slot="table"
          style={tableStyle}
        >
          <thead data-slot="thead">
            <tr data-slot="tr">
              {columns.map((column, index) => (
                <th
                  data-row-header={index === 0 || undefined}
                  data-slot="th"
                  id={column.key}
                  key={column.key}
                  scope="col"
                  className={cx(
                    getColumnClassName(column.key),
                    index === 0 && 'after:hidden',
                  )}
                  style={column.width ? { width: column.width } : undefined}
                >
                  <ColumnHeader>{column.title}</ColumnHeader>
                </th>
              ))}
            </tr>
          </thead>
          <tbody data-slot="tbody">
            {isLoading
              ? <TableLoadingRow colSpan={columns.length} />
              : rows.length === 0
                ? (
                  <tr data-key="empty" data-slot="tr">
                    <td colSpan={columns.length} data-slot="td">
                      {emptyState}
                    </td>
                  </tr>
                )
              : rows.map((row) => (
                  <UsageTableRow
                    key={row.id}
                    columns={columns as UsageColumnConfig[]}
                    isNew={markedRowIds.has(String(row.id))}
                    onNewAnimationEnd={clearMarkedRowId}
                    row={row}
                  />
                ))}
          </tbody>
        </table>
      </div>
      <div className="table__footer" data-slot="table-footer">
        <TablePaginationFooter
          page={page}
          pageSize={pageSize}
          setPage={setPage}
          setPageSize={setPageSize}
          total={total}
          totalPages={totalPages}
        />
      </div>
    </div>
  );
}
