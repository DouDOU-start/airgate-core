import { useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import { EmptyState, Table as HeroTable } from '@heroui/react';
import { Inbox } from 'lucide-react';
import type { UsageColumnConfig, UsageRow } from '../columns/usageColumns';
import { getTotalPages } from '../utils/pagination';
import { TableLoadingRow } from './TableLoadingRow';
import { TablePaginationFooter } from './TablePaginationFooter';

const END_ALIGNED_COLUMNS = new Set(['tokens', 'cost', 'first_token_ms', 'duration_ms']);
const FULL_CELL_CONTENT_COLUMNS = new Set(['cost', 'tokens']);
const NEW_ROW_MARK_DURATION_MS = 5000;

function cx(...classes: Array<string | false | null | undefined>) {
  return classes.filter(Boolean).join(' ');
}

function parseColumnWidth(width?: string): number {
  const match = width?.match(/^(\d+(?:\.\d+)?)px$/);
  return match ? Number(match[1]) : 128;
}

function ColumnHeader({ children, alignEnd }: { children: ReactNode; alignEnd: boolean }) {
  return (
    <span
      className={cx(
        'flex h-full w-full items-center gap-2 whitespace-nowrap px-2.5 text-xs font-semibold leading-none',
        alignEnd ? 'justify-end text-right' : 'justify-start text-left',
      )}
    >
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
  const batchClearTimerRef = useRef<number | null>(null);
  const [markedRowIds, setMarkedRowIds] = useState<Set<string>>(() => new Set());

  useEffect(() => () => {
    if (batchClearTimerRef.current != null) {
      window.clearTimeout(batchClearTimerRef.current);
    }
  }, []);

  useEffect(() => {
    const clearActiveBatch = () => {
      if (batchClearTimerRef.current != null) {
        window.clearTimeout(batchClearTimerRef.current);
        batchClearTimerRef.current = null;
      }
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

    if (batchClearTimerRef.current != null) {
      window.clearTimeout(batchClearTimerRef.current);
    }
    setMarkedRowIds(new Set(addedIds));
    batchClearTimerRef.current = window.setTimeout(() => {
      batchClearTimerRef.current = null;
      setMarkedRowIds(new Set());
    }, NEW_ROW_MARK_DURATION_MS);
  }, [dataVersion, enabled, paused, resetKey, rowIds]);

  return markedRowIds;
}

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
  const markedRowIds = useNewRowMarkers({
    dataVersion,
    enabled: highlightNewRows,
    paused: isLoading || suppressHighlight,
    resetKey: highlightResetKey,
    rows,
  });

  return (
    <HeroTable className="ag-usage-records-table min-h-[240px]" variant="primary">
      <HeroTable.ScrollContainer className="ag-usage-table-scroll">
        <HeroTable.Content
          aria-label={ariaLabel}
          className="ag-usage-table"
          style={tableStyle}
        >
          <HeroTable.Header>
            {columns.map((column, index) => {
              const alignEnd = END_ALIGNED_COLUMNS.has(column.key);

              return (
                <HeroTable.Column
                  id={column.key}
                  key={column.key}
                  className={cx(
                    getColumnClassName(column.key),
                    alignEnd && 'text-end',
                    index === 0 && 'after:hidden',
                  )}
                  isRowHeader={index === 0}
                  style={column.width ? { width: column.width } : undefined}
                >
                  <ColumnHeader alignEnd={alignEnd}>{column.title}</ColumnHeader>
                </HeroTable.Column>
              );
            })}
          </HeroTable.Header>
          <HeroTable.Body
            renderEmptyState={() => (
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
            )}
          >
            {isLoading
              ? <TableLoadingRow colSpan={columns.length} />
              : rows.map((row) => (
                  <HeroTable.Row
                    id={String(row.id)}
                    key={row.id}
                    className={markedRowIds.has(String(row.id)) ? 'ag-usage-table-row--new' : undefined}
                  >
                    {columns.map((column) => {
                      const alignEnd = END_ALIGNED_COLUMNS.has(column.key);
                      const fullCellContent = FULL_CELL_CONTENT_COLUMNS.has(column.key);

                      return (
                      <HeroTable.Cell
                        key={column.key}
                        className={cx(
                          getColumnClassName(column.key),
                          alignEnd && 'text-right',
                        )}
                      >
                        <div
                          className={cx(
                            'flex h-[var(--ag-usage-table-row-height)] w-full items-center overflow-hidden',
                            fullCellContent ? 'px-1 py-0.5' : 'px-2.5 py-0.5',
                            alignEnd && 'justify-end text-right',
                          )}
                        >
                          {column.render(row)}
                        </div>
                      </HeroTable.Cell>
                      );
                    })}
                  </HeroTable.Row>
                ))}
          </HeroTable.Body>
        </HeroTable.Content>
      </HeroTable.ScrollContainer>
      <HeroTable.Footer>
        <TablePaginationFooter
          page={page}
          pageSize={pageSize}
          setPage={setPage}
          setPageSize={setPageSize}
          total={total}
          totalPages={totalPages}
        />
      </HeroTable.Footer>
    </HeroTable>
  );
}
