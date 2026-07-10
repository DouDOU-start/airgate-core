import { del, get, post, put } from './client';
import type {
  TierResp,
  CreateTierReq,
  UpdateTierReq,
  PageReq,
  PagedData,
} from '../types';

export const tiersApi = {
  list: (params: PageReq) =>
    get<PagedData<TierResp>>('/api/v1/admin/tiers', params),
  create: (data: CreateTierReq) => post<TierResp>('/api/v1/admin/tiers', data),
  update: (id: number, data: UpdateTierReq) => put<TierResp>(`/api/v1/admin/tiers/${id}`, data),
  delete: (id: number) => del<void>(`/api/v1/admin/tiers/${id}`),
};
