import { type CSSProperties, type ReactNode } from 'react';
import { Table as HeroTable } from '@heroui/react';

type RowKey = string | number;

export interface CompactDataTableColumn<T> {
  align?: 'start' | 'end';
  key: string;
  render: (row: T, index: number) => ReactNode;
  title: ReactNode;
  width?: CSSProperties['width'];
}

interface CompactDataTableProps<T> {
  ariaLabel: string;
  className?: string;
  columns: CompactDataTableColumn<T>[];
  emptyText: ReactNode;
  minWidth?: CSSProperties['minWidth'];
  rowKey: (row: T, index: number) => RowKey;
  rows: T[];
}

function cx(...classes: Array<string | false | null | undefined>) {
  return classes.filter(Boolean).join(' ');
}

export function CompactDataTable<T>({
  ariaLabel,
  className,
  columns,
  emptyText,
  minWidth,
  rowKey,
  rows,
}: CompactDataTableProps<T>) {
  return (
    <HeroTable className={cx('ag-compact-data-table', className)} variant="secondary">
      <HeroTable.ScrollContainer>
        <HeroTable.Content
          aria-label={ariaLabel}
          className="ag-compact-data-table-content"
          style={minWidth ? { minWidth } : undefined}
        >
          <HeroTable.Header>
            {columns.map((column, index) => (
              <HeroTable.Column
                id={column.key}
                key={column.key}
                className={column.align === 'end' ? 'text-right' : undefined}
                isRowHeader={index === 0}
                style={column.width ? { width: column.width } : undefined}
              >
                <span
                  className={cx(
                    'ag-compact-data-table-heading',
                    column.align === 'end' ? 'justify-end text-right' : 'justify-start text-left',
                  )}
                >
                  {column.title}
                </span>
              </HeroTable.Column>
            ))}
          </HeroTable.Header>
          <HeroTable.Body
            renderEmptyState={() => (
              <div className="ag-compact-data-table-empty">{emptyText}</div>
            )}
          >
            {rows.map((row, rowIndex) => {
              const key = rowKey(row, rowIndex);

              return (
                <HeroTable.Row id={key} key={key}>
                  {columns.map((column) => (
                    <HeroTable.Cell
                      key={column.key}
                      className={column.align === 'end' ? 'text-right' : undefined}
                    >
                      <div
                        className={cx(
                          'ag-compact-data-table-cell',
                          column.align === 'end' ? 'justify-end text-right' : 'justify-start text-left',
                        )}
                      >
                        {column.render(row, rowIndex)}
                      </div>
                    </HeroTable.Cell>
                  ))}
                </HeroTable.Row>
              );
            })}
          </HeroTable.Body>
        </HeroTable.Content>
      </HeroTable.ScrollContainer>
    </HeroTable>
  );
}
