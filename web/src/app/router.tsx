import {
  createRouter,
  createRootRoute,
  createRoute,
  Outlet,
  redirect,
} from '@tanstack/react-router';
import { Suspense, lazy } from 'react';
import { AppShell } from './layout/AppShell';
import { useAuth } from './providers/AuthProvider';
import { ErrorBoundary } from './providers/ErrorBoundary';
import { getToken } from '../shared/api/client';
import { usersApi } from '../shared/api/users';
import { setupApi } from '../shared/api/setup';
import DashboardPage from '../pages/DashboardPage';
import UserOverviewPage from '../pages/user/UserOverviewPage';
import UsersPage from '../pages/admin/UsersPage';
import AccountsPage from '../pages/admin/AccountsPage';
import GroupsPage from '../pages/admin/GroupsPage';
import SubscriptionsPage from '../pages/admin/SubscriptionsPage';
import ProxiesPage from '../pages/admin/ProxiesPage';
import UsagePage from '../pages/admin/UsagePage';
import PluginsPage from '../pages/admin/PluginsPage';
import SettingsPage from '../pages/admin/SettingsPage';
import ProfilePage from '../pages/user/ProfilePage';
import UserKeysPage from '../pages/user/UserKeysPage';
import UserUsagePage from '../pages/user/UserUsagePage';
// 登录、安装、首页不常用，保持懒加载
const SetupPage = lazy(() => import('../pages/SetupPage'));
const LoginPage = lazy(() => import('../pages/LoginPage'));
const PluginPage = lazy(() => import('../pages/PluginPage'));
const PublicHomePage = lazy(() => import('../pages/HomePage'));
const DocsPage = lazy(() => import('../pages/DocsPage'));

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
    <Suspense fallback={null}>
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
    <Suspense fallback={null}>
      <PublicHomePage />
    </Suspense>
  ),
});

// 注意：/status 不再注册客户端路由，整个公开状态页交给 airgate-health 插件维护。
// 后端 GET /status 直接反代到插件的 handlePublicIndex，前端用 <a href="/status"> 跳转。
// 这样避免 core 与插件出现两份重复的状态页实现。

// 内置默认文档页 —— 当管理员未在 系统设置 → 站点品牌 → 文档链接 中填写外部 URL 时，
// 所有"文档"按钮 fallback 到这里。公开可访问，独立布局（不挂 AppShell）。
const docsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/docs',
  component: () => (
    <Suspense fallback={null}>
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
    <Suspense fallback={null}>
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

// 首页：API Key 登录重定向到使用记录，管理员看仪表盘，普通用户看个人概览
function HomePage() {
  const { user, isAPIKeySession } = useAuth();
  if (!user) return null;
  if (isAPIKeySession) return <UserUsagePage />;
  return user.role === 'admin' ? <DashboardPage /> : <UserOverviewPage />;
}
const dashboardRoute = createRoute({ getParentRoute: () => authLayout, path: '/', component: HomePage });

// 管理员布局（需要 admin 角色）
const adminLayout = createRoute({
  getParentRoute: () => authLayout,
  id: 'admin',
  beforeLoad: async () => {
    const user = await usersApi.me();
    if (user.role !== 'admin') {
      throw redirect({ to: '/' });
    }
  },
  component: Outlet,
});

// 管理员路由
const adminUsersRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/users', component: UsersPage });
const adminAccountsRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/accounts', component: AccountsPage });
const adminGroupsRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/groups', component: GroupsPage });
const adminSubscriptionsRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/subscriptions', component: SubscriptionsPage });
const adminProxiesRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/proxies', component: ProxiesPage });
const adminUsageRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/usage', component: UsagePage });
const adminPluginsRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/plugins', component: PluginsPage });
const adminSettingsRoute = createRoute({ getParentRoute: () => adminLayout, path: '/admin/settings', component: SettingsPage });

// 用户路由
const profileRoute = createRoute({ getParentRoute: () => authLayout, path: '/profile', component: ProfilePage });
const userKeysRoute = createRoute({ getParentRoute: () => authLayout, path: '/keys', component: UserKeysPage });
const userUsageRoute = createRoute({ getParentRoute: () => authLayout, path: '/usage', component: UserUsagePage });

const playgroundPluginRoute = createRoute({
  getParentRoute: () => authLayout,
  path: '/plugins/playground',
  component: () => (
    <Suspense fallback={null}>
      <PluginPage pluginNameOverride="airgate-playground" subPathOverride="/playground" />
    </Suspense>
  ),
});

// 插件页面路由（catch-all）
const pluginRoute = createRoute({
  getParentRoute: () => authLayout,
  path: '/plugins/$pluginName/$',
  component: () => (
    <Suspense fallback={null}>
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
    playgroundPluginRoute,
    pluginRoute,
  ]),
]);

export const router = createRouter({ routeTree });
