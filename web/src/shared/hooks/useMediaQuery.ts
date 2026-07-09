import { useState, useEffect } from 'react';

/** SSR 安全的媒体查询 hook */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => {
    if (typeof window === 'undefined') return false;
    return window.matchMedia(query).matches;
  });

  useEffect(() => {
    const mql = window.matchMedia(query);
    const handler = (e: MediaQueryListEvent) => setMatches(e.matches);
    mql.addEventListener('change', handler);
    setMatches(mql.matches);
    return () => mql.removeEventListener('change', handler);
  }, [query]);

  return matches;
}

/** 便捷封装：视口宽度 < 768px 时为 true */
export function useIsMobile(): boolean {
  return useMediaQuery('(max-width: 767px)');
}
