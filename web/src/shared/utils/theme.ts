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

/** 装饰色板：头像底色等按索引取色的确定性配色，取自有机花园调色板（跟随主题明暗自适应）。 */
export const decorativePalette = [
  'var(--ag-tone-emerald)',
  'var(--ag-secondary)',
  'var(--ag-tone-blue)',
  'var(--ag-tone-violet)',
  'var(--ag-tone-teal)',
  'var(--ag-tone-amber)',
  'var(--ag-tone-rose)',
  'var(--ag-tone-indigo)',
  'var(--ag-primary)',
  'var(--ag-tone-purple)',
];
