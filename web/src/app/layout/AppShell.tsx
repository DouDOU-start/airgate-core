import { type ReactNode, useEffect, useState } from 'react';
import { Link, useMatchRoute, useRouterState } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { useAuth } from '../providers/AuthProvider';
import { pluginsApi } from '../../shared/api/plugins';
import { queryKeys } from '../../shared/queryKeys';
import { useTheme } from '../providers/ThemeProvider';
import { useIsMobile } from '../../shared/hooks/useMediaQuery';
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
  Menu,
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

const adminMenuItems: MenuItem[] = [
  { path: '/', labelKey: 'nav.dashboard', icon: <LayoutDashboard className="w-[18px] h-[18px]" />, sectionKey: 'nav.overview' },
  { path: '/admin/users', labelKey: 'nav.users', icon: <Users className="w-[18px] h-[18px]" />, sectionKey: 'nav.management' },
  { path: '/admin/accounts', labelKey: 'nav.accounts', icon: <KeyRound className="w-[18px] h-[18px]" /> },
  { path: '/admin/groups', labelKey: 'nav.groups', icon: <FolderTree className="w-[18px] h-[18px]" /> },
  { path: '/admin/subscriptions', labelKey: 'nav.subscriptions', icon: <CreditCard className="w-[18px] h-[18px]" /> },
  { path: '/admin/proxies', labelKey: 'nav.proxies', icon: <Globe className="w-[18px] h-[18px]" /> },
  { path: '/admin/usage', labelKey: 'nav.usage', icon: <BarChart3 className="w-[18px] h-[18px]" /> },
  { path: '/admin/plugins', labelKey: 'nav.plugins', icon: <Puzzle className="w-[18px] h-[18px]" />, sectionKey: 'nav.system' },
  { path: '/admin/settings', labelKey: 'nav.settings', icon: <Settings className="w-[18px] h-[18px]" /> },
];

const userMenuItems: MenuItem[] = [
  { path: '/', labelKey: 'nav.my_overview', icon: <LayoutDashboard className="w-[18px] h-[18px]" />, sectionKey: 'nav.personal' },
  { path: '/profile', labelKey: 'nav.profile', icon: <User className="w-[18px] h-[18px]" /> },
  { path: '/keys', labelKey: 'nav.my_keys', icon: <Key className="w-[18px] h-[18px]" /> },
  { path: '/usage', labelKey: 'nav.my_usage', icon: <BarChart3 className="w-[18px] h-[18px]" /> },
];

