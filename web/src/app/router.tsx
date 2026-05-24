import {
  createRouter,
  createRootRoute,
  createRoute,
  Outlet,
  redirect,
} from '@tanstack/react-router';
import { Suspense, useEffect } from 'react';
import type { ElementType, ReactNode } from 'react';
import type { PluginBreadcrumbItem } from '../shared/components/PluginBreadcrumbs';
import { useAuth } from './providers/AuthProvider';
import { ErrorBoundary } from './providers/ErrorBoundary';
import { getToken, getTokenRole } from '../shared/api/client';
import { ChatPageLoading, FullPageLoading, PageLoading } from '../shared/components/PageLoading';
import { checkAdmin, withSetupCheck } from './routeGuards';
import {
  AccountsPage,
  ADMIN_IDLE_PRELOADS,
  DashboardPage,
  DocsPage,
  GroupsPage,
  lazyWithPreload,
  LoginPage,
  PluginPage,
  PluginsPage,
  preloadRoutePage,
  ProfilePage,
  ProxiesPage,
  PublicHomePage,
  SettingsPage,
  SetupPage,
  SubscriptionsPage,
  UsagePage,
  UserKeysPage,
  UserOverviewPage,
  USER_IDLE_PRELOADS,
  UsersPage,
  UserUsagePage,
} from './routePreloads';

function requestIdle(work: () => void) {
  const runtime = globalThis as typeof globalThis & {
    cancelIdleCallback?: (id: number) => void;
    requestIdleCallback?: (callback: () => void, options?: { timeout: number }) => number;
  };

  if (runtime.requestIdleCallback) {
    const id = runtime.requestIdleCallback(work, { timeout: 2500 });
    return () => runtime.cancelIdleCallback?.(id);
  }

  const id = globalThis.setTimeout(work, 500);
  return () => globalThis.clearTimeout(id);
}

const AppShell = lazyWithPreload<{ children: ReactNode }>(() =>
  import('./layout/AppShell').then((m) => ({ default: m.AppShell })),
);
const PluginShell = lazyWithPreload<{
  children: ReactNode;
  pluginName?: string;
  titleKey?: string;
  titleFallback?: string;
  breadcrumbs?: PluginBreadcrumbItem[];
}>(() =>
  import('./layout/PluginShell').then((m) => ({ default: m.PluginShell })),
);

function RoutePreloader() {
  const { user, isAPIKeySession } = useAuth();
  const hasUser = Boolean(user);
  const userRole = user?.role;

  useEffect(() => {
    if (!hasUser) return;

    const pages = isAPIKeySession
      ? [UserUsagePage]
      : userRole === 'admin'
        ? ADMIN_IDLE_PRELOADS
        : USER_IDLE_PRELOADS;
    let index = 0;
    let cancelIdle = () => {};
    let cancelled = false;

    const preloadNext = () => {
      if (cancelled || index >= pages.length) return;
      const page = pages[index++];
      if (!page) return;
      void preloadRoutePage(page).finally(() => {
        if (!cancelled) cancelIdle = requestIdle(preloadNext);
      });
    };

    cancelIdle = requestIdle(preloadNext);
    return () => {
      cancelled = true;
      cancelIdle();
    };
  }, [hasUser, isAPIKeySession, userRole]);

  return null;
}

// 根路由
const rootRoute = createRootRoute({
  component: () => (
    <ErrorBoundary>
      <Outlet />
    </ErrorBoundary>
  ),
});

// 安装向导（无需认证，懒加载）
const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/setup',
  beforeLoad: () => withSetupCheck((needs) => {
    if (!needs) throw redirect({ to: '/login' });
  }),
  component: () => (
    <Suspense fallback={<FullPageLoading />}>
      <SetupPage />
    </Suspense>
  ),
});

// 公共首页（无需认证，懒加载）
const homeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/home',
  beforeLoad: () => withSetupCheck((needs) => {
    if (needs) throw redirect({ to: '/setup' });
  }),
  component: () => (
    <Suspense fallback={<FullPageLoading />}>
      <PublicHomePage />
    </Suspense>
  ),
});

// 注意：/status 不再注册客户端路由，整个公开状态页交给 airgate-health 插件维护。
// 后端 GET /status 直接反代到插件的 handlePublicIndex，前端用普通 href 跳转。
// 这样避免 core 与插件出现两份重复的状态页实现。

// 内置默认文档页 —— 当管理员未在 系统设置 → 站点品牌 → 文档链接 中填写外部 URL 时，
// 所有"文档"按钮 fallback 到这里。公开可访问，独立布局（不挂 AppShell）。
const docsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/docs',
  component: () => (
    <Suspense fallback={<FullPageLoading />}>
      <DocsPage />
    </Suspense>
  ),
});

// 登录页（无需认证，懒加载）
const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  beforeLoad: () => withSetupCheck((needs) => {
    if (needs) throw redirect({ to: '/setup' });
  }),
  component: () => (
    <Suspense fallback={<FullPageLoading />}>
      <LoginPage />
    </Suspense>
  ),
});

