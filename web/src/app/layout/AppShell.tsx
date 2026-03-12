import { type ReactNode, useState } from 'react';
import { Link, useMatchRoute } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { useAuth } from '../providers/AuthProvider';
import { pluginsApi } from '../../shared/api/plugins';
import { useTheme } from '../providers/ThemeProvider';
import {
  LayoutDashboard,
  Users,
  KeyRound,
  FolderTree,
  Key,
  CreditCard,
  Globe,
  BarChart3,
  Puzzle,
  Settings,
  User,
  LogOut,
  PanelLeftClose,
  PanelLeft,
  Zap,
  Languages,
  Sun,
  Moon,
} from 'lucide-react';

interface AppShellProps {
  children: ReactNode;
}

interface MenuItem {
  path: string;
  labelKey: string;
  icon: ReactNode;
  sectionKey?: string;
}

// 菜单项使用 i18n key，运行时通过 t() 翻译
const adminMenuItems: MenuItem[] = [
  { path: '/', labelKey: 'nav.dashboard', icon: <LayoutDashboard className="w-[18px] h-[18px]" />, sectionKey: 'nav.overview' },
  { path: '/admin/users', labelKey: 'nav.users', icon: <Users className="w-[18px] h-[18px]" />, sectionKey: 'nav.management' },
  { path: '/admin/accounts', labelKey: 'nav.accounts', icon: <KeyRound className="w-[18px] h-[18px]" /> },
  { path: '/admin/groups', labelKey: 'nav.groups', icon: <FolderTree className="w-[18px] h-[18px]" /> },
  { path: '/admin/api-keys', labelKey: 'nav.api_keys', icon: <Key className="w-[18px] h-[18px]" /> },
  { path: '/admin/subscriptions', labelKey: 'nav.subscriptions', icon: <CreditCard className="w-[18px] h-[18px]" /> },
  { path: '/admin/proxies', labelKey: 'nav.proxies', icon: <Globe className="w-[18px] h-[18px]" /> },
  { path: '/admin/usage', labelKey: 'nav.usage', icon: <BarChart3 className="w-[18px] h-[18px]" /> },
  { path: '/admin/plugins', labelKey: 'nav.plugins', icon: <Puzzle className="w-[18px] h-[18px]" />, sectionKey: 'nav.system' },
  { path: '/admin/settings', labelKey: 'nav.settings', icon: <Settings className="w-[18px] h-[18px]" /> },
];

const userMenuItems: MenuItem[] = [
  { path: '/profile', labelKey: 'nav.profile', icon: <User className="w-[18px] h-[18px]" />, sectionKey: 'nav.personal' },
  { path: '/keys', labelKey: 'nav.my_keys', icon: <Key className="w-[18px] h-[18px]" /> },
];

/**
 * 从已启用插件中提取前端页面菜单项
 */
function usePluginMenuItems(): MenuItem[] {
  const { data } = useQuery({
    queryKey: ['plugins-menu'],
    queryFn: () => pluginsApi.list(),
    staleTime: 60_000,
  });

  if (!data?.list) return [];

  const items: MenuItem[] = [];
  let first = true;
  for (const p of data.list) {
    if (!p.frontend_pages?.length) continue;
    for (const page of p.frontend_pages) {
      items.push({
        path: `/plugins/${p.name}${page.path}`,
        labelKey: page.title,
        icon: <Puzzle className="w-[18px] h-[18px]" />,
        // 第一个插件菜单项带分区标题
        ...(first ? { sectionKey: 'nav.plugins' } : {}),
      });
      first = false;
    }
  }
  return items;
}

