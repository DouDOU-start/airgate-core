import { createElement, type ComponentType } from 'react';

export interface PluginOAuthStartResult {
  authorizeURL: string;
  state: string;
}

export interface PluginOAuthExchangeResult {
  accountType: string;
  accountName: string;
  credentials: Record<string, string>;
}

export interface PluginOAuthBridge {
  start: () => Promise<PluginOAuthStartResult>;
  exchange: (callbackURL: string) => Promise<PluginOAuthExchangeResult>;
}

/**
 * 插件账号表单组件 Props
 */
export interface AccountFormProps {
  credentials: Record<string, string>;
  onChange: (credentials: Record<string, string>) => void;
  mode: 'create' | 'edit';
  accountType?: string;
  onAccountTypeChange?: (type: string) => void;
  onSuggestedName?: (name: string) => void;
  oauth?: PluginOAuthBridge;
}

/**
 * 插件前端模块接口
 * 每个插件可选地暴露路由、菜单项和自定义账号表单组件，由核心 Shell 动态注入。
 */
export interface PluginFrontendModule {
  routes?: Array<{ path: string; component: ComponentType }>;
  menuItems?: Array<{ path: string; title: string; icon: string }>;
  accountForm?: ComponentType<AccountFormProps>;
  /** 插件提供的平台图标组件，接收 className 和 style */
  platformIcon?: ComponentType<{ className?: string; style?: React.CSSProperties }>;
}

function wrapPluginComponent<TProps extends object>(
  Component: ComponentType<TProps>,
): ComponentType<TProps> {
  return function WrappedPluginComponent(props) {
    return createElement(Component, (props ?? {}) as TProps);
  };
}

function normalizePluginFrontendModule(
  mod: PluginFrontendModule | null,
): PluginFrontendModule | null {
  if (!mod) return null;

  return {
    ...mod,
    accountForm: mod.accountForm
      ? wrapPluginComponent(mod.accountForm)
      : undefined,
    platformIcon: mod.platformIcon
      ? wrapPluginComponent(mod.platformIcon)
      : undefined,
    routes: mod.routes?.map((route) => ({
      ...route,
      component: wrapPluginComponent(route.component),
    })),
  };
}

// 核心通过 window.__airgate_shared 暴露的共享模块列表
const SHARED_MODULES = ['react', 'react-dom', 'react/jsx-runtime'];

function rewriteNamedImportSpecifiers(specifiers: string): string {
  return specifiers
    .split(',')
    .map((part) => part.trim())
    .filter(Boolean)
    .map((part) => {
      const aliasParts = part.split(/\s+as\s+/);
      const imported = aliasParts[0]?.trim();
      const local = aliasParts[1]?.trim();
      if (imported && local) {
        return `${imported}: ${local}`;
      }
      return part;
    })
    .join(', ');
}

/**
 * 将插件 ESM 代码中的裸 import 重写为从 window.__airgate_shared 取值
 * 例：import { jsx } from "react/jsx-runtime"
 * →  const { jsx } = window.__airgate_shared["react/jsx-runtime"]
 */
function rewriteBareImports(code: string): string {
  for (const mod of SHARED_MODULES) {
    // 匹配 import { ... } from "react/jsx-runtime" 和 import { ... } from 'react'
    const pattern = new RegExp(
      `import\\s*\\{([^}]+)\\}\\s*from\\s*["']${mod.replace('/', '\\/')}["'];?`,
      'g',
    );
    code = code.replace(pattern, (_match, imports: string) => {
      return `const { ${rewriteNamedImportSpecifiers(imports)} } = window.__airgate_shared["${mod}"];`;
    });
    // 匹配 import * as X from "react"
    const starPattern = new RegExp(
      `import\\s*\\*\\s*as\\s+(\\w+)\\s+from\\s*["']${mod.replace('/', '\\/')}["'];?`,
      'g',
    );
    code = code.replace(starPattern, (_match, name: string) => {
      return `const ${name} = window.__airgate_shared["${mod}"];`;
    });
    // 匹配 import React from "react"
    const defaultPattern = new RegExp(
      `import\\s+([\\w$]+)\\s+from\\s*["']${mod.replace('/', '\\/')}["'];?`,
      'g',
    );
    code = code.replace(defaultPattern, (_match, name: string) => {
      return `const ${name} = window.__airgate_shared["${mod}"];`;
    });
  }
  return code;
}

