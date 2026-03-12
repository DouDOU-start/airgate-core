import { createContext, useContext, useState, useEffect, type ReactNode } from 'react';
import { injectThemeStyle, setTheme, getStoredTheme, type ThemeName } from '@airgate/theme';

interface ThemeContextValue {
  theme: ThemeName;
  toggleTheme: () => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<ThemeName>(getStoredTheme);

  // 初始化：注入 CSS 变量 + 设置 data-theme
  useEffect(() => {
    injectThemeStyle();
    setTheme(theme);
  }, []);

  // 主题变化时更新 data-theme
  useEffect(() => {
    setTheme(theme);
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
