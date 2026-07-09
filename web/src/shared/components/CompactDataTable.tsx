import { type CSSProperties, type ReactNode } from 'react';
import { cx } from '../utils/cx';

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
    <div className={cx('ag-compact-data-table', className)}>
      <div data-slot="wrapper">
        <table
          aria-label={ariaLabel}
          className="ag-compact-data-table-content"
          data-slot="table"
          style={minWidth ? { minWidth } : undefined}
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
                  className={column.align === 'end' ? 'text-right' : undefined}
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
                </th>
              ))}
            </tr>
          </thead>
          <tbody data-slot="tbody">
            {rows.length === 0 ? (
              <tr data-key="empty" data-slot="tr">
                <td colSpan={columns.length} data-slot="td">
                  <div className="ag-compact-data-table-empty">{emptyText}</div>
                </td>
              </tr>
            ) : rows.map((row, rowIndex) => {
                const key = rowKey(row, rowIndex);

                return (
                  <tr data-key={String(key)} data-slot="tr" key={key}>
                    {columns.map((column) => (
                      <td
                        data-slot="td"
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
                      </td>
                    ))}
                  </tr>
                );
              })}
          </tbody>
        </table>
      </div>
    </div>
  );
}
