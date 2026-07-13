import {
  createRouter,
  createRootRoute,
  createRoute,
  Navigate,
  Outlet,
  redirect,
} from '@tanstack/react-router';
import { Suspense, useEffect } from 'react';
import type { ElementType, ReactNode } from 'react';
import { useAuth } from './providers/AuthProvider';
import { ErrorBoundary } from './providers/ErrorBoundary';
import { getToken, getTokenRole } from '../shared/api/client';
import { FullPageLoading, PageLoading } from '../shared/components/PageLoading';
import { checkAdmin } from './routeGuards';
import {
  ADMIN_IDLE_PRELOADS,
  AnnouncementsPage,
  ChannelsPage,
  DashboardPage,
  GroupsPage,
  InvitePage,
  InviteRebatePage,
  lazyWithPreload,
  LoginPage,
  ModelPricesPage,
  OAuthAuthorizePage,
  OAuthClientsPage,
  PaymentPage,
  preloadRoutePage,
  RedemptionCodesPage,
  ProfilePage,
  PublicHomePage,
  RechargePage,
  SettingsPage,
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

// 公共首页（无需认证，懒加载）
const homeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/home',
  component: () => (
    <Suspense fallback={<FullPageLoading />}>
      <PublicHomePage />
    </Suspense>
  ),
});

// 登录页（无需认证，懒加载）
const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  component: () => (
    <Suspense fallback={<FullPageLoading />}>
      <LoginPage />
    </Suspense>
  ),
});

// OAuth 授权页（公开路由，页面内部自行校验登录态并带回跳去登录页）
const oauthAuthorizeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/oauth/authorize',
  component: () => (
    <Suspense fallback={<FullPageLoading />}>
      <OAuthAuthorizePage />
    </Suspense>
  ),
});

// 认证布局（需要登录）
const authLayout = createRoute({
  getParentRoute: () => rootRoute,
  id: 'auth',
  beforeLoad: () => {
    if (!getToken()) throw redirect({ to: '/home' });
  },
  component: () => (
    <Suspense fallback={<FullPageLoading />}>
      <AppShell>
        <RoutePreloader />
        <Outlet />
      </AppShell>
    </Suspense>
  ),
});

// 根路径纯分流：管理员渲染全局仪表盘；普通用户跳个人概览；API Key 会话跳使用记录。
// 用重定向而不是原地渲染，保证 URL 与页面一致、侧边栏高亮不失联。
function HomePage() {
  const { user, loading, isAPIKeySession } = useAuth();
  if (loading) return <PageLoading />;
  if (!user) return null;

  if (isAPIKeySession) return <Navigate replace to="/usage" />;
  const isAdmin = getTokenRole() === 'admin' || user.role === 'admin';
  if (!isAdmin) return <Navigate replace to="/overview" />;
  return (
    <Suspense fallback={<PageLoading />}>
      <DashboardPage />
    </Suspense>
  );
}
const dashboardRoute = createRoute({ getParentRoute: () => authLayout, path: '/', component: HomePage });

// 个人概览：管理员与普通用户统一入口；API Key 会话只有使用记录视角，跳走。
function OverviewPage() {
  const { user, loading, isAPIKeySession } = useAuth();
  if (loading) return <PageLoading />;
  if (!user) return null;

  if (isAPIKeySession) return <Navigate replace to="/usage" />;
  return (
    <Suspense fallback={<PageLoading />}>
      <UserOverviewPage />
    </Suspense>
  );
}
const overviewRoute = createRoute({ getParentRoute: () => authLayout, path: '/overview', component: OverviewPage });

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
const adminPaymentRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/payment', component: renderPage(PaymentPage) });
const adminRedemptionRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/redemption', component: renderPage(RedemptionCodesPage) });
const adminInviteRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/invite', component: renderPage(InviteRebatePage) });
const adminSettingsRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/settings', component: renderPage(SettingsPage) });
const adminOAuthClientsRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/oauth-clients', component: renderPage(OAuthClientsPage) });

const profileRoute = createRoute({ getParentRoute: () => authLayout, path: '/profile', component: renderPage(ProfilePage) });
const userKeysRoute = createRoute({ getParentRoute: () => authLayout, path: '/keys', component: renderPage(UserKeysPage) });
const userUsageRoute = createRoute({ getParentRoute: () => authLayout, path: '/usage', component: renderPage(UserUsagePage) });
const rechargeRoute = createRoute({ getParentRoute: () => authLayout, path: '/recharge', component: renderPage(RechargePage) });
const inviteRoute = createRoute({ getParentRoute: () => authLayout, path: '/invite', component: renderPage(InvitePage) });

// 路由树
const routeTree = rootRoute.addChildren([
  homeRoute,
  loginRoute,
  oauthAuthorizeRoute,
  authLayout.addChildren([
    dashboardRoute,
    overviewRoute,
    adminLayout.addChildren([
      adminUsersRoute,
      adminChannelsRoute,
      adminModelPricesRoute,
      adminGroupsRoute,
      adminAnnouncementsRoute,
      adminUsageRoute,
      adminPaymentRoute,
      adminRedemptionRoute,
      adminInviteRoute,
      adminSettingsRoute,
      adminOAuthClientsRoute,
    ]),
    profileRoute,
    userKeysRoute,
    userUsageRoute,
    rechargeRoute,
    inviteRoute,
  ]),
]);

export const router = createRouter({
  routeTree,
  defaultPreload: 'intent',
  defaultPreloadStaleTime: 0,
});
