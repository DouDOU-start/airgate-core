import { del, get, put, post, uploadFile } from './client';
import type {
  SettingResp, UpdateSettingsReq, TestSMTPReq, TestWeChatReq,
  WeChatBindSessionResp, WeChatBindStatusResp,
} from '../types';

export interface CoreVersionInfo {
  version: string;
  go_version: string;
  platform: string;
}

export const settingsApi = {
  list: (group?: string) => get<SettingResp[]>('/api/v1/admin/settings', group ? { group } : undefined),
  update: (data: UpdateSettingsReq) => put<void>('/api/v1/admin/settings', data),
  testSMTP: (data: TestSMTPReq) => post<void>('/api/v1/admin/settings/test-smtp', data),
  testWeChat: (data: TestWeChatReq) => post<void>('/api/v1/admin/settings/test-wechat', data),
  createWeChatBind: () => post<WeChatBindSessionResp>('/api/v1/admin/settings/wechat-bind'),
  getWeChatBindStatus: (id: string) => get<WeChatBindStatusResp>(`/api/v1/admin/settings/wechat-bind/${encodeURIComponent(id)}`),
  unbindWeChat: () => del<void>('/api/v1/admin/settings/wechat-bind'),
  getPublic: () => get<Record<string, string>>('/api/v1/settings/public'),
  getCoreVersion: () => get<CoreVersionInfo>('/api/v1/admin/version'),
  uploadFile: (file: File) => uploadFile<{ url: string }>('/api/v1/admin/settings/upload', file),
};
