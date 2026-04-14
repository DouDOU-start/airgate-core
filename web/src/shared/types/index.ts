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
  platform?: string;
  service_tier?: 'fast' | 'flex';
}

// ==================== Auth ====================

export interface LoginReq {
  email: string;
  password: string;
}

export interface APIKeyLoginReq {
  key: string;
}

export interface LoginResp {
  token: string;
  user: UserResp;
  api_key_id?: number;
  api_key_name?: string;
}

export interface RegisterReq {
  email: string;
  password: string;
  username?: string;
  verify_code?: string;
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

  group_rates?: Record<number, number>;
  allowed_group_ids?: number[];
  balance_alert_threshold: number;
  status: string;
  api_key_id?: number;
  api_key_name?: string;
  api_key_quota_usd?: number;
  api_key_used_quota?: number;
  api_key_expires_at?: string;
  api_key_rate?: number;
  api_key_platform?: string;
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
  password?: string;
  role?: 'admin' | 'user';
  max_concurrency?: number;
  group_rates?: Record<number, number>;
  allowed_group_ids?: number[];
  status?: 'active' | 'disabled';
}

export interface AdjustBalanceReq {
  action: 'set' | 'add' | 'subtract';
  amount: number;
  remark?: string;
}

export interface BalanceLogResp {
  id: number;
  action: string;
  amount: number;
  before_balance: number;
  after_balance: number;
  remark: string;
  created_at: string;
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
  current_concurrency: number;
  proxy_id?: number;
  rate_multiplier: number;
  error_msg?: string;
  upstream_is_pool: boolean;
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
  upstream_is_pool?: boolean;
  group_ids?: number[];
}

export interface UpdateAccountReq {
  name?: string;
  type?: string;
  credentials?: Record<string, string>;
  status?: 'active' | 'disabled';
  priority?: number;
  max_concurrency?: number;
  proxy_id?: number | null;
  rate_multiplier?: number;
  upstream_is_pool?: boolean;
  group_ids?: number[];
}

// 批量更新账号请求（只传需要修改的字段，缺失 = 不改）
// add_group_ids 为「追加模式」：会与账号原有分组取并集
export interface BulkUpdateAccountsReq {
  account_ids: number[];
  status?: 'active' | 'disabled';
  priority?: number;
  max_concurrency?: number;
  rate_multiplier?: number;
  group_ids?: number[];
  proxy_id?: number;
}

// 批量操作单条结果
export interface BulkOpResultItem {
  id: number;
  success: boolean;
  error?: string;
}

// 批量操作汇总响应
export interface BulkOpResp {
  success: number;
  failed: number;
  success_ids: number[];
  failed_ids: number[];
  results: BulkOpResultItem[];
}

// 导出文件中的单条账号（精简字段，可被 import 还原）
export interface AccountExportItem {
  name: string;
  platform: string;
  type?: string;
  credentials: Record<string, string>;
  priority: number;
  max_concurrency: number;
  rate_multiplier: number;
  group_ids?: number[];
  proxy_id?: number;
}

// 导出文件结构
export interface AccountExportFile {
  version: number;
  exported_at: string;
  count: number;
  accounts: AccountExportItem[];
}

// 导入响应
export interface ImportAccountsResp {
  imported: number;
  failed: number;
  errors?: { index: number; name: string; message: string }[];
}

export interface CredentialField {
  key: string;
  label: string;
  type: 'text' | 'password' | 'textarea' | 'select';
  required: boolean;
  placeholder: string;
  edit_disabled?: boolean;
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
  service_tier?: 'fast' | 'flex';
  force_instructions?: string;
  note?: string;
  sort_weight: number;
  account_active: number;
  account_error: number;
  account_disabled: number;
  account_total: number;
  capacity_used: number;
  capacity_total: number;
  today_cost: number;
  total_cost: number;
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
  service_tier?: 'fast' | 'flex';
  force_instructions?: string;
  note?: string;
  sort_weight?: number;
  copy_accounts_from_group_ids?: number[];
}

export interface GroupRateOverrideResp {
  user_id: number;
  email: string;
  username: string;
  rate: number;
}

