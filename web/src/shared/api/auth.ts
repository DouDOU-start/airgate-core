import { post } from './client';
import type { LoginReq, LoginResp, RegisterReq, RefreshResp } from '../types';

export const authApi = {
  login: (data: LoginReq) => post<LoginResp>('/api/v1/auth/login', data),
  register: (data: RegisterReq) => post<void>('/api/v1/auth/register', data),
  refresh: () => post<RefreshResp>('/api/v1/auth/refresh'),
};
