import { del, get, getToken, patch, post, put } from './client';
import type {
  AccountExportResp,
  AccountImportReq,
  AccountImportResp,
  AccountListQuery,
  AccountResp,
  AccountToggleResp,
  BulkDeleteAccountsReq,
  BulkOpResp,
  BulkUpdateAccountsReq,
  CreateAccountReq,
  OAuthSessionResp,
  PagedData,
  StartOAuthReq,
  UpdateAccountReq,
} from '../types';

export type AccountTestModel = {
  id: string;
  display_name: string;
  kind: 'text' | 'image' | 'video';
  default_prompt: string;
};

export type AccountUsageStats = {
  history: Array<{
    date: string;
    label: string;
    requests: number;
    tokens: number;
    cost: number;
    actual_cost: number;
    user_cost: number;
  }>;
  summary: {
    days: number;
    actual_days_used: number;
    total_cost: number;
    total_user_cost: number;
    total_standard_cost: number;
    total_requests: number;
    total_tokens: number;
    avg_daily_cost: number;
    avg_daily_user_cost: number;
    avg_daily_requests: number;
    avg_daily_tokens: number;
    avg_duration_ms: number;
    today?: {
      date: string;
      label: string;
      cost: number;
      user_cost: number;
      requests: number;
      tokens?: number;
    } | null;
    highest_cost_day?: {
      date: string;
      label: string;
      cost: number;
      user_cost: number;
      requests: number;
    } | null;
    highest_request_day?: {
      date: string;
      label: string;
      cost: number;
      user_cost: number;
      requests: number;
    } | null;
  };
  models: Array<{
    model: string;
    requests: number;
    tokens: number;
    total_cost: number;
    actual_cost: number;
  }>;
};

export type AccountTestEvent = {
  type: string;
  text?: string;
  model?: string;
  success?: boolean;
  error?: string;
  media_kind?: 'image' | 'video';
  url?: string;
  request_id?: string;
  status?: string;
};

export const accountsApi = {
  list: (params?: AccountListQuery) =>
    get<PagedData<AccountResp>>('/api/v1/admin/accounts', params),
  create: (data: CreateAccountReq) =>
    post<AccountResp>('/api/v1/admin/accounts', data),
  update: (id: number, data: UpdateAccountReq) =>
    put<AccountResp>(`/api/v1/admin/accounts/${id}`, data),
  delete: (id: number) => del<void>(`/api/v1/admin/accounts/${id}`),
  toggle: (id: number) =>
    patch<AccountToggleResp>(`/api/v1/admin/accounts/${id}/toggle`),
  refreshUsage: (id: number) =>
    post<AccountResp>(`/api/v1/admin/accounts/${id}/usage/refresh`, {}),
  /** Codex 消费一枚限额重置积分，重置用量窗口 */
  consumeUsageReset: (id: number, creditId?: string) =>
    post<{
      code: string;
      windows_reset: number;
      account: AccountResp;
      usage?: AccountResp['usage'];
    }>(`/api/v1/admin/accounts/${id}/usage/reset`, creditId ? { credit_id: creditId } : {}),
  /** 近 N 天使用统计（对齐 sub2api） */
  stats: (id: number, days = 30) =>
    get<AccountUsageStats>(`/api/v1/admin/accounts/${id}/stats`, { days }),
  /** 连通性测试可选模型 */
  testModels: (id: number) =>
    get<AccountTestModel[]>(`/api/v1/admin/accounts/${id}/models`),
  /**
   * 连通性测试（SSE）。返回可读的事件流；调用方需自行 parse `data: {...}`。
   */
  testStream: async (
    id: number,
    body: {
      model_id?: string;
      prompt?: string;
      duration?: number;
      aspect_ratio?: string;
      resolution?: string;
    },
    opts?: { signal?: AbortSignal },
  ): Promise<ReadableStreamDefaultReader<Uint8Array>> => {
    const base = import.meta.env.VITE_API_BASE_URL || '';
    const res = await fetch(`${base}/api/v1/admin/accounts/${id}/test`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${getToken() || ''}`,
        'Content-Type': 'application/json',
        Accept: 'text/event-stream',
      },
      body: JSON.stringify(body ?? {}),
      signal: opts?.signal,
    });
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      // 尽量展示后端/上游原文（可能是 JSON 或纯文本）
      let msg = text?.trim() || `HTTP ${res.status}`;
      try {
        const j = JSON.parse(text) as { message?: string; error?: string; data?: unknown };
        if (j?.message) msg = j.message;
        else if (typeof j?.error === 'string' && j.error) msg = j.error;
      } catch {
        // 非 JSON：保留原文
      }
      throw new Error(msg);
    }
    if (!res.body) throw new Error('empty response body');
    return res.body.getReader();
  },
  export: (params?: Omit<AccountListQuery, 'page' | 'page_size'>) =>
    get<AccountExportResp>('/api/v1/admin/accounts/export', params),
  import: (data: AccountImportReq) =>
    post<AccountImportResp>('/api/v1/admin/accounts/import', data),
  bulkUpdate: (data: BulkUpdateAccountsReq) =>
    post<BulkOpResp>('/api/v1/admin/accounts/bulk-update', data),
  bulkDelete: (data: BulkDeleteAccountsReq) =>
    post<BulkOpResp>('/api/v1/admin/accounts/bulk-delete', data),
  credentialsSchema: (platform: string) =>
    get<Record<string, unknown>>(`/api/v1/admin/accounts/credentials-schema/${platform}`),
  platforms: () => get<string[]>('/api/v1/admin/accounts/platforms'),
  oauthHints: (platform: string) =>
    get<Record<string, unknown>>(`/api/v1/admin/accounts/oauth/${platform}/hints`),
  oauthStart: (platform: string, data?: StartOAuthReq) =>
    post<OAuthSessionResp>(`/api/v1/admin/accounts/oauth/${platform}/start`, data ?? {}),
  oauthSession: (sessionId: string) =>
    get<OAuthSessionResp>(`/api/v1/admin/accounts/oauth/sessions/${sessionId}`),
  oauthComplete: (sessionId: string, code: string) =>
    post<OAuthSessionResp>(`/api/v1/admin/accounts/oauth/sessions/${sessionId}/complete`, { code }),
  /** Codex RT 导入（对齐 airgate-openai import-refresh）；account_id 时为重新授权 */
  codexImportRefresh: (data: {
    refresh_token: string;
    client_id?: string;
    name?: string;
    proxy_url?: string;
    proxy_id?: number | null;
    priority?: number;
    weight?: number;
    max_concurrency?: number;
    rate_multiplier?: number;
    group_ids?: number[];
    account_id?: number;
  }) => post<AccountResp>('/api/v1/admin/accounts/oauth/codex/import-refresh', data),
  /** Codex Session 导入（对齐 airgate-openai import-session）；account_id 时为重新授权 */
  codexImportSession: (data: {
    session: string;
    name?: string;
    proxy_url?: string;
    proxy_id?: number | null;
    priority?: number;
    weight?: number;
    max_concurrency?: number;
    rate_multiplier?: number;
    group_ids?: number[];
    account_id?: number;
  }) => post<AccountResp>('/api/v1/admin/accounts/oauth/codex/import-session', data),
};
