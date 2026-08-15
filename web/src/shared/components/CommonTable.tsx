import type {
  ComponentPropsWithoutRef,
  CSSProperties,
  HTMLAttributes,
  ReactElement,
  ReactNode,
  TdHTMLAttributes,
  ThHTMLAttributes,
} from 'react';
import { Children, cloneElement, createContext, isValidElement, useContext, useMemo } from 'react';
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
  mobileLayout?: 'cards' | 'scroll';
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

interface TableColumnMeta {
  id?: string;
  label: string;
}

interface CommonTableCellInternalProps extends TdHTMLAttributes<HTMLTableCellElement> {
  mobileColumnId?: string;
  mobileLabel?: string;
}

const TableColumnsContext = createContext<TableColumnMeta[]>([]);

function nodeText(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node).trim();
  if (!isValidElement(node)) return '';
  const props = node.props as { children?: ReactNode; label?: ReactNode };
  const childrenText = Children.toArray(props.children)
    .map(nodeText)
    .filter(Boolean)
    .join(' ')
    .trim();
  if (childrenText) return childrenText;
  return nodeText(props.label);
}

function findColumnMeta(children: ReactNode): TableColumnMeta[] {
  let columns: TableColumnMeta[] = [];

  Children.forEach(children, (child) => {
    if (columns.length > 0 || !isValidElement(child)) return;
    const props = child.props as { children?: ReactNode };
    if (child.type === CommonTableHeader) {
      columns = Children.toArray(props.children).map((column) => {
        if (!isValidElement(column)) return { label: '' };
        const columnProps = column.props as CommonTableColumnProps;
        return {
          id: columnProps.id,
          label: nodeText(columnProps.children),
        };
      });
      return;
    }
    columns = findColumnMeta(props.children);
  });

  return columns;
}

function CommonTableRoot({
  ariaLabel,
  children,
  className,
  contentClassName,
  contentProps,
  contentStyle,
  footer,
  minWidth,
  // 默认保留原生表格结构，复杂指标/长文本表格在手机上通过横向滚动保证可读性。
  mobileLayout = 'scroll',
  scrollClassName,
  scrollOverlay,
}: CommonTableProps) {
  const resolvedContentStyle = minWidth == null
    ? contentStyle
    : {
        minWidth,
        ...contentStyle,
      };

  const columns = useMemo(() => findColumnMeta(children), [children]);

  return (
    <TableColumnsContext.Provider value={columns}>
      <div className={cx('ag-resource-table', `ag-resource-table--mobile-${mobileLayout}`, className)}>
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
    </TableColumnsContext.Provider>
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
  const columns = useContext(TableColumnsContext);
  let columnIndex = 0;
  const labelledChildren = Children.map(children, (child) => {
    if (!isValidElement(child) || child.type !== CommonTableCell) return child;
    const column = columns[columnIndex];
    columnIndex += Math.max(Number((child.props as TdHTMLAttributes<HTMLTableCellElement>).colSpan) || 1, 1);
    return cloneElement(child as ReactElement<CommonTableCellInternalProps>, {
      mobileColumnId: column?.id,
      mobileLabel: column?.label,
    });
  });

  return (
    <tr {...props} data-key={id == null ? undefined : String(id)} data-slot="tr">
      {labelledChildren}
    </tr>
  );
}

function CommonTableCell({
  children,
  mobileColumnId,
  mobileLabel,
  ...props
}: CommonTableCellInternalProps) {
  return (
    <td
      {...props}
      data-column-id={mobileColumnId || undefined}
      data-label={mobileLabel || undefined}
      data-slot="td"
    >
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
