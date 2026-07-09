import { get, post, patch, del } from './client';
import type {
  GenerateRedemptionCodesReq, RedemptionCode, RedemptionCodeListResp,
  RedemptionCodesQuery, RedemptionStats, RedeemResp,
} from '../types';

export const redemptionApi = {
  // ===== 用户端 =====
  redeem: (code: string) => post<RedeemResp>('/api/v1/redeem', { code }),

  // ===== 管理端 =====
  adminList: (params?: RedemptionCodesQuery) =>
    get<RedemptionCodeListResp>('/api/v1/admin/redemption-codes', params),
  adminStats: () => get<RedemptionStats>('/api/v1/admin/redemption-codes/stats'),
  adminGenerate: (data: GenerateRedemptionCodesReq) =>
    post<RedemptionCode[]>('/api/v1/admin/redemption-codes', data),
  adminUpdateStatus: (id: number, disabled: boolean) =>
    patch<{ id: number }>(`/api/v1/admin/redemption-codes/${id}/status`, { disabled }),
  adminDelete: (id: number) => del<{ id: number }>(`/api/v1/admin/redemption-codes/${id}`),
};
