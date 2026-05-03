import {
  createRouter,
  createRootRoute,
  createRoute,
  Outlet,
  redirect,
} from '@tanstack/react-router';
import { Suspense, lazy } from 'react';
import { AppShell } from './layout/AppShell';
import { ChatShell } from './layout/ChatShell';
import { useAuth } from './providers/AuthProvider';
import { ErrorBoundary } from './providers/ErrorBoundary';
import { getToken, getTokenRole } from '../shared/api/client';
import { usersApi } from '../shared/api/users';
import { setupApi } from '../shared/api/setup';
import { ChatPageLoading, FullPageLoading, PageLoading } from '../shared/components/PageLoading';
const SetupPage = lazy(() => import('../pages/SetupPage'));
const LoginPage = lazy(() => import('../pages/LoginPage'));
const PluginPage = lazy(() => import('../pages/PluginPage'));
const PublicHomePage = lazy(() => import('../pages/HomePage'));
const DocsPage = lazy(() => import('../pages/DocsPage'));
const DashboardPage = lazy(() => import('../pages/DashboardPage'));
const UserOverviewPage = lazy(() => import('../pages/user/UserOverviewPage'));
const UsersPage = lazy(() => import('../pages/admin/UsersPage'));
const AccountsPage = lazy(() => import('../pages/admin/AccountsPage'));
const GroupsPage = lazy(() => import('../pages/admin/GroupsPage'));
const SubscriptionsPage = lazy(() => import('../pages/admin/SubscriptionsPage'));
const ProxiesPage = lazy(() => import('../pages/admin/ProxiesPage'));
const UsagePage = lazy(() => import('../pages/admin/UsagePage'));
const PluginsPage = lazy(() => import('../pages/admin/PluginsPage'));
const SettingsPage = lazy(() => import('../pages/admin/SettingsPage'));
const ProfilePage = lazy(() => import('../pages/user/ProfilePage'));
const UserKeysPage = lazy(() => import('../pages/user/UserKeysPage'));
const UserUsagePage = lazy(() => import('../pages/user/UserUsagePage'));

// 缓存安装状态，避免每次路由跳转都请求
let setupChecked = false;
let needsSetup = false;

async function checkSetup() {
  if (!setupChecked) {
    try {
      const resp = await setupApi.status();
      needsSetup = resp.needs_setup;
    } catch {
      // 请求失败视为未安装
      needsSetup = true;
    }
    setupChecked = true;
  }
  return needsSetup;
}

// 安装完成后调用，重置缓存
export function resetSetupCache() {
  setupChecked = false;
  needsSetup = false;
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
  beforeLoad: async () => {
    const needs = await checkSetup();
    if (!needs) {
      throw redirect({ to: '/login' });
    }
  },
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
  beforeLoad: async () => {
    const needs = await checkSetup();
    if (needs) {
      throw redirect({ to: '/setup' });
    }
  },
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
  beforeLoad: async () => {
    const needs = await checkSetup();
    if (needs) {
      throw redirect({ to: '/setup' });
    }
  },
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
  beforeLoad: async () => {
    const needs = await checkSetup();
    if (needs) {
      throw redirect({ to: '/setup' });
    }
    if (!getToken()) {
      throw redirect({ to: '/home' });
    }
  },
  component: () => (
    <AppShell>
      <Outlet />
    </AppShell>
  ),
});

function HomePage() {
  const { user, isAPIKeySession } = useAuth();
  if (!user) return null;

  const isAdmin = getTokenRole() === 'admin' || user.role === 'admin';
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
  beforeLoad: async () => {
    if (getTokenRole() === 'admin') return;

    const user = await usersApi.me();
    if (user.role !== 'admin') {
      throw redirect({ to: '/' });
    }
  },
  component: Outlet,
});

function renderPage(Page: React.LazyExoticComponent<React.ComponentType>) {
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
// 仍要求登录 + 安装完成；走 ChatShell 极简顶栏。
const chatRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/chat',
  beforeLoad: async () => {
    const needs = await checkSetup();
    if (needs) {
      throw redirect({ to: '/setup' });
    }
    if (!getToken()) {
      throw redirect({ to: '/home' });
    }
  },
  component: () => (
    <ChatShell>
      <Suspense fallback={<ChatPageLoading />}>
        <PluginPage pluginNameOverride="airgate-playground" subPathOverride="/playground" />
      </Suspense>
    </ChatShell>
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

export const router = createRouter({ routeTree });
