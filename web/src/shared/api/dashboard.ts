import { get } from './client';
import type { DashboardStatsResp, DashboardTrendResp, DashboardTrendReq } from '../types';

export const dashboardApi = {
  stats: (params?: { user_id?: number }) => get<DashboardStatsResp>('/api/v1/admin/dashboard/stats', params),
  trend: (params: DashboardTrendReq & { user_id?: number }) => get<DashboardTrendResp>('/api/v1/admin/dashboard/trend', params),
};
