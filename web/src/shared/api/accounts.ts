import { get, post, put, del, patch } from './client';
import type {
  AccountResp, CreateAccountReq, UpdateAccountReq,
  AccountExportFile, ImportAccountsResp, AccountExportItem,
  BulkUpdateAccountsReq, BulkOpResp,
  CredentialSchemaResp, ModelInfo, PageReq, PagedData,
} from '../types';

export type AccountListFilter = { platform?: string; state?: string; account_type?: string; group_id?: number; proxy_id?: number };

export const accountsApi = {
  list: (params: PageReq & AccountListFilter) =>
    get<PagedData<AccountResp>>('/api/v1/admin/accounts', params),
  // 按当前筛选条件导出全部账号（不分页）；传入 ids 时仅导出指定账号。
  export: (params: { keyword?: string; ids?: number[] } & AccountListFilter) => {
    const { ids, ...rest } = params;
    return get<AccountExportFile>('/api/v1/admin/accounts/export', {
      ...rest,
      ids: ids && ids.length > 0 ? ids.join(',') : undefined,
    });
  },
  // 批量导入账号
  import: (accounts: AccountExportItem[]) =>
    post<ImportAccountsResp>('/api/v1/admin/accounts/import', { accounts }),
  create: (data: CreateAccountReq) => post<AccountResp>('/api/v1/admin/accounts', data),
  update: (id: number, data: UpdateAccountReq) => put<void>(`/api/v1/admin/accounts/${id}`, data),
  delete: (id: number) => del<void>(`/api/v1/admin/accounts/${id}`),
  // 切换调度状态（active ↔ disabled）
  toggleScheduling: (id: number) => patch<{ id: number; state: string }>(`/api/v1/admin/accounts/${id}/toggle`),
  clearFamilyCooldowns: (id: number) => del<{ cleared: number }>(`/api/v1/admin/accounts/${id}/family-cooldowns`),
  // 获取账号所属平台的模型列表
  models: (id: number) => get<ModelInfo[]>(`/api/v1/admin/accounts/${id}/models`),
  // 测试连接 URL（SSE 流式，前端用 fetch 消费）
  testUrl: (id: number) => `/api/v1/admin/accounts/${id}/test`,
  // 获取指定平台账号的用量窗口（插件提供，格式因平台而异）
  usage: (platform: string) =>
    get<{ accounts: Record<string, any> }>('/api/v1/admin/accounts/usage', { platform }),
  credentialsSchema: (platform: string) =>
    get<CredentialSchemaResp>(`/api/v1/admin/accounts/credentials-schema/${platform}`),
  // 手动刷新账号额度（调用插件 QueryQuota）。
  // reauth_warning 非空表示 refresh_token 已失效、本次是从存量 access_token 降级解析得到，
  // 前端需提示用户尽快重新授权。
  refreshQuota: (id: number) =>
    post<{
      plan_type?: string;
      email?: string;
      subscription_active_until?: string;
      reauth_warning?: string;
    }>(`/api/v1/admin/accounts/${id}/refresh-quota`),
  // 批量更新账号字段（group_ids 为追加模式）
  bulkUpdate: (data: BulkUpdateAccountsReq) =>
    post<BulkOpResp>('/api/v1/admin/accounts/bulk-update', data),
  // 批量删除账号
  bulkDelete: (ids: number[]) =>
    post<BulkOpResp>('/api/v1/admin/accounts/bulk-delete', { account_ids: ids }),
  // 批量刷新账号令牌/额度 —— SSE 流式接口，前端 URL，由调用方用 fetch 消费
  bulkRefreshQuotaUrl: () => '/api/v1/admin/accounts/bulk-refresh-quota',
  // 获取账号使用统计（可选时间范围）
  stats: (id: number, params?: { start_date?: string; end_date?: string }) =>
    get<AccountStatsResp>(`/api/v1/admin/accounts/${id}/stats`, params),
};

/**
 * 三个 cost 字段语义：
 *   - total_cost   原始上游定价（base，不含任何倍率）
 *   - account_cost 账号实际成本 = total × account_rate（"账号计费"统计）
 *   - actual_cost  用户扣费     = total × billing_rate
 *
 * image_count / image_cost 是 model 名 "gpt-image*" 的子集再聚一次，
 * 不影响 count / *_cost（它们仍然是全部请求总和）。
 */
export interface AccountPeriodStats {
  count: number;
  input_tokens: number;
  output_tokens: number;
  total_cost: number;
  account_cost: number;
  actual_cost: number;
  image_count: number;
  image_cost: number;
}

export interface AccountDailyStats {
  date: string;
  count: number;
  total_cost: number;
  account_cost: number;
  actual_cost: number;
}

export interface AccountModelStats {
  model: string;
  count: number;
  input_tokens: number;
  output_tokens: number;
  total_cost: number;
  account_cost: number;
  actual_cost: number;
}

export interface AccountPeakDay {
  date: string;
  count: number;
  total_cost: number;
  account_cost: number;
  actual_cost: number;
}

export interface AccountStatsResp {
  account_id: number;
  name: string;
  platform: string;
  state: string;
  start_date: string;
  end_date: string;
  total_days: number;
  today: AccountPeriodStats;
  range: AccountPeriodStats;
  daily_trend: AccountDailyStats[];
  models: AccountModelStats[];
  active_days: number;
  avg_duration_ms: number;
  peak_cost_day: AccountPeakDay;
  peak_request_day: AccountPeakDay;
}
