import { get } from './client';

export type ChannelStatusWindow = '5m' | '1h' | '6h' | '24h';
export type ChannelHealthStatus = 'idle' | 'low_sample' | 'healthy' | 'degraded' | 'unhealthy';

export interface ChannelStatusSample {
  idle: boolean;
  low_sample: boolean;
}

export interface ChannelStatusLatency {
  avg_ms: number;
}

export interface ChannelStatusOverview {
  window: ChannelStatusWindow | string;
  sample: ChannelStatusSample;
  success_rate: number;
  error_rate: number;
  latency: ChannelStatusLatency;
  ttft: ChannelStatusLatency;
  health_score: number | null;
  updated_at: string;
}

export interface ChannelStatusGroup {
  id: number;
  name: string;
  platform?: string;
  status: ChannelHealthStatus;
  window: ChannelStatusWindow | string;
  sample: ChannelStatusSample;
  success_rate: number;
  error_rate: number;
  latency: ChannelStatusLatency;
  ttft: ChannelStatusLatency;
  health_score: number | null;
}

export const channelStatusApi = {
  overview: (params?: { window?: ChannelStatusWindow | string }) =>
    get<ChannelStatusOverview>('/api/v1/channel-status/overview', params),
  groups: (params?: { window?: ChannelStatusWindow | string }) =>
    get<ChannelStatusGroup[]>('/api/v1/channel-status/groups', params),
};
