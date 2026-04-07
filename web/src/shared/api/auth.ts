import { post } from './client';
import type { LoginReq, LoginResp, RegisterReq, RefreshResp, APIKeyLoginReq } from '../types';

export const authApi = {
  login: (data: LoginReq) => post<LoginResp>('/api/v1/auth/login', data),
  loginByAPIKey: (data: APIKeyLoginReq) => post<LoginResp>('/api/v1/auth/login-apikey', data),
  register: (data: RegisterReq) => post<LoginResp>('/api/v1/auth/register', data),
  refresh: () => post<RefreshResp>('/api/v1/auth/refresh'),
  sendVerifyCode: (email: string) => post<void>('/api/v1/auth/send-verify-code', { email }),
};
