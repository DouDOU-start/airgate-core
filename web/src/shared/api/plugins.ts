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
  config_schema?: PluginConfigSchema;
  supported: boolean;
  enabled: boolean;
  running: boolean;
  source: string;
  installed_at: string;
  updated_at: string;
  binary_size: number;
  has_config: boolean;
  config_ready: boolean;
  error?: string;
}

export interface PluginConfigField {
  key: string;
  label: string;
  description?: string;
  widget: 'multi_select' | 'ordered_select' | 'string_list' | 'text';
  data_source?: 'groups' | 'accounts';
  required?: boolean;
  default?: unknown;
  filter?: Record<string, string>;
}

export interface PluginConfigSchema {
  version: string;
  fields: PluginConfigField[];
}

export interface PluginConfigForm {
  schema: PluginConfigSchema;
  values: Record<string, unknown>;
}

export interface InstallPluginURLRequest {
  url: string;
}

export const pluginsApi = {
  list: () => get<PluginStatus[]>('/api/v1/admin/plugins'),
  upload: (file: File) => {
    const form = new FormData();
    form.append('file', file);
    return uploadForm<PluginStatus>('/api/v1/admin/plugins/upload', form);
  },
  installURL: (data: InstallPluginURLRequest) =>
    post<PluginStatus>('/api/v1/admin/plugins/install-url', data),
  getConfig: (id: string) =>
    get<PluginConfigForm>(`/api/v1/admin/plugins/${encodeURIComponent(id)}/config`),
  updateConfig: (id: string, values: Record<string, unknown>) =>
    put<void>(`/api/v1/admin/plugins/${encodeURIComponent(id)}/config`, { values }),
  setEnabled: (id: string, enabled: boolean) =>
    patch<void>(`/api/v1/admin/plugins/${encodeURIComponent(id)}/enabled`, { enabled }),
  reload: (id: string) =>
    post<void>(`/api/v1/admin/plugins/${encodeURIComponent(id)}/reload`),
  uninstall: (id: string) =>
    del<void>(`/api/v1/admin/plugins/${encodeURIComponent(id)}`),
};
