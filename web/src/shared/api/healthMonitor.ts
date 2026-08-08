import { get } from './client';

export type HealthmonWindow = '5m' | '1h' | '6h' | '24h';

export interface HealthmonSample {
  n: number;
  s: number;
  e: number;
  idle: boolean;
  low_sample: boolean;
}

export interface HealthmonCounts {
  auth: number;
  rate_limit: number;
  upstream_5xx: number;
  client: number;
  canceled: number;
  precheck: number;
  other: number;
}

export interface HealthmonLatency {
  avg_ms: number;
  p95_ms: number;
  max_ms: number;
}

export interface HealthmonOverview {
  window: HealthmonWindow | string;
  sample: HealthmonSample;
  success_rate: number;
  error_rate: number;
  counts: HealthmonCounts;
  latency: HealthmonLatency;
  ttft: HealthmonLatency;
  health_score: number | null;
  availability: {
    channel_keys_available: number;
    channel_keys_total: number;
  };
}

export interface HealthmonEntity {
  id: number;
  kind: string;
  name: string;
  channel_id?: number;
  channel_name?: string;
  platform?: string;
  type?: string;
  sched_status?: string;
  health_status?: string;
  window: string;
  sample: HealthmonSample;
  success_rate: number;
  error_rate: number;
  counts: HealthmonCounts;
  latency: HealthmonLatency;
  ttft: HealthmonLatency;
  health_score: number | null;
  last_error?: {
    at: string;
    phase: string;
    message: string;
  } | null;
}

export const healthMonitorApi = {
  overview: (params?: { window?: HealthmonWindow | string }) =>
    get<HealthmonOverview>('/api/v1/admin/health-monitor/overview', params),
  entities: (params?: {
    window?: HealthmonWindow | string;
    scope?: string;
    only_unhealthy?: boolean;
    ids?: string;
  }) => get<HealthmonEntity[]>('/api/v1/admin/health-monitor/entities', params),
};
