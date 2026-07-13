import { get, patch, post } from './client';
import type {
  InviteListQuery,
  InviteMe,
  InviteeListResp,
  InviteOverrideListResp,
  InviteRebateLogListResp,
  InviteTransferResp,
} from '../types';

export const inviteApi = {
  // ===== 用户端 =====
  getMe: () => get<InviteMe>('/api/v1/invite/me'),
  listMyInvitees: (params?: InviteListQuery) => get<InviteeListResp>('/api/v1/invite/invitees', params),
  listMyLogs: (params?: InviteListQuery) => get<InviteRebateLogListResp>('/api/v1/invite/logs', params),
  transfer: () => post<InviteTransferResp>('/api/v1/invite/transfer'),

  // ===== 管理端 =====
  adminListOverrides: (params?: InviteListQuery) =>
    get<InviteOverrideListResp>('/api/v1/admin/invite/overrides', params),
  adminSetOverride: (userID: number, ratePercent: number | null) =>
    patch<{ user_id: number }>(`/api/v1/admin/invite/overrides/${userID}`, { rate_percent: ratePercent }),
  adminListInvitees: (params?: InviteListQuery) =>
    get<InviteeListResp>('/api/v1/admin/invite/invitees', params),
  adminListLogs: (params?: InviteListQuery) =>
    get<InviteRebateLogListResp>('/api/v1/admin/invite/logs', params),
};
