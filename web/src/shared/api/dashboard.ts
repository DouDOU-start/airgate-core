import { get } from './client';
import type { DashboardStatsResp, DashboardTrendResp, DashboardTrendReq } from '../types';

export const dashboardApi = {
  stats: () => get<DashboardStatsResp>('/api/v1/dashboard/stats'),
  trend: (params: DashboardTrendReq) => get<DashboardTrendResp>('/api/v1/dashboard/trend', params),
};
