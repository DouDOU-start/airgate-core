// 主题辅助（自 airgate-sdk 的 @doudou-start/airgate-theme v0.2.1 内嵌）。
// CSS 变量本体见 src/styles/theme-vars.css；存储键与 data-theme 属性
// 沿用原 SDK 约定，保证既有用户的主题偏好不丢失。

export type ThemeName = 'dark' | 'light';

const THEME_STORAGE_KEY = 'ag-theme';

/** 设置当前主题：documentElement 的 data-theme 属性 + localStorage 持久化。 */
export function setTheme(theme: ThemeName): void {
  document.documentElement.setAttribute('data-theme', theme);
  try {
    localStorage.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    // 存储不可用（隐私模式等）时主题切换仍需生效
  }
}

/** 读取已保存的主题偏好，默认 dark。 */
export function getStoredTheme(): ThemeName {
  try {
    return localStorage.getItem(THEME_STORAGE_KEY) === 'light' ? 'light' : 'dark';
  } catch {
    return 'dark';
  }
}

/** 装饰色板：头像底色等按索引取色的确定性配色。 */
export const decorativePalette = [
  '#3b82f6', // blue
  '#10b981', // emerald
  '#f59e0b', // amber
  '#ef4444', // red
  '#8b5cf6', // violet
  '#06b6d4', // cyan
  '#ec4899', // pink
  '#84cc16', // lime
  '#f97316', // orange
  '#6366f1', // indigo
  '#0d9488', // teal
  '#a855f7', // purple
];
