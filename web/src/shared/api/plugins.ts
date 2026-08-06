import { del, get, patch, post, put, uploadForm } from './client';

export interface PluginStatus {
  id: string;
  name: string;
  version: string;
  protocol_version: string;
  description: string;
  author: string;
  type: string;
  priority: number;
  capabilities: string[];
  supported: boolean;
  enabled: boolean;
  running: boolean;
  source: string;
  installed_at: string;
  updated_at: string;
  binary_size: number;
  has_config: boolean;
  error?: string;
}

export interface InstallPluginURLRequest {
  url: string;
  id?: string;
  config: string;
}

export const pluginsApi = {
  list: () => get<PluginStatus[]>('/api/v1/admin/plugins'),
  upload: (file: File, config: string, id?: string) => {
    const form = new FormData();
    form.append('file', file);
    form.append('config', config);
    if (id) form.append('id', id);
    return uploadForm<PluginStatus>('/api/v1/admin/plugins/upload', form);
  },
  installURL: (data: InstallPluginURLRequest) =>
    post<PluginStatus>('/api/v1/admin/plugins/install-url', data),
  getConfig: (id: string) =>
    get<{ config: string }>(`/api/v1/admin/plugins/${encodeURIComponent(id)}/config`),
  updateConfig: (id: string, config: string) =>
    put<void>(`/api/v1/admin/plugins/${encodeURIComponent(id)}/config`, { config }),
  setEnabled: (id: string, enabled: boolean) =>
    patch<void>(`/api/v1/admin/plugins/${encodeURIComponent(id)}/enabled`, { enabled }),
  reload: (id: string) =>
    post<void>(`/api/v1/admin/plugins/${encodeURIComponent(id)}/reload`),
  uninstall: (id: string) =>
    del<void>(`/api/v1/admin/plugins/${encodeURIComponent(id)}`),
};