export function AppShell({ children }: AppShellProps) {
  const { user, logout } = useAuth();
  const { t, i18n } = useTranslation();
  const { theme, toggleTheme } = useTheme();
  const [collapsed, setCollapsed] = useState(false);
  const matchRoute = useMatchRoute();

  const isAdmin = user?.role === 'admin';
  const pluginMenuItems = usePluginMenuItems();
  const menuItems = isAdmin
    ? [...adminMenuItems, ...pluginMenuItems, ...userMenuItems]
    : [...userMenuItems, ...pluginMenuItems];

  // 按 section 分组
  const sections: Array<{ titleKey?: string; items: MenuItem[] }> = [];
  let currentSection: { titleKey?: string; items: MenuItem[] } | null = null;

  menuItems.forEach((item) => {
    if (item.sectionKey) {
      currentSection = { titleKey: item.sectionKey, items: [item] };
      sections.push(currentSection);
    } else if (currentSection) {
      currentSection.items.push(item);
    } else {
      currentSection = { items: [item] };
      sections.push(currentSection);
    }
  });

  // 语言切换
  const toggleLanguage = () => {
    const nextLang = i18n.language === 'zh' ? 'en' : 'zh';
    i18n.changeLanguage(nextLang);
    localStorage.setItem('lang', nextLang);
  };

  return (
    <div className="flex h-screen bg-[var(--ag-bg-deep)]">
      {/* 侧边栏 */}
      <aside
        className="relative flex flex-col border-r border-[var(--ag-border)] bg-[var(--ag-bg)] transition-all duration-300 ease-in-out"
        style={{ width: collapsed ? 'var(--ag-sidebar-collapsed)' : 'var(--ag-sidebar-width)' }}
      >
        {/* Logo 区 */}
        <div className="flex items-center h-16 px-4 border-b border-[var(--ag-border)]">
          <div className="flex items-center gap-3 overflow-hidden">
            <div className="flex items-center justify-center w-9 h-9 rounded-[var(--ag-radius-md)] bg-[var(--ag-primary-subtle)] flex-shrink-0">
              <Zap className="w-[18px] h-[18px] text-[var(--ag-primary)]" />
            </div>
            {!collapsed && (
              <div className="overflow-hidden">
                <h1 className="text-sm font-bold text-[var(--ag-text)] tracking-tight whitespace-nowrap">
                  AirGate
                </h1>
                <p className="text-[10px] text-[var(--ag-text-tertiary)] font-medium tracking-wider uppercase">
                  Control Panel
                </p>
              </div>
            )}
          </div>
        </div>

        {/* 导航菜单 */}
        <nav className="flex-1 overflow-y-auto py-3 px-2.5 space-y-5">
          {sections.map((section, si) => (
            <div key={si}>
              {/* 分区标题 */}
              {section.titleKey && !collapsed && (
                <p className="text-[10px] font-semibold text-[var(--ag-text-tertiary)] uppercase tracking-[0.1em] px-2.5 mb-2">
                  {t(section.titleKey)}
                </p>
              )}
              {collapsed && si > 0 && (
                <div className="h-px mx-3 mb-2 bg-[var(--ag-border)]" />
              )}
              <div className="space-y-0.5">
                {section.items.map((item) => {
                  const isActive = !!matchRoute({ to: item.path, fuzzy: item.path !== '/' });
                  const isExactDashboard = item.path === '/' && !!matchRoute({ to: '/' });
                  const active = item.path === '/' ? isExactDashboard : isActive;

                  return (
                    <Link
                      key={item.path}
                      to={item.path}
                      className={`group flex items-center gap-3 rounded-[var(--ag-radius-md)] transition-all duration-200 relative ${
                        collapsed ? 'justify-center px-0 py-2.5 mx-1' : 'px-2.5 py-2'
                      } ${
                        active
                          ? 'bg-[var(--ag-primary-subtle)] text-[var(--ag-primary)]'
                          : 'text-[var(--ag-text-secondary)] hover:text-[var(--ag-text)] hover:bg-[var(--ag-bg-hover)]'
                      }`}
                    >
                      {/* 激活指示条 */}
                      {active && (
                        <div className="absolute left-0 top-1/2 -translate-y-1/2 w-[3px] h-4 rounded-r-full bg-[var(--ag-primary)]" />
                      )}
                      <span className="flex-shrink-0">{item.icon}</span>
                      {!collapsed && (
                        <span className="text-[13px] font-medium truncate">{t(item.labelKey, { defaultValue: item.labelKey })}</span>
                      )}
                      {/* 折叠时的 tooltip */}
                      {collapsed && (
                        <div className="absolute left-full ml-2 px-2.5 py-1.5 rounded-[var(--ag-radius-sm)] bg-[var(--ag-bg-surface)] border border-[var(--ag-glass-border)] shadow-[var(--ag-shadow-md)] text-xs text-[var(--ag-text)] whitespace-nowrap opacity-0 invisible group-hover:opacity-100 group-hover:visible transition-all z-50">
                          {t(item.labelKey, { defaultValue: item.labelKey })}
                        </div>
                      )}
                    </Link>
                  );
                })}
              </div>
            </div>
          ))}
        </nav>

        {/* 底部用户 + 折叠按钮 */}
        <div className="border-t border-[var(--ag-border)] p-3 space-y-2">
          {/* 用户信息 */}
          <div className={`flex items-center gap-3 rounded-[var(--ag-radius-md)] p-2 ${collapsed ? 'justify-center' : ''}`}>
            <div className="flex items-center justify-center w-8 h-8 rounded-full bg-[var(--ag-bg-hover)] flex-shrink-0 text-xs font-bold text-[var(--ag-primary)]">
              {user?.email?.charAt(0).toUpperCase() || 'U'}
            </div>
            {!collapsed && (
              <div className="flex-1 min-w-0">
                <p className="text-xs font-medium text-[var(--ag-text)] truncate">
                  {user?.username || user?.email}
                </p>
                <p className="text-[10px] text-[var(--ag-text-tertiary)] truncate">
                  {user?.role === 'admin' ? t('nav.admin') : t('nav.user')}
                </p>
              </div>
            )}
            {!collapsed && (
              <button
                onClick={logout}
                className="flex items-center justify-center w-7 h-7 rounded-[var(--ag-radius-sm)] text-[var(--ag-text-tertiary)] hover:text-[var(--ag-danger)] hover:bg-[var(--ag-danger-subtle)] transition-all"
                title={t('common.logout')}
              >
                <LogOut className="w-3.5 h-3.5" />
              </button>
            )}
          </div>

          {/* 语言切换 + 折叠按钮 */}
          <div className="flex items-center gap-1">
            <button
              onClick={toggleLanguage}
              className="flex items-center justify-center flex-1 h-8 rounded-[var(--ag-radius-sm)] text-[var(--ag-text-tertiary)] hover:text-[var(--ag-text)] hover:bg-[var(--ag-bg-hover)] transition-colors gap-1.5"
              title={i18n.language === 'zh' ? 'Switch to English' : '切换为中文'}
            >
              <Languages className="w-4 h-4" />
              {!collapsed && (
                <span className="text-[11px] font-medium uppercase">{i18n.language === 'zh' ? 'EN' : '中文'}</span>
              )}
            </button>
            <button
              onClick={toggleTheme}
              className="flex items-center justify-center flex-1 h-8 rounded-[var(--ag-radius-sm)] text-[var(--ag-text-tertiary)] hover:text-[var(--ag-text)] hover:bg-[var(--ag-bg-hover)] transition-colors"
              title={theme === 'dark' ? '切换亮色模式' : '切换暗色模式'}
            >
              {theme === 'dark' ? <Sun className="w-4 h-4" /> : <Moon className="w-4 h-4" />}
            </button>
            <button
              onClick={() => setCollapsed(!collapsed)}
              className="flex items-center justify-center flex-1 h-8 rounded-[var(--ag-radius-sm)] text-[var(--ag-text-tertiary)] hover:text-[var(--ag-text)] hover:bg-[var(--ag-bg-hover)] transition-colors"
            >
              {collapsed ? (
                <PanelLeft className="w-4 h-4" />
              ) : (
                <PanelLeftClose className="w-4 h-4" />
              )}
            </button>
          </div>
        </div>
      </aside>

      {/* 主内容 */}
      <main className="flex-1 overflow-auto">
        <div className="p-6 max-w-[1400px] mx-auto">
          {children}
        </div>
      </main>
    </div>
  );
}
