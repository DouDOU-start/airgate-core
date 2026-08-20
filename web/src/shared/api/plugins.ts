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
  fallback_key?: string;
  label: string;
  description?: string;
  widget: 'multi_select' | 'single_select' | 'ordered_select' | 'string_list' | 'text' | 'number' | 'textarea' | 'switch';
  data_source?: 'groups' | 'accounts';
  required?: boolean;
  secret?: boolean;
  default?: unknown;
  min?: number;
  max?: number;
  step?: number;
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

export interface PluginProviderBalance {
  balance_fen: number;
  held_fen: number;
  available_fen: number;
  currency: string;
}

export interface PluginProviderDefaults {
  product: 'oauth_30d' | 'oauth_7d';
  quantity: number;
  group_ids: number[];
  priority: number;
  max_concurrency: number;
}

export interface PluginProviderImportOptions {
  group_ids: number[];
  priority: number;
  max_concurrency: number;
}

export interface PluginProviderOrder {
  id?: string;
  product: string;
  quantity: number;
  status: string;
  source: 'auto' | 'manual' | string;
  import_options: PluginProviderImportOptions;
  imported_accounts?: number;
  created_at: string;
  completed_at?: string;
}

export interface PluginProviderOrderStatus {
  pending: boolean;
  order?: PluginProviderOrder | null;
  last_order?: PluginProviderOrder | null;
  last_error?: string;
}

export interface PluginProviderOverview {
  balance: PluginProviderBalance;
  defaults: PluginProviderDefaults;
  auto_refill_enabled: boolean;
  order: PluginProviderOrderStatus;
}

export interface PluginProviderInventory {
  available: number;
  missing: number;
  needs_production: boolean;
  minimum_remaining_seconds: number;
  maximum_remaining_seconds: number;
  estimated_total_fen: number;
  estimated_unit_price_fen: number;
}

export interface PluginProviderManualOrderRequest {
  product: 'oauth_30d' | 'oauth_7d';
  quantity: number;
  group_ids: number[];
  priority: number;
  max_concurrency: number;
}

export const pluginsApi = {
  list: () => get<PluginStatus[]>('/api/v1/admin/plugins'),
  upload: (file: File) => {
    const form = new FormData();
    form.append('file', file);
    return uploadForm<PluginStatus>('/api/v1/admin/plugins/upload', form);
  },
  updateBinary: (id: string, file: File) => {
    const form = new FormData();
    form.append('file', file);
    return uploadForm<PluginStatus>(`/api/v1/admin/plugins/${encodeURIComponent(id)}/upload`, form);
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
  action: <T>(id: string, action: string, payload: unknown = {}) => {
    const encodedAction = action.split('/').map(encodeURIComponent).join('/');
    return post<T>(`/api/v1/admin/plugins/${encodeURIComponent(id)}/actions/${encodedAction}`, payload);
  },
  uninstall: (id: string) =>
    del<void>(`/api/v1/admin/plugins/${encodeURIComponent(id)}`),
};
