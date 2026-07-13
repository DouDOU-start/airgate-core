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
  invite_code?: string;
}

// ==================== User ====================

export interface UserResp {
  id: number;
  email: string;
  username: string;
  balance: number;
  role: SessionRole;
  max_concurrency: number;
  /** 当前在途请求数（仅管理员列表返回有效值） */
  current_concurrency?: number;
  /** 当前分钟请求数（仅管理员列表返回有效值） */
  current_rpm?: number;

  group_rates?: Record<number, number>;
  allowed_group_ids?: number[];
  tier_id?: number;
  tier_name?: string;
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
  tier_id?: number;
}

export interface UpdateUserReq {
  username?: string;
  password?: string;
  role?: UserRole;
  max_concurrency?: number;
  group_rates?: Record<number, number>;
  allowed_group_ids?: number[];
  /** 用户等级：不传=不修改，0=清除，>0=设置 */
  tier_id?: number;
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

// ==================== Group ====================

export interface GroupResp {
  id: number;
  name: string;
  /** 历史字段：渠道化改造后为可空，新建分组不再填写。 */
  platform?: string;
  rate_multiplier: number;
  /** 当前用户在此分组的实际计费倍率（用户专属 > 等级 > 分组档位），仅用户视角接口返回 */
  effective_rate?: number;
  is_exclusive: boolean;
  status_visible: boolean;
  note?: string;
  sort_weight: number;
  today_cost: number;
  total_cost: number;
  /** 当前在途请求数（列表实时观测） */
  current_concurrency?: number;
  /** 当前分钟请求数（列表实时观测） */
  current_rpm?: number;
  created_at: string;
  updated_at: string;
}

export interface CreateGroupReq {
  name: string;
  rate_multiplier?: number;
  is_exclusive?: boolean;
  status_visible?: boolean;
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
  note?: string;
  sort_weight?: number;
}

// ==================== Tier（用户等级） ====================

export interface TierResp {
  id: number;
  name: string;
  /** 等级在各分组的计费倍率（按 group_id 键） */
  rates: Record<number, number>;
  note?: string;
  sort_weight: number;
  /** 归属此等级的用户数（列表返回） */
  user_count: number;
  created_at: string;
  updated_at: string;
}

export interface CreateTierReq {
  name: string;
  rates?: Record<number, number>;
  note?: string;
  sort_weight?: number;
}

export interface UpdateTierReq {
  name?: string;
  /** 提交即整体替换 */
  rates?: Record<number, number>;
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
  /** 最高计费倍率：>0 时实际扣费倍率超过该值直接拒绝请求，0 表示不限制 */
  max_rate: number;
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
  /** 最高计费倍率：>0 时实际扣费倍率超过该值拒绝请求。可空，默认 0 不限制 */
  max_rate?: number;
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
  /** 最高计费倍率，0 表示关闭限制；不传则不改动 */
  max_rate?: number;
  /** API Key 并发上限，0 表示关闭限制；不传则不改动 */
  max_concurrency?: number;
  expires_at?: string;
  status?: 'active' | 'disabled';
}

// ==================== 上游请求日志（失败留痕/渠道测试/拉模型，仅管理员） ====================

/** 重试链一跳（attempt_chain 元素） */
export interface UpstreamAttemptHop {
  seq: number;
  channel_id: number;
  channel_name: string;
  key_hint?: string;
  upstream_status?: number;
  verdict: string; // rateLimited / authFailed / transient / networkError / clientError / streamAborted
  reason?: string;
  retry_after_ms?: number;
  latency_ms?: number;
  auto_disabled?: boolean;
}

export interface UpstreamLogResp {
  id: number;
  request_id: string;
  /** 发起方：relay 用户转发 / channel_test 渠道测试（表内只有失败行） */
  source: 'relay' | 'channel_test';
  phase?: string;
  status_code: number;
  error_type?: string;
  error_code?: string;
  message: string;
  attempts: number;
  /** UpstreamAttemptHop 数组（后端原样透传 JSON） */
  attempt_chain?: UpstreamAttemptHop[];
  /** 该请求是否同时产生了消费记录（流式中断/带 usage 的 4xx） */
  billed: boolean;
  model?: string;
  endpoint?: string;
  stream: boolean;
  user_id?: number;
  user_email?: string;
  api_key_id?: number;
  group_id?: number;
  channel_id?: number;
  channel_name?: string;
  ip_address?: string;
  user_agent?: string;
  duration_ms: number;
  repeat_count: number;
  created_at: string;
}

export interface UpstreamLogQuery extends PageReq {
  source?: 'relay' | 'channel_test';
  phase?: string;
  user_id?: number;
  api_key_id?: number;
  channel_id?: number;
  request_id?: string;
  model?: string;
  start_date?: string;
  end_date?: string;
}

/**
 * UserUpstreamLogResp 用户视角失败请求（后端已脱敏：无渠道/重试链/IP/UA）。
 */
export interface UserUpstreamLogResp {
  id: number;
  request_id: string;
  phase?: string;
  status_code: number;
  error_type?: string;
  error_code?: string;
  message: string;
  attempts: number;
  billed: boolean;
  model?: string;
  endpoint?: string;
  stream: boolean;
  api_key_id?: number;
  duration_ms: number;
  repeat_count: number;
  created_at: string;
}

/** 单渠道近 N 分钟失败计数（verdict → 次数；clientError 不计入） */
export interface ChannelFailureCounts {
  channel_id: number;
  total: number;
  by_verdict: Record<string, number>;
}

/** 渠道失败计数响应（errlog Redis 分钟桶汇总） */
export interface ChannelFailureStatsResp {
  minutes: number;
  channels: ChannelFailureCounts[];
}

// ==================== Usage ====================

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
  /** 按次计费计次数（图像端点=响应产出张数）；token 计费端点恒 0 */
  calls: number;
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
  rate_multiplier: number;
  /** 快照：本次请求生效的 sell_rate；0 表示该 key 当时未启用 markup */
  sell_rate: number;
  /** 快照：本次请求生效的渠道成本倍率；渠道成本 = total_cost × 本值（前端现算） */
  account_rate_multiplier: number;
  service_tier?: string;
  stream: boolean;
  duration_ms: number;
  first_token_ms: number;
  user_agent?: string;
  ip_address?: string;
  /** 请求端点 */
  endpoint?: string;
  /** 记账来源：relay 用户转发 / channel_test 渠道测试 */
  source: 'relay' | 'channel_test';
  /** 请求 ID（X-Request-ID）：与失败请求留痕互查 */
  request_id?: string;
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
  /** 按次计费计次数（图像端点=响应产出张数）；token 计费端点恒 0 */
  calls: number;
  /** 客户视角："本次消耗 = X 美元" */
  cost: number;
  service_tier?: string;
  stream: boolean;
  duration_ms: number;
  first_token_ms: number;
  /** 请求端点 */
  endpoint?: string;
  /** 请求 ID（X-Request-ID） */
  request_id?: string;
  created_at: string;
}

