import { useCallback, useEffect, useState } from 'react';

/**
 * 表格隐藏列集合的 localStorage 持久化。
 * 存的是「被隐藏的列 key」（opt-out）：后续新增列默认可见，不受旧存档影响。
 */
export function usePersistentHiddenColumns(storageKey: string) {
  const [hiddenKeys, setHiddenKeys] = useState<ReadonlySet<string>>(() => {
    if (typeof window === 'undefined') return new Set<string>();
    try {
      const stored = window.localStorage.getItem(storageKey);
      if (!stored) return new Set<string>();
      const parsed: unknown = JSON.parse(stored);
      return new Set(Array.isArray(parsed) ? parsed.filter((v): v is string => typeof v === 'string') : []);
    } catch {
      return new Set<string>();
    }
  });

  const setHidden = useCallback((keys: Iterable<string>) => {
    setHiddenKeys(new Set(keys));
  }, []);

  useEffect(() => {
    if (typeof window === 'undefined') return;
    try {
      window.localStorage.setItem(storageKey, JSON.stringify([...hiddenKeys]));
    } catch {
      // 受限浏览器模式下 localStorage 可能不可用，静默忽略。
    }
  }, [hiddenKeys, storageKey]);

  return [hiddenKeys, setHidden] as const;
}
