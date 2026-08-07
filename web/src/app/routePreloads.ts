import { lazy } from 'react';
import type { ComponentType, LazyExoticComponent } from 'react';

export type RoutePreloadModule<TProps = Record<string, never>> = {
  default: ComponentType<TProps>;
  preloadUserUsageContent?: () => Promise<unknown>;
};

export type PreloadableLazyComponent<TProps = Record<string, never>> =
  LazyExoticComponent<ComponentType<TProps>> & {
    preload: () => Promise<RoutePreloadModule<TProps>>;
  };

type AnyPreloadableLazyComponent = PreloadableLazyComponent<any>;

export function lazyWithPreload<TProps>(
  load: () => Promise<RoutePreloadModule<TProps>>,
): PreloadableLazyComponent<TProps> {
  let promise: Promise<RoutePreloadModule<TProps>> | undefined;
  const preload = () => {
    promise ??= load();
    return promise;
  };
  const Component = lazy(preload) as PreloadableLazyComponent<TProps>;
  Component.preload = preload;
  return Component;
}

export const LoginPage = lazyWithPreload(() => import('../pages/LoginPage'));
export const PublicHomePage = lazyWithPreload(() => import('../pages/HomePage'));
export const ModelMarketPage = lazyWithPreload(() => import('../pages/ModelMarketPage'));
export const DashboardPage = lazyWithPreload(() => import('../pages/DashboardPage'));
export const UserOverviewPage = lazyWithPreload(() => import('../pages/user/UserOverviewPage'));
export const ChannelStatusPage = lazyWithPreload(() => import('../pages/user/ChannelStatusPage'));
export const UsersPage = lazyWithPreload(() => import('../pages/admin/UsersPage'));
export const ChannelsPage = lazyWithPreload(() => import('../pages/admin/ChannelsPage'));
export const ModelPricesPage = lazyWithPreload(() => import('../pages/admin/ModelPricesPage'));
export const GroupsPage = lazyWithPreload(() => import('../pages/admin/GroupsPage'));
export const AnnouncementsPage = lazyWithPreload(() => import('../pages/admin/AnnouncementsPage'));
export const UsagePage = lazyWithPreload(() => import('../pages/admin/UsagePage'));
export const PaymentPage = lazyWithPreload(() => import('../pages/admin/PaymentPage'));
export const RedemptionCodesPage = lazyWithPreload(() => import('../pages/admin/RedemptionCodesPage'));
export const InviteRebatePage = lazyWithPreload(() => import('../pages/admin/InviteRebatePage'));
export const SettingsPage = lazyWithPreload(() => import('../pages/admin/SettingsPage'));
export const ProfilePage = lazyWithPreload(() => import('../pages/user/ProfilePage'));
export const UserKeysPage = lazyWithPreload(() => import('../pages/user/UserKeysPage'));
export const UserUsagePage = lazyWithPreload(() => import('../pages/user/UserUsagePage'));
export const RechargePage = lazyWithPreload(() => import('../pages/user/RechargePage'));
export const InvitePage = lazyWithPreload(() => import('../pages/user/InvitePage'));
export const OAuthAuthorizePage = lazyWithPreload(() => import('../pages/OAuthAuthorizePage'));
export const OAuthClientsPage = lazyWithPreload(() => import('../pages/admin/OAuthClientsPage'));
export const RiskControlPage = lazyWithPreload(() => import('../pages/admin/RiskControlPage'));
export const BookmarksPage = lazyWithPreload(() => import('../pages/admin/BookmarksPage'));
export const AccountsPage = lazyWithPreload(() => import('../pages/admin/AccountsPage'));
export const ProxiesPage = lazyWithPreload(() => import('../pages/admin/ProxiesPage'));
export const PluginsPage = lazyWithPreload(() => import('../pages/admin/PluginsPage'));
export const RequestAuditsPage = lazyWithPreload(() => import('../pages/admin/RequestAuditsPage'));
export const OpsHealthPage = lazyWithPreload(() => import('../pages/admin/OpsHealthPage'));

export const ADMIN_IDLE_PRELOADS = [
  DashboardPage,
  // 管理员侧边栏个人区也有个人概览（/overview），空闲时一并预热
  UserOverviewPage,
  OpsHealthPage,
];

export const USER_IDLE_PRELOADS = [
  UserOverviewPage,
  ChannelStatusPage,
];

// 预加载路由 chunk，并顺带深预载页面导出的子内容模块（如 UserUsagePage 的用量内容）
export function preloadRoutePage(page: AnyPreloadableLazyComponent) {
  return page.preload().then((module) => module.preloadUserUsageContent?.());
}
