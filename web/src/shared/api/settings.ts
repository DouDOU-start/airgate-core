import { get, put, post, uploadFile } from './client';
import type {
  SettingResp, UpdateSettingsReq, TestSMTPReq, TestBarkReq,
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
  testBark: (data: TestBarkReq) => post<void>('/api/v1/admin/settings/test-bark', data),
  getPublic: () => get<Record<string, string>>('/api/v1/settings/public'),
  getCoreVersion: () => get<CoreVersionInfo>('/api/v1/admin/version'),
  uploadFile: (file: File) => uploadFile<{ url: string }>('/api/v1/admin/settings/upload', file),
};
