import { get, post, del } from './client';

export interface AdminAPIKeyResp {
  hint: string;
  key?: string; // 明文密钥，仅生成时返回一次
}

export const adminApiKeyApi = {
  get: () => get<AdminAPIKeyResp | null>('/api/v1/admin/settings/admin-api-key'),
  generate: () => post<AdminAPIKeyResp>('/api/v1/admin/settings/admin-api-key'),
  remove: () => del<null>('/api/v1/admin/settings/admin-api-key'),
};
