import { useMemo, type CSSProperties } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import DOMPurify from 'dompurify';
import { Button, Link as HeroLink } from '@heroui/react';
import { useSiteSettings, defaultLogoUrl } from '../app/providers/SiteSettingsProvider';
import { useTheme } from '../app/providers/ThemeProvider';
import { getToken } from '../shared/api/client';
import { effectiveDocUrl } from '../shared/utils/docUrl';
import { METRIC_TONE_VARS } from '../shared/components/StatCard';
import {
  Zap, Shield, Globe, ArrowRight, Sun, Moon, Sprout, BarChart3, KeyRound, Layers,
} from 'lucide-react';

/** 首页 Hero 浮尘光点的固定布局（避免 Math.random 每次渲染重排） */
const HERO_MOTES = [
  { left: '6%', size: 4, duration: '11s', delay: '0.4s', drift: '10px' },
  { left: '16%', size: 3, duration: '9s', delay: '3.2s', drift: '-8px' },
  { left: '28%', size: 5, duration: '13s', delay: '5.6s', drift: '12px' },
  { left: '40%', size: 3, duration: '10s', delay: '1.6s', drift: '-6px' },
  { left: '58%', size: 4, duration: '12s', delay: '6.8s', drift: '8px' },
  { left: '70%', size: 3, duration: '9.5s', delay: '2.4s', drift: '-10px' },
  { left: '82%', size: 5, duration: '14s', delay: '4.4s', drift: '6px' },
  { left: '93%', size: 3, duration: '10.5s', delay: '7.6s', drift: '-8px' },
] as const;

function HeroMotes() {
  return (
    <div aria-hidden className="pointer-events-none absolute inset-0 overflow-hidden">
      {HERO_MOTES.map((m, i) => (
        <span
          key={i}
          className="ag-float-mote absolute bottom-0 rounded-full"
          style={{
            left: m.left,
            width: m.size,
            height: m.size,
            background: 'color-mix(in oklab, var(--ag-primary) 70%, transparent)',
            boxShadow: `0 0 ${m.size * 2.5}px color-mix(in oklab, var(--ag-primary) 55%, transparent)`,
            '--mote-duration': m.duration,
            '--mote-delay': m.delay,
            '--mote-drift-x': m.drift,
          } as CSSProperties}
        />
      ))}
    </div>
  );
}