/**
 * 加载单个插件的前端模块
 * 插件前端打包后部署在 /plugins/{pluginId}/assets/index.js
 *
 * 由于插件构建时将 react 等声明为 external，产物包含裸 import（浏览器无法解析）。
 * 这里通过 fetch → 重写 import → Blob URL → dynamic import 来解决。
 */
export async function loadPluginFrontend(
  pluginId: string,
): Promise<PluginFrontendModule | null> {
  try {
    const url = `/plugins/${pluginId}/assets/index.js`;
    const resp = await fetch(url);
    if (!resp.ok) return null;

    let code = await resp.text();
    code = rewriteBareImports(code);

    const blob = new Blob([code], { type: 'application/javascript' });
    const blobUrl = URL.createObjectURL(blob);
    try {
      // 记录 import 前已有的 <style> id，用于检测插件注入的主题变量样式
      const styleIdsBefore = new Set(
        Array.from(document.querySelectorAll('style[id]'), (el) => el.id),
      );

      const module = await import(/* @vite-ignore */ blobUrl);

      // 移除插件注入的主题变量样式（id 含 "theme-vars"）
      // 插件会打包旧版 SDK token 并通过 injectThemeStyle 注入作用域变量，
      // 这些变量优先级高于全局 :root 声明，导致配色不同步。
      // 移除后插件元素自然继承核心注入的最新主题变量。
      document.querySelectorAll('style[id]').forEach((el) => {
        if (!styleIdsBefore.has(el.id) && el.id.includes('theme-vars')) {
          el.remove();
        }
      });

      return normalizePluginFrontendModule(module.default as PluginFrontendModule);
    } finally {
      URL.revokeObjectURL(blobUrl);
    }
  } catch (err) {
    // 插件可能没有前端模块，记录错误便于排查
    console.warn(`[plugin-loader] Failed to load plugin frontend: ${pluginId}`, err);
    return null;
  }
}

/** 全局平台图标注册表：platform → Icon 组件 */
const platformIconRegistry = new Map<string, ComponentType<{ className?: string; style?: React.CSSProperties }>>();

/** 图标注册变更监听器 */
const iconListeners = new Set<() => void>();

/** 注册插件提供的平台图标 */
export function registerPlatformIcon(platform: string, icon: ComponentType<{ className?: string; style?: React.CSSProperties }>) {
  platformIconRegistry.set(platform.toLowerCase(), icon);
  // 通知所有监听者图标已更新
  iconListeners.forEach((fn) => fn());
}

/** 获取插件提供的平台图标 */
export function getPluginPlatformIcon(platform: string): ComponentType<{ className?: string; style?: React.CSSProperties }> | undefined {
  return platformIconRegistry.get(platform.toLowerCase());
}

/** 订阅图标注册变更，返回取消订阅函数 */
export function onPlatformIconChange(listener: () => void): () => void {
  iconListeners.add(listener);
  return () => iconListeners.delete(listener);
}

/**
 * 批量加载所有启用插件的前端模块
 * 使用 Promise.allSettled 确保单个插件加载失败不影响其他插件
 */
export async function loadAllPluginFrontends(
  pluginIds: string[],
): Promise<Map<string, PluginFrontendModule>> {
  const results = new Map<string, PluginFrontendModule>();

  await Promise.allSettled(
    pluginIds.map(async (id) => {
      const mod = await loadPluginFrontend(id);
      if (mod) results.set(id, mod);
    }),
  );

  return results;
}
