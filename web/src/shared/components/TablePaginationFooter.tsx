import { ListBox, Pagination, Select } from '@heroui/react';
import { DEFAULT_PAGINATION_PAGE_SIZE_OPTIONS, getPaginationItems } from '../utils/pagination';

interface TablePaginationFooterProps {
  hasMore?: boolean;
  page: number;
  pageSize?: number;
  pageSizeOptions?: readonly number[];
  setPage: (page: number) => void;
  setPageSize?: (pageSize: number) => void;
  total: number;
  totalExact?: boolean;
  totalPages: number;
}

export function TablePaginationFooter({
  hasMore,
  page,
  pageSize,
  pageSizeOptions = DEFAULT_PAGINATION_PAGE_SIZE_OPTIONS,
  setPage,
  setPageSize,
  total,
  totalExact = true,
  totalPages,
}: TablePaginationFooterProps) {
  const safeTotalPages = totalExact
    ? Math.max(totalPages, 1)
    : Math.max(totalPages, page + (hasMore ? 1 : 0), 1);
  const canGoNext = totalExact ? page < safeTotalPages : !!hasMore;
  const showPageSize = pageSize != null && setPageSize != null;
  const selectedPageSize = pageSize == null ? '' : String(pageSize);
  const pageSizeItems = pageSizeOptions.map((size) => ({ id: String(size), label: String(size) }));

  return (
    <Pagination className="ag-table-pagination" size="sm">
      <Pagination.Summary className="ag-table-pagination-summary">
        <span>{totalExact ? '共' : '至少'}</span>
        <span className="ag-table-pagination-number">{total.toLocaleString()}</span>
        <span>条</span>
        <span className="ag-table-pagination-separator" aria-hidden="true" />
        <span>第</span>
        <span className="ag-table-pagination-number">{page}</span>
        <span>/</span>
        <span className="ag-table-pagination-number">{safeTotalPages}</span>
        <span>{totalExact ? '页' : '页+'}</span>
        {showPageSize ? (
          <div className="ag-table-page-size">
            <span>每页</span>
            <Select
              aria-label="每页数量"
              className="ag-table-page-size-select"
              selectedKey={selectedPageSize}
              onSelectionChange={(key) => {
                if (!setPageSize || key == null) return;
                setPageSize(Number(key));
                setPage(1);
              }}
            >
              <Select.Trigger>
                <Select.Value>{selectedPageSize}</Select.Value>
                <Select.Indicator />
              </Select.Trigger>
              <Select.Popover>
                <ListBox items={pageSizeItems}>
                  {(item) => (
                    <ListBox.Item id={item.id} textValue={item.label}>
                      {item.label}
                    </ListBox.Item>
                  )}
                </ListBox>
              </Select.Popover>
            </Select>
            <span>条</span>
          </div>
        ) : null}
      </Pagination.Summary>

      <Pagination.Content>
        <Pagination.Item>
          <Pagination.Previous isDisabled={page <= 1} onPress={() => setPage(Math.max(1, page - 1))}>
            <Pagination.PreviousIcon />
            <span>上一页</span>
          </Pagination.Previous>
        </Pagination.Item>
        {getPaginationItems(page, safeTotalPages).map((item, index) =>
          item === '...' ? (
            <Pagination.Item key={`ellipsis-${index}`}>
              <Pagination.Ellipsis />
            </Pagination.Item>
          ) : (
            <Pagination.Item key={item}>
              <Pagination.Link isActive={item === page} onPress={() => setPage(item)}>
                {item}
              </Pagination.Link>
            </Pagination.Item>
          ),
        )}
        <Pagination.Item>
          <Pagination.Next isDisabled={!canGoNext} onPress={() => setPage(page + 1)}>
            <span>下一页</span>
            <Pagination.NextIcon />
          </Pagination.Next>
        </Pagination.Item>
      </Pagination.Content>
    </Pagination>
  );
}
