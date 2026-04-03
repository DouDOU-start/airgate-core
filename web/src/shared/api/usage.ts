import { get } from './client';
import type { UsageLogResp, UsageQuery, UsageStatsResp, UsageTrendBucket, PagedData } from '../types';

export const usageApi = {
  // 用户接口
  list: (params: UsageQuery) =>
    get<PagedData<UsageLogResp>>('/api/v1/usage', params),
  userStats: (params: Omit<UsageQuery, 'page' | 'page_size'>) =>
    get<UsageStatsResp>('/api/v1/usage/stats', params),
  userTrend: (params: { granularity: string; start_date?: string; end_date?: string }) =>
    get<UsageTrendBucket[]>('/api/v1/usage/trend', params),

  // 管理员接口
  adminList: (params: UsageQuery) =>
    get<PagedData<UsageLogResp>>('/api/v1/admin/usage', params),
  stats: (params: { group_by: string; start_date?: string; end_date?: string; platform?: string; model?: string; user_id?: number }) =>
    get<UsageStatsResp>('/api/v1/admin/usage/stats', params),
  trend: (params: { granularity: string; start_date?: string; end_date?: string; platform?: string; model?: string; user_id?: number }) =>
    get<UsageTrendBucket[]>('/api/v1/admin/usage/trend', params),
};
