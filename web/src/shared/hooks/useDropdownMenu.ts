import { useState, useRef, useEffect, useCallback } from 'react';

export interface MenuPosition {
  id: number;
  top: number;
  left: number;
}

export function useDropdownMenu() {
  const [menu, setMenu] = useState<MenuPosition | null>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!menu) return;
    const handler = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setMenu(null);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [menu]);

  const open = useCallback((id: number, e: React.MouseEvent) => {
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    setMenu({ id, top: rect.bottom + 4, left: rect.right });
  }, []);

  const close = useCallback(() => setMenu(null), []);

  return { menu, menuRef, open, close };
}
