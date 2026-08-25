import { get, post, put, del } from './client';
import type {
  ProxyResp, CreateProxyReq, UpdateProxyReq, TestProxyResp,
  PageReq, PagedData,
} from '../types';

export const proxiesApi = {
  list: (params?: PageReq) =>
    get<PagedData<ProxyResp>>('/api/v1/admin/proxies', params),
  create: (data: CreateProxyReq) => post<ProxyResp>('/api/v1/admin/proxies', data),
  update: (id: number, data: UpdateProxyReq) => put<void>(`/api/v1/admin/proxies/${id}`, data),
  delete: (id: number) => del<void>(`/api/v1/admin/proxies/${id}`),
  test: (id: number) => post<TestProxyResp>(`/api/v1/admin/proxies/${id}/test`),
};
