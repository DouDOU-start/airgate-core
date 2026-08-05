import type { CSSProperties } from 'react';
import { useTheme } from '../../app/providers/ThemeProvider';
import iconAntigravity from '../../assets/icons/platforms/antigravity.svg';
import iconClaude from '../../assets/icons/platforms/claude.svg';
import iconCodex from '../../assets/icons/platforms/codex.svg';
import iconGemini from '../../assets/icons/platforms/gemini.svg';
import iconGrok from '../../assets/icons/platforms/grok.svg';
import iconGrokDark from '../../assets/icons/platforms/grok-dark.svg';
import iconKimiLight from '../../assets/icons/platforms/kimi-light.svg';
import iconKimiDark from '../../assets/icons/platforms/kimi-dark.svg';
import iconVertex from '../../assets/icons/platforms/vertex.svg';
import iconOpenaiLight from '../../assets/icons/platforms/openai-light.svg';
import iconOpenaiDark from '../../assets/icons/platforms/openai-dark.svg';

/** 与 CPA AuthFiles 一致的品牌色底（浅/深主题）。 */
const PLATFORM_COLORS: Record<string, { light: string; dark: string; textLight: string; textDark: string }> = {
  codex: { light: '#eae7ff', dark: '#262395', textLight: '#3538d4', textDark: '#b5b0ff' },
  claude: { light: '#fbece4', dark: '#5e2c14', textLight: '#c05621', textDark: '#e8a882' },
  antigravity: { light: '#e0f7fa', dark: '#004d40', textLight: '#006064', textDark: '#80deea' },
  kimi: { light: '#dce8ff', dark: '#003880', textLight: '#0560cf', textDark: '#70b5ff' },
  xai: { light: '#f3f4f6', dark: '#111827', textLight: '#111827', textDark: '#f9fafb' },
  gemini: { light: '#e3f2fd', dark: '#0d47a1', textLight: '#1565c0', textDark: '#64b5f6' },
  aistudio: { light: '#f0f2f5', dark: '#373c42', textLight: '#2f343c', textDark: '#cfd3db' },
  vertex: { light: '#e4edfd', dark: '#1a3d80', textLight: '#2b5fbc', textDark: '#89b3f7' },
};

type IconAsset = string | { light: string; dark: string };

/** 平台图标（来源 CPA web/src/assets/icons）。 */
const PLATFORM_ICONS: Record<string, IconAsset> = {
  codex: iconCodex,
  claude: iconClaude,
  antigravity: iconAntigravity,
  kimi: { light: iconKimiDark, dark: iconKimiLight }, // CPA 同款：浅色主题用深色字标
  xai: { light: iconGrok, dark: iconGrokDark },
  grok: { light: iconGrok, dark: iconGrokDark },
  gemini: iconGemini,
  aistudio: iconGemini,
  vertex: iconVertex,
  openai: { light: iconOpenaiLight, dark: iconOpenaiDark },
};

function normalizePlatform(platform?: string): string {
  const key = (platform || '').toLowerCase().trim();
  if (key === 'openai-compatibility' || key === 'openai_compat') return 'openai';
  if (key === 'google') return 'gemini';
  return key;
}

function resolveIconSrc(asset: IconAsset | undefined, theme: 'light' | 'dark'): string | null {
  if (!asset) return null;
  if (typeof asset === 'string') return asset;
  return theme === 'dark' ? asset.dark : asset.light;
}

export function platformDisplayLabel(platform?: string): string {
  const key = normalizePlatform(platform);
  const map: Record<string, string> = {
    codex: 'Codex',
    claude: 'Claude',
    antigravity: 'Antigravity',
    kimi: 'Kimi',
    xai: 'xAI',
    grok: 'Grok',
    gemini: 'Gemini',
    aistudio: 'AI Studio',
    vertex: 'Vertex',
    openai: 'OpenAI',
  };
  if (map[key]) return map[key];
  if (!platform) return '—';
  return platform.charAt(0).toUpperCase() + platform.slice(1);
}

/**
 * 账号/平台品牌图标（对齐 CPA AuthFiles / OAuth 页）。
 * size: 图标边长；withBadge 时加品牌底色圆角方块。
 */
export function PlatformIcon({
  platform,
  size = 18,
  withBadge = true,
  className = '',
  showLabel = false,
}: {
  platform?: string;
  size?: number;
  withBadge?: boolean;
  className?: string;
  /** 图标旁显示平台名 */
  showLabel?: boolean;
}) {
  const { theme } = useTheme();
  const key = normalizePlatform(platform);
  const src = resolveIconSrc(PLATFORM_ICONS[key], theme);
  const colors = PLATFORM_COLORS[key];
  const label = platformDisplayLabel(platform);

  const pad = withBadge ? 8 : 0;
  const box = size + pad;
  const badgeStyle: CSSProperties | undefined = withBadge
    ? {
        background: theme === 'dark' ? (colors?.dark ?? 'var(--ag-bg-surface)') : (colors?.light ?? 'var(--ag-bg-surface)'),
        width: box,
        height: box,
        border: theme === 'dark' ? '1px solid rgba(255,255,255,0.06)' : '1px solid rgba(0,0,0,0.04)',
      }
    : { width: size, height: size };

  const icon = src ? (
    <img
      src={src}
      alt=""
      width={size}
      height={size}
      className="shrink-0 object-contain"
      draggable={false}
    />
  ) : (
    <span
      className="inline-flex shrink-0 items-center justify-center rounded text-[10px] font-bold uppercase"
      style={{
        width: size,
        height: size,
        color: theme === 'dark' ? (colors?.textDark ?? 'var(--ag-text-secondary)') : (colors?.textLight ?? 'var(--ag-text-secondary)'),
      }}
      aria-hidden
    >
      {(label || '?').slice(0, 1)}
    </span>
  );

  if (showLabel) {
    return (
      <span className={`inline-flex items-center gap-1.5 min-w-0 ${className}`}>
        <span
          className="inline-flex shrink-0 items-center justify-center rounded-lg"
          style={badgeStyle}
        >
          {icon}
        </span>
        <span className="truncate text-xs font-medium">{label}</span>
      </span>
    );
  }

  return (
    <span
      className={`inline-flex shrink-0 items-center justify-center rounded-lg ${className}`}
      style={badgeStyle}
      title={label}
      aria-label={label}
    >
      {icon}
    </span>
  );
}