export interface UpdateGroupReq {
  name?: string;
  rate_multiplier?: number;
  is_exclusive?: boolean;
  subscription_type?: 'standard' | 'subscription';
  quotas?: Record<string, unknown>;
  model_routing?: Record<string, number[]>;
  service_tier?: 'fast' | 'flex';
  force_instructions?: string;
  note?: string;
  sort_weight?: number;
}

// ==================== API Key ====================

export interface APIKeyResp {
  id: number;
  name: string;
  key?: string;
  key_prefix: string;
  user_id: number;
  group_id: number | null;
  ip_whitelist?: string[];
  ip_blacklist?: string[];
  quota_usd: number;
  /** 账面已用（含 sell_rate markup）。end customer 通过 key 看到的就是这个数字 */
  used_quota: number;
  /** 真实成本已用（reseller 用于成本核算/利润计算，end customer 不可见） */
  used_quota_actual: number;
  /** 销售倍率：>0 启用 reseller markup，0 表示按平台原价计费 */
  sell_rate: number;
  /** API Key 级并发上限：同一把 key 同时在途请求数。0 表示不限制 */
  max_concurrency: number;
  today_cost: number;
  thirty_day_cost: number;
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
  /** 销售倍率：>0 启用 reseller markup（对客户的售价倍率）。可空，默认 0 */
  sell_rate?: number;
  /** API Key 并发上限，0 或不传表示不限制 */
  max_concurrency?: number;
  expires_at?: string;
}

export interface UpdateAPIKeyReq {
  name?: string;
  group_id?: number;
  ip_whitelist?: string[];
  ip_blacklist?: string[];
  quota_usd?: number;
  /** 销售倍率可随时动态调整，不影响历史 used_quota 累加值 */
  sell_rate?: number;
  /** API Key 并发上限，0 表示关闭限制；不传则不改动 */
  max_concurrency?: number;
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
  user_email?: string;
  api_key_id: number;
  api_key_name?: string;
  api_key_hint?: string;
  api_key_deleted: boolean;
  account_id: number;
  account_name?: string;
  group_id: number;
  platform: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
  cached_input_tokens: number;
  /** Anthropic 缓存创建总量（= 5m + 1h） */
  cache_creation_tokens: number;
  /** Anthropic 缓存创建 5m 档 */
  cache_creation_5m_tokens: number;
  /** Anthropic 缓存创建 1h 档 */
  cache_creation_1h_tokens: number;
  reasoning_output_tokens: number;
  input_price: number;
  output_price: number;
  cached_input_price: number;
  cache_creation_price: number;
  cache_creation_1h_price: number;
  input_cost: number;
  output_cost: number;
  cached_input_cost: number;
  cache_creation_cost: number;
  total_cost: number;
  /** 平台真实成本/用户扣费 = total × billing_rate */
  actual_cost: number;
  /** 客户账面消耗（含 sell_rate markup）；reseller 计算 actual_cost 与之差额即利润 */
  billed_cost: number;
  /** 账号实际成本 = total × account_rate；用于"账号计费"统计 */
  account_cost: number;
  rate_multiplier: number;
  /** 快照：本次请求生效的 sell_rate；0 表示该 key 当时未启用 markup */
  sell_rate: number;
  /** 快照：本次请求生效的 account_rate */
  account_rate_multiplier: number;
  service_tier?: string;
  stream: boolean;
  duration_ms: number;
  first_token_ms: number;
  user_agent?: string;
  ip_address?: string;
  created_at: string;
}

/**
 * CustomerUsageLogResp end customer 视角的精简响应。
 *
 * 当请求来自 API Key 登录拿到的 scoped JWT 时，后端返回此结构，
 * 不暴露 actual_cost / total_cost / 单价 / rate_multiplier 等会泄漏 reseller 毛利的字段。
 */
