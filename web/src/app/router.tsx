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

// 登录和安装页不常用，保持懒加载
const SetupPage = lazy(() => import('../pages/SetupPage'));
const LoginPage = lazy(() => import('../pages/LoginPage'));
const PluginPage = lazy(() => import('../pages/PluginPage'));

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
      throw redirect({ to: '/login' });
    }
  },
  component: () => (
    <AppShell>
      <Outlet />
    </AppShell>
  ),
});

// 首页：管理员看仪表盘，普通用户看个人概览
function HomePage() {
  const { user } = useAuth();
  if (!user) return null;
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
  loginRoute,
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
