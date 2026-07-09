import { get } from './client';
import type {
  ChannelFailureStatsResp, PagedData, UpstreamLogQuery, UpstreamLogResp, UserUpstreamLogResp,
} from '../types';

export const upstreamLogsApi = {
  list: (params?: Partial<UpstreamLogQuery>) =>
    get<PagedData<UpstreamLogResp>>('/api/v1/admin/upstream-logs', params),
  /** 用户视角失败请求（后端脱敏并强制按登录用户/scoped key 过滤） */
  userList: (params?: Partial<UpstreamLogQuery>) =>
    get<PagedData<UserUpstreamLogResp>>('/api/v1/usage/upstream-logs', params),
  /** 渠道近 N 分钟失败计数（errlog Redis 分钟桶；minutes 默认 30） */
  channelFailureStats: (ids: number[], minutes?: number) =>
    get<ChannelFailureStatsResp>('/api/v1/admin/channels/failure-stats', {
      ids: ids.join(','),
      ...(minutes ? { minutes } : {}),
    }),
};
