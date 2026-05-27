import { useCallback, useState } from 'react';
import { usePagination } from './usePagination';

export function useCursorPagination(defaultPageSize = 20, storageKey?: string) {
  const { page, setPage: setBasePage, pageSize, setPageSize: setBasePageSize } = usePagination(defaultPageSize, storageKey);
  const [cursors, setCursors] = useState<Record<number, number | undefined>>({});
  const beforeId = page > 1 ? cursors[page] : undefined;

  const resetCursorPagination = useCallback(() => {
    setCursors({});
    setBasePage(1);
  }, [setBasePage]);

  const setPage = useCallback((nextPage: number, nextCursor?: number | null) => {
    if (nextPage <= 1) {
      setBasePage(1);
      return;
    }

    if (nextPage === page + 1) {
      if (nextCursor == null || nextCursor <= 0) return;
      setCursors((current) => ({ ...current, [nextPage]: nextCursor }));
      setBasePage(nextPage);
      return;
    }

    if (nextPage < page || cursors[nextPage] != null) {
      setBasePage(nextPage);
    }
  }, [cursors, page, setBasePage]);

  const setPageSize = useCallback((nextPageSize: number) => {
    setCursors({});
    setBasePage(1);
    setBasePageSize(nextPageSize);
  }, [setBasePage, setBasePageSize]);

  return {
    beforeId,
    page,
    pageSize,
    resetCursorPagination,
    setPage,
    setPageSize,
  };
}