// 认证布局（需要登录）
const authLayout = createRoute({
  getParentRoute: () => rootRoute,
  id: 'auth',
  beforeLoad: () => withSetupCheck((needs) => {
    if (needs) throw redirect({ to: '/setup' });
    if (!getToken()) throw redirect({ to: '/home' });
  }),
  component: () => (
    <Suspense fallback={<FullPageLoading />}>
      <AppShell>
        <RoutePreloader />
        <Outlet />
      </AppShell>
    </Suspense>
  ),
});

function HomePage() {
  const { user, loading, isAPIKeySession } = useAuth();
  if (loading) return <PageLoading />;
  if (!user) return null;

  const isAdmin = !isAPIKeySession && (getTokenRole() === 'admin' || user.role === 'admin');
  const Page = isAPIKeySession ? UserUsagePage : isAdmin ? DashboardPage : UserOverviewPage;
  return (
    <Suspense fallback={<PageLoading />}>
      <Page />
    </Suspense>
  );
}
const dashboardRoute = createRoute({ getParentRoute: () => authLayout, path: '/', component: HomePage });

// 管理员布局（需要 admin 角色）
const adminLayout = createRoute({
  getParentRoute: () => authLayout,
  id: 'admin',
  beforeLoad: () => checkAdmin(),
  component: Outlet,
});

function renderPage(Page: ElementType) {
  return () => (
    <Suspense fallback={<PageLoading />}>
      <Page />
    </Suspense>
  );
}

const adminUsersRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/users', component: renderPage(UsersPage) });
const adminAccountsRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/accounts', component: renderPage(AccountsPage) });
const adminGroupsRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/groups', component: renderPage(GroupsPage) });
const adminSubscriptionsRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/subscriptions', component: renderPage(SubscriptionsPage) });
const adminProxiesRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/proxies', component: renderPage(ProxiesPage) });
const adminUsageRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/usage', component: renderPage(UsagePage) });
const adminPluginsRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/plugins', component: renderPage(PluginsPage) });
const adminSettingsRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/settings', component: renderPage(SettingsPage) });

const profileRoute = createRoute({ getParentRoute: () => authLayout, path: '/profile', component: renderPage(ProfilePage) });
const userKeysRoute = createRoute({ getParentRoute: () => authLayout, path: '/keys', component: renderPage(UserKeysPage) });
const userUsageRoute = createRoute({ getParentRoute: () => authLayout, path: '/usage', component: renderPage(UserUsagePage) });

// /chat: 全屏沉浸式 AI 对话页（airgate-playground 插件），独立布局不挂 AppShell。
// 仍要求登录 + 安装完成；走 PluginShell 通用插件顶栏。
const chatBeforeLoad = () => withSetupCheck((needs) => {
  if (needs) throw redirect({ to: '/setup' });
  if (!getToken()) throw redirect({ to: '/home' });
  if (getTokenRole() === 'api_key') throw redirect({ to: '/' });
});
const chatRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/chat',
  beforeLoad: chatBeforeLoad,
  component: () => (
    <Suspense fallback={<ChatPageLoading />}>
      <PluginShell
        pluginName="airgate-playground"
        titleKey="plugin_shell.playground_title"
        titleFallback="AI 对话"
        breadcrumbs={[
          { to: '/', labelKey: 'plugin_shell.console', labelFallback: '控制台' },
          { labelKey: 'plugin_shell.playground_title', labelFallback: 'AI 对话' },
        ]}
      >
        <PluginPage pluginNameOverride="airgate-playground" subPathOverride="/chat" />
      </PluginShell>
    </Suspense>
  ),
});
// /studio: 创作中心（airgate-studio 插件），独立全屏布局。
const studioRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/studio',
  beforeLoad: chatBeforeLoad,
  component: () => (
    <Suspense fallback={<ChatPageLoading />}>
      <PluginPage pluginNameOverride="airgate-studio" subPathOverride="/studio" />
    </Suspense>
  ),
});

// 旧路径 /plugins/playground 重定向到 /chat，避免历史书签 / 链接失效。
const playgroundLegacyRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/plugins/playground',
  beforeLoad: () => {
    throw redirect({ to: '/chat' });
  },
  component: () => null,
});

// 插件页面路由（catch-all）
const pluginRoute = createRoute({
  getParentRoute: () => authLayout,
  path: '/plugins/$pluginName/$',
  beforeLoad: () => {
    if (getTokenRole() === 'api_key') throw redirect({ to: '/' });
  },
  component: () => (
    <Suspense fallback={<PageLoading />}>
      <PluginPage />
    </Suspense>
  ),
});

// 路由树
const routeTree = rootRoute.addChildren([
  setupRoute,
  homeRoute,
  loginRoute,
  docsRoute,
  studioRoute,
  chatRoute,
  playgroundLegacyRoute,
  authLayout.addChildren([
    dashboardRoute,
    adminLayout.addChildren([
      adminUsersRoute,
      adminAccountsRoute,
      adminGroupsRoute,
      adminSubscriptionsRoute,
      adminProxiesRoute,
      adminUsageRoute,
      adminPluginsRoute,
      adminSettingsRoute,
    ]),
    profileRoute,
    userKeysRoute,
    userUsageRoute,
    pluginRoute,
  ]),
]);

export const router = createRouter({
  routeTree,
  defaultPreload: 'intent',
  defaultPreloadStaleTime: 0,
});
