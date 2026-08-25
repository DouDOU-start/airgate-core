import { get, post } from './client';
import type {
  SetupStatusResp, TestDBReq, TestRedisReq,
  InstallReq, TestConnectionResp,
} from '../types';

export const setupApi = {
  status: () => get<SetupStatusResp>('/setup/status'),
  testDB: (data: TestDBReq) => post<TestConnectionResp>('/setup/test-db', data),
  testRedis: (data: TestRedisReq) => post<TestConnectionResp>('/setup/test-redis', data),
  install: (data: InstallReq) => post<void>('/setup/install', data),
};
