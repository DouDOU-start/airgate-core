import { type ReactNode, useEffect, useMemo, useState } from 'react';
import { Link, useMatchRoute, useRouterState } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { useIsFetching, useQuery } from '@tanstack/react-query';
import { Button, Link as HeroLink, Tooltip } from '@heroui/react';
import { useAuth } from '../providers/AuthProvider';
import { getTokenRole } from '../../shared/api/client';
import { setStoredLanguage } from '../../i18n';
import { settingsApi } from '../../shared/api/settings';
import { useTheme } from '../providers/ThemeProvider';
import { useSiteSettings, defaultLogoUrl } from '../providers/SiteSettingsProvider';
import { effectiveDocUrl } from '../../shared/utils/docUrl';
import { useIsMobile } from '../../shared/hooks/useMediaQuery';
import { usePersistentBoolean } from '../../shared/hooks/usePersistentBoolean';
import { TopLoadingLine } from '../../shared/components/PageLoading';
import { AnnouncementBell } from '../../shared/components/announcements/AnnouncementBell';
import { AnnouncementPopup } from '../../shared/components/announcements/AnnouncementPopup';
import {
  LayoutDashboard,
  Users,
  Network,
  Boxes,
  FolderTree,
  KeyRound,
  ChartNoAxesCombined,
  ReceiptText,
  Megaphone,
  Settings,
  UserRoundCog,
  LogOut,
  Languages,
  Sun,
  Moon,
  Menu,
  ShieldCheck,
  BookOpen,
  MessageCircle,
  Github,
  HelpCircle,
  ChevronLeft,
  ChevronRight,
  CreditCard,
  Ticket,
  Wallet,
  AppWindow,
} from 'lucide-react';
import { oauthApi } from '../../shared/api/oauth';
import { queryKeys } from '../../shared/queryKeys';

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
  { path: '/', labelKey: 'nav.dashboard', icon: <LayoutDashboard className="h-5 w-5" />, sectionKey: 'nav.overview' },
  { path: '/admin/users', labelKey: 'nav.users', icon: <Users className="h-5 w-5" />, sectionKey: 'nav.management' },
  { path: '/admin/channels', labelKey: 'nav.channels', icon: <Network className="h-5 w-5" /> },
  { path: '/admin/model-prices', labelKey: 'nav.model_prices', icon: <Boxes className="h-5 w-5" /> },
  { path: '/admin/groups', labelKey: 'nav.groups', icon: <FolderTree className="h-5 w-5" /> },
  { path: '/admin/usage', labelKey: 'nav.usage', icon: <ChartNoAxesCombined className="h-5 w-5" /> },
  { path: '/admin/payment', labelKey: 'nav.payment', icon: <CreditCard className="h-5 w-5" /> },
  { path: '/admin/redemption', labelKey: 'nav.redemption', icon: <Ticket className="h-5 w-5" /> },
  { path: '/admin/announcements', labelKey: 'nav.announcements', icon: <Megaphone className="h-5 w-5" /> },
  { path: '/admin/oauth-clients', labelKey: 'nav.oauth_clients', icon: <AppWindow className="h-5 w-5" /> },
  { path: '/admin/settings', labelKey: 'nav.settings', icon: <Settings className="h-5 w-5" />, sectionKey: 'nav.system' },
];

const userMenuItems: MenuItem[] = [
  { path: '/overview', labelKey: 'nav.my_overview', icon: <LayoutDashboard className="h-5 w-5" />, sectionKey: 'nav.personal' },
  { path: '/profile', labelKey: 'nav.profile', icon: <UserRoundCog className="h-5 w-5" /> },
  { path: '/keys', labelKey: 'nav.my_keys', icon: <KeyRound className="h-5 w-5" /> },
  { path: '/usage', labelKey: 'nav.my_usage', icon: <ReceiptText className="h-5 w-5" /> },
  { path: '/recharge', labelKey: 'nav.recharge', icon: <Wallet className="h-5 w-5" /> },
];

// API Key 登录只能看使用记录
const apiKeyMenuItems: MenuItem[] = [
  { path: '/usage', labelKey: 'nav.my_usage', icon: <ReceiptText className="h-5 w-5" />, sectionKey: 'nav.personal' },
];

