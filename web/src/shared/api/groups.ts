import { del, get, post, put } from './client';
import type {
  GroupResp,
  CreateGroupReq,
  UpdateGroupReq,
  PageReq,
  PagedData,
  GroupRateOverrideResp,
  GroupAllowedUserResp,
} from '../types';

export const groupsApi = {
  // 用户接口
  listAvailable: (params: PageReq) =>
    get<PagedData<GroupResp>>('/api/v1/groups', params),

  // 管理员接口
  list: (params: PageReq) =>
    get<PagedData<GroupResp>>('/api/v1/admin/groups', params),
  create: (data: CreateGroupReq) => post<GroupResp>('/api/v1/admin/groups', data),
  update: (id: number, data: UpdateGroupReq) => put<void>(`/api/v1/admin/groups/${id}`, data),
  delete: (id: number) => del<void>(`/api/v1/admin/groups/${id}`),

  // 分组专属倍率（reverse 视角）
  listRateOverrides: (groupId: number) =>
    get<GroupRateOverrideResp[]>(`/api/v1/admin/groups/${groupId}/rate-overrides`),
  setRateOverride: (
    groupId: number,
    userId: number,
    payload: { rate: number },
  ) =>
    put<GroupRateOverrideResp>(`/api/v1/admin/groups/${groupId}/rate-overrides/${userId}`, payload),
  deleteRateOverride: (groupId: number, userId: number) =>
    del<void>(`/api/v1/admin/groups/${groupId}/rate-overrides/${userId}`),

  // 专属分组用户管理（哪些用户被开了这个专属分组）
  listAllowedUsers: (groupId: number) =>
    get<GroupAllowedUserResp[]>(`/api/v1/admin/groups/${groupId}/allowed-users`),
  grantAllowedUser: (groupId: number, userId: number) =>
    post<void>(`/api/v1/admin/groups/${groupId}/allowed-users/${userId}`),
  revokeAllowedUser: (groupId: number, userId: number) =>
    del<void>(`/api/v1/admin/groups/${groupId}/allowed-users/${userId}`),
};
