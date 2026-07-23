import { del, get, post, put } from './client';
import type {
  BookmarkResp,
  CreateBookmarkReq,
  UpdateBookmarkReq,
  PagedData,
} from '../types';

export const bookmarksApi = {
  list: (params: { page: number; page_size: number; keyword?: string }) =>
    get<PagedData<BookmarkResp>>('/api/v1/admin/bookmarks', params),
  create: (data: CreateBookmarkReq) =>
    post<BookmarkResp>('/api/v1/admin/bookmarks', data),
  update: (id: number, data: UpdateBookmarkReq) =>
    put<BookmarkResp>(`/api/v1/admin/bookmarks/${id}`, data),
  delete: (id: number) => del<void>(`/api/v1/admin/bookmarks/${id}`),
};
