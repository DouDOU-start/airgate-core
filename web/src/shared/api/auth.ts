import { post } from './client';
import type { LoginReq, LoginResp, RegisterReq, APIKeyLoginReq } from '../types';

export const authApi = {
  login: (data: LoginReq) => post<LoginResp>('/api/v1/auth/login', data),
  loginByAPIKey: (data: APIKeyLoginReq) => post<LoginResp>('/api/v1/auth/login-apikey', data),
  register: (data: RegisterReq) => post<LoginResp>('/api/v1/auth/register', data),
  sendVerifyCode: (email: string) => post<void>('/api/v1/auth/send-verify-code', { email }),
  verifyCode: (email: string, code: string) => post<void>('/api/v1/auth/verify-code', { email, code }),
};
