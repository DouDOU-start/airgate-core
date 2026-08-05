import { del, get, post, put } from './client';
import type {
  AppEntryResp,
  AuthorizeInfoResp,
  AuthorizeReq,
  AuthorizeResp,
  CreateOAuthClientReq,
  OAuthClientResp,
  OAuthClientSecretResp,
  UpdateOAuthClientReq,
} from '../types';

export const oauthApi = {
  // 用户接口：授权页 + 应用导航
  authorizeInfo: (clientId: string, redirectUri: string, scope: string) =>
    get<AuthorizeInfoResp>('/api/v1/oauth/authorize-info', {
      client_id: clientId,
      redirect_uri: redirectUri,
      scope,
    }),
  authorize: (data: AuthorizeReq) => post<AuthorizeResp>('/api/v1/oauth/authorize', data),
  listApps: () => get<AppEntryResp[]>('/api/v1/apps'),

  // 管理员接口：应用接入 CRUD
  list: () => get<OAuthClientResp[]>('/api/v1/admin/oauth-clients'),
  create: (data: CreateOAuthClientReq) =>
    post<OAuthClientSecretResp>('/api/v1/admin/oauth-clients', data),
  update: (id: number, data: UpdateOAuthClientReq) =>
    put<OAuthClientResp>(`/api/v1/admin/oauth-clients/${id}`, data),
  delete: (id: number) => del<void>(`/api/v1/admin/oauth-clients/${id}`),
  resetSecret: (id: number) =>
    post<OAuthClientSecretResp>(`/api/v1/admin/oauth-clients/${id}/reset-secret`),
};
