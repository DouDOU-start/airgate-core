import { get } from './client';
import type {
  PagedData,
  RequestAuditDetail,
  RequestAuditListItem,
  RequestAuditQuery,
} from '../types';

export const requestAuditsApi = {
  list: (params?: Partial<RequestAuditQuery>) =>
    get<PagedData<RequestAuditListItem>>('/api/v1/admin/request-audits', params),
  get: (id: number) =>
    get<RequestAuditDetail>(`/api/v1/admin/request-audits/${id}`),
};
