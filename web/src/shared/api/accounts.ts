import { get, post, put, del } from './client';
import type {
  AccountResp, CreateAccountReq, UpdateAccountReq,
  CredentialSchemaResp, ModelInfo, PageReq, PagedData,
} from '../types';

export const accountsApi = {
  list: (params: PageReq & { platform?: string; status?: string }) =>
    get<PagedData<AccountResp>>('/api/v1/admin/accounts', params),
  create: (data: CreateAccountReq) => post<AccountResp>('/api/v1/admin/accounts', data),
  update: (id: number, data: UpdateAccountReq) => put<void>(`/api/v1/admin/accounts/${id}`, data),
  delete: (id: number) => del<void>(`/api/v1/admin/accounts/${id}`),
  // 获取账号所属平台的模型列表
  models: (id: number) => get<ModelInfo[]>(`/api/v1/admin/accounts/${id}/models`),
  // 测试连接 URL（SSE 流式，前端用 fetch 消费）
  testUrl: (id: number) => `/api/v1/admin/accounts/${id}/test`,
  // 获取指定平台账号的用量窗口（插件提供，格式因平台而异）
  usage: (platform: string) =>
    get<{ accounts: Record<string, any> }>('/api/v1/admin/accounts/usage', { platform }),
  credentialsSchema: (platform: string) =>
    get<CredentialSchemaResp>(`/api/v1/admin/accounts/credentials-schema/${platform}`),
};
