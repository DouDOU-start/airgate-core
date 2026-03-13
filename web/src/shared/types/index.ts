// 统一响应类型 —— 与后端 response.R 对应
export interface ApiResponse<T = unknown> {
  code: number;
  data: T;
  message: string;
}

// 分页响应
export interface PagedData<T> {
  list: T[];
  total: number;
  page: number;
  page_size: number;
}

// 分页请求参数
export interface PageReq {
  page: number;
  page_size: number;
  keyword?: string;
}

// ==================== Auth ====================

export interface LoginReq {
  email: string;
  password: string;
  totp_code?: string;
}

export interface LoginResp {
  token: string;
  user: UserResp;
}

export interface RegisterReq {
  email: string;
  password: string;
  username?: string;
}

export interface TOTPSetupResp {
  secret: string;
  uri: string;
}

export interface TOTPVerifyReq {
  code: string;
}

export interface RefreshResp {
  token: string;
}

// ==================== User ====================

export interface UserResp {
  id: number;
  email: string;
  username: string;
  balance: number;
  role: 'admin' | 'user';
  max_concurrency: number;
  totp_enabled: boolean;
  group_rates?: Record<number, number>;
  status: string;
  created_at: string;
  updated_at: string;
}

export interface UpdateProfileReq {
  username?: string;
}

export interface ChangePasswordReq {
  old_password: string;
  new_password: string;
}

export interface CreateUserReq {
  email: string;
  password: string;
  username?: string;
  role: 'admin' | 'user';
  max_concurrency?: number;
  group_rates?: Record<number, number>;
}

export interface UpdateUserReq {
  username?: string;
  role?: 'admin' | 'user';
  max_concurrency?: number;
  group_rates?: Record<number, number>;
  status?: 'active' | 'disabled';
}

export interface AdjustBalanceReq {
  action: 'set' | 'add' | 'subtract';
  amount: number;
}

// ==================== Account ====================

export interface AccountResp {
  id: number;
  name: string;
  platform: string;
  type: string;
  credentials: Record<string, string>;
  status: 'active' | 'error' | 'disabled';
  priority: number;
  max_concurrency: number;
  proxy_id?: number;
  rate_multiplier: number;
  error_msg?: string;
  last_used_at?: string;
  group_ids: number[];
  created_at: string;
  updated_at: string;
}

export interface CreateAccountReq {
  name: string;
  platform: string;
  type?: string;
  credentials: Record<string, string>;
  priority?: number;
  max_concurrency?: number;
  proxy_id?: number;
  rate_multiplier?: number;
  group_ids?: number[];
}

export interface UpdateAccountReq {
  name?: string;
  type?: string;
  credentials?: Record<string, string>;
  status?: 'active' | 'disabled';
  priority?: number;
  max_concurrency?: number;
  proxy_id?: number;
  rate_multiplier?: number;
  group_ids?: number[];
}

export interface CredentialField {
  key: string;
  label: string;
  type: 'text' | 'password' | 'textarea' | 'select';
  required: boolean;
  placeholder: string;
}

export interface AccountTypeResp {
  key: string;
  label: string;
  description: string;
  fields: CredentialField[];
}

export interface CredentialSchemaResp {
  fields: CredentialField[];
  account_types?: AccountTypeResp[];
}

// ==================== Group ====================

export interface GroupResp {
  id: number;
  name: string;
  platform: string;
  rate_multiplier: number;
  is_exclusive: boolean;
  subscription_type: 'standard' | 'subscription';
  quotas?: Record<string, unknown>;
  model_routing?: Record<string, number[]>;
  sort_weight: number;
  created_at: string;
  updated_at: string;
}

export interface CreateGroupReq {
  name: string;
  platform: string;
  rate_multiplier?: number;
  is_exclusive?: boolean;
  subscription_type: 'standard' | 'subscription';
  quotas?: Record<string, unknown>;
  model_routing?: Record<string, number[]>;
  sort_weight?: number;
}

export interface UpdateGroupReq {
  name?: string;
  rate_multiplier?: number;
  is_exclusive?: boolean;
  subscription_type?: 'standard' | 'subscription';
  quotas?: Record<string, unknown>;
  model_routing?: Record<string, number[]>;
  sort_weight?: number;
}

// ==================== API Key ====================

export interface APIKeyResp {
  id: number;
  name: string;
  key?: string;
  key_prefix: string;
  user_id: number;
  group_id: number;
  ip_whitelist?: string[];
  ip_blacklist?: string[];
  quota_usd: number;
  used_quota: number;
  expires_at?: string;
  status: string;
  created_at: string;
  updated_at: string;
}

export interface CreateAPIKeyReq {
  name: string;
  group_id: number;
  ip_whitelist?: string[];
  ip_blacklist?: string[];
  quota_usd?: number;
  expires_at?: string;
}

export interface UpdateAPIKeyReq {
  name?: string;
  ip_whitelist?: string[];
  ip_blacklist?: string[];
  quota_usd?: number;
  expires_at?: string;
  status?: 'active' | 'disabled';
}

