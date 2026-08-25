import { get, put, post, del, patch } from './client';
import type {
  UserResp, UpdateProfileReq, ChangePasswordReq,
  CreateUserReq, UpdateUserReq, AdjustBalanceReq,
  BalanceLogResp, PageReq, PagedData,
} from '../types';

// APIKeyResp 从 types 中已有定义，这里直接引用
import type { APIKeyResp } from '../types';

export const usersApi = {
  // 用户接口
  me: () => get<UserResp>('/api/v1/users/me'),
  updateProfile: (data: UpdateProfileReq) => put<void>('/api/v1/users/me', data),
  changePassword: (data: ChangePasswordReq) => post<void>('/api/v1/users/me/password', data),
  updateBalanceAlert: (threshold: number) => put<void>('/api/v1/users/me/balance-alert', { threshold }),
  myBalanceHistory: (params: PageReq) =>
    get<PagedData<BalanceLogResp>>('/api/v1/users/me/balance-history', params),

  // 管理员接口
  list: (params: PageReq & { status?: string; role?: string }) =>
    get<PagedData<UserResp>>('/api/v1/admin/users', params),
  create: (data: CreateUserReq) => post<UserResp>('/api/v1/admin/users', data),
  update: (id: number, data: UpdateUserReq) => put<void>(`/api/v1/admin/users/${id}`, data),
  delete: (id: number) => del<void>(`/api/v1/admin/users/${id}`),
  toggleStatus: (id: number) => patch<{ id: number; status: string }>(`/api/v1/admin/users/${id}/toggle`),
  adjustBalance: (id: number, data: AdjustBalanceReq) =>
    post<void>(`/api/v1/admin/users/${id}/balance`, data),
  balanceHistory: (id: number, params: PageReq) =>
    get<PagedData<BalanceLogResp>>(`/api/v1/admin/users/${id}/balance-history`, params),
  apiKeys: (id: number, params: PageReq) =>
    get<PagedData<APIKeyResp>>(`/api/v1/admin/users/${id}/api-keys`, params),
};
