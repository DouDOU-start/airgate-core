import { get, post, put, del } from './client';
import type {
  ModelPriceResp, CreateModelPriceReq, UpdateModelPriceReq,
  ModelTagResp, PageReq, PagedData,
} from '../types';

export const modelPricesApi = {
  list: (params?: PageReq & { tag_id?: number }) =>
    get<PagedData<ModelPriceResp>>('/api/v1/admin/model-prices', params),
  create: (data: CreateModelPriceReq) => post<ModelPriceResp>('/api/v1/admin/model-prices', data),
  update: (id: number, data: UpdateModelPriceReq) => put<ModelPriceResp>(`/api/v1/admin/model-prices/${id}`, data),
  delete: (id: number) => del<void>(`/api/v1/admin/model-prices/${id}`),
};

// 模型标签（家族归类，归属模型管理）
export const modelTagsApi = {
  list: () => get<ModelTagResp[]>('/api/v1/admin/model-tags'),
  create: (name: string) => post<ModelTagResp>('/api/v1/admin/model-tags', { name }),
  update: (id: number, name: string) => put<ModelTagResp>(`/api/v1/admin/model-tags/${id}`, { name }),
  delete: (id: number) => del<void>(`/api/v1/admin/model-tags/${id}`),
};
