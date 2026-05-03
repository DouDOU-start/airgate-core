import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { Button, Card, Link as HeroLink } from '@heroui/react';
import { useSiteSettings, defaultLogoUrl } from '../app/providers/SiteSettingsProvider';
import { useTheme } from '../app/providers/ThemeProvider';
import { getToken } from '../shared/api/client';
import { effectiveDocUrl } from '../shared/utils/docUrl';
import {
  Zap, Shield, Globe, ArrowRight, Sun, Moon, Code, BarChart3, KeyRound, Layers, Activity,
} from 'lucide-react';

export default function HomePage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const site = useSiteSettings();
  const { theme, toggleTheme } = useTheme();

  const isLoggedIn = !!getToken();
  // 文档链接 fallback：管理员未填外部 doc_url 时回退到内置 /docs（详见 docUrl.ts）
  const docs = effectiveDocUrl(site.doc_url);

  const features = [
    { icon: <Zap className="w-6 h-6" />, titleKey: 'home.feature_gateway', descKey: 'home.feature_gateway_desc' },
    { icon: <Shield className="w-6 h-6" />, titleKey: 'home.feature_security', descKey: 'home.feature_security_desc' },
    { icon: <Layers className="w-6 h-6" />, titleKey: 'home.feature_plugins', descKey: 'home.feature_plugins_desc' },
    { icon: <BarChart3 className="w-6 h-6" />, titleKey: 'home.feature_analytics', descKey: 'home.feature_analytics_desc' },
    { icon: <KeyRound className="w-6 h-6" />, titleKey: 'home.feature_keys', descKey: 'home.feature_keys_desc' },
    { icon: <Globe className="w-6 h-6" />, titleKey: 'home.feature_multi_platform', descKey: 'home.feature_multi_platform_desc' },
  ];

  return (
    <div className="min-h-screen bg-bg-deep text-text relative overflow-hidden">
      {/* 导航栏 */}
      <nav className="relative z-10 flex items-center justify-between px-6 md:px-12 py-4 max-w-6xl mx-auto">
        <div className="flex items-center gap-2.5">
          <img src={site.site_logo || defaultLogoUrl} alt="" className="w-8 h-8 rounded-sm object-cover" />
          <span className="text-base font-bold">{site.site_name || 'AirGate'}</span>
        </div>
        <div className="flex items-center gap-2">
          {/* /status 由 health 插件 standalone 渲染，不在 SPA 路由树里，用 href 跳转 */}
          <HeroLink
            href="/status"
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-text-secondary hover:text-text transition-colors"
          >
            <Activity className="w-3.5 h-3.5" />
            {t('nav.status')}
          </HeroLink>
          <HeroLink
            href={docs.href}
            {...(docs.isExternal ? { target: '_blank', rel: 'noopener noreferrer' } : {})}
            className="px-3 py-1.5 text-xs font-medium text-text-secondary hover:text-text transition-colors"
          >
            {t('home.docs')}
          </HeroLink>
          <Button
            aria-label={theme === 'dark' ? '切换亮色模式' : '切换暗色模式'}
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

      {/* Hero */}
      <section className="relative z-10 text-center px-6 pt-16 pb-20 md:pt-24 md:pb-28 max-w-4xl mx-auto">
        <div className="inline-flex items-center gap-2 px-4 py-1.5 rounded-[var(--radius)] text-xs font-medium mb-6 border border-border bg-surface">
          <Code className="w-3.5 h-3.5 text-[var(--ag-primary)]" />
          <span className="text-text-secondary">{t('home.badge')}</span>
        </div>
        <h1 className="text-4xl md:text-5xl font-bold leading-tight mb-4">
          {site.site_name || 'AirGate'}
        </h1>
        <p className="text-base md:text-lg text-text-tertiary max-w-xl mx-auto mb-8 leading-relaxed">
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
            className="inline-flex items-center gap-2 px-6 py-2.5 text-sm font-medium rounded-[var(--radius)] border border-border text-text-secondary hover:text-text hover:bg-bg-hover transition-colors"
          >
            {t('home.view_docs')}
          </HeroLink>
        </div>

        {/* API 地址展示 */}
        {site.api_base_url && (
          <div className="mt-10 inline-flex items-center gap-2 px-5 py-2.5 rounded-[var(--radius)] bg-surface border border-border text-sm font-mono">
            <span className="text-text-tertiary">API</span>
            <span className="text-text">{site.api_base_url}</span>
          </div>
        )}
      </section>

      {/* 特性卡片 */}
      <section className="relative z-10 px-6 pb-20 max-w-5xl mx-auto">
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
          {features.map((f) => (
            <Card key={f.titleKey}>
              <Card.Content className="p-5">
                <div className="flex items-center justify-center w-10 h-10 rounded-[var(--radius)] bg-primary-subtle text-primary mb-3">
                  {f.icon}
                </div>
                <h3 className="text-sm font-semibold mb-1">{t(f.titleKey)}</h3>
                <p className="text-xs text-text-tertiary leading-relaxed">{t(f.descKey)}</p>
              </Card.Content>
            </Card>
          ))}
        </div>
      </section>

      {/* 自定义 HTML 内容 */}
      {site.home_content && (
        <section className="relative z-10 px-6 pb-16 max-w-4xl mx-auto">
          <div
            className="prose prose-sm dark:prose-invert max-w-none text-text-secondary"
            dangerouslySetInnerHTML={{ __html: site.home_content }}
          />
        </section>
      )}

      {/* 联系方式 & 底部 */}
      <footer className="relative z-10 border-t border-[var(--ag-glass-border)] py-8 text-center">
        <div className="flex items-center justify-center gap-4 text-xs text-text-tertiary">
          <span>{site.site_name || 'AirGate'}</span>
          {site.contact_info && (
            <>
              <span className="w-px h-3 bg-[var(--ag-border)]" />
              <span>{site.contact_info}</span>
            </>
          )}
          <span className="w-px h-3 bg-[var(--ag-border)]" />
          <HeroLink
            href={docs.href}
            {...(docs.isExternal ? { target: '_blank', rel: 'noopener noreferrer' } : {})}
            className="hover:text-text transition-colors"
          >
            {t('home.docs')}
          </HeroLink>
          <span className="w-px h-3 bg-[var(--ag-border)]" />
          <HeroLink href="/status" className="inline-flex items-center gap-1 hover:text-text transition-colors">
            <Activity className="w-3 h-3" />
            {t('nav.status')}
          </HeroLink>
        </div>
      </footer>
    </div>
  );
}
