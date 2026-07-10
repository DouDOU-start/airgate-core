import { useMemo } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import DOMPurify from 'dompurify';
import { Button, Link as HeroLink } from '@heroui/react';
import { useSiteSettings, defaultLogoUrl } from '../app/providers/SiteSettingsProvider';
import { useTheme } from '../app/providers/ThemeProvider';
import { getToken } from '../shared/api/client';
import { effectiveDocUrl } from '../shared/utils/docUrl';
import {
  Zap, Shield, Globe, ArrowRight, Sun, Moon, Code, BarChart3, KeyRound, Layers,
} from 'lucide-react';

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

  const features = [
    { icon: <Zap className="w-6 h-6" />, titleKey: 'home.feature_gateway', descKey: 'home.feature_gateway_desc' },
    { icon: <Shield className="w-6 h-6" />, titleKey: 'home.feature_security', descKey: 'home.feature_security_desc' },
    { icon: <Layers className="w-6 h-6" />, titleKey: 'home.feature_channels', descKey: 'home.feature_channels_desc' },
    { icon: <BarChart3 className="w-6 h-6" />, titleKey: 'home.feature_analytics', descKey: 'home.feature_analytics_desc' },
    { icon: <KeyRound className="w-6 h-6" />, titleKey: 'home.feature_keys', descKey: 'home.feature_keys_desc' },
    { icon: <Globe className="w-6 h-6" />, titleKey: 'home.feature_multi_platform', descKey: 'home.feature_multi_platform_desc' },
  ];

  return (
    <div className="min-h-screen bg-bg-deep text-text relative overflow-hidden">
      {/* 中央辉光（黑白光几何：光即是色彩） */}
      <div
        aria-hidden
        className="pointer-events-none absolute left-1/2 top-[8rem] h-[30rem] w-[30rem] -translate-x-1/2 rounded-full"
        style={{ background: 'radial-gradient(closest-side, color-mix(in oklab, var(--ag-text) 10%, transparent), transparent 70%)' }}
      />

      {/* 导航栏 */}
      <nav className="relative z-10 flex items-center justify-between px-6 md:px-12 py-4 max-w-6xl mx-auto">
        <div className="flex items-center gap-2.5">
          <img src={site.site_logo || defaultLogoUrl} alt="" className="w-8 h-8 rounded-md object-cover" />
          <span className="text-base font-semibold tracking-tight">{site.site_name || 'AirGate'}</span>
        </div>
        <div className="flex items-center gap-2">
          <HeroLink
            href={docs.href}
            {...(docs.isExternal ? { target: '_blank', rel: 'noopener noreferrer' } : {})}
            className="px-3 py-1.5 text-xs font-medium text-text-secondary hover:text-text transition-colors"
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

      {/* Hero：发光几何 + 大字 */}
      <section className="relative z-10 text-center px-6 pt-14 pb-16 md:pt-20 md:pb-20 max-w-4xl mx-auto">
        <span className="ag-breathe mx-auto mb-9 flex h-16 w-16 items-center justify-center rounded-xl bg-text">
          <img src={site.site_logo || defaultLogoUrl} alt="" className="h-11 w-11 rounded-lg object-cover" />
        </span>
        <div className="mb-6 inline-flex items-center gap-2.5">
          <Code className="w-3.5 h-3.5 text-text-tertiary" />
          <span className="ag-kicker">{t('home.badge')}</span>
        </div>
        <h1 className="text-[2.75rem] md:text-[4rem] font-semibold leading-[1.05] tracking-[-0.035em] mb-6">
          {site.site_name || 'AirGate'}
        </h1>
        <p className="text-base md:text-lg text-text-tertiary max-w-xl mx-auto mb-10 leading-relaxed">
          {site.site_subtitle || t('home.subtitle')}
        </p>
        <div className="flex items-center justify-center gap-3">
          <Button
            size="lg"
            variant="primary"
            onPress={() => navigate({ to: isLoggedIn ? '/' : '/login' })}
          >
            {isLoggedIn ? t('home.go_dashboard') : t('home.get_started')}
            <ArrowRight className="w-4 h-4" />
          </Button>
          <HeroLink
            href={docs.href}
            {...(docs.isExternal ? { target: '_blank', rel: 'noopener noreferrer' } : {})}
            className="inline-flex items-center gap-2 px-6 py-2.5 text-sm font-medium rounded-[var(--field-radius)] border border-border bg-surface text-text-secondary transition-colors hover:border-[var(--ag-border-strong)] hover:text-text"
          >
            {t('home.view_docs')}
          </HeroLink>
        </div>

        {/* API 地址展示 */}
        {site.api_base_url && (
          <div className="mt-10 inline-flex items-center gap-3 px-4 py-2.5 rounded-[var(--field-radius)] bg-surface border border-border text-sm font-mono">
            <span className="ag-kicker">API</span>
            <span className="h-3 w-px bg-border" />
            <span className="text-text">{site.api_base_url}</span>
          </div>
        )}
      </section>

      {/* 扫描线分隔 */}
      <div className="relative z-10 mx-auto mb-14 max-w-5xl px-6">
        <div className="ag-scanline" />
      </div>

      {/* 特性卡片：hover 边框提亮 */}
      <section className="relative z-10 px-6 pb-20 max-w-5xl mx-auto">
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
          {features.map((f, i) => (
            <div
              key={f.titleKey}
              className="group rounded-[var(--radius-xl)] border border-border bg-surface p-5 transition-colors hover:border-[var(--ag-border-strong)]"
            >
              <div className="mb-4 flex items-center justify-between">
                <span className="text-text-secondary transition-colors group-hover:text-text [&>svg]:h-5 [&>svg]:w-5">
                  {f.icon}
                </span>
                <span className="font-mono text-[10px] tabular-nums tracking-[0.14em] text-text-tertiary">
                  {String(i + 1).padStart(2, '0')}
                </span>
              </div>
              <h3 className="text-[15px] font-semibold mb-1">{t(f.titleKey)}</h3>
              <p className="text-[13px] text-text-tertiary leading-relaxed">{t(f.descKey)}</p>
            </div>
          ))}
        </div>
      </section>

      {/* 自定义 HTML 内容 */}
      {safeHomeContent && (
        <section className="relative z-10 px-6 pb-16 max-w-4xl mx-auto">
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