export default function HomePage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const site = useSiteSettings();
  const { theme, toggleTheme } = useTheme();
  const isLoggedIn = !!getToken();
  // 文档链接 fallback：管理员未填外部 doc_url 时回退到内置 /docs（详见 docUrl.ts）
  const docs = effectiveDocUrl(site.doc_url);
  // 自定义首页内容为管理员可编辑的富文本，注入前统一经 DOMPurify 白名单消毒，防存储型 XSS
  const safeHomeContent = useMemo(
    () => (site.home_content ? DOMPurify.sanitize(site.home_content, { USE_PROFILES: { html: true } }) : ''),
    [site.home_content],
  );

  const featureTones = ['emerald', 'teal', 'blue', 'violet', 'indigo', 'amber'] as const;
  const features = [
    { icon: <Zap className="w-5 h-5" />, titleKey: 'home.feature_gateway', descKey: 'home.feature_gateway_desc' },
    { icon: <Shield className="w-5 h-5" />, titleKey: 'home.feature_security', descKey: 'home.feature_security_desc' },
    { icon: <Layers className="w-5 h-5" />, titleKey: 'home.feature_channels', descKey: 'home.feature_channels_desc' },
    { icon: <BarChart3 className="w-5 h-5" />, titleKey: 'home.feature_analytics', descKey: 'home.feature_analytics_desc' },
    { icon: <KeyRound className="w-5 h-5" />, titleKey: 'home.feature_keys', descKey: 'home.feature_keys_desc' },
    { icon: <Globe className="w-5 h-5" />, titleKey: 'home.feature_multi_platform', descKey: 'home.feature_multi_platform_desc' },
  ];

  return (
    <div className="ag-organic-canvas relative min-h-screen overflow-hidden bg-bg text-text">
      {/* 导航栏 */}
      <nav className="relative z-10 mx-auto flex max-w-6xl items-center justify-between px-6 py-4 md:px-12">
        <div className="flex items-center gap-2.5">
          <img src={site.site_logo || defaultLogoUrl} alt="" className="h-8 w-8 rounded-[var(--radius-md)] object-cover" />
          <span className="font-display text-base font-semibold tracking-tight">{site.site_name || 'AirGate'}</span>
        </div>
        <div className="flex items-center gap-2">
          <HeroLink
            href={docs.href}
            {...(docs.isExternal ? { target: '_blank', rel: 'noopener noreferrer' } : {})}
            className="px-3 py-1.5 text-xs font-medium text-text-secondary transition-colors hover:text-text"
          >
            {t('home.docs')}
          </HeroLink>
          <Button
            aria-label={theme === 'dark' ? t('common.toggle_theme_light') : t('common.toggle_theme_dark')}
            isIconOnly
            size="sm"
            variant="ghost"
            onPress={toggleTheme}
          >
            {theme === 'dark' ? <Sun className="w-4 h-4" /> : <Moon className="w-4 h-4" />}
          </Button>
          <Button
            className="ml-2"
            size="sm"
            variant="primary"
            onPress={() => navigate({ to: isLoggedIn ? '/' : '/login' })}
          >
            {isLoggedIn ? t('home.go_dashboard') : t('home.login')}
          </Button>
        </div>
      </nav>

      {/* Hero：呼吸品牌徽标 + 渐变大字 + 浮尘光点 */}
      <section className="relative z-10 mx-auto max-w-4xl px-6 pb-16 pt-14 text-center md:pb-20 md:pt-20">
        <HeroMotes />
        <div className="ag-page-body relative">
          <span className="ag-breathe mx-auto mb-9 flex h-16 w-16 items-center justify-center rounded-[var(--radius-lg)] bg-primary shadow-[var(--ag-shadow-md)]">
            <img src={site.site_logo || defaultLogoUrl} alt="" className="h-11 w-11 rounded-[var(--radius-md)] object-cover" />
          </span>
          <div className="mb-6 inline-flex items-center gap-2">
            <Sprout className="h-3.5 w-3.5 text-text-tertiary" strokeWidth={2.25} />
            <span className="ag-kicker">{t('home.badge')}</span>
          </div>
          <h1 className="ag-gradient-text font-display mb-6 text-[2.75rem] font-medium leading-[1.08] tracking-[-0.025em] md:text-[4rem]">
            {site.site_name || 'AirGate'}
          </h1>
          <p className="mx-auto mb-10 max-w-xl text-base leading-relaxed text-text-tertiary md:text-lg">
            {site.site_subtitle || t('home.subtitle')}
          </p>
          <div className="flex flex-col items-center justify-center gap-3 sm:flex-row">
            <Button
              size="lg"
              variant="primary"
              className="w-full sm:w-auto"
              onPress={() => navigate({ to: isLoggedIn ? '/' : '/login' })}
            >
              {isLoggedIn ? t('home.go_dashboard') : t('home.get_started')}
              <ArrowRight className="w-4 h-4" />
            </Button>
            <HeroLink
              href={docs.href}
              {...(docs.isExternal ? { target: '_blank', rel: 'noopener noreferrer' } : {})}
              className="inline-flex w-full items-center justify-center gap-2 rounded-[var(--field-radius)] border border-border bg-surface px-6 py-2.5 text-sm font-medium text-text-secondary transition-colors hover:border-[var(--ag-border-strong)] hover:text-text sm:w-auto"
            >
              {t('home.view_docs')}
            </HeroLink>
          </div>

          {/* API 地址展示 */}
          {site.api_base_url && (
            <div className="mt-10 inline-flex items-center gap-3 rounded-[var(--field-radius)] border border-border bg-surface px-4 py-2.5 text-sm font-mono">
              <span className="ag-kicker">API</span>
              <span className="h-3 w-px bg-border" />
              <span className="text-text">{site.api_base_url}</span>
            </div>
          )}
        </div>
      </section>

      {/* 有机分隔线 */}
      <div className="relative z-10 mx-auto mb-14 max-w-5xl px-6">
        <div className="ag-scanline" />
      </div>

      {/* 特性卡片：花园色调图标 + 悬停上浮 */}
      <section className="relative z-10 mx-auto max-w-5xl px-6 pb-20">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {features.map((f, i) => (
            <div
              key={f.titleKey}
              className="group rounded-[var(--radius-lg)] border border-border bg-surface p-5 shadow-[var(--ag-shadow-sm)] transition-[transform,box-shadow,border-color] duration-200 hover:-translate-y-0.5 hover:border-[var(--ag-border-strong)] hover:shadow-[var(--ag-shadow-md)]"
            >
              <div className="mb-4 flex items-center justify-between">
                <span
                  className="flex h-10 w-10 items-center justify-center rounded-[var(--field-radius)]"
                  style={{
                    background: `color-mix(in oklab, var(${METRIC_TONE_VARS[featureTones[i % featureTones.length]!]}) 14%, transparent)`,
                    color: `var(${METRIC_TONE_VARS[featureTones[i % featureTones.length]!]})`,
                  }}
                >
                  {f.icon}
                </span>
                <span className="font-mono text-[10px] tabular-nums tracking-[0.14em] text-text-tertiary">
                  {String(i + 1).padStart(2, '0')}
                </span>
              </div>
              <h3 className="font-display mb-1 text-[15px] font-medium">{t(f.titleKey)}</h3>
              <p className="text-[13px] leading-relaxed text-text-tertiary">{t(f.descKey)}</p>
            </div>
          ))}
        </div>
      </section>

      {/* 自定义 HTML 内容 */}
      {safeHomeContent && (
        <section className="relative z-10 mx-auto max-w-4xl px-6 pb-16">
          <div
            className="prose prose-sm dark:prose-invert max-w-none text-text-secondary"
            dangerouslySetInnerHTML={{ __html: safeHomeContent }}
          />
        </section>
      )}

      {/* 联系方式 & 底部 */}
      <footer className="relative z-10 border-t border-[var(--ag-glass-border)] py-8 text-center">
        <div className="flex items-center justify-center gap-4 text-xs text-text-tertiary">
          <span>© {new Date().getFullYear()} {site.site_name || 'AirGate'} · {t('home.copyright')}</span>
          {site.contact_info && (
            <>
              <span className="w-px h-3 bg-[var(--ag-border)]" />
              <span>{site.contact_info}</span>
            </>
          )}
        </div>
      </footer>
    </div>
  );
}
