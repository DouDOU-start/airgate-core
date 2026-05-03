import { useEffect, useState } from 'react';

export function usePersistentBoolean(key: string, defaultValue = false) {
  const [value, setValue] = useState(() => {
    if (typeof window === 'undefined') return defaultValue;
    try {
      const stored = window.localStorage.getItem(key);
      if (stored == null) return defaultValue;
      return stored === 'true';
    } catch {
      return defaultValue;
    }
  });

  useEffect(() => {
    try {
      window.localStorage.setItem(key, String(value));
    } catch {
      // localStorage can be unavailable in restricted browser modes.
    }
  }, [key, value]);

  return [value, setValue] as const;
}
