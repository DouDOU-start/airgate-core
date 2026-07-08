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

export type UserRole = 'admin' | 'user';
export type SessionRole = UserRole | 'api_key';

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
  role: SessionRole;
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
  role: UserRole;
  max_concurrency?: number;
  group_rates?: Record<number, number>;
}

export interface UpdateUserReq {
  username?: string;
  password?: string;
  role?: UserRole;
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

// ==================== 批量操作（通用） ====================

// 批量操作单条结果（渠道等资源的批量接口通用结构）
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

// ==================== Group ====================

export interface GroupResp {
  id: number;
  name: string;
  /** 历史字段：渠道化改造后为可空，新建分组不再填写。 */
  platform?: string;
  rate_multiplier: number;
  is_exclusive: boolean;
  status_visible: boolean;
  quotas?: Record<string, unknown>;
  force_instructions?: string;
  note?: string;
  sort_weight: number;
  today_cost: number;
  total_cost: number;
  created_at: string;
  updated_at: string;
}

export interface CreateGroupReq {
  name: string;
  rate_multiplier?: number;
  is_exclusive?: boolean;
  status_visible?: boolean;
  quotas?: Record<string, unknown>;
  force_instructions?: string;
  note?: string;
  sort_weight?: number;
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
  status_visible?: boolean;
  quotas?: Record<string, unknown>;
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

// ==================== Usage ====================

export interface UsageAttribute {
  key?: string;
  label: string;
  kind?: string;
  value: string;
  metadata?: Record<string, string>;
}

export interface UsageMetric {
  key?: string;
  label: string;
  kind?: string;
  unit?: string;
  value: number;
  account_cost?: number;
  currency?: string;
  metadata?: Record<string, string>;
}

export interface UsageCostDetail {
  key?: string;
  label: string;
  account_cost: number;
  user_cost?: number;
  billing_multiplier?: number;
  currency?: string;
  metadata?: Record<string, string>;
}

export interface UsageLogResp {
  id: number;
  user_id: number;
  user_email?: string;
  user_deleted?: boolean;
  api_key_id: number;
  api_key_name?: string;
  api_key_hint?: string;
  api_key_deleted: boolean;
  channel_id: number;
  channel_name?: string;
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
  /** 渠道成本（JSON 键沿用 account_cost）= total × channel.cost_ratio */
  account_cost: number;
  rate_multiplier: number;
  /** 快照：本次请求生效的 sell_rate；0 表示该 key 当时未启用 markup */
  sell_rate: number;
  /** 快照：本次请求生效的渠道成本倍率（JSON 键沿用 account_rate_multiplier） */
  account_rate_multiplier: number;
  service_tier?: string;
  /** 图像生成实际出图尺寸（"WxH"），非图像请求不返。admin 后台显示在模型名下方做计费分档解释。 */
  image_size?: string;
  stream: boolean;
  duration_ms: number;
  first_token_ms: number;
  user_agent?: string;
  ip_address?: string;
  /** 请求端点 */
  endpoint?: string;
  /** 推理强度档位 */
  reasoning_effort?: string;
  usage_attributes?: UsageAttribute[];
  usage_metrics?: UsageMetric[];
  usage_cost_details?: UsageCostDetail[];
  usage_metadata?: Record<string, string>;
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
  /** Anthropic 缓存创建总量（= 5m + 1h） */
  cache_creation_tokens: number;
  /** Anthropic 缓存创建 5m 档 */
  cache_creation_5m_tokens: number;
  /** Anthropic 缓存创建 1h 档 */
  cache_creation_1h_tokens: number;
  reasoning_output_tokens: number;
  /** 客户视角："本次消耗 = X 美元" */
  cost: number;
  service_tier?: string;
  /** 图像生成实际出图尺寸（"WxH"），非图像请求不返。 */
  image_size?: string;
  stream: boolean;
  duration_ms: number;
  first_token_ms: number;
  /** 请求端点 */
  endpoint?: string;
  /** 推理强度档位 */
  reasoning_effort?: string;
  usage_attributes?: UsageAttribute[];
  usage_metrics?: UsageMetric[];
  usage_metadata?: Record<string, string>;
  created_at: string;
}

export interface UsageQuery extends PageReq {
  user_id?: number;
  api_key_id?: number;
  channel_id?: number;
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
  /** 客户视角 / reseller scope 的账面费用；admin scope omit */
  total_billed_cost?: number;
  by_model?: ModelStats[];
  by_user?: UserStats[];
  by_channel?: ChannelStats[];
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

export interface ChannelStats {
  channel_id: number;
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

// ==================== Channel ====================

/** 渠道协议类型 */
export type ChannelType = 'openai_compatible' | 'anthropic' | 'gemini' | 'custom';

/** 渠道状态：enabled 启用（status_until 未过期时为冷却中）/ disabled_manual 手动禁用 / disabled_auto 自动禁用 */
export type ChannelStatus = 'enabled' | 'disabled_manual' | 'disabled_auto';

// 渠道响应 —— 与后端 dto.ChannelResp 对应。api_keys 明文永不出现在任何响应，
// 仅回 api_keys_count 与 api_key_hints（尾 4 位提示）。
export interface ChannelResp {
  id: number;
  name: string;
  type: ChannelType;
  base_url: string;
  api_keys_count: number;
  api_key_hints: string[];
  models: string[];
  model_mapping: Record<string, string> | null;
  param_override: Record<string, unknown> | null;
  header_override: Record<string, string> | null;
  status: ChannelStatus;
  /** 冷却截止时间；缺省表示无冷却 */
  status_until?: string;
  error_msg: string;
  priority: number;
  weight: number;
  max_concurrency: number;
  max_rpm: number;
  cost_ratio: number;
  tags: string[];
  test_model: string;
  custom_config: Record<string, unknown> | null;
  response_time_ms: number;
  tested_at?: string;
  last_used_at?: string;
  group_ids: number[];
  created_at: string;
  updated_at: string;
}

export interface CreateChannelReq {
  name: string;
  type: ChannelType;
  base_url: string;
  api_keys: string[];
  models: string[];
  model_mapping?: Record<string, string>;
  param_override?: Record<string, unknown>;
  header_override?: Record<string, string>;
  status?: 'enabled' | 'disabled_manual';
  priority?: number;
  weight?: number;
  max_concurrency?: number;
  max_rpm?: number;
  cost_ratio?: number;
  tags?: string[];
  test_model?: string;
  custom_config?: Record<string, unknown>;
  group_ids?: number[];
}

// 更新渠道请求（partial）：字段缺省 = 不改；api_keys/models 提供非空数组 = 整组替换，
// 留空 = 不改；映射/覆写/标签/分组提供（含空集合）= 整组替换。
export interface UpdateChannelReq {
  name?: string;
  type?: ChannelType;
  base_url?: string;
  api_keys?: string[];
  models?: string[];
  model_mapping?: Record<string, string>;
  param_override?: Record<string, unknown>;
  header_override?: Record<string, string>;
  status?: 'enabled' | 'disabled_manual';
  priority?: number;
  weight?: number;
  max_concurrency?: number;
  max_rpm?: number;
  cost_ratio?: number;
  tags?: string[];
  test_model?: string;
  custom_config?: Record<string, unknown>;
  group_ids?: number[];
}

export interface TestChannelReq {
  /** 缺省时后端取渠道 test_model 或首个模型 */
  model?: string;
}

export interface TestChannelResp {
  latency_ms: number;
  message: string;
}

export interface FetchChannelModelsResp {
  models: string[];
}

// 预览拉取模型请求（渠道未保存，直接给连接参数）
export interface FetchChannelModelsPreviewReq {
  type: ChannelType;
  base_url: string;
  api_key: string;
}

export type BulkChannelAction = 'enable' | 'disable' | 'delete' | 'set_priority';

export interface BulkUpdateChannelsReq {
  ids: number[];
  action: BulkChannelAction;
  priority?: number;
}

export interface BulkUpdateChannelsResp {
  affected: number;
}

export interface ChannelListQuery extends PageReq {
  type?: string;
  status?: string;
  tag?: string;
  group_id?: number;
}

// ==================== ModelPrice ====================

// 模型价格响应 —— 价格单位 USD / 1M tokens；per_request_price 为 USD / 次。
export interface ModelPriceResp {
  id: number;
  model: string;
  input_price: number;
  output_price: number;
  cached_input_price: number;
  cache_creation_price: number;
  cache_creation_1h_price: number;
  per_request_price: number;
  pricing_extra?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface CreateModelPriceReq {
  model: string;
  input_price?: number;
  output_price?: number;
  cached_input_price?: number;
  cache_creation_price?: number;
  cache_creation_1h_price?: number;
  per_request_price?: number;
  pricing_extra?: Record<string, unknown>;
}

export interface UpdateModelPriceReq {
  model?: string;
  input_price?: number;
  output_price?: number;
  cached_input_price?: number;
  cache_creation_price?: number;
  cache_creation_1h_price?: number;
  per_request_price?: number;
  pricing_extra?: Record<string, unknown>;
}

export interface ImportModelPriceItem {
  model: string;
  input_price?: number;
  output_price?: number;
  cached_input_price?: number;
  cache_creation_price?: number;
  cache_creation_1h_price?: number;
  per_request_price?: number;
  pricing_extra?: Record<string, unknown>;
}

export interface ImportModelPricesReq {
  items: ImportModelPriceItem[];
}

export interface ImportModelPricesResp {
  created: number;
  updated: number;
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
  total_channels: number;
  enabled_channels: number;
  disabled_channels: number;
  today_requests: number;
  today_image_requests: number;
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
  avg_first_token_ms: number;
  avg_duration_ms: number;
  avg_image_duration_ms: number;
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
  cache_read?: number;
  cache_creation?: number;
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

// ==================== Announcement ====================

export type AnnouncementStatus = 'draft' | 'active' | 'archived';
export type AnnouncementNotifyMode = 'silent' | 'popup';

export interface AnnouncementResp {
  id: number;
  title: string;
  content: string;
  status: AnnouncementStatus;
  notify_mode: AnnouncementNotifyMode;
  starts_at: string | null;
  ends_at: string | null;
  created_at: string;
  updated_at: string;
}

// 用户端公告：不含 status，附带当前用户已读时间
export interface UserAnnouncementResp {
  id: number;
  title: string;
  content: string;
  notify_mode: AnnouncementNotifyMode;
  read_at: string | null;
  created_at: string;
  updated_at: string;
}

// 时间字段为 RFC3339 字符串；空串 = 立即生效 / 永久展示
export interface CreateAnnouncementReq {
  title: string;
  content: string;
  status?: AnnouncementStatus;
  notify_mode?: AnnouncementNotifyMode;
  starts_at?: string;
  ends_at?: string;
}

// 更新时时间字段：不传 = 不修改，空串 = 清空
export interface UpdateAnnouncementReq {
  title?: string;
  content?: string;
  status?: AnnouncementStatus;
  notify_mode?: AnnouncementNotifyMode;
  starts_at?: string;
  ends_at?: string;
}
