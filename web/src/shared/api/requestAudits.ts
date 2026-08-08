import { del, get } from './client';
import type {
  PagedData,
  RequestAuditDetail,
  RequestAuditListItem,
  RequestAuditQuery,
} from '../types';

export type RequestAuditClearMode = 'all' | 'older_than_24h';

export const requestAuditsApi = {
  list: (params?: Partial<RequestAuditQuery>) =>
    get<PagedData<RequestAuditListItem>>('/api/v1/admin/request-audits', params),
  get: (id: number) =>
    get<RequestAuditDetail>(`/api/v1/admin/request-audits/${id}`),
  /** 清空请求审计：all 全部；older_than_24h 仅 24 小时前 */
  clear: (mode: RequestAuditClearMode = 'all') =>
    del<{ deleted: number; mode: RequestAuditClearMode }>('/api/v1/admin/request-audits', { mode }),
};
