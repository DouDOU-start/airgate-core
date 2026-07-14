import { useMemo } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import DOMPurify from 'dompurify';
import { Button, Link as HeroLink } from '@heroui/react';
import { useSiteSettings, defaultLogoUrl } from '../app/providers/SiteSettingsProvider';
import { useTheme } from '../app/providers/ThemeProvider';
import { getToken } from '../shared/api/client';
import { effectiveDocUrl } from '../shared/utils/docUrl';
import { METRIC_TONE_VARS } from '../shared/components/StatCard';
import { setStoredLanguage } from '../i18n';
import { AmbientAurora } from './login/AmbientAurora';
import {
  Zap, Shield, Coins, ArrowRight, Sun, Moon, Sprout, BarChart3, KeyRound, Layers,
  Github, Languages, MessageCircle,
} from 'lucide-react';

export default function HomePage() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const site = useSiteSettings();
  const { theme, toggleTheme } = useTheme();
  const isLoggedIn = !!getToken();

  const toggleLanguage = () => {
    const nextLang = i18n.language === 'zh' ? 'en' : 'zh';
    i18n.changeLanguage(nextLang);
    setStoredLanguage(nextLang);
  };
  // 文档入口：仅当管理员填写了外部 doc_url 时才显示（详见 docUrl.ts）
  const docsUrl = effectiveDocUrl(site.doc_url);
  // 自定义首页内容为管理员可编辑的富文本，注入前统一经 DOMPurify 白名单消毒，防存储型 XSS
  const safeHomeContent = useMemo(
    () => (site.home_content ? DOMPurify.sanitize(site.home_content, { USE_PROFILES: { html: true } }) : ''),
    [site.home_content],
  );

  const featureTones = ['blue', 'indigo', 'violet', 'purple', 'rose', 'amber'] as const;
  const features = [
    { icon: <Zap className="w-5 h-5" />, titleKey: 'home.feature_gateway', descKey: 'home.feature_gateway_desc' },
    { icon: <Shield className="w-5 h-5" />, titleKey: 'home.feature_security', descKey: 'home.feature_security_desc' },
    { icon: <Layers className="w-5 h-5" />, titleKey: 'home.feature_channels', descKey: 'home.feature_channels_desc' },
    { icon: <BarChart3 className="w-5 h-5" />, titleKey: 'home.feature_analytics', descKey: 'home.feature_analytics_desc' },
    { icon: <KeyRound className="w-5 h-5" />, titleKey: 'home.feature_keys', descKey: 'home.feature_keys_desc' },
    { icon: <Coins className="w-5 h-5" />, titleKey: 'home.feature_billing', descKey: 'home.feature_billing_desc' },
  ];

  return (
    <div className="relative flex min-h-screen flex-col bg-bg text-text">
      {/* 全页统一极光背景：与登录页同一套克莱因蓝氛围，固定于视口、内容滚动其上 */}
      <AmbientAurora active={false} className="pointer-events-none fixed inset-0" />

      {/* 主体内容：以块级布局承载，保证内部 mx-auto 居中不受外层 flex 影响；grow 占满空高把页脚顶到底部 */}
      <div className="relative z-10 flex-1">
      {/* 导航栏 */}
      <nav className="mx-auto flex max-w-6xl items-center justify-between px-6 py-4 md:px-12">
        <div className="flex items-center gap-2.5">
          <img src={site.site_logo || defaultLogoUrl} alt="" className="h-8 w-8 rounded-[var(--radius-md)] object-cover" />
          <span className="font-display text-base font-semibold tracking-tight">{site.site_name || 'AirGate'}</span>
        </div>
        <div className="flex items-center gap-1.5">
          {site.contact_info && (
            <span
              className="mr-1 hidden items-center gap-1.5 text-xs text-text-tertiary md:inline-flex"
              title={t('home.contact')}
            >
              <MessageCircle className="h-3.5 w-3.5" />
              <span>{site.contact_info}</span>
            </span>
          )}
          <Button
            size="sm"
            variant="ghost"
            className="px-3 text-xs font-medium"
            onPress={() => navigate({ to: '/model-market' })}
          >
            {t('home.model_market')}
          </Button>
          {docsUrl && (
            <HeroLink
              href={docsUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="px-3 py-1.5 text-xs font-medium text-text-secondary transition-colors hover:text-text"
            >
              {t('home.docs')}
            </HeroLink>
          )}
          <HeroLink
            href="https://github.com/DouDOU-start/airgate-core"
            target="_blank"
            rel="noopener noreferrer"
            aria-label="GitHub"
            className="flex h-8 w-8 items-center justify-center rounded-[var(--radius-md)] text-text-secondary transition-colors hover:text-text"
          >
            <Github className="w-4 h-4" />
          </HeroLink>
          <Button
            aria-label={i18n.language === 'zh' ? 'Switch to English' : '切换为中文'}
            size="sm"
            variant="ghost"
            className="gap-1.5 px-2.5"
            onPress={toggleLanguage}
          >
            <Languages className="w-4 h-4" />
            <span className="font-mono text-xs uppercase">{i18n.language === 'zh' ? 'EN' : '中文'}</span>
          </Button>
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
            onPress={() => navigate({ to: isLoggedIn ? '/' : '/login', search: (prev: Record<string, unknown>) => prev })}
          >
            {isLoggedIn ? t('home.go_dashboard') : t('home.login')}
          </Button>
        </div>
      </nav>

      {/* Hero：呼吸品牌徽标 + 渐变大字 + 浮尘光点 */}
      <section className="relative z-10 mx-auto max-w-4xl px-6 pb-16 pt-14 text-center md:pb-20 md:pt-20">
        <div className="ag-page-body relative flex flex-col items-center text-center">
          <div className="mb-6 inline-flex items-center gap-2">
            <Sprout className="h-3.5 w-3.5 text-text-tertiary" strokeWidth={2.25} />
            <span className="ag-kicker">{t('home.badge')}</span>
          </div>
          <h1 className="font-display mb-10 max-w-2xl text-[1.75rem] font-medium leading-[1.2] tracking-tight text-text md:text-[2.5rem]">
            {site.site_subtitle || t('home.subtitle')}
          </h1>
          <div className="flex w-full flex-col items-center justify-center gap-3 sm:flex-row">
            <Button
              size="lg"
              variant="primary"
              className="w-full sm:w-auto"
              onPress={() => navigate({ to: isLoggedIn ? '/' : '/login', search: (prev: Record<string, unknown>) => prev })}
            >
              {isLoggedIn ? t('home.go_dashboard') : t('home.get_started')}
              <ArrowRight className="w-4 h-4" />
            </Button>
            {docsUrl && (
              <HeroLink
                href={docsUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex w-full items-center justify-center gap-2 rounded-[var(--field-radius)] border border-[var(--ag-glass-border)] bg-[var(--ag-glass)] px-6 py-2.5 text-sm font-medium text-text-secondary backdrop-blur-sm transition-colors hover:border-[var(--ag-border-strong)] hover:text-text sm:w-auto"
              >
                {t('home.view_docs')}
              </HeroLink>
            )}
          </div>

          {/* API 地址展示 */}
          {site.api_base_url && (
            <div className="mt-10 inline-flex items-center gap-3 rounded-[var(--field-radius)] border border-[var(--ag-glass-border)] bg-[var(--ag-glass)] px-4 py-2.5 text-sm font-mono backdrop-blur-sm">
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
              className="group rounded-[var(--radius-lg)] border border-[var(--ag-glass-border)] bg-[var(--ag-glass)] p-5 shadow-[var(--ag-shadow-sm)] backdrop-blur-md transition-[transform,box-shadow,border-color] duration-200 hover:-translate-y-0.5 hover:border-[var(--ag-border-strong)] hover:shadow-[var(--ag-shadow-md)]"
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
      </div>

      {/* 底部：版权（联系方式已移至顶部导航） */}
      <footer className="relative z-10 border-t border-[var(--ag-glass-border)] py-5 text-center">
        <div className="flex items-center justify-center gap-4 text-xs text-text-tertiary">
          <span>© {new Date().getFullYear()} {site.site_name || 'AirGate'} · {t('home.copyright')}</span>
        </div>
      </footer>
    </div>
  );
}
