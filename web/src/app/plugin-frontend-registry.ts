import type { ComponentType } from 'react';
import type {
  AccountSurfaceProps,
  PluginFrontendModule,
  PluginPlatformIconProps,
  UsageRecordSurfaceProps,
} from '@doudou-start/airgate-theme/plugin';

type RegistryListener = () => void;

function platformKey(platform: string): string {
  return platform.toLowerCase();
}

function createComponentRegistry<TProps extends object>() {
  const registry = new Map<string, ComponentType<TProps>>();
  const listeners = new Set<RegistryListener>();
  let version = 0;

  return {
    register(platform: string, component: ComponentType<TProps>) {
      registry.set(platformKey(platform), component);
      version++;
      listeners.forEach((listener) => listener());
    },
    get(platform: string): ComponentType<TProps> | undefined {
      return registry.get(platformKey(platform));
    },
    subscribe(listener: RegistryListener): () => void {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    getVersion(): number {
      return version;
    },
  };
}

const platformIconRegistry = createComponentRegistry<PluginPlatformIconProps>();
const accountIdentityRegistry = createComponentRegistry<AccountSurfaceProps>();
const usageMetricDetailRegistry = createComponentRegistry<UsageRecordSurfaceProps>();
const usageModelMetaRegistry = createComponentRegistry<UsageRecordSurfaceProps>();
const usageCostDetailRegistry = createComponentRegistry<UsageRecordSurfaceProps>();

export function registerPluginFrontendModule(
  platform: string,
  mod: PluginFrontendModule,
) {
  if (mod.platformIcon) registerPlatformIcon(platform, mod.platformIcon);
  if (mod.accountIdentity) registerAccountIdentity(platform, mod.accountIdentity);
  // 账号用量窗口现由 core 统一渲染（基于规范化后的 display_label/slot/group 契约），
  // 不再消费插件 export 的 accountUsageWindow。SDK 类型暂保留，便于未来需要时恢复。
  if (mod.usageModelMeta) registerUsageModelMeta(platform, mod.usageModelMeta);
  if (mod.usageMetricDetail) registerUsageMetricDetail(platform, mod.usageMetricDetail);
  if (mod.usageCostDetail) registerUsageCostDetail(platform, mod.usageCostDetail);
}

export function registerPlatformIcon(
  platform: string,
  icon: ComponentType<PluginPlatformIconProps>,
) {
  platformIconRegistry.register(platform, icon);
}

export function getPluginPlatformIcon(
  platform: string,
): ComponentType<PluginPlatformIconProps> | undefined {
  return platformIconRegistry.get(platform);
}

export function onPlatformIconChange(listener: RegistryListener): () => void {
  return platformIconRegistry.subscribe(listener);
}

export function registerAccountIdentity(
  platform: string,
  component: ComponentType<AccountSurfaceProps>,
) {
  accountIdentityRegistry.register(platform, component);
}

export function getPluginAccountIdentity(
  platform: string,
): ComponentType<AccountSurfaceProps> | undefined {
  return accountIdentityRegistry.get(platform);
}

export function subscribeAccountIdentityChange(listener: RegistryListener): () => void {
  return accountIdentityRegistry.subscribe(listener);
}

export function getAccountIdentityVersion(): number {
  return accountIdentityRegistry.getVersion();
}

export function registerUsageMetricDetail(
  platform: string,
  component: ComponentType<UsageRecordSurfaceProps>,
) {
  usageMetricDetailRegistry.register(platform, component);
}

export function getPluginUsageMetricDetail(
  platform: string,
): ComponentType<UsageRecordSurfaceProps> | undefined {
  return usageMetricDetailRegistry.get(platform);
}

export function subscribeUsageMetricDetailChange(listener: RegistryListener): () => void {
  return usageMetricDetailRegistry.subscribe(listener);
}

export function getUsageMetricDetailVersion(): number {
  return usageMetricDetailRegistry.getVersion();
}

export function registerUsageModelMeta(
  platform: string,
  component: ComponentType<UsageRecordSurfaceProps>,
) {
  usageModelMetaRegistry.register(platform, component);
}

export function getPluginUsageModelMeta(
  platform: string,
): ComponentType<UsageRecordSurfaceProps> | undefined {
  return usageModelMetaRegistry.get(platform);
}

export function subscribeUsageModelMetaChange(listener: RegistryListener): () => void {
  return usageModelMetaRegistry.subscribe(listener);
}

export function getUsageModelMetaVersion(): number {
  return usageModelMetaRegistry.getVersion();
}

export function registerUsageCostDetail(
  platform: string,
  component: ComponentType<UsageRecordSurfaceProps>,
) {
  usageCostDetailRegistry.register(platform, component);
}

export function getPluginUsageCostDetail(
  platform: string,
): ComponentType<UsageRecordSurfaceProps> | undefined {
  return usageCostDetailRegistry.get(platform);
}

export function subscribeUsageCostDetailChange(listener: RegistryListener): () => void {
  return usageCostDetailRegistry.subscribe(listener);
}

export function getUsageCostDetailVersion(): number {
  return usageCostDetailRegistry.getVersion();
}
