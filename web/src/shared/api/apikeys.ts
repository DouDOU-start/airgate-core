import { get, post, put, del } from './client';
import type { APIKeyResp, CreateAPIKeyReq, UpdateAPIKeyReq, PageReq, PagedData } from '../types';

type APIKeyRequestOptions = {
  signal?: AbortSignal;
};

type APIKeyListParams = PageReq & {
  search_scope?: 'api_key';
};

export const apikeysApi = {
  // 用户接口
  list: (params?: APIKeyListParams, options?: APIKeyRequestOptions) =>
    get<PagedData<APIKeyResp>>('/api/v1/api-keys', params, options),
  create: (data: CreateAPIKeyReq) => post<APIKeyResp>('/api/v1/api-keys', data),
  update: (id: number, data: UpdateAPIKeyReq) => put<void>(`/api/v1/api-keys/${id}`, data),
  delete: (id: number) => del<void>(`/api/v1/api-keys/${id}`),
  reveal: (id: number) => get<APIKeyResp>(`/api/v1/api-keys/${id}/reveal`),

  // 管理员接口
  adminList: (params?: APIKeyListParams, options?: APIKeyRequestOptions) =>
    get<PagedData<APIKeyResp>>('/api/v1/admin/api-keys', params, options),
  adminUpdate: (id: number, data: UpdateAPIKeyReq) =>
    put<void>(`/api/v1/admin/api-keys/${id}`, data),
};
