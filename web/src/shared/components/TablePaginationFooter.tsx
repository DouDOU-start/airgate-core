import { ListBox, Pagination, Select } from '@heroui/react';
import { useTranslation } from 'react-i18next';
import { DEFAULT_PAGINATION_PAGE_SIZE_OPTIONS, getPaginationItems } from '../utils/pagination';

interface TablePaginationFooterProps {
  page: number;
  pageSize?: number;
  pageSizeOptions?: readonly number[];
  setPage: (page: number) => void;
  setPageSize?: (pageSize: number) => void;
  total: number;
  totalPages: number;
}

export function TablePaginationFooter({
  page,
  pageSize,
  pageSizeOptions = DEFAULT_PAGINATION_PAGE_SIZE_OPTIONS,
  setPage,
  setPageSize,
  total,
  totalPages,
}: TablePaginationFooterProps) {
  const { t } = useTranslation();
  const safeTotalPages = Math.max(totalPages, 1);
  const showPageSize = pageSize != null && setPageSize != null;
  const selectedPageSize = pageSize == null ? '' : String(pageSize);
  const pageSizeItems = pageSizeOptions.map((size) => ({ id: String(size), label: String(size) }));

  return (
    <Pagination className="ag-table-pagination" size="sm">
      <Pagination.Summary className="ag-table-pagination-summary">
        <span>{t('pagination.total', { count: total })}</span>
        <span className="ag-table-pagination-separator" aria-hidden="true" />
        <span>{t('pagination.page', { page, total: safeTotalPages })}</span>
        {showPageSize ? (
          <div className="ag-table-page-size">
            <span>{t('pagination.perPage')}</span>
            <Select
              aria-label={t('pagination.perPage')}
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
          </div>
        ) : null}
      </Pagination.Summary>

      <Pagination.Content>
        <Pagination.Item>
          <Pagination.Previous isDisabled={page <= 1} onPress={() => setPage(Math.max(1, page - 1))}>
            <Pagination.PreviousIcon />
            <span>{t('pagination.prev')}</span>
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
          <Pagination.Next isDisabled={page >= safeTotalPages} onPress={() => setPage(Math.min(safeTotalPages, page + 1))}>
            <span>{t('pagination.next')}</span>
            <Pagination.NextIcon />
          </Pagination.Next>
        </Pagination.Item>
      </Pagination.Content>
    </Pagination>
  );
}
