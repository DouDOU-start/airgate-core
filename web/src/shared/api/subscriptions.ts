import { get, post, put } from './client';
import type {
  SubscriptionResp,
  AssignSubscriptionReq, BulkAssignReq, AdjustSubscriptionReq,
  PageReq, PagedData,
} from '../types';

export const subscriptionsApi = {
  // 管理员接口
  adminList: (params: PageReq & { user_id?: number; group_id?: number; status?: string }) =>
    get<PagedData<SubscriptionResp>>('/api/v1/admin/subscriptions', params),
  assign: (data: AssignSubscriptionReq) => post<void>('/api/v1/admin/subscriptions/assign', data),
  bulkAssign: (data: BulkAssignReq) => post<void>('/api/v1/admin/subscriptions/bulk-assign', data),
  adjust: (id: number, data: AdjustSubscriptionReq) =>
    put<void>(`/api/v1/admin/subscriptions/${id}/adjust`, data),
};
