import { type ReactNode, useEffect } from 'react';
import { Link } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { Button } from '@heroui/react';
import { useAuth } from '../providers/AuthProvider';
import { getTokenRole } from '../../shared/api/client';
import { useTheme } from '../providers/ThemeProvider';
import { useSiteSettings, defaultLogoUrl } from '../providers/SiteSettingsProvider';
import {
  ArrowLeft,
  LogOut,
  Languages,
  Sun,
  Moon,
  ShieldCheck,
} from 'lucide-react';

interface ChatShellProps {
  children: ReactNode;
}

/**
 * 全屏沉浸式布局：仅一条窄顶栏（返回控制台 + 用户/主题/语言/退出），
 * 主区高度填满视口，不限制宽度、不加内边距。供 /chat 等需要最大化使用空间的页面挂载。
 */
export function ChatShell({ children }: ChatShellProps) {
  const { user, logout, isAPIKeySession } = useAuth();
  const { t, i18n } = useTranslation();
  const { theme, toggleTheme } = useTheme();
  const site = useSiteSettings();
  const isAdmin = getTokenRole() === 'admin' || user?.role === 'admin';

  useEffect(() => {
    document.title = site.site_name || 'AirGate';
  }, [site.site_name]);

  const toggleLanguage = () => {
    const nextLang = i18n.language === 'zh' ? 'en' : 'zh';
    i18n.changeLanguage(nextLang);
    localStorage.setItem('lang', nextLang);
  };

  return (
    <div className="flex flex-col h-screen" style={{ height: '100dvh' }}>
      <header className="flex items-center justify-between h-12 px-3 md:px-4 border-b border-border bg-bg shrink-0">
        <div className="flex items-center gap-2 min-w-0">
          <Link
            to="/"
            className="flex items-center gap-1.5 h-8 px-2 rounded-[10px] text-text-tertiary hover:text-text hover:bg-bg-hover transition-colors"
            title={t('nav.back_to_console')}
          >
            <ArrowLeft className="w-3.5 h-3.5 shrink-0" />
            <span className="text-[12px] font-medium hidden sm:inline">
              {t('nav.back_to_console')}
            </span>
          </Link>
          <div className="w-px h-4 bg-border mx-1" />
          <img
            src={site.site_logo || defaultLogoUrl}
            alt=""
            className="w-6 h-6 rounded-sm flex-shrink-0 object-cover"
          />
          <span className="text-[13px] font-semibold text-text truncate">
            {site.site_name || 'AirGate'}
          </span>
        </div>

        <div className="flex items-center gap-1">
          <Button
            aria-label={i18n.language === 'zh' ? 'Switch to English' : '切换为中文'}
            size="sm"
            variant="ghost"
            onPress={toggleLanguage}
          >
            <Languages className="w-3.5 h-3.5" />
            <span className="text-[10px] font-mono uppercase hidden sm:inline">
              {i18n.language === 'zh' ? 'EN' : '中文'}
            </span>
          </Button>
          <Button
            aria-label={theme === 'dark' ? '切换亮色模式' : '切换暗色模式'}
            isIconOnly
            size="sm"
            variant="ghost"
            onPress={toggleTheme}
          >
            {theme === 'dark' ? <Sun className="w-3.5 h-3.5" /> : <Moon className="w-3.5 h-3.5" />}
          </Button>

          <div className="w-px h-5 bg-border mx-1.5" />

          <div className="flex items-center gap-2 pl-1">
            {!isAPIKeySession && (
              <div className="hidden md:block text-right">
                <p className="text-xs font-medium text-text leading-tight">
                  {user?.username || user?.email?.split('@')[0]}
                </p>
                <p className="text-[10px] text-text-tertiary leading-tight">
                  {user?.email}
                </p>
              </div>
            )}
            {isAdmin ? (
              <div className="flex items-center justify-center w-7 h-7 rounded-full bg-primary-subtle text-primary shrink-0">
                <ShieldCheck className="w-3.5 h-3.5" />
              </div>
            ) : (
              <div className="flex items-center justify-center w-7 h-7 rounded-full bg-primary-subtle text-[11px] font-bold text-primary shrink-0">
                {(user?.username || user?.email || 'U').charAt(0).toUpperCase()}
              </div>
            )}
            <Button
              aria-label={t('common.logout')}
              isIconOnly
              size="sm"
              variant="ghost"
              onPress={logout}
            >
              <LogOut className="w-3.5 h-3.5" />
            </Button>
          </div>
        </div>
      </header>

      <main className="flex-1 min-h-0 overflow-hidden">
        {children}
      </main>
    </div>
  );
}