function usePluginMenuItems(): MenuItem[] {
  const { data } = useQuery({
    queryKey: queryKeys.pluginsMenu(),
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
  const [mobileOpen, setMobileOpen] = useState(false);
  const isMobile = useIsMobile();
  const matchRoute = useMatchRoute();
  const routerPath = useRouterState({ select: (s) => s.location.pathname });

  // Close mobile drawer on route change
  useEffect(() => {
    setMobileOpen(false);
  }, [routerPath]);

  // Prevent body scroll when mobile drawer is open
  useEffect(() => {
    if (mobileOpen) {
      document.body.style.overflow = 'hidden';
      return () => { document.body.style.overflow = ''; };
    }
  }, [mobileOpen]);

  const isAdmin = user?.role === 'admin';
  const pluginMenuItems = usePluginMenuItems();
  const adminUserItems = userMenuItems
    .filter((item) => item.path !== '/')
    .map((item, i) => (i === 0 ? { ...item, sectionKey: 'nav.personal' } : item));
  const menuItems = isAdmin
    ? [...adminMenuItems, ...pluginMenuItems, ...adminUserItems]
    : [...userMenuItems, ...pluginMenuItems];

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

  const toggleLanguage = () => {
    const nextLang = i18n.language === 'zh' ? 'en' : 'zh';
    i18n.changeLanguage(nextLang);
    localStorage.setItem('lang', nextLang);
  };

  const activeItem = menuItems.find((item) => {
    if (item.path === '/') return !!matchRoute({ to: '/' });
    return !!matchRoute({ to: item.path, fuzzy: true });
  });
  const pageTitle = activeItem ? t(activeItem.labelKey, { defaultValue: activeItem.labelKey }) : '';
  useEffect(() => {
    document.title = 'AirGate';
  }, []);

  // On mobile, sidebar is always expanded inside the drawer
  const sidebarCollapsed = isMobile ? false : collapsed;

  const sidebarContent = (
    <>
      {/* Logo */}
      <div className="flex items-center h-16 px-4 border-b border-border">
        <div className="flex items-center gap-2.5 overflow-hidden">
          <div className="flex items-center justify-center w-8 h-8 rounded-[10px] bg-primary-subtle flex-shrink-0">
            <Zap className="w-4 h-4 text-primary" />
          </div>
          {!sidebarCollapsed && (
            <div className="overflow-hidden">
              <h1 className="text-sm font-semibold text-text tracking-tight whitespace-nowrap">
                AirGate
              </h1>
              <p className="text-[9px] text-text-tertiary font-mono tracking-[0.1em] uppercase">
                Control Panel
              </p>
            </div>
          )}
        </div>
      </div>

      {/* Nav */}
      <nav className="flex-1 overflow-y-auto py-3 px-2 space-y-4">
        {sections.map((section, si) => (
          <div key={si}>
            {section.titleKey && !sidebarCollapsed && (
              <p className="text-[10px] font-medium text-text-tertiary uppercase tracking-[0.1em] px-2.5 mb-1.5">
                {t(section.titleKey)}
              </p>
            )}
            {sidebarCollapsed && si > 0 && (
              <div className="h-px mx-3 mb-2 bg-border" />
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
                    className={`group flex items-center gap-2.5 rounded-[10px] transition-all duration-150 relative ${
                      sidebarCollapsed ? 'justify-center px-0 py-2.5 mx-1' : 'px-2.5 py-[7px]'
                    } ${
                      active
                        ? 'bg-primary-subtle text-primary'
                        : 'text-text-tertiary hover:text-text-secondary hover:bg-bg-hover'
                    }`}
                  >
                    {active && (
                      <div className="absolute left-0 top-0 bottom-0 flex items-center"><div className="w-[2px] h-3.5 rounded-r-full bg-primary" /></div>
                    )}
                    <span className="flex-shrink-0">{item.icon}</span>
                    {!sidebarCollapsed && (
                      <span className="text-[13px] font-medium truncate">{t(item.labelKey, { defaultValue: item.labelKey })}</span>
                    )}
                    {sidebarCollapsed && (
                      <div className="ag-glass-dropdown absolute left-full ml-2 px-2.5 py-1.5 rounded-lg text-xs text-text whitespace-nowrap opacity-0 invisible group-hover:opacity-100 group-hover:visible transition-all z-50">
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

      {/* Collapse toggle (desktop only) */}
      {!isMobile && (
        <div className="border-t border-border p-2.5">
          <button
            onClick={() => setCollapsed(!collapsed)}
            className="flex items-center justify-center w-full h-7 rounded-[10px] text-text-tertiary hover:text-text-secondary hover:bg-bg-hover transition-colors"
          >
            {collapsed ? (
              <PanelLeft className="w-3.5 h-3.5" />
            ) : (
              <PanelLeftClose className="w-3.5 h-3.5" />
            )}
          </button>
        </div>
      )}
    </>
  );

  return (
    <div className="flex h-screen">
      {/* Mobile backdrop */}
      {isMobile && mobileOpen && (
        <div
          className="fixed inset-0 z-40 bg-black/40"
          onClick={() => setMobileOpen(false)}
          style={{ animation: 'ag-fade-in 0.15s ease-out' }}
        />
      )}

      {/* Sidebar */}
      {isMobile ? (
        <aside
          className="fixed inset-y-0 left-0 z-50 flex flex-col bg-bg border-r border-border transition-transform duration-300 ease-in-out"
          style={{ width: 'var(--ag-sidebar-width)', transform: mobileOpen ? 'translateX(0)' : 'translateX(-100%)' }}
        >
          {sidebarContent}
        </aside>
      ) : (
        <aside
          className="relative flex flex-col border-r border-border bg-bg transition-all duration-300 ease-in-out"
          style={{ width: collapsed ? 'var(--ag-sidebar-collapsed)' : 'var(--ag-sidebar-width)' }}
        >
          {sidebarContent}
        </aside>
      )}

      {/* Main content */}
      <div className="flex-1 flex flex-col overflow-hidden">
        {/* Header */}
        <header className="flex items-center justify-between h-16 px-4 md:px-6 border-b border-border bg-bg shrink-0">
          <div className="flex items-center gap-3">
            {isMobile && (
              <button
                onClick={() => setMobileOpen(true)}
                className="flex items-center justify-center w-8 h-8 rounded-[10px] text-text-tertiary hover:text-text hover:bg-bg-hover transition-colors"
              >
                <Menu className="w-5 h-5" />
              </button>
            )}
            <h2 className="text-sm font-semibold text-text">{pageTitle}</h2>
          </div>
          <div className="flex items-center gap-1.5">
            {/* Language toggle */}
            <button
              onClick={toggleLanguage}
              className="flex items-center justify-center h-8 px-2.5 rounded-[10px] text-text-tertiary hover:text-text-secondary hover:bg-bg-hover transition-colors gap-1.5"
              title={i18n.language === 'zh' ? 'Switch to English' : '切换为中文'}
            >
              <Languages className="w-3.5 h-3.5" />
              <span className="text-[10px] font-mono uppercase hidden sm:inline">{i18n.language === 'zh' ? 'EN' : '中文'}</span>
            </button>
            {/* Theme toggle */}
            <button
              onClick={toggleTheme}
              className="flex items-center justify-center w-8 h-8 rounded-[10px] text-text-tertiary hover:text-text-secondary hover:bg-bg-hover transition-colors"
              title={theme === 'dark' ? '切换亮色模式' : '切换暗色模式'}
            >
              {theme === 'dark' ? <Sun className="w-3.5 h-3.5" /> : <Moon className="w-3.5 h-3.5" />}
            </button>

            <div className="w-px h-5 bg-border mx-1.5" />

            {/* User info */}
            <div className="flex items-center gap-2.5 pl-1">
              <div className="text-right hidden sm:block">
                <p className="text-xs font-medium text-text leading-tight">
                  {user?.username || user?.email}
                </p>
                <p className="text-[10px] text-text-tertiary leading-tight font-mono">
                  {user?.role === 'admin' ? t('nav.admin') : t('nav.user')}
                </p>
              </div>
              <div className="flex items-center justify-center w-7 h-7 rounded-full bg-primary-subtle text-[10px] font-bold text-primary">
                {user?.email?.charAt(0).toUpperCase() || 'U'}
              </div>
              <button
                onClick={logout}
                className="flex items-center justify-center w-7 h-7 rounded-[10px] text-text-tertiary hover:text-danger hover:bg-danger-subtle transition-all"
                title={t('common.logout')}
              >
                <LogOut className="w-3.5 h-3.5" />
              </button>
            </div>
          </div>
        </header>

        <main className="flex-1 overflow-auto">
          <div className="p-4 md:p-6 lg:p-8 max-w-[1400px] mx-auto">
            {children}
          </div>
        </main>
      </div>
    </div>
  );
}
