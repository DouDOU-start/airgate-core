import { useCallback, useEffect, useState } from 'react';

const PAGE_SIZE_STORAGE_PREFIX = 'airgate:pagination:page-size:';

function readStoredPageSize(storageKey: string | undefined, fallback: number) {
  if (!storageKey || typeof window === 'undefined') return fallback;

  try {
    const raw = window.localStorage.getItem(`${PAGE_SIZE_STORAGE_PREFIX}${storageKey}`);
    if (raw == null) return fallback;
    const parsed = Number(raw);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
  } catch {
    return fallback;
  }
}

function writeStoredPageSize(storageKey: string | undefined, pageSize: number) {
  if (!storageKey || typeof window === 'undefined') return;

  try {
    window.localStorage.setItem(`${PAGE_SIZE_STORAGE_PREFIX}${storageKey}`, String(pageSize));
  } catch {
    // localStorage may be unavailable in private mode; pagination should still work.
  }
}

export function usePagination(defaultPageSize = 20, storageKey?: string) {
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(() => readStoredPageSize(storageKey, defaultPageSize));

  useEffect(() => {
    setPageSize(readStoredPageSize(storageKey, defaultPageSize));
    setPage(1);
  }, [defaultPageSize, storageKey]);

  const handlePageSizeChange = useCallback((size: number) => {
    setPageSize(size);
    writeStoredPageSize(storageKey, size);
    setPage(1);
  }, [storageKey]);

  return { page, setPage, pageSize, setPageSize: handlePageSizeChange };
}