export interface UsageQuery extends PageReq {
  user_id?: number;
  api_key_id?: number;
  channel_id?: number;
  channel_key_id?: number;
  group_id?: number;
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
  by_channel_key?: ChannelKeyStats[];
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

export interface ChannelKeyStats {
  channel_key_id: number;
  name: string;
  channel_name: string;
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
export type ChannelType = 'openai_compatible' | 'anthropic' | 'gemini' | 'custom' | 'openai_video' | 'suno';

/** 渠道状态：enabled 启用 / disabled_manual 手动禁用 / disabled_auto 自动禁用 */
export type ChannelStatus = 'enabled' | 'disabled_manual' | 'disabled_auto';

// 渠道下的一把上游 Key —— 与后端 dto.ChannelKeyResp 对应。
// 明文密钥永不回显，仅回 api_key_hint（尾 4 位提示）。每把 key 有独立的
// 类型 / 模型 / 映射 / 覆写 / 分组 / 优先级权重并发限流 / 成本倍率 / 标签 / 启停状态。
export interface ChannelKeyResp {
  id: number;
  channel_id: number;
  /** 所属渠道名 / base_url —— 密钥视图（跨渠道平铺）展示用；渠道视图下与父渠道重复 */
  channel_name: string;
  base_url: string;
  name: string;
  type: ChannelType;
  api_key_hint: string;
  models: string[];
  model_mapping: Record<string, string>;
  param_override: Record<string, unknown> | null;
  header_override: Record<string, string> | null;
  status: ChannelStatus;
  error_msg: string;
  priority: number;
  weight: number;
  max_concurrency: number;
  max_rpm: number;
  cost_ratio: number;
  tags: string[];
  test_model: string;
  response_time_ms: number;
  tested_at?: string;
  last_used_at?: string;
  group_ids: number[];
  /** 当前在途请求数（列表实时观测） */
  current_concurrency: number;
  /** 当前分钟请求数（列表实时观测） */
  current_rpm: number;
  /** 上游账户余额（USD）；仅 openai_compatible key 可查，按 key 刷新 */
  balance: number;
  balance_updated_at?: string;
  /** 累计成本（standard × cost_ratio 快照） */
  total_cost: number;
  /** 累计平台收益（actual_cost 实际扣费） */
  total_revenue: number;
  /** 今日成本（按请求 tz 当日零点起算） */
  today_cost: number;
  /** 今日平台收益（口径同 total_revenue） */
  today_revenue: number;
  created_at: string;
  updated_at: string;
}

// 渠道响应 —— 渠道退化为「容器」，只保留 name / base_url；
// 类型、模型、限流、计费、余额等落到每把 key（keys）。
// balance/total_cost/... 为各 key 的汇总（rollup），仅只读展示，不再有渠道级余额刷新。
export interface ChannelResp {
  id: number;
  name: string;
  base_url: string;
  /** 上游账户余额汇总（各 key 求和，只读） */
  balance: number;
  balance_updated_at?: string;
  /** 渠道下的全部 key */
  keys: ChannelKeyResp[];
  /** 累计成本汇总（各 key 求和，只读） */
  total_cost: number;
  /** 累计平台收益汇总（只读） */
  total_revenue: number;
  /** 今日成本汇总（只读） */
  today_cost: number;
  /** 今日平台收益汇总（只读） */
  today_revenue: number;
  created_at: string;
  updated_at: string;
}

// 单把 key 的新增/更新请求（共用）。语义：
// - 新增（POST /channels/:id/keys）：type / api_key 必填。
// - 更新（PUT /channels/keys/:id）：api_key 留空 = 保持原密钥；type 留空 = 不改。
// - models/model_mapping 由列表页「模型」弹窗维护，表单不携带。
export interface ChannelKeyReq {
  name?: string;
  // 新增时必填；更新（模型弹窗、单 key 编辑）时可省：type 省=不改、api_key 省/空=保持原密钥。
  type?: ChannelType;
  api_key?: string;
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
  group_ids?: number[];
}

// 渠道只管 name / base_url；key 增删改走独立端点。
export interface CreateChannelReq {
  name: string;
  base_url: string;
}

export interface UpdateChannelReq {
  name?: string;
  base_url?: string;
}

export interface TestChannelReq {
  /** 缺省时后端取渠道 test_model 或首个模型 */
  model?: string;
  /** 测试端点（仅 openai 协议渠道生效）：chat_completions（默认）/ responses */
  endpoint?: 'chat_completions' | 'responses';
}

export interface TestChannelResp {
  latency_ms: number;
  message: string;
}

export interface FetchChannelModelsResp {
  models: string[];
}

export interface RefreshChannelBalanceResp {
  balance: number;
  balance_updated_at?: string;
}

// 未保存前的模型预览请求（POST /channels/fetch-models）：用临时凭据探测上游模型列表。
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

/** 可排序字段：优先级 / 权重 / 名称 / 状态 / 创建时间 */
export type ChannelKeySortBy = 'priority' | 'weight' | 'name' | 'status' | 'created_at';
export type SortOrder = 'asc' | 'desc';

// 密钥视图（跨渠道平铺）查询参数：keyword 同时匹配 key 名与渠道名。
export interface ChannelKeyListQuery extends PageReq {
  type?: string;
  status?: string;
  tag?: string;
  channel_id?: number;
  group_id?: number;
  sort_by?: ChannelKeySortBy;
  sort_order?: SortOrder;
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
  /** 模型标签（家族归类，可空）。 */
  tag?: { id: number; name: string } | null;
  created_at: string;
  updated_at: string;
}

/** 模型标签；model_count 为引用该标签的模型数。 */
export interface ModelTagResp {
  id: number;
  name: string;
  model_count: number;
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
  /** 模型标签 ID（省略或 0 = 不挂标签）。 */
  tag_id?: number;
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
  /** 三态：省略 = 不改；0 = 清空标签；正数 = 设为该标签。 */
  tag_id?: number;
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
  enabled_keys: number;
  disabled_keys: number;
  today_requests: number;
  today_image_requests: number;
  alltime_requests: number;
  total_users: number;
  new_users_today: number;
  today_tokens: number;
  today_cost: number;
  today_standard_cost: number;
  /** 今日渠道成本（Σ total_cost × 成本倍率快照） */
  today_channel_cost: number;
  alltime_tokens: number;
  alltime_cost: number;
  alltime_standard_cost: number;
  /** 累计渠道成本 */
  alltime_channel_cost: number;
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
  /** 渠道过滤（渠道消耗统计弹窗用）；缺省不过滤 */
  channel_id?: number;
  /** 密钥端点过滤（key 消耗统计弹窗用）；缺省不过滤 */
  channel_key_id?: number;
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
  /** 渠道成本（Σ total_cost × 成本倍率快照） */
  channel_cost?: number;
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
  /** 该时段请求数 */
  requests?: number;
  input_tokens: number;
  output_tokens: number;
  cached_input: number;
  cache_read?: number;
  cache_creation?: number;
  actual_cost: number;
  standard_cost: number;
  /** 渠道成本（Σ total_cost × 成本倍率快照） */
  channel_cost?: number;
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

// ==================== Payment ====================

export type PaymentOrderStatus = 'pending' | 'paid' | 'expired';

// 充值订单 —— 与后端 dto.PaymentOrderResp 对应
export interface PaymentOrder {
  out_trade_no: string;
  user_id: number;
  /** 仅管理端列表返回 */
  user_email?: string;
  method: string;
  provider_id: string;
  amount: number;
  status: PaymentOrderStatus;
  subject: string;
  payment_url?: string;
  qr_code_content?: string;
  paid_at?: string;
  expires_at: string;
  created_at: string;
  updated_at: string;
}

// 支付方式展示元信息（后端 provider.MethodInfo）
export interface PaymentMethodInfo {
  key: string;
  label: string;
  icon: string;
  description: string;
}

// 用户可用支付方式响应
export interface PaymentMethodsResp {
  methods: PaymentMethodInfo[];
  configured: boolean;
}

export interface CreatePaymentOrderReq {
  amount: number;
  method: string;
  subject?: string;
}

// 用户充值记录响应（分页）
export interface PaymentOrderListResp {
  list: PaymentOrder[];
  total: number;
}

// 管理端订单统计
export interface PaymentOrderStats {
  total: number;
  paid: number;
  pending: number;
  expired: number;
  total_amount: number;
  today_amount: number;
}

// 管理端订单列表响应
export interface AdminPaymentOrdersResp {
  list: PaymentOrder[];
  total: number;
  stats: PaymentOrderStats;
}

export interface AdminPaymentOrdersQuery {
  page?: number;
  page_size?: number;
  email?: string;
  status?: string;
}

// 服务商配置表单字段描述（驱动前端动态表单）
export interface PaymentProviderFieldDescriptor {
  key: string;
  label: string;
  /** text / password / textarea / number / bool / method-multi */
  type: string;
  required?: boolean;
  placeholder?: string;
  description?: string;
}

// 服务商协议类型元信息
export interface PaymentProviderKindMeta {
  kind: string;
  name: string;
  description: string;
  supported_methods: string[];
  field_descriptors: PaymentProviderFieldDescriptor[];
}

// 服务商实例（config 中敏感字段已掩码为空串，sensitive_keys 标记「已配置、留空保持不变」）
export interface PaymentProviderItem {
  id: string;
  kind: string;
  name: string;
  enabled: boolean;
  config: Record<string, string>;
  supported_methods: string[];
  is_running: boolean;
  sensitive_keys: string[];
}

// 服务商列表 + 协议类型元信息响应
export interface PaymentProvidersResp {
  providers: PaymentProviderItem[];
  kinds: PaymentProviderKindMeta[];
}

// 新增/编辑服务商实例请求（id 留空自动生成；original_id 非空且 ≠ id 表示重命名）
export interface UpsertPaymentProviderReq {
  id?: string;
  original_id?: string;
  kind: string;
  enabled: boolean;
  config: Record<string, string>;
}

export interface UpsertPaymentProviderResp {
  id: string;
}

// ==================== OAuth 应用接入 ====================

// OAuth 客户端（管理面），secret 只出 hint
export interface OAuthClientResp {
  id: number;
  client_id: string;
  secret_hint: string;
  name: string;
  description: string;
  redirect_uris: string[];
  first_party: boolean;
  enabled: boolean;
  show_in_nav: boolean;
  launch_url: string;
  icon: string;
  sort_order: number;
  created_at: string;
  updated_at: string;
}

// 创建/重置 secret 响应：client_secret 明文仅此一次
export interface OAuthClientSecretResp extends OAuthClientResp {
  client_secret: string;
}

export interface CreateOAuthClientReq {
  name: string;
  description?: string;
  redirect_uris: string[];
  first_party?: boolean;
  enabled?: boolean;
  show_in_nav?: boolean;
  launch_url?: string;
  icon?: string;
  sort_order?: number;
}

export interface UpdateOAuthClientReq {
  name: string;
  description: string;
  redirect_uris: string[];
  first_party: boolean;
  enabled: boolean;
  show_in_nav: boolean;
  launch_url: string;
  icon: string;
  sort_order: number;
}

// 授权页信息
export interface AuthorizeInfoResp {
  name: string;
  description: string;
  icon: string;
  first_party: boolean;
}

export interface AuthorizeReq {
  client_id: string;
  redirect_uri: string;
  scope?: string;
  state?: string;
  code_challenge: string;
  code_challenge_method: string;
}

export interface AuthorizeResp {
  code: string;
  state: string;
}

// 用户端导航应用入口
export interface AppEntryResp {
  name: string;
  description: string;
  icon: string;
  launch_url: string;
}

// ==================== 兑换码 ====================

export type RedemptionCodeStatus = 'unused' | 'used' | 'disabled' | 'expired';

// 兑换码（仅管理端可见；status 为后端现算，含 expired 虚拟状态）
export interface RedemptionCode {
  id: number;
  code: string;
  value: number;
  status: RedemptionCodeStatus;
  remark?: string;
  used_by_id?: number;
  used_by_email?: string;
  used_at?: string;
  expires_at?: string;
  created_at: string;
  updated_at: string;
}

// 兑换码统计（unused 已剔除过期码）
export interface RedemptionStats {
  total: number;
  unused: number;
  used: number;
  disabled: number;
  expired: number;
  used_value: number;
  unused_value: number;
}

export interface GenerateRedemptionCodesReq {
  count: number;
  value: number;
  remark?: string;
  expires_at?: string;
}

export interface RedemptionCodeListResp {
  list: RedemptionCode[];
  total: number;
  page: number;
  page_size: number;
}

export interface RedemptionCodesQuery {
  page?: number;
  page_size?: number;
  status?: string;
  keyword?: string;
}

// 用户兑换结果
export interface RedeemResp {
  value: number;
  balance: number;
}

// ==================== 邀请返利 ====================

// 我的邀请信息
export interface InviteMe {
  enabled: boolean;
  invite_code?: string;
  inviter_id?: number;
  effective_rate_percent?: number;
  invited_count: number;
  rebate_balance: number;
  rebate_total: number;
}

// 邀请关系条目（用户端"我邀请的人" / 管理端全量列表复用）
export interface InviteeItem {
  inviter_id: number;
  inviter_email?: string;
  inviter_username?: string;
  invitee_id: number;
  invitee_email?: string;
  invitee_username?: string;
  created_at: string;
  total_rebate: number;
}

export interface InviteeListResp {
  list: InviteeItem[];
  total: number;
  page: number;
  page_size: number;
}

// 返利流水条目
export interface InviteRebateLogItem {
  id: number;
  user_id: number;
  user_email?: string;
  action: 'accrue' | 'transfer';
  amount: number;
  source_user_id?: number;
  source_user_email?: string;
  source_order_no?: string;
  balance_after?: number;
  created_at: string;
}

export interface InviteRebateLogListResp {
  list: InviteRebateLogItem[];
  total: number;
  page: number;
  page_size: number;
}

// 返利转入余额结果
export interface InviteTransferResp {
  transferred: number;
  balance: number;
}

// 专属返利比例覆盖列表条目（管理端）
export interface InviteOverrideEntry {
  user_id: number;
  email?: string;
  username?: string;
  invite_code: string;
  rate_percent?: number;
  invited_count: number;
}

export interface InviteOverrideListResp {
  list: InviteOverrideEntry[];
  total: number;
  page: number;
  page_size: number;
}

export interface InviteListQuery {
  page?: number;
  page_size?: number;
  keyword?: string;
}
