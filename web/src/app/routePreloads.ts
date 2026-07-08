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

export const SetupPage = lazyWithPreload(() => import('../pages/SetupPage'));
export const LoginPage = lazyWithPreload(() => import('../pages/LoginPage'));
export const PublicHomePage = lazyWithPreload(() => import('../pages/HomePage'));
export const DocsPage = lazyWithPreload(() => import('../pages/DocsPage'));
export const DashboardPage = lazyWithPreload(() => import('../pages/DashboardPage'));
export const UserOverviewPage = lazyWithPreload(() => import('../pages/user/UserOverviewPage'));
export const UsersPage = lazyWithPreload(() => import('../pages/admin/UsersPage'));
export const ChannelsPage = lazyWithPreload(() => import('../pages/admin/ChannelsPage'));
export const ModelPricesPage = lazyWithPreload(() => import('../pages/admin/ModelPricesPage'));
export const GroupsPage = lazyWithPreload(() => import('../pages/admin/GroupsPage'));
export const AnnouncementsPage = lazyWithPreload(() => import('../pages/admin/AnnouncementsPage'));
export const UsagePage = lazyWithPreload(() => import('../pages/admin/UsagePage'));
export const SettingsPage = lazyWithPreload(() => import('../pages/admin/SettingsPage'));
export const ProfilePage = lazyWithPreload(() => import('../pages/user/ProfilePage'));
export const UserKeysPage = lazyWithPreload(() => import('../pages/user/UserKeysPage'));
export const UserUsagePage = lazyWithPreload(() => import('../pages/user/UserUsagePage'));

export const ADMIN_IDLE_PRELOADS = [
  DashboardPage,
];

export const USER_IDLE_PRELOADS = [
  UserOverviewPage,
];

const ROUTE_PRELOADS = new Map<string, AnyPreloadableLazyComponent[]>([
  ['/', [DashboardPage, UserOverviewPage]],
  ['/home', [PublicHomePage]],
  ['/login', [LoginPage]],
  ['/setup', [SetupPage]],
  ['/docs', [DocsPage]],
  ['/profile', [ProfilePage]],
  ['/keys', [UserKeysPage]],
  ['/usage', [UserUsagePage]],
  ['/admin/users', [UsersPage]],
  ['/admin/channels', [ChannelsPage]],
  ['/admin/model-prices', [ModelPricesPage]],
  ['/admin/groups', [GroupsPage]],
  ['/admin/announcements', [AnnouncementsPage]],
  ['/admin/usage', [UsagePage]],
  ['/admin/settings', [SettingsPage]],
]);

function normalizePreloadPath(path: string) {
  const [pathname = '/'] = path.split(/[?#]/, 1);
  return pathname || '/';
}

export function preloadRoutePage(
  page: AnyPreloadableLazyComponent,
  options: { deep?: boolean } = {},
) {
  return page.preload().then((module) => (
    options.deep === false ? undefined : module.preloadUserUsageContent?.()
  ));
}

export function preloadRoutePath(path: string, options: { deep?: boolean } = {}) {
  const pathname = normalizePreloadPath(path);
  const pages = ROUTE_PRELOADS.get(pathname);

  if (!pages?.length) return Promise.resolve();
  return Promise.all(pages.map((page) => preloadRoutePage(page, options))).then(() => undefined);
}
