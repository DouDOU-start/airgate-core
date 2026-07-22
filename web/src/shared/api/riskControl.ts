import { get, post, put, del } from './client';
import type { PagedData } from '../types';

// ==================== 类型 ====================

export type ModerationMode = 'off' | 'observe' | 'pre_block';
export type KeywordBlockingMode = 'keyword_only' | 'keyword_and_api' | 'api_only';
export type ModelFilterType = 'all' | 'include' | 'exclude';
export type ModerationKeyStatusValue = 'unknown' | 'ok' | 'error' | 'frozen';

export interface ModerationModelFilter {
  type: ModelFilterType;
  models: string[];
}

export interface ModerationKeyStatus {
  index: number;
  key_hash: string;
  masked: string;
  status: ModerationKeyStatusValue;
  failure_count: number;
  success_count: number;
  last_error: string;
  last_checked_at?: string;
  frozen_until?: string;
  last_latency_ms: number;
  last_http_status: number;
  last_tested: boolean;
  configured: boolean;
}

export interface ModerationKeyLoad {
  index: number;
  key_hash: string;
  masked: string;
  status: ModerationKeyStatusValue;
  active: number;
  total: number;
  success: number;
  errors: number;
  avg_latency_ms: number;
  last_latency_ms: number;
  last_http_status: number;
}

export interface RiskControlConfig {
  mode: ModerationMode;
  base_url: string;
  model: string;
  api_key_count: number;
  api_key_masks: string[];
  api_key_statuses: ModerationKeyStatus[];
  timeout_ms: number;
  sample_rate: number;
  all_groups: boolean;
  group_ids: number[];
  record_non_hits: boolean;
  thresholds: Record<string, number>;
  categories: string[];
  worker_count: number;
  queue_size: number;
  block_status: number;
  block_message: string;
  email_on_hit: boolean;
  auto_ban_enabled: boolean;
  ban_threshold: number;
  violation_window_hours: number;
  retry_count: number;
  hit_retention_days: number;
  non_hit_retention_days: number;
  pre_hash_check_enabled: boolean;
  blocked_keywords: string[];
  keyword_blocking_mode: KeywordBlockingMode;
  model_filter: ModerationModelFilter;
}

export interface UpdateRiskControlConfigReq {
  mode?: ModerationMode;
  base_url?: string;
  model?: string;
  api_keys?: string[];
  api_keys_mode?: 'append' | 'replace';
  delete_api_key_hashes?: string[];
  clear_api_keys?: boolean;
  timeout_ms?: number;
  sample_rate?: number;
  all_groups?: boolean;
  group_ids?: number[];
  record_non_hits?: boolean;
  thresholds?: Record<string, number>;
  worker_count?: number;
  queue_size?: number;
  block_status?: number;
  block_message?: string;
  email_on_hit?: boolean;
  auto_ban_enabled?: boolean;
  ban_threshold?: number;
  violation_window_hours?: number;
  retry_count?: number;
  hit_retention_days?: number;
  non_hit_retention_days?: number;
  pre_hash_check_enabled?: boolean;
  blocked_keywords?: string[];
  keyword_blocking_mode?: KeywordBlockingMode;
  model_filter?: ModerationModelFilter;
}

export interface RiskControlStatus {
  mode: ModerationMode | '';
  worker_count: number;
  max_workers: number;
  active_workers: number;
  queue_size: number;
  queue_length: number;
  queue_usage_percent: number;
  enqueued: number;
  dropped: number;
  processed: number;
  errors: number;
  pre_block_active: number;
  pre_block_checked: number;
  pre_block_allowed: number;
  pre_block_blocked: number;
  pre_block_errors: number;
  pre_block_avg_latency_ms: number;
  api_key_available_count: number;
  api_key_loads: ModerationKeyLoad[];
  api_key_statuses: ModerationKeyStatus[];
  flagged_hash_count: number;
  last_cleanup_at?: string;
  last_cleanup_deleted_hit: number;
  last_cleanup_deleted_non_hit: number;
}

export interface TestRiskControlKeysReq {
  api_keys?: string[];
  base_url?: string;
  model?: string;
  timeout_ms?: number;
  prompt?: string;
  images?: string[];
}

export interface RiskControlTestAuditResult {
  flagged: boolean;
  highest_category: string;
  highest_score: number;
  category_scores: Record<string, number>;
  thresholds: Record<string, number>;
}

export interface TestRiskControlKeysResp {
  items: ModerationKeyStatus[];
  audit_result?: RiskControlTestAuditResult;
  image_count: number;
}

export interface ModerationLog {
  id: number;
  request_id: string;
  user_id: number;
  user_email: string;
  api_key_id: number;
  group_id: number;
  group_name: string;
  endpoint: string;
  protocol: string;
  model: string;
  mode: string;
  action: string;
  flagged: boolean;
  highest_category: string;
  highest_score: number;
  matched_keyword: string;
  category_scores: Record<string, number>;
  threshold_snapshot: Record<string, number>;
  input_excerpt: string;
  input_hash: string;
  upstream_latency_ms: number;
  queue_delay_ms: number;
  error: string;
  violation_count: number;
  auto_banned: boolean;
  email_sent: boolean;
  user_status: string;
  created_at: string;
}

export interface RiskControlLogsQuery {
  page?: number;
  page_size?: number;
  result?: 'hit' | 'blocked' | 'pass' | 'error' | '';
  group_id?: number;
  endpoint?: string;
  search?: string;
  from?: string;
  to?: string;
}

// ==================== API ====================

export const riskControlApi = {
  getConfig: () => get<RiskControlConfig>('/api/v1/admin/risk-control/config'),
  updateConfig: (data: UpdateRiskControlConfigReq) =>
    put<RiskControlConfig>('/api/v1/admin/risk-control/config', data),
  getStatus: () => get<RiskControlStatus>('/api/v1/admin/risk-control/status'),
  testKeys: (data: TestRiskControlKeysReq) =>
    post<TestRiskControlKeysResp>('/api/v1/admin/risk-control/api-keys/test', data),
  listLogs: (params?: RiskControlLogsQuery) =>
    get<PagedData<ModerationLog>>('/api/v1/admin/risk-control/logs', params),
  unbanUser: (userId: number) =>
    post<{ user_id: number; status: string }>(`/api/v1/admin/risk-control/users/${userId}/unban`, {}),
  deleteHash: (inputHash: string) =>
    del<{ input_hash: string; deleted: boolean }>('/api/v1/admin/risk-control/hashes', { input_hash: inputHash }),
  clearLogs: (result?: string) =>
    del<{ deleted: number; result: string }>('/api/v1/admin/risk-control/logs', { result: result ?? '' }),
  clearHashes: () =>
    del<{ deleted: number }>('/api/v1/admin/risk-control/hashes/all'),
};
