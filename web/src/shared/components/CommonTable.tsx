import type {
  ComponentPropsWithoutRef,
  CSSProperties,
  HTMLAttributes,
  ReactNode,
  TdHTMLAttributes,
  ThHTMLAttributes,
} from 'react';
import { cx } from '../utils/cx';

type NativeTableProps = ComponentPropsWithoutRef<'table'>;

const DEFAULT_ROW_HEADER_COLUMN_IDS = new Set(['action', 'email', 'id', 'name', 'user_id']);

interface CommonTableProps {
  ariaLabel: string;
  children: ReactNode;
  className?: string;
  contentClassName?: string;
  contentProps?: Omit<NativeTableProps, 'aria-label' | 'children' | 'className' | 'style'>;
  contentStyle?: CSSProperties;
  footer?: ReactNode;
  minWidth?: number | string;
  scrollClassName?: string;
  scrollOverlay?: ReactNode;
}

type CommonTableColumnProps = Omit<ThHTMLAttributes<HTMLTableCellElement>, 'id'> & {
  id?: string;
  isRowHeader?: boolean;
};
type CommonTableRowProps = Omit<HTMLAttributes<HTMLTableRowElement>, 'id'> & {
  id?: string | number;
};

function CommonTableRoot({
  ariaLabel,
  children,
  className,
  contentClassName,
  contentProps,
  contentStyle,
  footer,
  minWidth,
  scrollClassName,
  scrollOverlay,
}: CommonTableProps) {
  const resolvedContentStyle = minWidth == null
    ? contentStyle
    : {
        minWidth,
        ...contentStyle,
      };

  return (
    <div className={cx('ag-resource-table', className)}>
      <div className={cx('ag-resource-table-scroll', scrollClassName)} data-slot="wrapper">
        {scrollOverlay}
        <table
          {...contentProps}
          aria-label={ariaLabel}
          className={cx('ag-resource-table-content', contentClassName)}
          data-slot="table"
          style={resolvedContentStyle}
        >
          {children}
        </table>
      </div>
      {footer ? (
        <div className="table__footer" data-slot="table-footer">
          {footer}
        </div>
      ) : null}
    </div>
  );
}

function CommonTableHeader({ children, ...props }: HTMLAttributes<HTMLTableSectionElement>) {
  return (
    <thead data-slot="thead" {...props}>
      <tr data-slot="tr">{children}</tr>
    </thead>
  );
}

function CommonTableBody({ children, ...props }: HTMLAttributes<HTMLTableSectionElement>) {
  return (
    <tbody data-slot="tbody" {...props}>
      {children}
    </tbody>
  );
}

function CommonTableColumn({ id, isRowHeader, children, ...props }: CommonTableColumnProps) {
  const shouldMarkRowHeader =
    isRowHeader ?? (typeof id === 'string' && DEFAULT_ROW_HEADER_COLUMN_IDS.has(id));

  return (
    <th
      {...props}
      data-row-header={shouldMarkRowHeader || undefined}
      data-slot="th"
      id={id}
      scope="col"
    >
      {children}
    </th>
  );
}

function CommonTableRow({ id, children, ...props }: CommonTableRowProps) {
  return (
    <tr {...props} data-key={id == null ? undefined : String(id)} data-slot="tr">
      {children}
    </tr>
  );
}

function CommonTableCell({ children, ...props }: TdHTMLAttributes<HTMLTableCellElement>) {
  return (
    <td {...props} data-slot="td">
      {children}
    </td>
  );
}

export const CommonTable = Object.assign(CommonTableRoot, {
  Body: CommonTableBody,
  Cell: CommonTableCell,
  Column: CommonTableColumn,
  Header: CommonTableHeader,
  Row: CommonTableRow,
});