// ==================== Subscription ====================

export interface SubscriptionResp {
  id: number;
  user_id: number;
  group_id: number;
  group_name: string;
  effective_at: string;
  expires_at: string;
  usage: Record<string, unknown>;
  status: 'active' | 'expired' | 'suspended';
  created_at: string;
  updated_at: string;
}

export interface UsageWindow {
  used: number;
  limit: number;
  reset: string;
}

export interface SubscriptionProgressResp {
  group_id: number;
  group_name: string;
  daily?: UsageWindow;
  weekly?: UsageWindow;
  monthly?: UsageWindow;
}

export interface AssignSubscriptionReq {
  user_id: number;
  group_id: number;
  expires_at: string;
}

export interface BulkAssignReq {
  user_ids: number[];
  group_id: number;
  expires_at: string;
}

export interface AdjustSubscriptionReq {
  expires_at?: string;
  status?: 'active' | 'suspended';
}

// ==================== Usage ====================

export interface UsageLogResp {
  id: number;
  user_id: number;
  api_key_id: number;
  account_id: number;
  group_id: number;
  platform: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
  cache_tokens: number;
  input_cost: number;
  output_cost: number;
  cache_cost: number;
  total_cost: number;
  actual_cost: number;
  rate_multiplier: number;
  account_rate_multiplier: number;
  stream: boolean;
  duration_ms: number;
  first_token_ms: number;
  user_agent?: string;
  ip_address?: string;
  created_at: string;
}

export interface UsageQuery extends PageReq {
  user_id?: number;
  api_key_id?: number;
  account_id?: number;
  group_id?: number;
  platform?: string;
  model?: string;
  start_date?: string;
  end_date?: string;
}

export interface UsageStatsResp {
  total_requests: number;
  total_tokens: number;
  total_cost: number;
  total_actual_cost: number;
  by_model?: ModelStats[];
  by_user?: UserStats[];
  by_account?: AccountStats[];
  by_group?: GroupStats[];
}

export interface ModelStats {
  model: string;
  requests: number;
  tokens: number;
  total_cost: number;
}

export interface UserStats {
  user_id: number;
  email: string;
  requests: number;
  total_cost: number;
}

export interface AccountStats {
  account_id: number;
  name: string;
  requests: number;
  total_cost: number;
}

export interface GroupStats {
  group_id: number;
  name: string;
  requests: number;
  total_cost: number;
}

// ==================== Proxy ====================

export interface ProxyResp {
  id: number;
  name: string;
  protocol: 'http' | 'socks5';
  address: string;
  port: number;
  username?: string;
  status: string;
  created_at: string;
  updated_at: string;
}

export interface CreateProxyReq {
  name: string;
  protocol: 'http' | 'socks5';
  address: string;
  port: number;
  username?: string;
  password?: string;
}

export interface UpdateProxyReq {
  name?: string;
  protocol?: 'http' | 'socks5';
  address?: string;
  port?: number;
  username?: string;
  password?: string;
  status?: 'active' | 'disabled';
}

export interface TestProxyResp {
  success: boolean;
  latency_ms: number;
  error_msg?: string;
}

// ==================== Plugin ====================

export interface PluginResp {
  name: string;
  display_name?: string;
  version?: string;
  author?: string;
  type?: string;
  platform: string;
  account_types?: Array<{
    key: string;
    label: string;
    description?: string;
  }>;
  frontend_pages?: Array<{
    path: string;
    title: string;
    icon?: string;
    description?: string;
  }>;
  has_web_assets?: boolean;
  is_dev?: boolean;
}

export interface MarketplacePluginResp {
  name: string;
  version: string;
  description: string;
  author: string;
  type: string;
  installed: boolean;
}

// ==================== Settings ====================

export interface SettingResp {
  key: string;
  value: string;
  group: string;
}

export interface UpdateSettingsReq {
  settings: SettingItem[];
}

export interface SettingItem {
  key: string;
  value: string;
}

// ==================== Dashboard ====================

export interface DashboardStatsResp {
  total_users: number;
  total_accounts: number;
  total_groups: number;
  total_api_keys: number;
  total_requests: number;
  total_tokens: number;
  total_revenue: number;
  active_plugins: number;
}

// ==================== Setup ====================

export interface SetupStatusResp {
  needs_setup: boolean;
}

export interface TestDBReq {
  host: string;
  port: number;
  user: string;
  password?: string;
  dbname: string;
  sslmode?: string;
}

export interface TestRedisReq {
  host: string;
  port: number;
  password?: string;
  db?: number;
  tls?: boolean;
}

export interface InstallReq {
  database: TestDBReq;
  redis: TestRedisReq;
  admin: AdminSetup;
}

export interface AdminSetup {
  email: string;
  password: string;
}

export interface TestConnectionResp {
  success: boolean;
  error_msg?: string;
}

export interface ModelInfo {
  id: string;
  name: string;
}
