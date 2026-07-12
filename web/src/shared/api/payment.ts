import { get, post, del } from './client';
import type {
  PaymentMethodsResp, PaymentOrder, PaymentOrderListResp,
  CreatePaymentOrderReq, AdminPaymentOrdersResp, AdminPaymentOrdersQuery,
  PaymentProvidersResp, UpsertPaymentProviderReq, UpsertPaymentProviderResp,
} from '../types';

export const paymentApi = {
  // ===== 用户端 =====
  methods: () => get<PaymentMethodsResp>('/api/v1/payment/methods'),
  createOrder: (data: CreatePaymentOrderReq) => post<PaymentOrder>('/api/v1/payment/orders', data),
  listOrders: (params?: { page?: number; page_size?: number }) =>
    get<PaymentOrderListResp>('/api/v1/payment/orders', params),
  getOrder: (outTradeNo: string) => get<PaymentOrder>(`/api/v1/payment/orders/${outTradeNo}`),

  // ===== 管理端 =====
  adminListOrders: (params?: AdminPaymentOrdersQuery) =>
    get<AdminPaymentOrdersResp>('/api/v1/admin/payment/orders', params),
  adminListProviders: () => get<PaymentProvidersResp>('/api/v1/admin/payment/providers'),
  adminUpsertProvider: (data: UpsertPaymentProviderReq) =>
    post<UpsertPaymentProviderResp>('/api/v1/admin/payment/providers', data),
  adminDeleteProvider: (id: string) =>
    del<{ ok: boolean }>(`/api/v1/admin/payment/providers/${id}`),
};
