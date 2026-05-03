import { useMemo, type ReactNode } from 'react';
import { EmptyState, Skeleton, Table as HeroTable } from '@heroui/react';
import { Inbox } from 'lucide-react';
import type { UsageColumnConfig, UsageRow } from '../columns/usageColumns';
import { getTotalPages } from '../utils/pagination';
import { TablePaginationFooter } from './TablePaginationFooter';

const END_ALIGNED_COLUMNS = new Set(['tokens', 'cost', 'first_token_ms', 'duration_ms']);
const FULL_CELL_CONTENT_COLUMNS = new Set(['cost', 'tokens']);

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

export function UsageRecordsTable<T extends UsageRow>({
  ariaLabel,
  columns,
  emptyDescription,
  emptyTitle,
  isLoading,
  page,
  pageSize,
  rows,
  setPage,
  setPageSize,
  total,
}: {
  ariaLabel: string;
  columns: UsageColumnConfig<T>[];
  emptyDescription?: string;
  emptyTitle: string;
  isLoading: boolean;
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

  return (
    <HeroTable className="ag-usage-records-table min-h-[240px]" variant="primary">
      <HeroTable.ScrollContainer>
        <HeroTable.Content
          aria-label={ariaLabel}
          className="ag-usage-table"
          style={{ minWidth: tableMinWidth }}
        >
          <HeroTable.Header>
            {columns.map((column, index) => {
              const alignEnd = END_ALIGNED_COLUMNS.has(column.key);

              return (
                <HeroTable.Column
                  id={column.key}
                  key={column.key}
                  className={cx(
                    column.hideOnMobile && 'hidden md:table-cell',
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
              ? Array.from({ length: 6 }).map((_, rowIndex) => (
                  <HeroTable.Row id={`loading-${rowIndex}`} key={`loading-${rowIndex}`}>
                    {columns.map((column, cellIndex) => {
                      const alignEnd = END_ALIGNED_COLUMNS.has(column.key);

                      return (
                      <HeroTable.Cell
                        key={column.key}
                        className={cx(
                          column.hideOnMobile && 'hidden md:table-cell',
                          alignEnd && 'text-right',
                        )}
                      >
                        <div className={cx('flex h-[var(--ag-usage-table-row-height)] w-full items-center gap-2.5 px-2.5 py-1', alignEnd && 'justify-end')}>
                          {cellIndex === 0 ? <Skeleton className="h-7 w-7 rounded-full" /> : null}
                          <Skeleton
                            className={cx('h-4 rounded-md', cellIndex === 0 ? 'w-28' : 'w-24')}
                            style={{ animationDelay: `${rowIndex * 90 + cellIndex * 20}ms` }}
                          />
                        </div>
                      </HeroTable.Cell>
                      );
                    })}
                  </HeroTable.Row>
                ))
              : rows.map((row) => (
                  <HeroTable.Row id={String(row.id)} key={row.id}>
                    {columns.map((column) => {
                      const alignEnd = END_ALIGNED_COLUMNS.has(column.key);
                      const fullCellContent = FULL_CELL_CONTENT_COLUMNS.has(column.key);

                      return (
                      <HeroTable.Cell
                        key={column.key}
                        className={cx(
                          column.hideOnMobile && 'hidden md:table-cell',
                          alignEnd && 'text-right',
                        )}
                      >
                        <div
                          className={cx(
                            'flex h-[var(--ag-usage-table-row-height)] w-full items-center overflow-hidden',
                            !fullCellContent && 'px-2.5 py-1',
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