const SIDEBAR_COLLAPSED_STORAGE_KEY = 'airgate:sidebar:collapsed';

export function AppShell({ children }: AppShellProps) {
  const { user, logout } = useAuth();
  const { t, i18n } = useTranslation();
  const { theme, toggleTheme } = useTheme();
  const site = useSiteSettings();
  // 文档入口：仅当管理员填写了外部 doc_url 时才显示
  const docsUrl = effectiveDocUrl(site.doc_url);
  const [collapsed, setCollapsed] = usePersistentBoolean(SIDEBAR_COLLAPSED_STORAGE_KEY, false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const isMobile = useIsMobile();
  const matchRoute = useMatchRoute();
  const routerPath = useRouterState({ select: (s) => s.location.pathname });
  const routerStatus = useRouterState({ select: (s) => s.status });
  const blockingFetches = useIsFetching({
    predicate: (query) => (
      query.state.fetchStatus === 'fetching'
      && (query.meta as { globalLoading?: boolean } | undefined)?.globalLoading !== false
    ),
  });
  const topLoadingActive = routerStatus === 'pending' || blockingFetches > 0;

  // 路由切换时收起移动端抽屉
  useEffect(() => {
    setMobileOpen(false);
  }, [routerPath]);

  // 移动端抽屉打开时禁止 body 滚动
  useEffect(() => {
    if (mobileOpen) {
      document.body.style.overflow = 'hidden';
      return () => { document.body.style.overflow = ''; };
    }
  }, [mobileOpen]);

  const isAPIKeySession = user?.role === 'api_key' || !!(user?.api_key_id && user.api_key_id > 0);
  const isAdmin = !isAPIKeySession && (getTokenRole() === 'admin' || user?.role === 'admin');

  // 仅管理员拉取 core 版本号；普通用户和 API Key 会话不暴露版本指纹。
  const { data: coreVersion } = useQuery({
    queryKey: ['core-version'],
    queryFn: () => settingsApi.getCoreVersion(),
    enabled: isAdmin && !isAPIKeySession,
    staleTime: 5 * 60_000,
    refetchOnWindowFocus: false,
  });

  // 应用入口（管理员在「应用接入」里配置 show_in_nav 的 OAuth 应用）
  const { data: navApps } = useQuery({
    queryKey: queryKeys.navApps(),
    queryFn: () => oauthApi.listApps(),
    enabled: !!user && !isAPIKeySession,
    staleTime: 5 * 60_000,
    refetchOnWindowFocus: false,
    meta: { globalLoading: false },
  });
  const appEntries = (navApps ?? []).filter((app) => app.launch_url);
  const sections = useMemo(() => {
    // 个人概览已独立在 /overview，与管理仪表盘（/）不再冲突，管理员直接拼完整用户菜单。
    const menuItems = isAPIKeySession
      ? apiKeyMenuItems
      : isAdmin
        ? [...adminMenuItems, ...userMenuItems]
        : [...userMenuItems];

    const nextSections: Array<{ titleKey?: string; items: MenuItem[] }> = [];
    let currentSection: { titleKey?: string; items: MenuItem[] } | null = null;

    menuItems.forEach((item) => {
      if (item.sectionKey) {
        currentSection = { titleKey: item.sectionKey, items: [item] };
        nextSections.push(currentSection);
      } else if (currentSection) {
        currentSection.items.push(item);
      } else {
        currentSection = { items: [item] };
        nextSections.push(currentSection);
      }
    });

    return nextSections;
  }, [isAPIKeySession, isAdmin]);

  const toggleLanguage = () => {
    const nextLang = i18n.language === 'zh' ? 'en' : 'zh';
    i18n.changeLanguage(nextLang);
    setStoredLanguage(nextLang);
  };

  const displayName = user?.username || user?.email?.split('@')[0] || site.site_name || 'AirGate';
  const roleLabel = user?.role === 'api_key'
    ? 'API Key'
    : isAdmin ? t('users.role_admin', 'Admin') : t('users.role_user', 'User');
  // 移动端抽屉内侧边栏恒为展开态
  const sidebarCollapsed = isMobile ? false : collapsed;

  const sidebarContent = (
    <>
      <div className="flex h-20 items-center px-4">
        <div className={`flex min-w-0 ${sidebarCollapsed ? 'w-full flex-col items-center justify-center' : 'w-full items-center gap-3'}`}>
          <div className="relative flex h-10 w-10 shrink-0 items-center justify-center overflow-hidden rounded-[var(--radius)] bg-primary-subtle">
            <img src={site.site_logo || defaultLogoUrl} alt="" className="h-full w-full object-cover" />
          </div>
          {!sidebarCollapsed && (
            <div className="min-w-0 flex-1">
              <div className="flex min-w-0 items-center gap-1.5">
                <h1 className="truncate text-sm font-semibold text-text">{displayName}</h1>
                {coreVersion?.version && (
                  <span
                    className="shrink-0 text-[9px] text-text-tertiary font-mono"
                    title={`${coreVersion.version} · ${coreVersion.platform} · ${coreVersion.go_version}`}
                  >
                    {coreVersion.version}
                  </span>
                )}
              </div>
              <p className="mt-0.5 truncate text-xs text-text-tertiary">{roleLabel}</p>
            </div>
          )}
          {!isMobile && !sidebarCollapsed && (
            <Button
              aria-label={t('nav.collapse_sidebar', 'Collapse sidebar')}
              className="ag-sidebar-collapse-button shrink-0"
              isIconOnly
              size="sm"
              variant="ghost"
              onPress={() => setCollapsed(true)}
            >
              <ChevronLeft className="h-4 w-4" />
            </Button>
          )}
        </div>
      </div>

      {!isMobile && sidebarCollapsed && (
        <div className="mb-1 flex justify-center">
          <Button
            aria-label={t('nav.expand_sidebar', 'Expand sidebar')}
            className="ag-sidebar-collapse-button"
            isIconOnly
            size="sm"
            variant="ghost"
            onPress={() => setCollapsed(false)}
          >
            <ChevronRight className="h-4 w-4" />
          </Button>
        </div>
      )}

      <nav className={`ag-sidebar-nav flex-1 overflow-y-auto pb-4 space-y-5 ${sidebarCollapsed ? 'px-0' : 'px-3'}`}>
        {sections.map((section, si) => (
          <div key={si}>
            {section.titleKey && !sidebarCollapsed && (
              <p className="ag-kicker px-2.5 pb-2">
                {t(section.titleKey)}
              </p>
            )}
            {sidebarCollapsed && si > 0 && (
              <div className="mx-3 mb-2.5 h-px bg-border" />
            )}
            <div className="space-y-1">
              {section.items.map((item) => {
                const isCurrentActive = item.path === '/'
                  ? !!matchRoute({ to: '/' })
                  : !!matchRoute({ to: item.path, fuzzy: true });
                const isPendingActive = item.path === '/'
                  ? !!matchRoute({ to: '/', pending: true })
                  : !!matchRoute({ to: item.path, fuzzy: true, pending: true });
                const active = routerStatus === 'pending' ? isPendingActive : isCurrentActive;
                const label = t(item.labelKey, { defaultValue: item.labelKey });

                const link = (
                  <Link
                    key={item.path}
                    to={item.path}
                    preload={false}
                    data-active={active ? 'true' : undefined}
                    className={`ag-sidebar-nav-item group relative flex items-center transition-colors duration-150 ${sidebarCollapsed ? 'mx-auto h-10 w-10 justify-center p-0' : 'px-2 py-1.5'}`}
                  >
                    <span className="flex shrink-0 items-center justify-center">{item.icon}</span>
                    {!sidebarCollapsed && (
                      <span className="ag-sidebar-nav-item-label truncate">{label}</span>
                    )}
                  </Link>
                );

                return sidebarCollapsed ? (
                  <Tooltip key={item.path}>
                    <Tooltip.Trigger className="block w-full">{link}</Tooltip.Trigger>
                    <Tooltip.Content>{label}</Tooltip.Content>
                  </Tooltip>
                ) : link;
              })}
            </div>
          </div>
        ))}

        {/* 应用入口：外链跳转到独立部署的 OAuth 应用（对话、创作中心等） */}
        {appEntries.length > 0 && (
          <div>
            {!sidebarCollapsed && (
              <p className="ag-kicker px-2.5 pb-2">
                {t('nav.apps')}
              </p>
            )}
            {sidebarCollapsed && <div className="mx-3 mb-2.5 h-px bg-border" />}
            <div className="space-y-1">
              {appEntries.map((app) => {
                const icon = app.icon.startsWith('http://') || app.icon.startsWith('https://')
                  ? <img alt="" className="h-5 w-5 rounded" src={app.icon} />
                  : app.icon
                    ? <span className="text-base leading-none">{app.icon}</span>
                    : <AppWindow className="h-5 w-5" />;
                const link = (
                  <a
                    key={app.launch_url}
                    className={`ag-sidebar-nav-item group relative flex items-center transition-colors duration-150 ${sidebarCollapsed ? 'mx-auto h-10 w-10 justify-center p-0' : 'px-2 py-1.5'}`}
                    href={app.launch_url}
                    rel="noreferrer"
                    target="_blank"
                    title={app.description || undefined}
                  >
                    <span className="flex shrink-0 items-center justify-center">{icon}</span>
                    {!sidebarCollapsed && (
                      <span className="ag-sidebar-nav-item-label truncate">{app.name}</span>
                    )}
                  </a>
                );
                return sidebarCollapsed ? (
                  <Tooltip key={app.launch_url}>
                    <Tooltip.Trigger className="block w-full">{link}</Tooltip.Trigger>
                    <Tooltip.Content>{app.name}</Tooltip.Content>
                  </Tooltip>
                ) : link;
              })}
            </div>
          </div>
        )}
      </nav>

      {docsUrl && (
        <div className="space-y-1 border-t border-border p-3">
          {!sidebarCollapsed && (
            <Button
              className="w-full justify-center"
              size="sm"
              variant="ghost"
              onPress={() => { window.open(docsUrl, '_blank', 'noopener,noreferrer'); }}
            >
              <HelpCircle className="h-4 w-4" />
              {t('nav.docs')}
            </Button>
          )}
          {!isMobile && sidebarCollapsed && (
            <Button
              aria-label={t('nav.docs')}
              className="w-full"
              isIconOnly
              size="sm"
              variant="ghost"
              onPress={() => { window.open(docsUrl, '_blank', 'noopener,noreferrer'); }}
            >
              <HelpCircle className="h-4 w-4" />
            </Button>
          )}
        </div>
      )}
    </>
  );

  return (
    <div className="fixed inset-0 flex overflow-hidden bg-bg text-text">
      <TopLoadingLine active={topLoadingActive} />

      {/* 移动端遮罩 */}
      {isMobile && mobileOpen && (
        <div
          className="fixed inset-0 z-40 bg-black/40"
          onClick={() => setMobileOpen(false)}
        />
      )}

      {/* 侧边栏 */}
      {isMobile ? (
        <aside
          className="fixed inset-y-0 left-0 z-50 flex flex-col bg-surface border-r border-border transition-transform duration-150 ease-out"
          style={{ width: 'var(--ag-sidebar-width)', transform: mobileOpen ? 'translateX(0)' : 'translateX(-100%)' }}
        >
          {sidebarContent}
        </aside>
      ) : (
        <aside
          className="relative flex flex-col border-r border-border bg-surface transition-[width] duration-150 ease-out"
          style={{ width: collapsed ? 'var(--ag-sidebar-collapsed)' : 'var(--ag-sidebar-width)' }}
        >
          {sidebarContent}
        </aside>
      )}

      {/* 主内容区 */}
      <div className="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
        <header className="ag-topbar pointer-events-auto absolute inset-x-0 top-0 z-20 flex h-12 items-center justify-between gap-3 px-4 md:px-5">
          <div className="flex shrink-0 items-center gap-3">
            {isMobile && (
              <Button
                aria-label={t('nav.open_menu', 'Open menu')}
                isIconOnly
                size="sm"
                variant="ghost"
                onPress={() => {
                  setMobileOpen(true);
                }}
              >
                <Menu className="h-5 w-5" />
              </Button>
            )}
          </div>

          <div className="flex shrink-0 items-center gap-2">
            {/* GitHub */}
            <HeroLink
              href="https://github.com/DouDOU-start/airgate-core"
              target="_blank"
              rel="noopener noreferrer"
              aria-label="GitHub"
              className="hidden h-10 w-10 items-center justify-center rounded-[var(--radius)] text-text-secondary transition-colors hover:text-text sm:flex"
            >
              <Github className="h-5 w-5" />
            </HeroLink>
            {/* Docs：仅当管理员配置了外部文档链接时显示 */}
            {docsUrl && (
              <HeroLink
                href={docsUrl}
                target="_blank"
                rel="noopener noreferrer"
                aria-label={t('nav.docs')}
                className="hidden h-10 w-10 items-center justify-center rounded-[var(--radius)] text-text-secondary transition-colors hover:text-text sm:flex"
              >
                <BookOpen className="h-5 w-5" />
              </HeroLink>
            )}
            {/* 联系方式 */}
            {site.contact_info && (
              <div className="hidden items-center gap-2 text-text-tertiary lg:flex">
                <MessageCircle className="h-5 w-5 shrink-0" />
                <span className="text-sm">{site.contact_info}</span>
              </div>
            )}
            {/* 语言切换 */}
            <Button
              aria-label={i18n.language === 'zh' ? 'Switch to English' : '切换为中文'}
              className="h-10 px-3"
              size="sm"
              variant="ghost"
              onPress={toggleLanguage}
            >
              <Languages className="h-5 w-5" />
              <span className="hidden w-8 text-center font-mono text-xs uppercase sm:inline-block">{i18n.language === 'zh' ? 'EN' : '中文'}</span>
            </Button>
            {/* 主题切换 */}
            <Button
              aria-label={theme === 'dark' ? t('common.toggle_theme_light') : t('common.toggle_theme_dark')}
              className="h-10 w-10"
              isIconOnly
              size="sm"
              variant="ghost"
              onPress={toggleTheme}
            >
              {theme === 'dark' ? <Sun className="h-5 w-5" /> : <Moon className="h-5 w-5" />}
            </Button>
            {/* 公告铃铛（API Key 会话无公告接口权限，不展示） */}
            {!isAPIKeySession && <AnnouncementBell />}

            <div className="mx-1.5 hidden h-6 w-px bg-border sm:block" />

            <div className="hidden items-center gap-2.5 pl-1 sm:flex">
              {!isAPIKeySession && (
                <div className="hidden text-right md:block">
                  <p className="flex items-center justify-end gap-1.5 text-sm font-medium leading-tight text-text">
                    {user?.tier_name ? (
                      <span className="inline-flex shrink-0 items-center rounded-full bg-warning-subtle px-1.5 py-px text-[10px] font-medium leading-tight text-warning">
                        {user.tier_name}
                      </span>
                    ) : null}
                    <span className="min-w-0 truncate">{displayName}</span>
                  </p>
                  <p className="text-xs leading-tight text-text-tertiary">
                    {user?.email}
                  </p>
                </div>
              )}
              {isAdmin ? (
                <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-[var(--radius)] text-primary">
                  <ShieldCheck className="h-5 w-5" />
                </div>
              ) : (
                <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-[var(--radius)] text-sm font-bold text-primary">
                  {(user?.username || user?.email || 'U').charAt(0).toUpperCase()}
                </div>
              )}
            </div>

            {/* 退出登录 */}
            <div className="mx-1 hidden h-6 w-px bg-border sm:block" />
            <Button
              aria-label={t('common.logout')}
              className="h-10 w-10 text-text-secondary hover:bg-danger/10 hover:text-danger"
              isIconOnly
              size="sm"
              variant="ghost"
              onPress={logout}
            >
              <LogOut className="h-5 w-5" />
            </Button>
          </div>
        </header>

        {/* popup 模式未读公告弹窗（逐条强提醒） */}
        {!isAPIKeySession && <AnnouncementPopup />}

        <main className="min-h-0 flex-1 overflow-auto bg-bg pt-12 ag-main">
          <div className="ag-main-content mx-auto w-full max-w-[1920px] p-4 md:p-6 2xl:p-8">
            {children}
          </div>
        </main>
      </div>
    </div>
  );
}
