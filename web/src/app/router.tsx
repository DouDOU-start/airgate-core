import {
  createRouter,
  createRootRoute,
  createRoute,
  Outlet,
  redirect,
} from '@tanstack/react-router';
import { Suspense, useEffect } from 'react';
import type { ElementType, ReactNode } from 'react';
import { useAuth } from './providers/AuthProvider';
import { ErrorBoundary } from './providers/ErrorBoundary';
import { getToken, getTokenRole } from '../shared/api/client';
import { FullPageLoading, PageLoading } from '../shared/components/PageLoading';
import { checkAdmin, withSetupCheck } from './routeGuards';
import {
  ADMIN_IDLE_PRELOADS,
  AnnouncementsPage,
  ChannelsPage,
  DashboardPage,
  DocsPage,
  GroupsPage,
  lazyWithPreload,
  LoginPage,
  ModelPricesPage,
  preloadRoutePage,
  ProfilePage,
  PublicHomePage,
  SettingsPage,
  SetupPage,
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
const adminChannelsRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/channels', component: renderPage(ChannelsPage) });
const adminModelPricesRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/model-prices', component: renderPage(ModelPricesPage) });
const adminGroupsRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/groups', component: renderPage(GroupsPage) });
const adminAnnouncementsRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/announcements', component: renderPage(AnnouncementsPage) });
const adminUsageRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/usage', component: renderPage(UsagePage) });
const adminSettingsRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/settings', component: renderPage(SettingsPage) });

const profileRoute = createRoute({ getParentRoute: () => authLayout, path: '/profile', component: renderPage(ProfilePage) });
const userKeysRoute = createRoute({ getParentRoute: () => authLayout, path: '/keys', component: renderPage(UserKeysPage) });
const userUsageRoute = createRoute({ getParentRoute: () => authLayout, path: '/usage', component: renderPage(UserUsagePage) });

// 路由树
const routeTree = rootRoute.addChildren([
  setupRoute,
  homeRoute,
  loginRoute,
  docsRoute,
  authLayout.addChildren([
    dashboardRoute,
    adminLayout.addChildren([
      adminUsersRoute,
      adminChannelsRoute,
      adminModelPricesRoute,
      adminGroupsRoute,
      adminAnnouncementsRoute,
      adminUsageRoute,
      adminSettingsRoute,
    ]),
    profileRoute,
    userKeysRoute,
    userUsageRoute,
  ]),
]);

export const router = createRouter({
  routeTree,
  defaultPreload: 'intent',
  defaultPreloadStaleTime: 0,
});
