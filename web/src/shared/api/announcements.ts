import { del, get, post, put } from './client';
import type {
  AnnouncementResp,
  UserAnnouncementResp,
  CreateAnnouncementReq,
  UpdateAnnouncementReq,
  PagedData,
} from '../types';

export const announcementsApi = {
  // 用户接口
  listMine: (unreadOnly?: boolean) =>
    get<UserAnnouncementResp[]>(
      '/api/v1/announcements',
      unreadOnly ? { unread_only: 'true' } : undefined,
    ),
  markRead: (id: number) => post<void>(`/api/v1/announcements/${id}/read`),
  markAllRead: () => post<void>('/api/v1/announcements/read-all'),

  // 管理员接口
  list: (params: { page: number; page_size: number; keyword?: string; status?: string }) =>
    get<PagedData<AnnouncementResp>>('/api/v1/admin/announcements', params),
  create: (data: CreateAnnouncementReq) =>
    post<AnnouncementResp>('/api/v1/admin/announcements', data),
  update: (id: number, data: UpdateAnnouncementReq) =>
    put<AnnouncementResp>(`/api/v1/admin/announcements/${id}`, data),
  delete: (id: number) => del<void>(`/api/v1/admin/announcements/${id}`),
};
