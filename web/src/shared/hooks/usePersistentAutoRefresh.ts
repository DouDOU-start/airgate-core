import { useCallback, useEffect, useState } from 'react';

export const ADMIN_AUTO_REFRESH_OPTIONS = [0, 1, 3, 5, 15, 30] as const;
export const USER_AUTO_REFRESH_OPTIONS = [0, 5, 10, 15, 30] as const;

export type AutoRefreshOptions = readonly number[];
type AutoRefreshValueInput = string | number | boolean | null | undefined | ((previous: number) => unknown);
export type SetAutoRefreshValue = (value: AutoRefreshValueInput) => void;

function defaultEnabledOption(options: AutoRefreshOptions): number {
  return options.find((option) => option > 0) ?? 0;
}

export function normalizeAutoRefresh(value: unknown, options: AutoRefreshOptions = ADMIN_AUTO_REFRESH_OPTIONS): number {
  if (value === true || value === 'true') {
    return defaultEnabledOption(options);
  }
  if (value === false || value === 'false' || value == null) {
    return 0;
  }

  const parsed = typeof value === 'number' ? value : Number(value);
  return options.includes(parsed) ? parsed : 0;
}

export function usePersistentAutoRefresh(key: string, defaultValue = 0, options: AutoRefreshOptions = ADMIN_AUTO_REFRESH_OPTIONS) {
  const [value, setValue] = useState(() => {
    const normalizedDefault = normalizeAutoRefresh(defaultValue, options);
    if (typeof window === 'undefined') return normalizedDefault;
    try {
      const stored = window.localStorage.getItem(key);
      if (stored == null) return normalizedDefault;
      return normalizeAutoRefresh(stored, options);
    } catch {
      return normalizedDefault;
    }
  });
  const setNormalizedValue = useCallback<SetAutoRefreshValue>((nextValue) => {
    setValue((previous) => {
      const resolvedValue = typeof nextValue === 'function' ? nextValue(previous) : nextValue;
      return normalizeAutoRefresh(resolvedValue, options);
    });
  }, [options]);

  useEffect(() => {
    setValue((previous) => normalizeAutoRefresh(previous, options));
  }, [options]);

  useEffect(() => {
    if (typeof window === 'undefined') return;
    try {
      window.localStorage.setItem(key, String(value));
    } catch {
      // localStorage can be unavailable in restricted browser modes.
    }
  }, [key, value]);

  return [value, setNormalizedValue] as const;
}
