import { get } from './client';
import type { UsageLogResp, CustomerUsageLogResp, UsageQuery, UsageStatsResp, UsageTrendBucket, PagedData } from '../types';

type UsageRequestOptions = {
  signal?: AbortSignal;
};

export const usageApi = {
  // 用户接口
  list: (params: UsageQuery, options?: UsageRequestOptions) =>
    get<PagedData<UsageLogResp | CustomerUsageLogResp>>('/api/v1/usage', params, options),
  userStats: (params: Omit<UsageQuery, 'page' | 'page_size'>, options?: UsageRequestOptions) =>
    get<UsageStatsResp>('/api/v1/usage/stats', params, options),
  userTrend: (params: { granularity: string; start_date?: string; end_date?: string }, options?: UsageRequestOptions) =>
    get<UsageTrendBucket[]>('/api/v1/usage/trend', params, options),

  // 管理员接口
  adminList: (params: UsageQuery, options?: UsageRequestOptions) =>
    get<PagedData<UsageLogResp>>('/api/v1/admin/usage', params, options),
  stats: (params: { group_by: string; start_date?: string; end_date?: string; platform?: string; model?: string; user_id?: number; api_key_id?: number }, options?: UsageRequestOptions) =>
    get<UsageStatsResp>('/api/v1/admin/usage/stats', params, options),
  trend: (params: { granularity: string; start_date?: string; end_date?: string; platform?: string; model?: string; user_id?: number; api_key_id?: number }, options?: UsageRequestOptions) =>
    get<UsageTrendBucket[]>('/api/v1/admin/usage/trend', params, options),
};