export interface CustomerUsageLogResp {
  id: number;
  api_key_id: number;
  platform: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
  cached_input_tokens: number;
  /** 客户视角："本次消耗 = X 美元" */
  cost: number;
  service_tier?: string;
  stream: boolean;
  duration_ms: number;
  first_token_ms: number;
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
  /** Reseller scope 才会下发；admin scope omit */
  total_billed_cost?: number;
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
  actual_cost: number;
  billed_cost?: number;
}

export interface UserStats {
  user_id: number;
  email: string;
  requests: number;
  tokens: number;
  total_cost: number;
  actual_cost: number;
  billed_cost?: number;
}

export interface AccountStats {
  account_id: number;
  name: string;
  requests: number;
  tokens: number;
  total_cost: number;
  actual_cost: number;
  billed_cost?: number;
}

export interface GroupStats {
  group_id: number;
  name: string;
  requests: number;
  tokens: number;
  total_cost: number;
  actual_cost: number;
  billed_cost?: number;
}

export interface UsageTrendBucket {
  time: string;
  input_tokens: number;
  output_tokens: number;
  cache_creation: number;
  cache_read: number;
  actual_cost: number;
  standard_cost: number;
  billed_cost?: number;
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
  ip_address?: string;
  country?: string;
  country_code?: string;
  city?: string;
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
    /** "admin" | "user" | "all"，空字符串视为 "admin"（向后兼容） */
    audience?: string;
  }>;
  config_schema?: Array<{
    key: string;
    label?: string;
    type: string;
    required?: boolean;
    default?: string;
    description?: string;
    placeholder?: string;
  }>;
  instruction_presets?: string[];
  has_web_assets?: boolean;
  is_dev?: boolean;
}

export interface MarketplacePluginResp {
  name: string;
  version: string;
  description: string;
  author: string;
  type: string;
  github_repo?: string;
  installed: boolean;
  installed_version?: string;
  has_update?: boolean;
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
  group?: string;
}

export interface TestSMTPReq {
  host: string;
  port: number;
  username: string;
  password: string;
  use_tls: boolean;
  from: string;
  to: string;
}

// ==================== Dashboard ====================

export interface DashboardStatsResp {
  total_api_keys: number;
  enabled_api_keys: number;
  total_accounts: number;
  enabled_accounts: number;
  error_accounts: number;
  today_requests: number;
  alltime_requests: number;
  total_users: number;
  new_users_today: number;
  today_tokens: number;
  today_cost: number;
  today_standard_cost: number;
  alltime_tokens: number;
  alltime_cost: number;
  alltime_standard_cost: number;
  rpm: number;
  tpm: number;
  avg_duration_ms: number;
  active_users: number;
}

export interface DashboardTrendReq {
  range: 'today' | '7d' | '30d' | '90d' | 'custom';
  granularity: 'hour' | 'day';
  start_date?: string;
  end_date?: string;
}

export interface DashboardTrendResp {
  model_distribution: DashboardModelStats[];
  user_ranking: DashboardUserRanking[];
  token_trend: DashboardTimeBucket[];
  top_users: DashboardUserTrend[];
}

export interface DashboardModelStats {
  model: string;
  requests: number;
  tokens: number;
  actual_cost: number;
  standard_cost: number;
}

export interface DashboardUserRanking {
  user_id: number;
  email: string;
  requests: number;
  tokens: number;
  actual_cost: number;
  standard_cost: number;
}

export interface DashboardTimeBucket {
  time: string;
  input_tokens: number;
  output_tokens: number;
  cached_input: number;
  actual_cost: number;
  standard_cost: number;
}

export interface DashboardUserTrend {
  user_id: number;
  email: string;
  trend: DashboardUserTrendPoint[];
}

export interface DashboardUserTrendPoint {
  time: string;
  tokens: number;
}

// ==================== Setup ====================

export interface SetupStatusResp {
  needs_setup: boolean;
  // 后端检测到 DB 环境变量已配置且可连通时返回提示，前端据此跳过数据库步骤
  env_db?: EnvDBHint;
  env_redis?: EnvRedisHint;
}

export interface EnvDBHint {
  host: string;
  port: number;
  user: string;
  dbname: string;
  sslmode: string;
}

export interface EnvRedisHint {
  host: string;
  port: number;
  db: number;
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
