import { createContext, useContext, useState, useEffect, type ReactNode } from 'react';
import { injectThemeStyle, setTheme, getStoredTheme, type ThemeName } from '@airgate/theme';

interface ThemeContextValue {
  theme: ThemeName;
  toggleTheme: () => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

function syncHeroUIThemeClass(theme: ThemeName) {
  document.documentElement.classList.toggle('light', theme === 'light');
  document.documentElement.classList.toggle('dark', theme === 'dark');
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<ThemeName>(getStoredTheme);

  // 初始化：注入 AirGate CSS 变量。
  useEffect(() => {
    injectThemeStyle();
  }, []);

  // 主题变化时同步 AirGate data-theme 与 HeroUI light/dark class。
  useEffect(() => {
    setTheme(theme);
    syncHeroUIThemeClass(theme);
  }, [theme]);

  const toggleTheme = () => {
    setThemeState((t) => (t === 'dark' ? 'light' : 'dark'));
  };

  return (
    <ThemeContext value={{ theme, toggleTheme }}>
      {children}
    </ThemeContext>
  );
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error('useTheme 必须在 ThemeProvider 内使用');
  return ctx;
}
