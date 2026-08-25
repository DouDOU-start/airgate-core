import { get, post, put, upload } from './client';
import type {
  PluginResp, MarketplacePluginResp, PageReq, PagedData,
} from '../types';

export const pluginsApi = {
  list: (params?: PageReq) =>
    get<PagedData<PluginResp>>('/api/v1/admin/plugins', params),
  // 普通用户可访问的精简插件菜单（仅 name + frontend_pages，用于侧边栏）
  menu: () => get<PagedData<PluginResp>>('/api/v1/plugins/menu'),
  uninstall: (name: string) => post<void>(`/api/v1/admin/plugins/${name}/uninstall`),
  reload: (name: string) => post<void>(`/api/v1/admin/plugins/${name}/reload`),
  marketplace: (params?: PageReq) =>
    get<PagedData<MarketplacePluginResp>>('/api/v1/admin/marketplace/plugins', params),
  // 强制从 GitHub 同步市场列表
  refreshMarketplace: () => post<void>('/api/v1/admin/marketplace/refresh'),
  // 上传安装插件
  upload: (file: File, name?: string) => {
    const fd = new FormData();
    fd.append('file', file);
    if (name) fd.append('name', name);
    return upload<void>('/api/v1/admin/plugins/upload', fd);
  },
  // 从 GitHub Release 安装
  installGithub: (repo: string) =>
    post<void>('/api/v1/admin/plugins/install-github', { repo }),
  // 通用插件 RPC 调用，action 由插件自行定义
  rpc: <T = unknown>(name: string, action: string, body?: unknown) =>
    post<T>(`/api/v1/admin/plugins/${name}/rpc/${action}`, body),
  // 读取插件配置
  getConfig: (name: string) =>
    get<{ config: Record<string, string> }>(`/api/v1/admin/plugins/${name}/config`),
  // 更新插件配置（自动 reload 插件）
  updateConfig: (name: string, config: Record<string, string>) =>
    put<void>(`/api/v1/admin/plugins/${name}/config`, { config }),
};
