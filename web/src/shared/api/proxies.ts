import { del, get, post, put } from './client';
import type {
  CreateProxyReq,
  PagedData,
  ProxyListQuery,
  ProxyResp,
  ProxyTestResult,
  UpdateProxyReq,
} from '../types';

export const proxiesApi = {
  list: (params?: ProxyListQuery) =>
    get<PagedData<ProxyResp>>('/api/v1/admin/proxies', params),
  create: (data: CreateProxyReq) =>
    post<ProxyResp>('/api/v1/admin/proxies', data),
  update: (id: number, data: UpdateProxyReq) =>
    put<ProxyResp>(`/api/v1/admin/proxies/${id}`, data),
  delete: (id: number) => del<void>(`/api/v1/admin/proxies/${id}`),
  test: (id: number) => post<ProxyTestResult>(`/api/v1/admin/proxies/${id}/test`),
};
